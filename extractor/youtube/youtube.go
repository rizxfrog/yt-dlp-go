// Package youtube implements an InfoExtractor for YouTube. It demonstrates the
// hardest part of any such tool: parsing ytInitialPlayerResponse, detecting
// ciphered (signature-protected) streams, fetching the player JavaScript, and
// deobfuscating the signature with the pure-Go evaluator in the extractor
// package.
//
// Live YouTube internals change often; this is a faithful structural port. If
// signature deobfuscation fails against the current site, the engine reports the
// limitation rather than crashing, and the recommended fix is to back
// DeobfuscateSignature with an embedded JS engine (goja).
package youtube

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
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
func (YouTubeIE) Extract(ctx *extractor.Context, pageURL string) (*extractor.Info, error) {
	videoID := extractVideoID(pageURL)
	if videoID == "" {
		return nil, fmt.Errorf("could not parse YouTube video id from %q", pageURL)
	}

	html, err := extractor.DownloadWebpage(ctx, pageURL, nil, nil)
	if err != nil {
		return nil, err
	}

	player, err := extractJSONAssign(html, "ytInitialPlayerResponse")
	if err != nil {
		return nil, err
	}

	details := extractor.TraverseObj(player, "videoDetails")
	title := extractor.StrOrNone(extractor.TraverseObj(details, "title"))
	if title == "" {
		title = extractor.StrOrNone(extractor.TraverseObj(player, "microformat", "playerMicroformatRenderer", "title", "simpleText"))
	}
	lengthStr := extractor.StrOrNone(extractor.TraverseObj(details, "lengthSeconds"))

	info := &extractor.Info{
		ID:         videoID,
		Title:      title,
		WebpageURL: pageURL,
		Ext:        "mp4",
		Raw:        player,
	}

	var streaming any
	if s := extractor.TraverseObj(player, "streamingData"); s != nil {
		streaming = s
	}
	if streaming == nil {
		// Age-restricted / bot-challenged pages omit streamingData.
		return nil, fmt.Errorf("no streamingData (video may be age-restricted or require consent)")
	}

	// Collect both progressive and adaptive formats.
	formats := []any{}
	if f := extractor.TraverseObj(streaming, "formats"); f != nil {
		if arr, ok := f.([]any); ok {
			formats = append(formats, arr...)
		}
	}
	if f := extractor.TraverseObj(streaming, "adaptiveFormats"); f != nil {
		if arr, ok := f.([]any); ok {
			formats = append(formats, arr...)
		}
	}

	// Fetch the player JS once (needed only if any format is ciphered).
	var playerJS string
	var jsErr error
	needsJS := false
	for _, f := range formats {
		if m, ok := f.(map[string]any); ok {
			if extractor.StrOrNone(m["signatureCipher"]) != "" || extractor.StrOrNone(m["cipher"]) != "" {
				needsJS = true
				break
			}
		}
	}
	if needsJS {
		playerJS, jsErr = fetchPlayerJS(ctx, html)
	}

	for _, f := range formats {
		m, ok := f.(map[string]any)
		if !ok {
			continue
		}
		fmtObj, err := buildFormat(m, playerJS, jsErr, ctx)
		if err != nil {
			// Skip formats we cannot resolve rather than aborting the whole run.
			continue
		}
		info.Formats = append(info.Formats, fmtObj)
	}

	if len(info.Formats) == 0 {
		return nil, fmt.Errorf("no resolvable formats found")
	}
	_ = lengthStr
	return info, nil
}

// buildFormat turns a single streamingData format object into a Format.
func buildFormat(m map[string]any, playerJS string, jsErr error, ctx *extractor.Context) (extractor.Format, error) {
	f := extractor.Format{
		FormatID:   fmt.Sprintf("%d", extractor.IntOrNone(m["itag"])),
		VCodec:     mimeVideoCodec(extractor.StrOrNone(m["mimeType"])),
		ACodec:     mimeAudioCodec(extractor.StrOrNone(m["mimeType"])),
		Width:      extractor.IntOrNone(m["width"]),
		Height:     extractor.IntOrNone(m["height"]),
		Filesize:   int64(extractor.FloatOrNone(m["contentLength"])),
		FormatNote: extractor.StrOrNone(m["qualityLabel"]),
		TBR:        extractor.FloatOrNone(m["bitrate"]) / 1000,
	}

	// Direct URL (unciphered progressive/adaptive).
	if u := extractor.StrOrNone(m["url"]); u != "" {
		f.URL = u
		f.Protocol, f.Ext = classifyURL(u, extractor.StrOrNone(m["mimeType"]))
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
	deob, err := extractor.DeobfuscateSignature(playerJS, sig)
	if err != nil {
		return f, err
	}
	sep := "&"
	if strings.Contains(base, "?") {
		sep = "&"
	}
	f.URL = base + sep + sp + "=" + url.QueryEscape(deob)
	f.Protocol, f.Ext = classifyURL(f.URL, extractor.StrOrNone(m["mimeType"]))
	return f, nil
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
