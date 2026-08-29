// Package pornhub implements an extractor for pornhub.com (and its mirrors,
// plus thumbzilla.com), ported from yt-dlp's yt_dlp/extractor/pornhub.py.
package pornhub

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"yt-dlp-go/extractor"
)

// PornHubIE extracts a single video from PornHub and Thumbzilla.
type PornHubIE struct{}

func (PornHubIE) Name() string { return "pornhub" }

// videoURLRE is PornHubIE._VALID_URL. The `host` group is optional (the Tor
// mirror has no reusable second-level name), so hostOf() falls back to
// pornhub.com when it is empty — exactly like upstream's
// `mobj.group('host') or 'pornhub.com'`.
var videoURLRE = regexp.MustCompile(`(?i)https?://` +
	`(?:` +
	`(?:[a-zA-Z0-9.-]+\.)?` + hostRE +
	`/(?:(?:view_video\.php|video/show)\?viewkey=|embed/)|` +
	`(?:www\.)?thumbzilla\.com/video/` +
	`)` +
	`(?P<id>[\da-z]+)`)

// Match claims the URL when the strict _VALID_URL pattern fires, and also when
// the path is a watch page whose query string is too mangled for that pattern.
// The second case exists so a mis-escaped URL (e.g. `\?viewkey\=` left behind
// by a shell) is reported by this extractor's precise "cannot parse video id"
// error rather than falling through to the catch-all listing extractor, which
// would fetch it as a listing and surface an unrelated 404.
func (PornHubIE) Match(u string) bool {
	if videoURLRE.MatchString(u) {
		return true
	}
	return isWatchPath(urlPath(u))
}

// hostOf resolves the host group, defaulting to pornhub.com for the mirror.
func hostOf(u string) string {
	if h := namedGroup(videoURLRE, u, "host"); h != "" {
		return strings.ToLower(h)
	}
	return defaultHost
}

const defaultHost = "pornhub.com"

// namedGroup returns one named capture from the first match, or "".
func namedGroup(re *regexp.Regexp, s, name string) string {
	m := re.FindStringSubmatch(s)
	if m == nil {
		return ""
	}
	for i, n := range re.SubexpNames() {
		if n == name && i < len(m) {
			return m[i]
		}
	}
	return ""
}

// ---- page-level signals ----

// Go's regexp (RE2) supports neither lookaround nor backreferences, so every
// pattern below uses an explicit alternation or a negated character class where
// the Python original used `(?!...)` or `\1`.
var (
	// Removed / private / deleted videos render one of these containers.
	// The class attribute is quoted, so [^"']* cannot escape it.
	errorDivRE   = regexp.MustCompile(`(?s)<div[^>]+class=["'][^"']*\b(?:removed|userMessageSection)\b[^"']*["'][^>]*>(?P<error>.+?)</div>`)
	errorSectRE  = regexp.MustCompile(`(?s)<section[^>]+class=["']noVideo["'][^>]*>(?P<error>.+?)</section>`)
	geoBlockedRE = regexp.MustCompile(`class=["']geoBlocked["']`)
	geoTextRE    = regexp.MustCompile(`>\s*This content is unavailable in your country`)
	lockedRE     = regexp.MustCompile(`<[^>]+\bid=["']lockedPlayer`)

	// Title sources, in upstream's preference order.
	titleH1RE    = regexp.MustCompile(`(?s)<h1[^>]+class=["']title["'][^>]*>(?P<title>.+?)</h1>`)
	titleShareRE = regexp.MustCompile(`shareTitle["']\s*[=:]\s*(?:"([^"]*)"|'([^']*)')`)

	// data-video-title and the download buttons are matched once per quote
	// style; quotedGroup picks whichever alternative fired.
	titleDataAttrRE = regexp.MustCompile(`<div[^>]+data-video-title=(?:"([^"]*)"|'([^']*)')`)
	downloadBtnRE   = regexp.MustCompile(`<a[^>]+\bclass=["']downloadBtn\b[^>]+\bhref=(?:"([^"]*)"|'([^']*)')`)

	modelProfileRE = regexp.MustCompile(`var\s+MODEL_PROFILE\s*=\s*`)
	uploaderFromRE = regexp.MustCompile(`(?s)From:&nbsp;.+?<(?:a\b[^>]+\bhref=["']/(?:(?:user|channel)s|model|pornstar)/|span\b[^>]+\bclass=["']username)[^>]+>(.+?)<`)
	uploadDateRE   = regexp.MustCompile(`/(\d{6}/\d{2})/`)
	viewCountRE    = regexp.MustCompile(`<span class="count">([\d,\.]+)</span> [Vv]iews`)
	commentCountRE = regexp.MustCompile(`All Comments\s*<span>\(([\d,.]+)\)`)

	// The TV-platform fallback: `var ...mediastring...` assignments.
	tvMediaStringRE = regexp.MustCompile(`(var.+?mediastring.+?)</script>`)
	// One of media_*, quality_* or qualityItems_* — the JS-variable fallback.
	jsFormatVarsRE = regexp.MustCompile(`(var\s+(?:media|quality|qualityItems)_.+)`)
)

