// Package pornhub implements an extractor for pornhub.com (and its mirrors,
// plus thumbzilla.com), ported from yt-dlp's yt_dlp/extractor/pornhub.py.
//
// PornHub serves its media list through a JavaScript object literal named
// `flashvars_<digits>` embedded in the watch page. That object carries
// `mediaDefinitions` (a list of {videoUrl, quality}), the thumbnail, the
// duration and an optional closed-caption URL. When the object is absent the
// page falls back to plain `media_*` / `quality_*` / `qualityItems_*` JS
// variable assignments (PornHub's TV platform build), and finally to
// `class="downloadBtn"` anchors. Every URL is then classified: .mpd goes to the
// DASH downloader, .m3u8 to the native HLS downloader, everything else to the
// plain HTTP downloader with a height parsed from the path (`720p_1500k`).
//
// The base class also holds the age-gate cookie values and the per-format
// Origin/Referer headers (PornHub answers 412 to manifest requests missing
// them).
package pornhub

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"

	"yt-dlp-go/extractor"
)

// hostRE is PornHubBaseIE._PORNHUB_HOST_RE: the canonical host plus the Tor
// mirror. The `host` group is optional because the mirror has no second-level
// name to reuse, so callers fall back to pornhub.com when it is empty.
const hostRE = `(?:(?P<host>pornhub(?:premium)?\.(?:com|net|org))|pornhubvybmsymdol4iibwgwtkpwmeyd6luq2gxajgjzfjvotyt5zhyd\.onion)`

// init registers every PornHub extractor in one explicit list.
//
// The order matters: MatchURL returns the first extractor whose Match()
// accepts a URL, and PornHubPagedVideoListIE is a catch-all that matches any
// PornHub path. Registering from a single place (rather than one init() per
// type) keeps that order explicit instead of leaving it to the file-name
// ordering Go initialises package-level variables in — playlist.go sorts
// before video.go, so the catch-all would otherwise be consulted first and
// claim watch-page URLs as listings.
func init() {
	// Most specific first, catch-all last.
	extractor.Register(PornHubIE{})
	extractor.Register(PornHubUserVideosUploadIE{})
	extractor.Register(PornHubUserIE{})
	extractor.Register(PornHubPlaylistIE{})
	extractor.Register(PornHubPagedVideoListIE{})
}

// Age-gate cookies PornHub's interstitial sets once the visitor confirms the
// disclaimer (PornHubBaseIE._set_age_cookies). The site itself sets
// accessAgeDisclaimerPH=2; upstream notes that in a comment.
var ageCookies = []struct{ name, value string }{
	{"age_verified", "1"},
	{"accessAgeDisclaimerPH", "1"},
	{"accessAgeDisclaimerUK", "1"},
	{"accessPH", "1"},
}

// ageCookieHeader renders the age-gate cookies as a Cookie request header.
//
// yt-dlp mutates the cookie jar directly; yt-dlp-go has no runtime set-cookie
// API, so the extractor sends them as an explicit header on the watch-page
// request instead. The jar (--cookies) is still consulted by net/http, and
// because headerTransport only fills headers the caller left empty, an explicit
// cookie set by the user still wins.
func ageCookieHeader() string {
	parts := make([]string, 0, len(ageCookies))
	for _, c := range ageCookies {
		parts = append(parts, c.name+"="+c.value)
	}
	return strings.Join(parts, "; ")
}

// siteHeaders mirrors PornHubBaseIE._get_headers: Origin and Referer are
// required on manifest requests or the CDN answers 412 Precondition Failed.
func siteHeaders(host string) map[string]string {
	return map[string]string{
		"Origin":  "https://www." + host,
		"Referer": "https://www." + host + "/",
	}
}

// ---- small porting helpers, mirroring yt_dlp.utils ----

// strToInt is yt-dlp's `str_to_int`: a relaxed integer parse that tolerates
// thousands separators (`1,234` / `1.234` / `1+234`). Returns 0 when the input
// is not a recognisable number, which matches int_or_none's None -> the
// extractor leaves the field unset.
func strToInt(s string) int {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	var b strings.Builder
	for _, r := range s {
		switch r {
		case ',', '.', '+', ' ':
			// grouping separators, dropped
		default:
			b.WriteRune(r)
		}
	}
	out := b.String()
	if out == "" {
		return 0
	}
	n, err := parseInt(out)
	if err != nil {
		return 0
	}
	return n
}

// removeQuotes is `remove_quotes`: strips one matching pair of surrounding
// single or double quotes.
func removeQuotes(s string) string {
	if len(s) < 2 {
		return s
	}
	if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
		return s[1 : len(s)-1]
	}
	return s
}

// removeStart is `remove_start`.
func removeStart(s, prefix string) string {
	if strings.HasPrefix(s, prefix) {
		return s[len(prefix):]
	}
	return s
}

