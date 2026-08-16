// Package hongguo implements an InfoExtractor for hongguoduanju.com (红果短剧,
// ByteDance's free short-drama platform).
//
// The player page is a server-rendered SPA that embeds the whole playable video
// payload in a global `_ROUTER_DATA = {...}` JSON assignment (React Router
// loader data). No API signature (a_bogus / X-Bogus) is required: the current
// episode's direct mp4 URL lives at
//
//	loaderData["player_(series_id)/page"].video_player_info.main_url
//
// with the drama metadata (series_name / series_intro / series_cover / vid_list)
// under `.seriesDetail`. This is a single-episode extractor for now; switching
// episodes needs the client-side episode API which is not yet implemented.
package hongguo

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"yt-dlp-go/extractor"
)

// HongguoIE extracts from hongguoduanju.com.
type HongguoIE struct{}

func init() { extractor.Register(HongguoIE{}) }

func (HongguoIE) Name() string { return "hongguo" }

var hongguoURLRE = regexp.MustCompile(`(?i)hongguoduanju\.com/player/([0-9]+)`)

func (HongguoIE) Match(u string) bool { return hongguoURLRE.MatchString(u) }

// Extract fetches the player page and normalises the embedded payload.
func (HongguoIE) Extract(ctx *extractor.Context, pageURL string) (*extractor.Info, error) {
	html, err := extractor.DownloadWebpage(ctx, pageURL, nil, nil)
	if err != nil {
		return nil, err
	}
	obj, err := extractJSONAssign(html, "_ROUTER_DATA")
	if err != nil {
		return nil, err
	}
	return parseRouterData(obj, pageURL)
}

// parseRouterData turns the _ROUTER_DATA object into a normalised Info.
func parseRouterData(obj map[string]any, pageURL string) (*extractor.Info, error) {
	loader, ok := extractor.TraverseObj(obj, "loaderData").(map[string]any)
	if !ok {
		return nil, fmt.Errorf("hongguo: loaderData missing from _ROUTER_DATA")
	}

	// The page key is dynamic ("player_(series_id)/page"), so locate the loader
	// entry that carries the video payload instead of hardcoding the key.
	var page map[string]any
	for _, v := range loader {
		if m, ok := v.(map[string]any); ok {
			if _, has := m["video_player_info"]; has {
				page = m
				break
			}
		}
	}
	if page == nil {
		return nil, fmt.Errorf("hongguo: player page data not found in _ROUTER_DATA")
	}

	vpi, _ := page["video_player_info"].(map[string]any)
	mainURL := extractor.StrOrNone(extractor.TraverseObj(vpi, "main_url"))
	if mainURL == "" {
		return nil, fmt.Errorf("hongguo: no playable video URL (episode may require login/payment)")
	}

	sd, _ := page["seriesDetail"].(map[string]any)
	seriesName := extractor.StrOrNone(extractor.TraverseObj(sd, "series_name"))
	seriesIntro := extractor.StrOrNone(extractor.TraverseObj(sd, "series_intro"))
	cover := extractor.StrOrNone(extractor.TraverseObj(sd, "series_cover"))
	if cover == "" {
		cover = extractor.StrOrNone(extractor.TraverseObj(vpi, "poster_url"))
	}
	vid := extractor.StrOrNone(page["vid"])

	// Episode number = position of vid in the drama's vid_list (1-based).
	ep := 0
	if vl, ok := sd["vid_list"].([]any); ok {
		for i, e := range vl {
			if extractor.StrOrNone(e) == vid {
				ep = i + 1
				break
			}
		}
	}
	title := seriesName
	if ep > 0 {
		title = fmt.Sprintf("%s 第%d集", seriesName, ep)
	}

	return &extractor.Info{
		ID:          vid,
		Title:       title,
		Description: seriesIntro,
		Thumbnail:   cover,
		WebpageURL:  pageURL,
		Ext:         "mp4",
		Duration:    extractor.FloatOrNone(extractor.TraverseObj(vpi, "duration")),
		Subtitles:   map[string][]extractor.Subtitle{},
		Raw:         page,
		Formats: []extractor.Format{{
			FormatID: "main",
			URL:      mainURL,
			Protocol: "http",
			Ext:      "mp4",
			// 红果短剧 serves a single muxed mp4 (H.264 video + AAC audio).
			VCodec: "h264",
			ACodec: "aac",
			Width:  extractor.IntOrNone(extractor.TraverseObj(vpi, "width")),
			Height: extractor.IntOrNone(extractor.TraverseObj(vpi, "height")),
		}},
	}, nil
}

// extractJSONAssign extracts the object assigned to `key = {...}` from raw HTML
// (the assignment may be `key = {...}` or `key={...}`; whitespace-tolerant).
func extractJSONAssign(html, key string) (map[string]any, error) {
	i := strings.Index(html, key)
	if i < 0 {
		return nil, fmt.Errorf("%s not found", key)
	}
	eq := strings.Index(html[i:], "=")
	if eq < 0 {
		return nil, fmt.Errorf("%s has no assignment", key)
	}
	start := i + eq + 1
	for start < len(html) && (html[start] == ' ' || html[start] == '\n' || html[start] == '\t') {
		start++
	}
	if start >= len(html) || html[start] != '{' {
		return nil, fmt.Errorf("expected { after %s", key)
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
				if err := json.Unmarshal([]byte(html[start:j+1]), &v); err != nil {
					return nil, fmt.Errorf("%s decode: %w", key, err)
				}
				if m, ok := v.(map[string]any); ok {
					return m, nil
				}
				return nil, fmt.Errorf("%s is not an object", key)
			}
		}
	}
	return nil, fmt.Errorf("unterminated %s", key)
}