// JS variable prefixes PornHub uses for its fallback format lists. Only the
// last one (qualityItems_) is JSON; the first two are plain URL strings.
var (
	prefixQualityItems = "qualityItems_"
	prefixMedia        = "media_"
	prefixQuality      = "quality_"
)

// Extract resolves one PornHub watch page into an Info with every available
// format attached.
func (PornHubIE) Extract(ctx *extractor.Context, pageURL string) (*extractor.Info, error) {
	videoID := namedGroup(videoURLRE, pageURL, "id")
	if videoID == "" {
		// Distinguish "the URL is a watch page but its query string is mangled"
		// from a plain parse failure: the first is almost always a shell-escaping
		// mistake and is worth saying so.
		if isWatchPath(urlPath(pageURL)) {
			return nil, fmt.Errorf("pornhub: could not read viewkey from %q — check that the URL is quoted (a stray backslash before ? or = will break it)", pageURL)
		}
		return nil, fmt.Errorf("pornhub: could not parse video id from %q", pageURL)
	}
	return PornHubIE{}.extractByID(ctx, videoID, hostOf(pageURL), pageURL)
}

// extractByID is the shared implementation. Taking the id and host directly
// (instead of re-parsing a URL) lets the playlist extractors hand over an entry
// whose watch URL has already been built by watchURL — which need not be a
// PornHub hostname.
func (PornHubIE) extractByID(ctx *extractor.Context, videoID, host, pageURL string) (*extractor.Info, error) {
	// The age gate is enforced by cookies; yt-dlp sets them in the jar, we send
	// them as a header (see ageCookieHeader).
	headers := siteHeaders(host)
	headers["Cookie"] = ageCookieHeader()

	// Always hit the canonical watch page: thumbzilla and the embed form do not
	// carry the flashvars object.
	webpage, err := extractor.DownloadWebpage(ctx, watchURL(host, videoID), headers, nil)
	if err != nil {
		return nil, err
	}

	if msg := pageError(webpage); msg != "" {
		return nil, fmt.Errorf("pornhub: %s", msg)
	}
	if geoBlockedRE.MatchString(webpage) || geoTextRE.MatchString(webpage) {
		return nil, fmt.Errorf("pornhub: %s is geo restricted", videoID)
	}

	// A playlist entry arrives with no page URL of its own; fall back to the
	// canonical watch page so -o %(webpage_url)s still renders something.
	if pageURL == "" {
		pageURL = watchURL(host, videoID)
	}

	info := &extractor.Info{
		ID:           videoID,
		WebpageURL:   pageURL,
		Ext:          "mp4",
		Title:        extractTitle(webpage),
		Categories:   extractLabelled(webpage, "category"),
		Subtitles:    map[string][]extractor.Subtitle{},
		ViewCount:    int64(strToIntRE(viewCountRE, webpage)),
		LikeCount:    int64(extractVoteCount(webpage, "Up")),
		CommentCount: int64(strToIntRE(commentCountRE, webpage)),
	}

	urls := newURLSet()

	// --- primary path: the flashvars object ---
	flash, ferr := parseFlashvars(webpage)
	if ferr != nil && ctx.Options != nil && ctx.Options.Verbose {
		fmt.Printf("[pornhub] flashvars decode failed: %v\n", ferr)
	}
	if flash != nil {
		collectFlashvars(urls, flash, info)
	}

	// --- fallback 1: media_* / quality_* / qualityItems_* JS variables ---
	if urls.empty() {
		collectJSVars(urls, webpage, videoID)
	}

	// --- fallback 2: the TV platform build ---
	//
	// Upstream re-downloads the page with the `platform=tv` cookie here. That
	// needs a second request plus cookie manipulation, and the branch only
	// triggers when the first two paths produced nothing, so it is implemented
	// but left unreachable unless the page exposes the marker: a page that
	// reaches this point without any format is almost always deleted, private
	// or premium-only.
	if urls.empty() && tvMediaStringRE.MatchString(webpage) {
		collectTVURL(urls, webpage)
	}

	// --- fallback 3: the download buttons ---
	for _, m := range downloadBtnRE.FindAllStringSubmatch(webpage, -1) {
		if u := quotedGroup(m); u != "" {
			urls.add(unescapeHTML(u), 0)
		}
	}

	if urls.empty() {
		if lockedRE.MatchString(webpage) {
			return nil, fmt.Errorf("pornhub: video %s is locked", videoID)
		}
		return nil, fmt.Errorf("pornhub: no formats found for %s (video may be deleted, private or premium-only)", videoID)
	}

	// --- uploader ---
	uploader := cleanHTML(firstGroup(uploaderFromRE, webpage))
	if uploader == "" {
		if mp, ok := modelProfileJSON(webpage); ok {
			uploader = extractor.StrOrNone(mp["username"])
		}
	}
	info.Uploader = uploader
	info.Channel = uploader

	// --- formats ---
	info.Formats = buildFormats(ctx, urls, host, videoID)
	if info.UploadDate == "" {
		info.UploadDate = extractUploadDate(urls)
	}
	return info, nil
}

