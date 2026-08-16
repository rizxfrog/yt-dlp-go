// Package hongguo implements an InfoExtractor for hongguoduanju.com (红果短剧,
// ByteDance's free short-drama platform).
//
// The player page is a server-rendered SPA that embeds the whole playable video
// payload in a global `_ROUTER_DATA = {...}` JSON assignment (React Router
// loader data). No API signature (a_bogus / X-Bogus) is required.
//
// URL forms:
//
//	/player/<series_id>       → the whole drama as a playlist (one entry per
//	                            episode; episodes that require login/payment 404
//	                            and are skipped)
//	/player/<series_id>/<vid> → a single, specific episode
//
// The episode payload lives at
//
//	loaderData["player_(series_id)/page"].video_player_info.main_url
//
// with the drama metadata (series_name / series_intro / series_cover / vid_list)
// under `.seriesDetail`. Switching episodes is simply navigating to
// /player/<series_id>/<vid>, which re-renders the SSR page with that episode's
// main_url (the loader key becomes "player_(series_id)/(vid)/page").
package hongguo

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"yt-dlp-go/extractor"
)

// HongguoIE extracts from hongguoduanju.com.
type HongguoIE struct{}

func init() { extractor.Register(HongguoIE{}) }

func (HongguoIE) Name() string { return "hongguo" }

// maxEpisodeConcurrency bounds how many episode pages we fetch in parallel when
// expanding a drama into a playlist. Kept low: the site rate-limits aggressive
// concurrent scraping.
const maxEpisodeConcurrency = 4

var (
	// /player/<series_id> or /player/<series_id>/<vid>.
	hongguoURLRE = regexp.MustCompile(`(?i)hongguoduanju\.com/player/([0-9]+)(?:/([0-9]+))?`)
)

// parsePlayerURL returns the (series_id, vid) from a player URL; vid is empty
// when the URL points at the whole drama.
func parsePlayerURL(u string) (sid, vid string) {
	m := hongguoURLRE.FindStringSubmatch(u)
	if m == nil {
		return "", ""
	}
	return m[1], m[2]
}

func (HongguoIE) Match(u string) bool { return hongguoURLRE.MatchString(u) }

// Extract fetches the player page and normalises the embedded payload. A URL
// with an explicit /vid yields a single episode; a bare /series_id yields the
// whole drama as a playlist.
func (HongguoIE) Extract(ctx *extractor.Context, pageURL string) (*extractor.Info, error) {
	sid, vid := parsePlayerURL(pageURL)
	if sid == "" {
		return nil, fmt.Errorf("could not parse Hongguo series id from %q", pageURL)
	}

	firstURL := "https://hongguoduanju.com/player/" + sid
	if vid != "" {
		firstURL += "/" + vid
	}

	html, err := extractor.DownloadWebpage(ctx, firstURL, nil, nil)
	if err != nil {
		return nil, err
	}
	obj, err := extractJSONAssign(html, "_ROUTER_DATA")
	if err != nil {
		return nil, err
	}
	page, err := findPlayerPage(obj)
	if err != nil {
		return nil, err
	}

	// Explicit episode URL → single episode.
	if vid != "" {
		return episodeInfoFromPage(page, episodeNumber(page, vid), firstURL)
	}

	// Whole drama → playlist.
	return expandPlaylist(ctx, sid, page, pageURL)
}

