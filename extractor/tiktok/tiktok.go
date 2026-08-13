// Package tiktok implements an InfoExtractor for tiktok.com and its short-link
// domains (vm.tiktok.com, vt.tiktok.com).
//
// The most reliable offline-friendly path is parsing the Open Graph meta tags
// (og:video:secure_url / og:title) from the page HTML. We also attempt to read
// window.__NEXT_DATA__ for richer metadata. When a direct video URL is present
// we expose it as a single progressive format.
package tiktok

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"yt-dlp-go/extractor"
)

// TikTokIE extracts from tiktok.com / vm.tiktok.com / vt.tiktok.com.
type TikTokIE struct{}

func init() { extractor.Register(TikTokIE{}) }

func (TikTokIE) Name() string { return "tiktok" }

var tiktokURLRE = regexp.MustCompile(`(?i)(?:https?://)?(?:www\.|vm\.|vt\.)?tiktok\.com`)

func (TikTokIE) Match(u string) bool {
	return tiktokURLRE.MatchString(u)
}

// Extract performs the full extraction.
func (TikTokIE) Extract(ctx *extractor.Context, pageURL string) (*extractor.Info, error) {
	html, err := extractor.DownloadWebpage(ctx, pageURL, nil, nil)
	if err != nil {
		return nil, err
	}
	return ParsePage(html, pageURL)
}

// ParsePage turns raw TikTok page HTML into an Info. Exported so it can be
// unit-tested without any network access.
func ParsePage(html, pageURL string) (*extractor.Info, error) {
	videoURL := metaContent(html, "og:video:secure_url")
	if videoURL == "" {
		videoURL = metaContent(html, "og:video")
	}
	title := metaContent(html, "og:title")
	if title == "" {
		title = metaContent(html, "title")
	}
	title = htmlUnescape(title)

	info := &extractor.Info{
		Title:        title,
		WebpageURL:   pageURL,
		Ext:          "mp4",
		Subtitles:    map[string][]extractor.Subtitle{},
		Raw:          map[string]any{},
	}

	// Enrich from __NEXT_DATA__ when present.
	if nd, e := parseNextData(html); e == nil {
		if id := extractor.StrOrNone(extractor.TraverseObj(nd, "props", "pageProps", "videoData", "id")); id != "" {
			info.ID = id
		}
		if desc := extractor.StrOrNone(extractor.TraverseObj(nd, "props", "pageProps", "videoData", "desc")); desc != "" {
			info.Description = desc
		}
		if author := extractor.StrOrNone(extractor.TraverseObj(nd, "props", "pageProps", "videoData", "author", "uniqueId")); author != "" {
			info.Uploader = author
		}
		// Some pages embed the direct URL here too.
		if v := extractor.StrOrNone(extractor.TraverseObj(nd, "props", "pageProps", "videoData", "videoUrl")); v != "" && videoURL == "" {
			videoURL = v
		}
	}

	if videoURL == "" {
		return nil, fmt.Errorf("no playable video URL found on TikTok page")
	}
	info.Formats = []extractor.Format{{
		FormatID: "1",
		URL:      videoURL,
		Protocol: "http",
		Ext:      "mp4",
	}}
	return info, nil
}

// metaContent reads the content of a <meta property="..."> or <meta name="...">.
func metaContent(html, prop string) string {
	re := regexp.MustCompile(`(?i)<meta[^>]+(?:property|name)\s*=\s*["']` + regexp.QuoteMeta(prop) + `["'][^>]*?content\s*=\s*["']([^"']*)["']`)
	if m := re.FindStringSubmatch(html); m != nil {
		return m[1]
	}
	// Also match the reversed attribute order.
	re2 := regexp.MustCompile(`(?i)<meta[^>]+content\s*=\s*["']([^"']*)["'][^>]*?(?:property|name)\s*=\s*["']` + regexp.QuoteMeta(prop) + `["']`)
	if m := re2.FindStringSubmatch(html); m != nil {
		return m[1]
	}
	return ""
}

// parseNextData extracts the window.__NEXT_DATA__ JSON object. It handles both
// the `window.__NEXT_DATA__ = {...}` assignment form and the
// `<script id="__NEXT_DATA__" type="application/json">{...}</script>` form.
func parseNextData(html string) (map[string]any, error) {
	// Form 1: window.__NEXT_DATA__ = {...}
	if obj, err := parseNextDataAssign(html); err == nil {
		return obj, nil
	}
	// Form 2: <script id="__NEXT_DATA__" ...>{...}</script>
	if m := nextDataScriptRE.FindStringSubmatch(html); m != nil {
		var v any
		if err := json.Unmarshal([]byte(m[1]), &v); err == nil {
			if mp, ok := v.(map[string]any); ok {
				return mp, nil
			}
			return nil, fmt.Errorf("__NEXT_DATA__ script is not an object")
		} else {
			return nil, fmt.Errorf("__NEXT_DATA__ script decode: %w", err)
		}
	}
	return nil, fmt.Errorf("__NEXT_DATA__ not found")
}

var nextDataScriptRE = regexp.MustCompile(`(?i)<script[^>]+id=["']__NEXT_DATA__["'][^>]*>([\s\S]*?)</script>`)

// parseNextDataAssign handles `window.__NEXT_DATA__ = {...}`.
func parseNextDataAssign(html string) (map[string]any, error) {
	marker := "window.__NEXT_DATA__="
	i := strings.Index(html, marker)
	if i < 0 {
		return nil, fmt.Errorf("__NEXT_DATA__ assignment not found")
	}
	start := i + len(marker)
	for start < len(html) && (html[start] == ' ' || html[start] == '\n') {
		start++
	}
	if start >= len(html) || html[start] != '{' {
		return nil, fmt.Errorf("expected object after __NEXT_DATA__")
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
				var v any
				if err := json.Unmarshal([]byte(html[start:j+1]), &v); err == nil {
					if m, ok := v.(map[string]any); ok {
						return m, nil
					}
					return nil, fmt.Errorf("__NEXT_DATA__ is not an object")
				} else {
					return nil, fmt.Errorf("__NEXT_DATA__ decode: %w", err)
				}
			}
		}
	}
	return nil, fmt.Errorf("unterminated __NEXT_DATA__")
}

func htmlUnescape(s string) string {
	repl := strings.NewReplacer("&amp;", "&", "&lt;", "<", "&gt;", ">", "&quot;", "\"", "&#39;", "'", "&apos;", "'")
	return repl.Replace(s)
}
