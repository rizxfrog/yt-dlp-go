// Package cg51 implements an extractor for 51cg1.com archive pages
// (https://51cg1.com/archives/<id>/).
//
// Each article embeds an HLS player. The reference yt-dlp plugin for this site
// drives a headless Chromium (Playwright) to let the page's JavaScript build the
// .m3u8 URL and inline the lazy-loaded preview images before scraping the DOM.
// yt-dlp-go has no browser, so this extractor works from the served HTML alone:
// it looks for an .m3u8 reference in the markup (script tags, iframe/video/data
// attributes and player configs) and collects whatever <img> sources the server
// already rendered. The Python plugin's DOM cleanup, caption extraction and
// data:-URI thumbnail decoding are reproduced below so both implementations
// yield the same metadata for well-formed pages.
//
// Pages whose manifest is only assembled at runtime by JavaScript are not
// supported; use the Playwright-based plugin for those.
package cg51

import (
	"fmt"
	"regexp"
	"strings"

	"yt-dlp-go/extractor"
)

// Cg51IE extracts from 51cg1.com archive pages.
type Cg51IE struct{}

func init() { extractor.Register(Cg51IE{}) }

func (Cg51IE) Name() string { return "51cg" }

// The Python plugin's _VALID_URL.
var cg51URLRE = regexp.MustCompile(`(?i)https?://(?:www\.)?51cg1\.com/archives/(\d+)/?`)

func (Cg51IE) Match(u string) bool { return cg51URLRE.MatchString(u) }

// User agent and cookies mirror the Python plugin so the site serves the same
// markup (the `user-choose` cookie suppresses the interstitial nag).
const cg51UserAgent = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36"

// noiseClasses are the class tokens the Python plugin's _CLEANUP_SCRIPT removes
// from the article body before extracting the description.
var noiseClasses = []string{"txt-apps", "dplayer", "table-responsive", "content-copyright", "content-tabs", "btn-download"}

// blockTagRE matches the opening/closing tags whose nesting stripNoiseBlocks
// tracks while locating the end of a noise block.
var blockTagRE = regexp.MustCompile(`(?is)<(/?)` + "(" + "div|p|blockquote" + `)\b[^>]*?>`)

// stripNoiseBlocks removes the elements listed in noiseClasses plus every
// <blockquote>, mirroring the DOM removals in the Python plugin. Removal is
// done with a balanced-tag scan so nested <div>s (the dplayer container nests
// several layers) disappear whole instead of being cut short by a regex.
func stripNoiseBlocks(html string) string {
	var out strings.Builder
	pos := 0
	for pos < len(html) {
		open := blockTagRE.FindStringSubmatchIndex(html[pos:])
		if open == nil {
			out.WriteString(html[pos:])
			break
		}
		start := pos + open[0]
		out.WriteString(html[pos:start])

		// Group indices are relative to the searched slice html[pos:].
		openIdx := pos + open[1]
		isClose := open[2] >= 0 && open[2] < open[3] && html[pos+open[2]:pos+open[3]] == "/"
		if isClose {
			// Stray closing tag; keep it and move on.
			out.WriteString(html[start:openIdx])
			pos = openIdx
			continue
		}

		tag := strings.ToLower(html[pos+open[4] : pos+open[5]])
		openText := strings.ToLower(html[start:openIdx])
		noise := tag == "blockquote"
		if !noise {
			for _, class := range noiseClasses {
				if strings.Contains(openText, class) {
					noise = true
					break
				}
			}
		}
		if !noise {
			out.WriteString(html[start:openIdx])
			pos = openIdx
			continue
		}

		// Drop the whole element: scan forward for the matching close tag while
		// tracking same-tag nesting.
		rest := html[openIdx:]
		depth := 1
		endOff := -1
		for _, cm := range blockTagRE.FindAllStringSubmatchIndex(rest, -1) {
			closeTag := rest[cm[4]:cm[5]]
			if !strings.EqualFold(closeTag, tag) {
				continue
			}
			if cm[2] >= 0 && cm[2] < cm[3] && rest[cm[2]:cm[3]] == "/" {
				depth--
				if depth == 0 {
					endOff = openIdx + cm[1]
					break
				}
			} else {
				depth++
			}
		}
		if endOff < 0 {
			// Unterminated block: drop the rest of the document.
			break
		}
		pos = endOff
	}
	return out.String()
}

// Boilerplate stripped from the description text.
var cg51DescriptionBlacklist = []string{
	"热门吃瓜",
	"版权声明：本文著作权归 51吃瓜网所有， 任何媒体、网站或个人未经授权不得复制、转载、摘编或以其他方式使用， 否则将依法追究其法律责任。",
}