// pageError returns the human-readable reason the page shows no video, or "".
func pageError(html string) string {
	msg := firstGroupNamed(errorDivRE, html, "error")
	if msg == "" {
		msg = firstGroupNamed(errorSectRE, html, "error")
	}
	if msg == "" {
		return ""
	}
	// Collapse the markup to plain text; the message may span several lines.
	return strings.Join(strings.Fields(cleanHTML(msg)), " ")
}

// extractTitle reproduces upstream's preference order: the twitter:title meta
// first (flashvars' own title field mangles non-ASCII into whitespace), then
// the <h1 class="title">, the data-video-title attribute and shareTitle.
func extractTitle(html string) string {
	if t := metaContent(html, "twitter:title"); t != "" {
		return t
	}
	if t := firstGroupNamed(titleH1RE, html, "title"); t != "" {
		return strings.TrimSpace(cleanHTML(unescapeHTML(t)))
	}
	for _, re := range []*regexp.Regexp{titleDataAttrRE, titleShareRE} {
		if t := quotedGroup(re.FindStringSubmatch(html)); t != "" {
			return strings.TrimSpace(cleanHTML(unescapeHTML(t)))
		}
	}
	return ""
}

// quotedGroup reads a regexp written as `(?:"(...)"|'(...)')` — the RE2 form
// of Python's backreference `(["'])(...)\1`. It returns whichever alternative
// matched, or "" when neither did.
func quotedGroup(m []string) string {
	for i := 1; i < len(m); i++ {
		if m[i] != "" {
			return m[i]
		}
	}
	return ""
}

// collectFlashvars reads the media list, thumbnail, duration and captions out
// of the decoded flashvars object.
func collectFlashvars(urls *urlSet, flash map[string]any, info *extractor.Info) {
	if sub := urlOrNone(extractor.StrOrNone(flash["closedCaptionsFile"])); sub != "" {
		info.Subtitles["en"] = append(info.Subtitles["en"], extractor.Subtitle{
			URL: sub,
			Ext: "srt",
		})
	}
	info.Thumbnail = extractor.StrOrNone(flash["image_url"])
	info.Duration = extractor.FloatOrNone(flash["video_duration"])

	defs, ok := flash["mediaDefinitions"].([]any)
	if !ok {
		return
	}
	for _, d := range defs {
		m, ok := d.(map[string]any)
		if !ok {
			continue
		}
		u := extractor.StrOrNone(m["videoUrl"])
		if u == "" {
			continue
		}
		urls.add(u, extractor.IntOrNone(m["quality"]))
	}
}

