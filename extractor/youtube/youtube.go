// Package youtube implements an InfoExtractor for YouTube.
//
// Stream URLs are obtained from the InnerTube player API (see innertube.go),
// because the watch page's ytInitialPlayerResponse now ships formats with
// metadata only — no "url" and no "signatureCipher". The webpage is still used
// for the visitor id, metadata, subtitles, and as a fallback format source.
//
// The ciphered path is retained for that fallback: it detects
// signature-protected streams, fetches the player JavaScript, and deobfuscates
// the signature (and the "n" throttling parameter) with the goja-backed
// evaluator in the extractor package.
//
// Live YouTube internals change often; this mirrors yt-dlp's own structure and
// client table so it can be re-synced when upstream adapts.
package youtube

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"yt-dlp-go/extractor"
)

// YouTubeIE extracts from youtube.com / youtu.be.
type YouTubeIE struct{}

func init() { extractor.Register(YouTubeIE{}) }

func (YouTubeIE) Name() string { return "youtube" }

var youtubeURLRE = regexp.MustCompile(`(?i)(?:https?://)?(?:www\.)?(?:youtube\.com/(?:watch\?(?:.*&)?v=|shorts/|embed/|v/)|youtu\.be/)([A-Za-z0-9_-]{11})`)

func (YouTubeIE) Match(u string) bool {
	return youtubeURLRE.MatchString(u)
}

func extractVideoID(u string) string {
	m := youtubeURLRE.FindStringSubmatch(u)
	if m == nil {
		return ""
	}
	return m[1]
}