// Extract scrapes an archive page for its HLS manifest, title, description and
// preview images.
func (Cg51IE) Extract(ctx *extractor.Context, pageURL string) (*extractor.Info, error) {
	if !cg51URLRE.MatchString(pageURL) {
		return nil, fmt.Errorf("cg51: not a 51cg1.com archive URL: %q", pageURL)
	}

	headers := map[string]string{
		"User-Agent": cg51UserAgent,
		"Referer":    pageURL,
		"Cookie":     "user-choose=true",
	}
	html, err := extractor.DownloadWebpage(ctx, pageURL, headers, nil)
	if err != nil {
		return nil, fmt.Errorf("cg51: %w", err)
	}
	return extractHTML(html, pageURL)
}

// extractHTML parses a downloaded archive page. Split out from Extract for
// tests, which feed fixture markup directly instead of a network fetch.
func extractHTML(html, pageURL string) (*extractor.Info, error) {
	m := cg51URLRE.FindStringSubmatch(pageURL)
	if m == nil {
		return nil, fmt.Errorf("cg51: not a 51cg1.com archive URL: %q", pageURL)
	}
	videoID := m[1]

	title := strings.TrimSpace(extractor.CleanHTML(firstGroup(cg51TitleRE, html)))
	if title == "" {
		return nil, fmt.Errorf("cg51: title not found on %s", pageURL)
	}

	m3u8 := findManifestURL(html)
	if m3u8 == "" {
		return nil, fmt.Errorf("cg51: no HLS playlist (.m3u8) found on %s (page may require JavaScript)", pageURL)
	}

	info := &extractor.Info{
		ID:         videoID,
		Title:      title,
		WebpageURL: pageURL,
		Ext:        "mp4",
		Description: sanitizeDescription(
			extractor.CleanHTML(stripNoiseBlocks(extractPostContent(html))),
			pageURL,
		),
		// Per-format headers so the fragment downloader can fetch the manifest
		// and its segments (the CDN rejects requests without Origin/Referer).
		Formats: []extractor.Format{{
			FormatID: "hls",
			URL:      m3u8,
			Protocol: "m3u8_native",
			Ext:      "m3u8",
			Source:   "hls",
			Headers: map[string]string{
				"Origin":     "https://51cg1.com",
				"Referer":    pageURL,
				"User-Agent": cg51UserAgent,
			},
		}},
	}
	info.Thumbnail = pickThumbnail(extractPostContent(html))
	return info, nil
}

var (
	cg51TitleRE   = regexp.MustCompile(`(?is)<h1[^>]*class\s*=\s*"[^"]*\bpost-title\b[^"]*"[^>]*>(.*?)</h1>`)
	openContentRE = regexp.MustCompile(`(?is)<div[^>]*?(?:itemprop\s*=\s*"articleBody"|class\s*=\s*"[^"]*\bpost-content\b[^"]*")[^>]*>`)
	imgTagRE      = regexp.MustCompile(`(?is)<img\b[^>]*>`)
	imgAttrRE     = regexp.MustCompile(`(?i)\b(src|data-src|data-original|data-xkrkllgl)\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s>]+))`)
	m3u8RE        = regexp.MustCompile(`https?://[^"'\s<>\)]+?\.m3u8[^"'\s<>\)]*`)
	srcAttrRE     = regexp.MustCompile(`(?i)\bsrc\s*=\s*(?:"([^"]+)"|'([^']+)')`)
)

// extractPostContent returns the article body HTML, preferring the
// itemprop="articleBody" container the Python plugin targets.
//
// The body is found by scanning for balanced <div>...</div> nesting from the
// opening tag rather than by regex, because the body itself contains nested
// divs (player, download widgets, tables) that a non-greedy pattern would cut
// short.
func extractPostContent(html string) string {
	loc := openContentRE.FindStringIndex(html)
	if loc == nil {
		return ""
	}
	return innerBlock(html, loc[1], "div")
}

// innerBlock returns the markup between the tag opened just before start and its
// matching closing tag, tracking nesting of the given tag name. Self-closing
// forms (<div/>) do not affect the depth.
func innerBlock(html string, start int, tag string) string {
	re := regexp.MustCompile(`(?i)<(/?)` + regexp.QuoteMeta(tag) + `\b[^>]*?(/?)>`)
	depth := 1
	for _, m := range re.FindAllStringSubmatchIndex(html[start:], -1) {
		isClose := html[start+m[2]:start+m[3]] == "/"
		isSelfClose := html[start+m[4]:start+m[5]] == "/"
		if isSelfClose {
			continue
		}
		if isClose {
			depth--
			if depth == 0 {
				return html[start : start+m[0]]
			}
			continue
		}
		depth++
	}
	return html[start:]
}