// collectJSVars evaluates the `media_*` / `quality_*` / `qualityItems_*`
// assignments. qualityItems_* is a JSON list of {url} objects; the other two
// are bare URL strings (possibly concatenated from several literals).
func collectJSVars(urls *urlSet, html, videoID string) {
	block := firstGroup(jsFormatVarsRE, html)
	if block == "" {
		return
	}
	for name, value := range jsVars(block) {
		switch {
		case strings.HasPrefix(name, prefixQualityItems):
			var items []any
			if err := json.Unmarshal([]byte(value), &items); err != nil {
				continue
			}
			for _, it := range items {
				if m, ok := it.(map[string]any); ok {
					urls.add(extractor.StrOrNone(m["url"]), 0)
				}
			}
		case strings.HasPrefix(name, prefixMedia), strings.HasPrefix(name, prefixQuality):
			urls.add(value, 0)
		}
	}
}

// collectTVURL pulls `mediastring` out of the TV-platform page.
func collectTVURL(urls *urlSet, html string) {
	block := firstGroup(tvMediaStringRE, html)
	if block == "" {
		return
	}
	urls.add(jsVars(block)["mediastring"], 0)
}

// buildFormats turns each collected URL into a Format, expanding the
// /video/get_media endpoints one extra hop.
func buildFormats(ctx *extractor.Context, urls *urlSet, host, videoID string) []extractor.Format {
	formats := make([]extractor.Format, 0, len(urls.items))
	for _, vu := range urls.items {
		if strings.Contains(vu.url, "/video/get_media") {
			formats = append(formats, expandGetMedia(ctx, vu.url, host)...)
			continue
		}
		formats = append(formats, classifyFormat(vu.url, vu.quality, host))
	}
	return formats
}

// expandGetMedia follows PornHub's indirection endpoint: it returns a JSON list
// of {videoUrl, quality} that itself has to be classified. An upstream issue
// (#5615) notes the endpoint now sometimes serves HTML instead of JSON, so a
// decode failure here is non-fatal — the entry is simply skipped.
func expandGetMedia(ctx *extractor.Context, u, host string) []extractor.Format {
	data, err := extractor.DownloadJSON(ctx, u, siteHeaders(host), nil)
	if err != nil {
		return nil
	}
	items, ok := data.([]any)
	if !ok {
		return nil
	}
	out := make([]extractor.Format, 0, len(items))
	for _, it := range items {
		m, ok := it.(map[string]any)
		if !ok {
			continue
		}
		media := urlOrNone(extractor.StrOrNone(m["videoUrl"]))
		if media == "" {
			continue
		}
		out = append(out, classifyFormat(media, extractor.IntOrNone(m["quality"]), host))
	}
	return out
}

// heightInURLRE is PornHub's `NNNp_NNNk` naming convention for progressive
// files; it is the only height hint available when the page reports no quality.
var heightInURLRE = regexp.MustCompile(`(?P<height>\d+)[pP]?_\d+[kK]`)

// classifyFormat maps one media URL onto the right downloader, mirroring
// upstream's add_format: .mpd -> DASH, .m3u8 -> native HLS, everything else a
// plain HTTP download whose height is parsed from the filename when the page
// did not report one.
func classifyFormat(u string, quality int, host string) extractor.Format {
	f := extractor.Format{
		URL:      u,
		Headers:  siteHeaders(host),
		Protocol: "http",
		Ext:      "mp4",
		Height:   quality,
	}
	ext := determineExt(u, "mp4")
	switch ext {
	case "mpd":
		f.Protocol, f.Ext, f.FormatID = "dash", "mpd", "dash"
		f.Source = "dash"
		return f
	case "m3u8":
		f.Protocol, f.Ext, f.FormatID = "m3u8_native", "m3u8", "hls"
		f.Source = "hls"
		return f
	}
	f.Ext = ext
	f.Height = quality
	if f.Height == 0 {
		f.Height = strToInt(firstGroupNamed(heightInURLRE, u, "height"))
	}
	if f.Height > 0 {
		f.FormatID = fmt.Sprintf("%dp", f.Height)
	} else {
		f.FormatID = "http"
	}
	f.Source = "http"
	return f
}