// Extract performs the full extraction.
//
// Formats come from the InnerTube player API first (see innertube.go for the
// rationale): the watch page's ytInitialPlayerResponse no longer carries stream
// URLs. The webpage is still fetched, because it supplies the visitor id the API
// requires plus metadata/subtitles, and its player response stays as the last
// fallback — it is the one that can still carry signatureCipher formats.
func (YouTubeIE) Extract(ctx *extractor.Context, pageURL string) (*extractor.Info, error) {
	videoID := extractVideoID(pageURL)
	if videoID == "" {
		return nil, fmt.Errorf("could not parse YouTube video id from %q", pageURL)
	}

	html, err := extractor.DownloadWebpage(ctx, pageURL, nil, nil)
	if err != nil {
		return nil, err
	}

	// A missing webpage player response is no longer fatal: formats and metadata
	// both have API sources now.
	htmlPlayer, htmlPlayerErr := extractJSONAssign(html, "ytInitialPlayerResponse")
	visitorData := extractVisitorData(html)
	verbosef(ctx, "[youtube] webpage %d bytes, visitorData %d chars, webpage player response ok=%v\n",
		len(html), len(visitorData), htmlPlayerErr == nil)

	player, clientLabel, err := resolvePlayerResponse(ctx, videoID, visitorData, htmlPlayer)
	if err != nil {
		return nil, err
	}
	verbosef(ctx, "[youtube] formats sourced from client %q\n", clientLabel)

	// Metadata and subtitles: prefer the chosen response, fall back to the
	// webpage's (the lean app clients omit captions and some microformat data).
	sources := []map[string]any{player}
	if htmlPlayerErr == nil && htmlPlayer != nil {
		sources = append(sources, htmlPlayer)
	}

	title := firstNonEmpty(sources, func(p map[string]any) string {
		if t := extractor.StrOrNone(extractor.TraverseObj(p, "videoDetails", "title")); t != "" {
			return t
		}
		return extractor.StrOrNone(extractor.TraverseObj(p,
			"microformat", "playerMicroformatRenderer", "title", "simpleText"))
	})
	lengthStr := firstNonEmpty(sources, func(p map[string]any) string {
		return extractor.StrOrNone(extractor.TraverseObj(p, "videoDetails", "lengthSeconds"))
	})
	description := firstNonEmpty(sources, func(p map[string]any) string {
		return extractor.StrOrNone(extractor.TraverseObj(p, "videoDetails", "shortDescription"))
	})
	channel := firstNonEmpty(sources, func(p map[string]any) string {
		return extractor.StrOrNone(extractor.TraverseObj(p, "videoDetails", "author"))
	})
	viewCount := int64(extractor.IntOrNone(firstNonEmpty(sources, func(p map[string]any) string {
		return extractor.StrOrNone(extractor.TraverseObj(p, "videoDetails", "viewCount"))
	})))
	var categories []string
	for _, p := range sources {
		if kw, ok := extractor.TraverseObj(p, "videoDetails", "keywords").([]any); ok {
			for _, k := range kw {
				if s := extractor.StrOrNone(k); s != "" {
					categories = append(categories, s)
				}
			}
			break
		}
	}

	// Signature timestamp: passed as the second argument to the signature
	// transform by modern player builds. Only used on the ciphered fallback path.
	sts := firstNonEmpty(sources, func(p map[string]any) string {
		if raw := extractor.TraverseObj(p, "sts"); raw != nil {
			return fmt.Sprintf("%v", raw)
		}
		return ""
	})

	subs := map[string][]extractor.Subtitle{}
	for _, p := range sources {
		if s := extractSubtitles(p); len(s) > 0 {
			subs = s
			break
		}
	}

	info := &extractor.Info{
		ID:          videoID,
		Title:       title,
		Description: description,
		Channel:     channel,
		ViewCount:   viewCount,
		Categories:  categories,
		WebpageURL:  pageURL,
		Ext:         "mp4",
		Subtitles:   subs,
		Chapters:    parseChaptersFromDescription(description),
		Raw:         player,
	}
	if d, e := strconv.ParseFloat(lengthStr, 64); e == nil {
		info.Duration = d
	}

	formats := collectRawFormats(player)
	isLive := youtubeIsLive(player)
	info.IsLive = isLive

	// A live stream carries no adaptiveFormats; its only playable source is the
	// HLS manifest. Don't misreport that as "age-restricted".
	if len(formats) == 0 && !isLive {
		return nil, fmt.Errorf("no streamingData formats in the %q player response "+
			"(video may be age-restricted or require consent)", clientLabel)
	}

	// Fetch the player JS once, and only when something actually needs it. The
	// default clients hand back plain URLs, so this is normally skipped.
	var playerJS string
	var jsErr error
	if formatsNeedJS(formats) {
		playerJS, jsErr = fetchPlayerJS(ctx, html)
		verbosef(ctx, "[youtube] player JS: %d bytes, err=%v\n", len(playerJS), jsErr)
	}

	skipped := map[string]int{}
	for _, f := range formats {
		m, ok := f.(map[string]any)
		if !ok {
			continue
		}
		fmtObj, err := buildFormat(m, playerJS, jsErr, sts, ctx)
		if err != nil {
			// Skip formats we cannot resolve rather than aborting the whole run,
			// but remember why so a total failure stays explainable.
			skipped[err.Error()]++
			verbosef(ctx, "[youtube] skip itag %v: %v\n", m["itag"], err)
			continue
		}
		info.Formats = append(info.Formats, fmtObj)
	}

	if len(info.Formats) == 0 {
		// Last chance: a live broadcast exposes only an HLS manifest, not the
		// adaptiveFormats list we iterated above.
		if live := buildLiveFormats(player); len(live) > 0 {
			info.Formats = append(info.Formats, live...)
		}
	}
	if len(info.Formats) == 0 {
		return nil, fmt.Errorf("no resolvable formats found (client %q): %s",
			clientLabel, summarizeSkips(len(formats), skipped))
	}
	return info, nil
}

// youtubeIsLive reports whether the player response describes a live broadcast.
func youtubeIsLive(player map[string]any) bool {
	if b, ok := extractor.TraverseObj(player, "videoDetails", "isLiveContent").(bool); ok {
		return b
	}
	if b, ok := extractor.TraverseObj(player, "videoDetails", "isLive").(bool); ok {
		return b
	}
	return false
}

// buildLiveFormats returns the HLS manifest format for a live stream. YouTube
// serves live video exclusively through streamingData.hlsManifestUrl; the
// core routes a "m3u8_native" protocol to the fragment downloader, which
// assembles the HLS segments.
func buildLiveFormats(player map[string]any) []extractor.Format {
	sd := extractor.TraverseObj(player, "streamingData")
	sdMap, ok := sd.(map[string]any)
	if !ok {
		return nil
	}
	hls := extractor.StrOrNone(sdMap["hlsManifestUrl"])
	if hls == "" {
		return nil
	}
	return []extractor.Format{{
		FormatID:   "hls",
		URL:        hls,
		Protocol:   "m3u8_native",
		Ext:        "m3u8",
		Source:     "hls",
		FormatNote: "live",
	}}
}