// determineExt is `determine_ext`: the extension is whatever follows the last
// dot in the path, provided it is alphanumeric. Trailing-slash forms
// (`/foo.mp4/`) are handled because PornHub's CDN sometimes appends a path
// segment after the extension.
func determineExt(u, defExt string) string {
	if u == "" {
		return defExt
	}
	path := u
	if i := strings.IndexAny(path, "?#"); i >= 0 {
		path = path[:i]
	}
	if !strings.Contains(path, ".") {
		return defExt
	}
	guess := path[strings.LastIndex(path, ".")+1:]
	guess = strings.TrimRight(guess, "/")
	for _, r := range guess {
		if !isAlphaNum(r) {
			return defExt
		}
	}
	if guess == "" {
		return defExt
	}
	return guess
}

func isAlphaNum(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
}

// urlOrNone returns "" for anything that is not an absolute HTTP(S) URL,
// matching yt-dlp's `url_or_none` guard used before every add_video_url call.
func urlOrNone(s string) string {
	if !strings.HasPrefix(s, "http://") && !strings.HasPrefix(s, "https://") {
		return ""
	}
	return s
}

// parseInt / parseIntBase are thin strconv wrappers kept here so the helpers
// above read the same way the Python originals do.
func parseInt(s string) (int, error) {
	n, err := parseIntBase(s, 10)
	return int(n), err
}

func parseIntBase(s string, base int) (int64, error) {
	return strconv.ParseInt(s, base, 64)
}

// firstGroup returns the first capture group of the first match, or "" when the
// pattern does not match. It is the yt-dlp `_search_regex(..., default=None)`
// idiom: a missing match is not an error, the caller just gets no value.
func firstGroup(re *regexp.Regexp, s string) string {
	if m := re.FindStringSubmatch(s); m != nil && len(m) > 1 {
		return m[1]
	}
	return ""
}

// metaContent is a minimal <meta name=... content=...> lookup. PornHub's page
// carries <meta name="twitter:title" content="...">, which is the preferred
// title source because flashvars mangles non-ASCII titles into whitespace.
// contentAttrRE reads a quoted content="..." / content='...' value. Two
// alternatives instead of a backreference, which RE2 does not support.
var contentAttrRE = regexp.MustCompile(`(?is)content=(?:"([^"]*)"|'([^']*)')`)

func metaContent(html, name string) string {
	re := regexp.MustCompile(`(?is)<meta[^>]+(?:name|property)=["']` + regexp.QuoteMeta(name) + `["'][^>]*>`)
	m := re.FindString(html)
	if m == "" {
		return ""
	}
	return unescapeHTML(quotedGroup(contentAttrRE.FindStringSubmatch(m)))
}

// strToIntRE combines the two steps every count extraction does: find the
// pattern, then relax-parse the captured digits.
func strToIntRE(re *regexp.Regexp, html string) int {
	return strToInt(firstGroup(re, html))
}

// ---- media URL collection ----

// videoURL is one collected candidate: the URL plus the quality (pixel height)
// the page reported for it, when known.
type videoURL struct {
	url     string
	quality int
}

// urlSet collects candidate media URLs in first-seen order while rejecting
// duplicates, mirroring upstream's video_urls list + video_urls_set pair.
type urlSet struct {
	items []videoURL
	seen  map[string]bool
}

func newURLSet() *urlSet { return &urlSet{seen: map[string]bool{}} }

func (s *urlSet) add(u string, quality int) {
	u = urlOrNone(u)
	if u == "" || s.seen[u] {
		return
	}
	s.seen[u] = true
	s.items = append(s.items, videoURL{url: u, quality: quality})
}

func (s *urlSet) empty() bool { return len(s.items) == 0 }

// ---- JS variable evaluation ----

// blockCommentRE matches /* ... */ comments, which PornHub scatters inside its
// variable assignments as anti-scraping noise.
var blockCommentRE = regexp.MustCompile(`(?s)/\*.*?\*/`)

// jsVars evaluates a run of `var name = <string expression>;` assignments and
// returns name -> value.
//
// This is `extract_js_vars` / `parse_js_value` from upstream. The expressions
// are only ever string literals, possibly split across several concatenated
// pieces (`"https://" + "cdn.example/" + "720p.mp4"`) and possibly referencing
// an earlier variable by name. Anything that is not a quoted literal is taken
// verbatim (upstream's remove_quotes leaves unquoted text as-is), so a value we
// cannot evaluate simply fails the url_or_none check later instead of aborting
// the whole extraction.
//
// Unlike the Python original this does not recurse through `+` with reduce;
// it walks the concatenation left to right, which is equivalent for assignment
// order (PornHub never forward-references).
func jsVars(assignments string) map[string]string {
	out := map[string]string{}
	cleaned := blockCommentRE.ReplaceAllString(assignments, "")
	for _, stmt := range strings.Split(cleaned, ";") {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}
		stmt = varKeywordRE.ReplaceAllString(stmt, "")
		name, value, ok := strings.Cut(stmt, "=")
		if !ok {
			continue
		}
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		out[name] = evalJSValue(value, out)
	}
	return out
}