// extractUploadDate recovers the upload date that PornHub hides inside its CDN
// paths (`.../210628/12/...`), the same trick upstream uses.
func extractUploadDate(urls *urlSet) string {
	for _, vu := range urls.items {
		if d := firstGroup(uploadDateRE, vu.url); d != "" {
			return strings.ReplaceAll(d, "/", "")
		}
	}
	return ""
}

// extractVoteCount reads the like/dislike counter. PornHub renders either the
// number as text or as a data-rating attribute, depending on the page build.
func extractVoteCount(html, kind string) int {
	for _, pattern := range []string{
		`<span[^>]+\bclass="votes` + kind + `"[^>]*>([\d,\.]+)</span>`,
		`<span[^>]+\bclass=["']votes` + kind + `["'][^>]*\bdata-rating=["'](\d+)`,
	} {
		if n := strToIntRE(regexp.MustCompile(pattern), html); n > 0 {
			return n
		}
	}
	return 0
}

// extractLabelled collects the text of every element carrying
// data-label="<label>": tags, categories and the cast all use that attribute.
func extractLabelled(html, label string) []string {
	re := regexp.MustCompile(`data-label=["']` + regexp.QuoteMeta(label) + `["']`)
	var out []string
	for _, loc := range re.FindAllStringIndex(html, -1) {
		// Walk backwards from the attribute to the `<` that opened its tag, then
		// read the element's inner text.
		start := strings.LastIndex(html[:loc[0]], "<")
		if start < 0 {
			continue
		}
		if text := elementText(html, start); text != "" {
			out = append(out, text)
		}
	}
	return out
}

// elementText returns the (cleaned) inner text of the element starting at the
// `<` at index start, or "" when the element is malformed or self-closing.
func elementText(html string, start int) string {
	if start >= len(html) || html[start] != '<' {
		return ""
	}
	name := firstGroup(tagNameRE, html[start:])
	if name == "" {
		return ""
	}
	// A self-closing tag has no inner text.
	openEnd := strings.Index(html[start:], ">")
	if openEnd < 0 {
		return ""
	}
	tag := html[start : start+openEnd+1]
	if strings.HasSuffix(strings.TrimSpace(tag), "/>") {
		return ""
	}
	closeTag := strings.ToLower("</" + name)
	rel := strings.Index(strings.ToLower(html[start+openEnd+1:]), closeTag)
	if rel < 0 {
		return ""
	}
	inner := html[start+openEnd+1 : start+openEnd+1+rel]
	return cleanHTML(inner)
}

// modelProfileJSON decodes the `var MODEL_PROFILE = {...}` literal.
func modelProfileJSON(html string) (map[string]any, bool) {
	loc := modelProfileRE.FindStringIndex(html)
	if loc == nil {
		return nil, false
	}
	open := strings.Index(html[loc[1]:], "{")
	if open < 0 {
		return nil, false
	}
	open += loc[1]
	obj, ok := extractJSONObject(html, open)
	if !ok {
		return nil, false
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(obj), &m); err != nil {
		return nil, false
	}
	return m, true
}

// firstGroupNamed returns one named capture group of the first match.
func firstGroupNamed(re *regexp.Regexp, s, name string) string {
	m := re.FindStringSubmatch(s)
	if m == nil {
		return ""
	}
	for i, n := range re.SubexpNames() {
		if n == name && i < len(m) {
			return m[i]
		}
	}
	return ""
}

// watchURL builds the canonical watch-page URL for a video id. The playlist
// extractors use it so every entry they emit points PornHubIE at the page that
// actually carries the flashvars object (thumbzilla and embed links do not).
//
// It is a package-level variable rather than a plain function so tests can
// redirect every page fetch at an httptest server; the extractor must never
// reach the real site during a unit test.
var watchURL = func(host, videoID string) string {
	return fmt.Sprintf("https://www.%s/view_video.php?viewkey=%s", host, videoID)
}