// resolvePlayerResponse asks each default InnerTube client in turn for a player
// response that actually carries usable formats, then falls back to the
// webpage's own player response. The returned label identifies the source and is
// used in log and error messages.
func resolvePlayerResponse(ctx *extractor.Context, videoID, visitorData string,
	htmlPlayer map[string]any) (map[string]any, string, error) {

	var problems []string
	for _, label := range defaultPlayerClients {
		client, ok := innertubeClients[label]
		if !ok {
			continue
		}
		player, err := playerAPIFn(ctx, client, videoID, visitorData)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: request failed: %v", label, err))
			verbosef(ctx, "[youtube] client %s: request failed: %v\n", label, err)
			continue
		}
		if perr := playabilityError(player); perr != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", label, perr))
			verbosef(ctx, "[youtube] client %s: %v\n", label, perr)
			continue
		}
		raws := collectRawFormats(player)
		if countResolvable(raws) == 0 {
			problems = append(problems, fmt.Sprintf(
				"%s: %d formats but none carried a URL or cipher (a PO token is likely required)",
				label, len(raws)))
			verbosef(ctx, "[youtube] client %s: %d formats, none resolvable\n", label, len(raws))
			continue
		}
		return player, label, nil
	}

	// Last resort: the webpage's player response. Historically the only source,
	// and still the one that can carry signatureCipher formats.
	if htmlPlayer != nil {
		raws := collectRawFormats(htmlPlayer)
		if countResolvable(raws) > 0 {
			return htmlPlayer, "webpage", nil
		}
		problems = append(problems, fmt.Sprintf(
			"webpage: %d formats but none carried a URL or cipher", len(raws)))
	}

	return nil, "", fmt.Errorf("could not obtain playable formats from any client:\n  - %s",
		strings.Join(problems, "\n  - "))
}

// formatsNeedJS reports whether any format requires the player JavaScript, i.e.
// it is signature-ciphered or carries an "n" throttling parameter. The query is
// parsed properly rather than substring-matched, so parameters that merely end
// in "n" (fn=, sn=, ...) do not trigger a needless player download.
func formatsNeedJS(formats []any) bool {
	for _, f := range formats {
		m, ok := f.(map[string]any)
		if !ok {
			continue
		}
		if extractor.StrOrNone(m["signatureCipher"]) != "" || extractor.StrOrNone(m["cipher"]) != "" {
			return true
		}
		if u := extractor.StrOrNone(m["url"]); u != "" {
			if pu, perr := url.Parse(u); perr == nil && pu.Query().Get("n") != "" {
				return true
			}
		}
	}
	return false
}

// firstNonEmpty returns the first non-empty value produced by get over sources.
func firstNonEmpty(sources []map[string]any, get func(map[string]any) string) string {
	for _, p := range sources {
		if v := get(p); v != "" {
			return v
		}
	}
	return ""
}

// summarizeSkips renders the aggregated per-format skip reasons so that a total
// extraction failure explains itself instead of just saying "no formats".
func summarizeSkips(total int, skipped map[string]int) string {
	if len(skipped) == 0 {
		return fmt.Sprintf("%d formats present, none usable", total)
	}
	reasons := make([]string, 0, len(skipped))
	for reason, n := range skipped {
		reasons = append(reasons, fmt.Sprintf("%s (x%d)", reason, n))
	}
	sort.Strings(reasons)
	return fmt.Sprintf("all %d formats skipped: %s", total, strings.Join(reasons, "; "))
}

// verbosef logs to stderr when -v/--verbose is set.
func verbosef(ctx *extractor.Context, format string, args ...any) {
	if ctx == nil || ctx.Options == nil || !ctx.Options.Verbose {
		return
	}
	fmt.Fprintf(os.Stderr, format, args...)
}

