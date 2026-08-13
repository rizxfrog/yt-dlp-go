// Package generic provides a fallback extractor for direct media URLs (links that
// already point at a .mp4/.m3u8/... file). It mirrors yt-dlp's behaviour of
// accepting a plain media URL when no site-specific extractor claims it.
package generic

import (
	"net/url"
	"regexp"
	"strings"

	"yt-dlp-go/extractor"
)

// DirectIE handles URLs that end in a known media extension.
type DirectIE struct{}

func init() { extractor.Register(DirectIE{}) }

func (DirectIE) Name() string { return "generic" }

var mediaExtRE = regexp.MustCompile(`(?i)\.(mp4|m4a|m4v|webm|mp3|ogg|oga|opus|mov|flv|f4v|mkv|m3u8|mpd)(?:[?#].*)?$`)

func (DirectIE) Match(u string) bool {
	p, err := url.Parse(u)
	if err != nil {
		return false
	}
	return mediaExtRE.MatchString(p.Path)
}

// Extract builds a single-format Info from the URL alone.
func (DirectIE) Extract(ctx *extractor.Context, pageURL string) (*extractor.Info, error) {
	low := strings.ToLower(pageURL)
	var protocol, ext string
	switch {
	case strings.Contains(low, ".m3u8"):
		protocol, ext = "m3u8_native", "m3u8"
	case strings.Contains(low, ".mpd"):
		protocol, ext = "dash", "mpd"
	default:
		protocol, ext = "http", extFromURL(pageURL)
	}
	info := &extractor.Info{
		ID:         baseName(pageURL),
		Title:      baseName(pageURL),
		WebpageURL: pageURL,
		Ext:        ext,
		Formats: []extractor.Format{{
			FormatID: "1",
			URL:      pageURL,
			Protocol: protocol,
			Ext:      ext,
		}},
	}
	return info, nil
}

func extFromURL(u string) string {
	p, err := url.Parse(u)
	if err != nil {
		return "mp4"
	}
	e := mediaExtRE.FindString(p.Path)
	e = strings.TrimLeft(e, ".")
	if i := strings.Index(e, "?"); i >= 0 {
		e = e[:i]
	}
	if e == "" {
		return "mp4"
	}
	return e
}

func baseName(u string) string {
	p, err := url.Parse(u)
	if err != nil {
		return "media"
	}
	segs := strings.Split(strings.Trim(p.Path, "/"), "/")
	name := segs[len(segs)-1]
	if i := strings.Index(name, "?"); i >= 0 {
		name = name[:i]
	}
	return name
}