// findManifestURL returns the first .m3u8 URL referenced by the page. The
// reference plugin captures it from network traffic; without a browser we scan
// the markup and, as a last resort, the src of any iframe or <video>.
func findManifestURL(html string) string {
	// Some pages embed the URL in a JavaScript string literal, where slashes are
	// escaped as `https:\/\/...`. Search the HTML as-is first, then a copy with
	// JSON escapes and HTML entities relaxed so both forms resolve.
	if u := m3u8RE.FindString(html); u != "" {
		return unescapeHTMLURL(u)
	}
	relaxed := strings.ReplaceAll(html, `\/`, "/")
	if u := m3u8RE.FindString(relaxed); u != "" {
		return unescapeHTMLURL(u)
	}
	for _, m := range srcAttrRE.FindAllStringSubmatch(html, -1) {
		src := unescapeHTMLURL(joinGroups(m))
		if strings.Contains(strings.ToLower(src), ".m3u8") {
			return src
		}
	}
	return ""
}

// articleImageURLs returns the source URLs of every image inside the article
// body, in document order. Each <img> tag contributes one URL — the most
// informative attribute wins: data-xkrkllgl (the site's real lazy payload),
// then data-original / data-src, then src.
func articleImageURLs(body string) []string {
	if body == "" {
		return nil
	}
	var urls []string
	for _, tag := range imgTagRE.FindAllString(body, -1) {
		var picked string
		for _, m := range imgAttrRE.FindAllStringSubmatch(tag, -1) {
			attr := strings.ToLower(m[1])
			// Groups: 1=attribute name, 2=double-quoted value, 3=single-quoted
			// value, 4=unquoted value.
			val := firstNonEmpty(m[2:]...)
			if val == "" {
				continue
			}
			if picked == "" {
				picked = val
			}
			// data-xkrkllgl is the real payload; prefer it over the placeholder
			// src when both are present.
			if attr == "data-xkrkllgl" {
				picked = val
			}
		}
		if picked != "" {
			urls = append(urls, picked)
		}
	}
	return urls
}

// pickThumbnail mirrors _pick_primary_thumbnail (the last image in the article
// body wins) with one Go-specific adjustment: a real http(s) image URL is
// preferred over an inlined data: URI.
//
// The site serves empty placeholders for remote <img> sources and inlines the
// real previews as data: URIs only after its JavaScript runs, so data: URIs are
// still accepted as a fallback — but only when they decode to an actual image.
// A multi-megabyte data: URI would otherwise end up in every -j dump and could
// not be fetched by the thumbnail writer.
func pickThumbnail(body string) string {
	var lastHTTP, lastInline string
	for _, raw := range articleImageURLs(body) {
		src := strings.TrimSpace(unescapeHTMLURL(raw))
		switch {
		case strings.HasPrefix(src, "http://"), strings.HasPrefix(src, "https://"):
			lastHTTP = src
		case strings.HasPrefix(src, "data:"):
			if du, err := extractor.DecodeDataURL(src); err == nil && len(du.Data) > 0 {
				lastInline = src
			}
		}
	}
	if lastHTTP != "" {
		return lastHTTP
	}
	return lastInline
}

// sanitizeDescription reproduces _sanitize_description: drop the site's
// boilerplate and append the source URL.
func sanitizeDescription(text, sourceURL string) string {
	cleaned := strings.TrimSpace(text)
	for _, forbidden := range cg51DescriptionBlacklist {
		cleaned = strings.ReplaceAll(cleaned, forbidden, "")
	}
	cleaned = strings.TrimSpace(cleaned)
	switch {
	case sourceURL == "":
		return cleaned
	case cleaned == "":
		return "Source: " + sourceURL
	default:
		return cleaned + "\n\nSource: " + sourceURL
	}
}

// ---- small helpers ----

// joinGroups returns the first non-empty capture group of a submatch slice.
func joinGroups(m []string) string {
	for _, g := range m[1:] {
		if g != "" {
			return g
		}
	}
	return ""
}

func firstGroup(re *regexp.Regexp, s string) string {
	if m := re.FindStringSubmatch(s); m != nil {
		return m[1]
	}
	return ""
}

// firstNonEmpty returns the first non-empty string among vals.
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// unescapeHTMLURL reverses the escaping the page may apply inside attributes or
// JSON-embedded strings: HTML entities, JSON \/ and backslash-escaped slashes.
func unescapeHTMLURL(u string) string {
	u = strings.ReplaceAll(u, "&amp;", "&")
	u = strings.ReplaceAll(u, `\/`, "/")
	u = strings.ReplaceAll(u, `\\/`, "/")
	u = strings.TrimRight(u, `'`)
	return u
}