var varKeywordRE = regexp.MustCompile(`\bvar\s+`)

// evalJSValue resolves a JS string expression: a chain of quoted literals and
// variable references joined by `+`.
func evalJSValue(expr string, vars map[string]string) string {
	var b strings.Builder
	for _, part := range strings.Split(expr, "+") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if v, ok := vars[part]; ok {
			b.WriteString(v)
			continue
		}
		b.WriteString(removeQuotes(part))
	}
	return b.String()
}

// ---- JSON extraction from HTML ----

// flashvarsRE locates the `var flashvars_<digits> = {...};` object literal.
//
// The trailing `;` in upstream's pattern anchors the non-greedy `{.+?}` so it
// cannot stop at the first `}` of a nested object. (Go's RE2 has no lazy
// quantifier semantics problem here: the negated-class form below is
// equivalent for well-formed JSON without nested braces at depth 1, and the
// balanced scan in extractJSONObject handles the general case.)
var flashvarsRE = regexp.MustCompile(`var\s+flashvars_\d+\s*=\s*(\{)`)

// extractJSONObject returns the balanced JSON object starting at the `{`
// captured by flashvarsRE. JSON allows braces inside string literals, so the
// scan skips over quoted runs and honours backslash escapes; without that,
// a title containing `}` would truncate the object.
func extractJSONObject(s string, open int) (string, bool) {
	if open < 0 || open >= len(s) || s[open] != '{' {
		return "", false
	}
	depth := 0
	inStr := false
	escaped := false
	for i := open; i < len(s); i++ {
		c := s[i]
		if escaped {
			escaped = false
			continue
		}
		switch {
		case c == '\\' && inStr:
			escaped = true
		case c == '"':
			inStr = !inStr
		case inStr:
			// content of a string literal: no structural meaning
		case c == '{':
			depth++
		case c == '}':
			depth--
			if depth == 0 {
				return s[open : i+1], true
			}
		}
	}
	return "", false
}

// parseFlashvars pulls the flashvars object out of the page and decodes it.
// A page without the object yields a nil map without an error, which is the
// signal to try the JS-variable fallbacks.
func parseFlashvars(html string) (map[string]any, error) {
	loc := flashvarsRE.FindStringSubmatchIndex(html)
	if loc == nil || len(loc) < 4 {
		return nil, nil
	}
	obj, ok := extractJSONObject(html, loc[2])
	if !ok {
		return nil, nil
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(obj), &m); err != nil {
		return nil, err
	}
	return m, nil
}

// ---- HTML text helpers ----

var (
	// elementAttributes is used by findElements to locate the opening tag of an
	// element carrying a given attribute/value pair.
	tagNameRE = regexp.MustCompile(`<([a-zA-Z][\w:.-]*)`)

	// htmlEntityRE matches a single HTML entity for unescapeHTML.
	htmlEntityRE = regexp.MustCompile(`&(#?[0-9a-zA-Z]{1,10});`)
)

// unescapeHTML decodes the numeric and named entities that actually occur in
// PornHub's markup. It is deliberately narrower than a full HTML5 entity table
// (which would be several hundred entries); the fallback leaves unrecognised
// entities untouched rather than mangling them.
func unescapeHTML(s string) string {
	named := map[string]string{
		"amp": "&", "lt": "<", "gt": ">", "quot": `"`, "apos": "'",
		"nbsp": " ", "#39": "'", "#34": `"`, "#x27": "'", "#x22": `"`,
	}
	return htmlEntityRE.ReplaceAllStringFunc(s, func(m string) string {
		inner := m[1 : len(m)-1]
		if v, ok := named[strings.ToLower(inner)]; ok {
			return v
		}
		if strings.HasPrefix(inner, "#") {
			var code int64
			var err error
			body := inner[1:]
			if len(body) > 0 && (body[0] == 'x' || body[0] == 'X') {
				code, err = parseIntBase(body[1:], 16)
			} else {
				code, err = parseIntBase(body, 10)
			}
			if err == nil && code > 0 && code < 0x110000 {
				return string(rune(code))
			}
		}
		return m
	})
}

// cleanHTML is `clean_html`: collapse whitespace, turn <br> and paragraph
// breaks into newlines, strip every remaining tag, then decode entities.
func cleanHTML(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	s = regexp.MustCompile(`(?i)\s?<br\s*/?>\s?`).ReplaceAllString(s, "\n")
	s = regexp.MustCompile(`(?i)</p>\s*<p[^>]*>`).ReplaceAllString(s, "\n")
	s = regexp.MustCompile(`<[^>]*>`).ReplaceAllString(s, "")
	return strings.TrimSpace(unescapeHTML(s))
}
