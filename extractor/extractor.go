// Package extractor defines the core extraction types: the Info result, the
// Extractor interface, the global registry (mirroring yt-dlp's explicit
// _extractors.py list), and the shared helpers (JSON/webpage fetching, field
// traversal, HTML cleaning, date parsing) that every site extractor reuses.
//
// Each concrete extractor lives in its own subpackage and registers itself via
// Register() from its init() function. The core engine then dispatches a URL to
// the first registered extractor whose Match() returns true.
package extractor

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"yt-dlp-go/options"
)

// Format describes one available media representation.
type Format struct {
	FormatID    string
	URL         string // direct media URL (for progressive/HTTP)
	ManifestURL string // m3u8/m3u8 manifest URL (for HLS/DASH)
	Protocol    string
	Ext         string
	VCodec      string
	ACodec      string
	Width       int
	Height      int
	FPS         float64
	Filesize    int64
	FormatNote  string
	TBR         float64 // total bitrate kbps
}

// Info is the normalised extraction result.
type Info struct {
	ID           string
	Title        string
	Description  string
	Uploader     string
	UploadDate   string
	Duration     float64
	Thumbnail    string
	WebpageURL   string
	Ext          string
	Formats      []Format
	Subtitles    map[string][]Subtitle
	IsLive       bool
	// Entries holds the child Info objects when this result is a playlist /
	// channel / feed. A non-nil, non-empty Entries slice marks the result as a
	// playlist that the core should iterate instead of downloading this node.
	Entries []*Info
	// Raw is the original JSON object for debugging / advanced access.
	Raw map[string]any
}

// Subtitle is one subtitle/closed-caption source.
type Subtitle struct {
	URL  string
	Ext  string
	Name string
}

// Context carries the HTTP client and options into an extractor.
type Context struct {
	Client  *http.Client
	Options *options.Options
	Headers map[string]string
}

// Extractor is implemented by every site support.
type Extractor interface {
	Name() string
	Match(url string) bool
	Extract(ctx *Context, url string) (*Info, error)
}

// ---- registry ----

var registry []Extractor

// Register adds an extractor to the global registry.
func Register(e Extractor) {
	registry = append(registry, e)
}

// All returns the registered extractors in registration order.
func All() []Extractor { return registry }

// MatchURL returns the first extractor that claims the URL, or nil.
func MatchURL(u string) Extractor {
	for _, e := range registry {
		if e.Match(u) {
			return e
		}
	}
	return nil
}

// ExtractURL resolves a URL to its matching extractor and runs extraction.
// Playlist extractors use this to fetch each child entry's full Info.
func ExtractURL(ctx *Context, url string) (*Info, error) {
	ie := MatchURL(url)
	if ie == nil {
		return nil, fmt.Errorf("no extractor found for %q", url)
	}
	return ie.Extract(ctx, url)
}

// ---- network helpers ----

// DownloadWebpageRaw fetches a URL and returns the raw body bytes.
func DownloadWebpageRaw(ctx *Context, u string, headers map[string]string, query url.Values) ([]byte, error) {
	if query != nil {
		if strings.Contains(u, "?") {
			u += "&" + query.Encode()
		} else {
			u += "?" + query.Encode()
		}
	}
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range ctx.Headers {
		req.Header.Set(k, v)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := ctx.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: status %d", u, resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

// DownloadWebpage fetches a URL and returns the body as a string.
func DownloadWebpage(ctx *Context, u string, headers map[string]string, query url.Values) (string, error) {
	b, err := DownloadWebpageRaw(ctx, u, headers, query)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// DownloadJSON fetches a URL and unmarshals the JSON response into a generic
// structure (map[string]any / []any).
func DownloadJSON(ctx *Context, u string, headers map[string]string, query url.Values) (any, error) {
	b, err := DownloadWebpageRaw(ctx, u, headers, query)
	if err != nil {
		return nil, err
	}
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return nil, fmt.Errorf("json decode: %w", err)
	}
	return v, nil
}

// ---- value helpers (the workhorses of most extractors) ----

// TraverseObj walks a decoded JSON tree by alternating string keys and int
// indices, returning nil if any step fails. Equivalent to yt-dlp's traverse_obj.
func TraverseObj(obj any, path ...any) any {
	cur := obj
	for _, p := range path {
		switch k := p.(type) {
		case string:
			m, ok := cur.(map[string]any)
			if !ok {
				return nil
			}
			cur = m[k]
		case int:
			s, ok := cur.([]any)
			if !ok {
				return nil
			}
			if k < 0 || k >= len(s) {
				return nil
			}
			cur = s[k]
		default:
			return nil
		}
	}
	return cur
}

var tagRE = regexp.MustCompile(`<[^>]+>`)

// CleanHTML strips HTML tags and collapses whitespace.
func CleanHTML(s string) string {
	s = tagRE.ReplaceAllString(s, "")
	s = strings.ReplaceAll(s, "&nbsp;", " ")
	s = strings.ReplaceAll(s, "&amp;", "&")
	s = strings.ReplaceAll(s, "&quot;", "\"")
	s = strings.ReplaceAll(s, "&#39;", "'")
	return strings.TrimSpace(s)
}

// IntOrNone coerces a value to int, returning 0 when impossible.
func IntOrNone(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case string:
		i, err := strconv.Atoi(strings.TrimSpace(n))
		if err == nil {
			return i
		}
	}
	return 0
}

// FloatOrNone coerces a value to float64.
func FloatOrNone(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(n), 64)
		if err == nil {
			return f
		}
	}
	return 0
}

// ParseISO8601 parses an ISO-8601 timestamp (used by many APIs).
func ParseISO8601(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	for _, layout := range []string{
		time.RFC3339,
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
		"2006-01-02",
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unparseable date %q", s)
}

// StrOrNone returns the string form of a value, or "" when nil.
func StrOrNone(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}