// extractSubtitles pulls the caption tracks from the player response so they can
// be downloaded by the core pipeline (--write-subs). YouTube serves each track's
// XML/TTML via its baseUrl.
func extractSubtitles(player map[string]any) map[string][]extractor.Subtitle {
	subs := map[string][]extractor.Subtitle{}
	tracks := extractor.TraverseObj(player, "captions", "playerCaptionsTracklistRenderer", "captionTracks")
	arr, ok := tracks.([]any)
	if !ok {
		return subs
	}
	for _, tr := range arr {
		m, ok := tr.(map[string]any)
		if !ok {
			continue
		}
		lang := extractor.StrOrNone(m["languageCode"])
		baseURL := extractor.StrOrNone(m["baseUrl"])
		if lang == "" || baseURL == "" {
			continue
		}
		name := extractor.StrOrNone(extractor.TraverseObj(m, "name", "simpleText"))
		subs[lang] = append(subs[lang], extractor.Subtitle{URL: baseURL, Ext: "xml", Name: name})
	}
	return subs
}

// buildFormat turns a single streamingData format object into a Format.
func buildFormat(m map[string]any, playerJS string, jsErr error, sts string, ctx *extractor.Context) (extractor.Format, error) {
	f := extractor.Format{
		FormatID:        fmt.Sprintf("%d", extractor.IntOrNone(m["itag"])),
		VCodec:          mimeVideoCodec(extractor.StrOrNone(m["mimeType"])),
		ACodec:          mimeAudioCodec(extractor.StrOrNone(m["mimeType"])),
		Width:           extractor.IntOrNone(m["width"]),
		Height:          extractor.IntOrNone(m["height"]),
		Filesize:        int64(extractor.FloatOrNone(m["contentLength"])),
		FormatNote:      extractor.StrOrNone(m["qualityLabel"]),
		TBR:             extractor.FloatOrNone(m["bitrate"]) / 1000,
		AudioSampleRate: extractor.IntOrNone(m["audioSampleRate"]),
		AudioChannels:   extractor.IntOrNone(m["audioChannels"]),
		DynamicRange:    youtubeDynamicRange(m),
		Source:          "dash",
	}

	// Direct URL (unciphered progressive/adaptive).
	if u := extractor.StrOrNone(m["url"]); u != "" {
		f.URL = u
		f.Protocol, f.Ext = classifyURL(u, extractor.StrOrNone(m["mimeType"]))
		f.URL = rewriteNParam(playerJS, jsErr, f.URL)
		return f, nil
	}

	// Ciphered stream: decode the signature.
	cipher := extractor.StrOrNone(m["signatureCipher"])
	if cipher == "" {
		cipher = extractor.StrOrNone(m["cipher"])
	}
	if cipher == "" {
		return f, fmt.Errorf("format has neither url nor cipher")
	}
	q, err := url.ParseQuery(cipher)
	if err != nil {
		return f, err
	}
	base := q.Get("url")
	sig := q.Get("s")
	sp := q.Get("sp")
	if sp == "" {
		sp = "signature"
	}
	if base == "" || sig == "" {
		return f, fmt.Errorf("incomplete cipher (url=%q sig=%q)", base, sig)
	}
	if jsErr != nil {
		return f, fmt.Errorf("signature deobfuscation unavailable: %w", jsErr)
	}
	deob, err := extractor.DeobfuscateSignature(playerJS, sig, sts)
	if err != nil {
		return f, err
	}
	sep := "&"
	if strings.Contains(base, "?") {
		sep = "&"
	}
	f.URL = base + sep + sp + "=" + url.QueryEscape(deob)
	f.Protocol, f.Ext = classifyURL(f.URL, extractor.StrOrNone(m["mimeType"]))
	f.URL = rewriteNParam(playerJS, jsErr, f.URL)
	return f, nil
}

// youtubeDynamicRange reports the dynamic-range label for a streamingData format
// object, used by --format-sort (hdr/dynamic_range). It detects HDR via the
// qualityLabel suffix (e.g. "1080p60 HDR") and the dynamicRangeInfo blob.
func youtubeDynamicRange(m map[string]any) string {
	if dr := extractor.StrOrNone(m["dynamicRange"]); dr != "" {
		return strings.ToUpper(dr)
	}
	if dri, ok := m["dynamicRangeInfo"].(map[string]any); ok {
		if v := extractor.StrOrNone(dri["dynamicRange"]); v != "" {
			return strings.ToUpper(v)
		}
	}
	if strings.Contains(strings.ToUpper(extractor.StrOrNone(m["qualityLabel"])), "HDR") {
		return "HDR10"
	}
	return ""
}