// expandPlaylist turns the first episode's page (which carries the full vid_list)
// into a playlist Info with one entry per fetchable episode.
func expandPlaylist(ctx *extractor.Context, sid string, firstPage map[string]any, pageURL string) (*extractor.Info, error) {
	sd, _ := firstPage["seriesDetail"].(map[string]any)
	seriesName := extractor.StrOrNone(extractor.TraverseObj(sd, "series_name"))
	seriesIntro := extractor.StrOrNone(extractor.TraverseObj(sd, "series_intro"))
	cover := extractor.StrOrNone(extractor.TraverseObj(sd, "series_cover"))
	firstVid := extractor.StrOrNone(firstPage["vid"])

	vidList, _ := sd["vid_list"].([]any)

	// Only the first `accessible_episode_cnt` episodes are playable without
	// login/payment; the rest 404 and, if fetched en masse, trip the site's rate
	// limiter. Clamp to that window (falling back to the full list when unknown).
	limit := len(vidList)
	if n := extractor.IntOrNone(extractor.TraverseObj(sd, "accessible_episode_cnt")); n > 0 && n < limit {
		limit = n
	}
	vidList = vidList[:limit]

	entries := make([]*extractor.Info, len(vidList))
	var wg sync.WaitGroup
	sem := make(chan struct{}, maxEpisodeConcurrency)

	for i, v := range vidList {
		epVid := extractor.StrOrNone(v)
		epNum := i + 1
		// The first page already resolved the first episode; reuse it.
		if i == 0 && epVid == firstVid {
			if ep, err := episodeInfoFromPage(firstPage, epNum, pageURL); err == nil {
				entries[i] = ep
			}
			continue
		}
		wg.Add(1)
		go func(i int, epVid string, epNum int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			ep, err := fetchEpisode(ctx, sid, epVid, epNum)
			if err == nil {
				entries[i] = ep
			}
		}(i, epVid, epNum)
	}
	wg.Wait()

	var out []*extractor.Info
	for _, e := range entries {
		if e != nil {
			out = append(out, e)
		}
	}

	return &extractor.Info{
		ID:          sid,
		Title:       seriesName,
		Description: seriesIntro,
		Thumbnail:   cover,
		WebpageURL:  pageURL,
		Ext:         "mp4",
		Subtitles:   map[string][]extractor.Subtitle{},
		Raw:         firstPage,
		Entries:     out,
	}, nil
}

// fetchEpisode resolves a single episode's page into an Info.
func fetchEpisode(ctx *extractor.Context, sid, vid string, epNum int) (*extractor.Info, error) {
	u := fmt.Sprintf("https://hongguoduanju.com/player/%s/%s", sid, vid)
	html, err := extractor.DownloadWebpage(ctx, u, nil, nil)
	if err != nil {
		return nil, err // e.g. 404 for login/payment-gated episodes
	}
	obj, err := extractJSONAssign(html, "_ROUTER_DATA")
	if err != nil {
		return nil, err
	}
	page, err := findPlayerPage(obj)
	if err != nil {
		return nil, err
	}
	return episodeInfoFromPage(page, epNum, u)
}

// findPlayerPage locates the loader entry carrying the video payload. The key is
// dynamic ("player_(series_id)/page" or "player_(series_id)/(vid)/page"), so we
// scan the loaderData map instead of hardcoding it.
func findPlayerPage(obj map[string]any) (map[string]any, error) {
	loader, ok := extractor.TraverseObj(obj, "loaderData").(map[string]any)
	if !ok {
		return nil, fmt.Errorf("hongguo: loaderData missing from _ROUTER_DATA")
	}
	for _, v := range loader {
		if m, ok := v.(map[string]any); ok {
			if _, has := m["video_player_info"]; has {
				return m, nil
			}
		}
	}
	return nil, fmt.Errorf("hongguo: player page data not found in _ROUTER_DATA")
}

// episodeNumber returns the 1-based position of vid within the drama's vid_list
// (0 when unknown).
func episodeNumber(page map[string]any, vid string) int {
	sd, _ := page["seriesDetail"].(map[string]any)
	if vl, ok := sd["vid_list"].([]any); ok {
		for i, e := range vl {
			if extractor.StrOrNone(e) == vid {
				return i + 1
			}
		}
	}
	return 0
}

// episodeInfoFromPage builds a single-episode Info from a player page object.
// epNum <= 0 suppresses the "第N集" suffix.
func episodeInfoFromPage(page map[string]any, epNum int, pageURL string) (*extractor.Info, error) {
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

	title := seriesName
	if epNum > 0 {
		title = fmt.Sprintf("%s 第%d集", seriesName, epNum)
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