// rewriteNParam deobfuscates a format URL's n (throttling) query parameter via
// the embedded goja engine. If the player JS is unavailable or the n function
// cannot be evaluated, the original URL is returned unchanged (graceful).
func rewriteNParam(playerJS string, jsErr error, rawURL string) string {
	if playerJS == "" || jsErr != nil {
		return rawURL
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	n := u.Query().Get("n")
	if n == "" {
		return rawURL
	}
	deob, err := extractor.DeobfuscateNSig(playerJS, n)
	if err != nil || deob == "" {
		return rawURL
	}
	q := u.Query()
	q.Set("n", deob)
	u.RawQuery = q.Encode()
	return u.String()
}

func classifyURL(u, mime string) (protocol, ext string) {
	low := strings.ToLower(u)
	switch {
	case strings.Contains(low, ".m3u8") || strings.Contains(mime, "x-mpegurl") || strings.Contains(mime, "vnd.apple.mpegurl"):
		return "m3u8_native", "m3u8"
	case strings.Contains(low, ".mpd") || strings.Contains(mime, "dash+xml"):
		return "dash", "mpd"
	default:
		return "http", mimeExt(mime)
	}
}

func mimeExt(mime string) string {
	switch {
	case strings.Contains(mime, "mp4"):
		return "mp4"
	case strings.Contains(mime, "webm"):
		return "webm"
	case strings.Contains(mime, "m4a") || strings.Contains(mime, "mp4a"):
		return "m4a"
	case strings.Contains(mime, "ogg") || strings.Contains(mime, "opus"):
		return "opus"
	default:
		return "mp4"
	}
}

func mimeVideoCodec(mime string) string {
	if strings.HasPrefix(mime, "audio") {
		return ""
	}
	return "vp9/h264"
}

func mimeAudioCodec(mime string) string {
	if strings.HasPrefix(mime, "video") {
		return ""
	}
	return "aac/opus"
}

// fetchPlayerJS finds and downloads the player JavaScript used for signatures.
func fetchPlayerJS(ctx *extractor.Context, html string) (string, error) {
	re := regexp.MustCompile(`(?i)(?:jsUrl|PLAYER_JS_URL)"?\s*[:=]\s*"(/s/[^"]+\.js)"`)
	m := re.FindStringSubmatch(html)
	if m == nil {
		// Fallback: any /s/*.js script reference.
		re2 := regexp.MustCompile(`(?i)(https://[^\s"']+/s/[^\s"']+\.js)`)
		m2 := re2.FindStringSubmatch(html)
		if m2 == nil {
			return "", fmt.Errorf("could not locate player JavaScript")
		}
		return extractor.DownloadWebpage(ctx, m2[1], nil, nil)
	}
	jsURL := "https://www.youtube.com" + m[1]
	return extractor.DownloadWebpage(ctx, jsURL, nil, nil)
}

// extractJSONAssign extracts the object assigned to `key = {...}` from raw HTML/JS,
// honouring string literals so brace counting is correct.
func extractJSONAssign(html, key string) (map[string]any, error) {
	marker := key + " = "
	i := strings.Index(html, marker)
	if i < 0 {
		return nil, fmt.Errorf("%s not found", key)
	}
	start := i + len(marker)
	for start < len(html) && (html[start] == ' ' || html[start] == '\n' || html[start] == '\t') {
		start++
	}
	if start >= len(html) || html[start] != '{' {
		return nil, fmt.Errorf("expected object after %s", key)
	}
	depth := 0
	inStr := false
	var quote byte
	for j := start; j < len(html); j++ {
		c := html[j]
		if inStr {
			if c == '\\' {
				j++
				continue
			}
			if c == quote {
				inStr = false
			}
			continue
		}
		switch c {
		case '"', '\'':
			inStr = true
			quote = c
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				raw := html[start : j+1]
				var v any
				if uerr := json.Unmarshal([]byte(raw), &v); uerr == nil {
					if m, ok := v.(map[string]any); ok {
						return m, nil
					}
					return nil, fmt.Errorf("json decode of %s produced a non-object", key)
				} else {
					return nil, fmt.Errorf("json decode of %s failed: %w", key, uerr)
				}
			}
		}
	}
	return nil, fmt.Errorf("unterminated object for %s", key)
}
