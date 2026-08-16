// Package bilibili implements an InfoExtractor for bilibili.com (BV-style video
// pages and b23.tv short links).
//
// Bilibili serves two things we care about:
//  1. Page metadata embedded as JSON in `window.__INITIAL_STATE__`.
//  2. The actual media URLs from the playurl API
//     (https://api.bilibili.com/x/player/wbi/playurl), which is protected by the
//     "WBI" request-signing scheme. We implement WBI signing in pure Go so the
//     extractor is self-contained (no extra dependency) and the signing step is
//     unit-testable.
//
// Live Bilibili internals change; when the playurl call fails (e.g. login-gated
// high resolutions, rate limiting), the extractor still returns the metadata so
// the failure is reported rather than crashing.
package bilibili

import (
	"crypto/md5"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"yt-dlp-go/extractor"
)

// BilibiliIE extracts from bilibili.com / b23.tv.
type BilibiliIE struct{}

func init() { extractor.Register(BilibiliIE{}) }

func (BilibiliIE) Name() string { return "bilibili" }

var (
	biliURLRE = regexp.MustCompile(`(?i)bilibili\.com/(?:video/)?(BV[0-9A-Za-z]+)`)
	b23RE     = regexp.MustCompile(`(?i)b23\.tv/([0-9A-Za-z]+)`)
)

func (BilibiliIE) Match(u string) bool {
	return biliURLRE.MatchString(u) || b23RE.MatchString(u)
}

func extractBVID(u string) string {
	if m := biliURLRE.FindStringSubmatch(u); m != nil {
		return m[1]
	}
	return ""
}

// Extract performs the full extraction.
func (BilibiliIE) Extract(ctx *extractor.Context, pageURL string) (*extractor.Info, error) {
	bvid := extractBVID(pageURL)
	if bvid == "" {
		return nil, fmt.Errorf("could not parse Bilibili BV id from %q", pageURL)
	}

	html, err := extractor.DownloadWebpage(ctx, pageURL, nil, nil)
	if err != nil {
		return nil, err
	}

	meta, err := parseInitialState(html)
	if err != nil {
		return nil, err
	}

	// A UGC 合集 (season) with more than one episode expands to a playlist so a
	// single video URL downloads the whole set (use --no-playlist for just this
	// one). A single-episode season falls through to the normal single-video path.
	if meta.Season != nil && len(meta.Season.Episodes) > 1 {
		return buildSeasonPlaylist(ctx, pageURL, meta)
	}

	info := &extractor.Info{
		ID:           meta.BVID,
		Title:        meta.Title,
		Description:  meta.Desc,
		Uploader:     meta.Owner,
		Channel:      meta.Owner,
		UploadDate:   meta.PubDate,
		Thumbnail:    httpsThumbnail(meta.Pic),
		WebpageURL:   pageURL,
		Ext:          "mp4",
		Duration:     meta.Duration,
		ViewCount:    meta.View,
		LikeCount:    meta.Like,
		CommentCount: meta.Reply,
		RepostCount:  meta.Share,
		Subtitles:    map[string][]extractor.Subtitle{},
		Raw:          meta.Raw,
	}

	// Best-effort media resolution via the WBI-signed playurl API.
	formats, ferr := fetchFormats(ctx, meta)
	if ferr != nil {
		if ctx.Options != nil && ctx.Options.Verbose {
			fmt.Printf("[bilibili] playurl unavailable: %v\n", ferr)
		}
	} else {
		info.Formats = append(info.Formats, formats...)
	}
	return info, nil
}

// buildSeasonPlaylist expands a UGC 合集 (season) into a playlist: one entry per
// episode, each a fully-resolved Info with its own media formats. The episode
// metadata is SSR-injected on the page, so only the playurl API is called per
// episode (WBI keys fetched once and reused). Episodes whose playurl fails are
// skipped so the rest of the set still downloads.
func buildSeasonPlaylist(ctx *extractor.Context, pageURL string, meta *initialState) (*extractor.Info, error) {
	keys, err := fetchWbiKeys(ctx)
	if err != nil {
		return nil, fmt.Errorf("bilibili: WBI keys: %w", err)
	}

	pl := &extractor.Info{
		ID:         fmt.Sprintf("season-%d", meta.Season.ID),
		Title:      meta.Season.Title,
		WebpageURL: pageURL,
		Entries:    make([]*extractor.Info, 0, len(meta.Season.Episodes)),
	}

	for _, ep := range meta.Season.Episodes {
		entry := &extractor.Info{
			ID:           ep.BVID,
			Title:        ep.Title,
			Uploader:     ep.Author,
			Channel:      ep.Author,
			UploadDate:   ep.PubDate,
			Thumbnail:    httpsThumbnail(ep.Pic),
			WebpageURL:   "https://www.bilibili.com/video/" + ep.BVID + "/",
			Ext:          "mp4",
			Duration:     ep.Duration,
			ViewCount:    ep.View,
			LikeCount:    ep.Like,
			CommentCount: ep.Reply,
			RepostCount:  ep.Share,
			Subtitles:    map[string][]extractor.Subtitle{},
		}
		formats, ferr := fetchFormatsForCid(ctx, ep.BVID, ep.CID, keys)
		if ferr != nil {
			if ctx.Options != nil && ctx.Options.Verbose {
				fmt.Printf("[bilibili] episode %s (%s) playurl unavailable: %v\n", ep.BVID, ep.Title, ferr)
			}
			continue // skip episodes we cannot resolve
		}
		entry.Formats = formats
		pl.Entries = append(pl.Entries, entry)
	}
	return pl, nil
}

// ---- metadata parsing ----

type initialState struct {
	BVID     string
	Title    string
	Desc     string
	Owner    string
	PubDate  string
	CID      int64
	AID      int64
	Pic      string
	Duration float64
	View     int64
	Like     int64
	Reply    int64
	Share    int64
	// Season is populated when the video belongs to a UGC 合集 (season): a
	// creator-curated set of independent BVs grouped under one season_id.
	Season *seasonInfo
	Raw    map[string]any
}

// seasonInfo is a Bilibili UGC 合集 (season) and its episodes. Each episode is
// an independent video (its own bvid + cid), so a season expands to a playlist.
type seasonInfo struct {
	ID       int64
	Title    string
	Episodes []seasonEpisode
}

// seasonEpisode is one video inside a UGC 合集.
type seasonEpisode struct {
	BVID     string
	CID      int64
	Title    string
	Duration float64
	Pic      string
	View     int64
	Like     int64
	Reply    int64
	Share    int64
	PubDate  string
	Author   string
}

// parseInitialState extracts the `window.__INITIAL_STATE__` blob and pulls the
// videoData fields out of it.
func parseInitialState(html string) (*initialState, error) {
	obj, err := extractJSONAssign(html, "window.__INITIAL_STATE__")
	if err != nil {
		return nil, err
	}
	vd, ok := extractor.TraverseObj(obj, "videoData").(map[string]any)
	if !ok {
		return nil, fmt.Errorf("videoData not found in __INITIAL_STATE__")
	}
	s := &initialState{Raw: obj}
	s.BVID = extractor.StrOrNone(extractor.TraverseObj(vd, "bvid"))
	s.Title = extractor.StrOrNone(extractor.TraverseObj(vd, "title"))
	s.Desc = extractor.StrOrNone(extractor.TraverseObj(vd, "desc"))
	s.Owner = extractor.StrOrNone(extractor.TraverseObj(vd, "owner", "name"))
	s.Pic = extractor.StrOrNone(extractor.TraverseObj(vd, "pic"))
	s.CID = int64(extractor.IntOrNone(extractor.TraverseObj(vd, "cid")))
	s.AID = int64(extractor.IntOrNone(extractor.TraverseObj(vd, "aid")))
	s.Duration = extractor.FloatOrNone(extractor.TraverseObj(vd, "duration"))
	if pub := extractor.IntOrNone(extractor.TraverseObj(vd, "pubdate")); pub > 0 {
		s.PubDate = time.Unix(int64(pub), 0).UTC().Format("20060102")
	}
	s.View = int64(extractor.IntOrNone(extractor.TraverseObj(vd, "stat", "view")))
	s.Like = int64(extractor.IntOrNone(extractor.TraverseObj(vd, "stat", "like")))
	s.Reply = int64(extractor.IntOrNone(extractor.TraverseObj(vd, "stat", "reply")))
	s.Share = int64(extractor.IntOrNone(extractor.TraverseObj(vd, "stat", "share")))
	s.Season = parseSeason(vd)
	return s, nil
}

// parseSeason extracts the UGC 合集 (season) that a video belongs to, if any.
// The full episode list is SSR-injected under videoData.ugc_season.sections[].
// episodes[], so no extra API call is needed to enumerate the season.
func parseSeason(vd map[string]any) *seasonInfo {
	ugc, ok := extractor.TraverseObj(vd, "ugc_season").(map[string]any)
	if !ok {
		return nil
	}
	s := &seasonInfo{
		ID:    int64(extractor.IntOrNone(ugc["id"])),
		Title: extractor.StrOrNone(ugc["title"]),
	}
	sections, _ := extractor.TraverseObj(ugc, "sections").([]any)
	for _, sec := range sections {
		sm, ok := sec.(map[string]any)
		if !ok {
			continue
		}
		eps, _ := extractor.TraverseObj(sm, "episodes").([]any)
		for _, ep := range eps {
			em, ok := ep.(map[string]any)
			if !ok {
				continue
			}
			e := seasonEpisode{
				BVID:     extractor.StrOrNone(em["bvid"]),
				CID:      int64(extractor.IntOrNone(em["cid"])),
				Title:    extractor.StrOrNone(em["title"]),
				Duration: extractor.FloatOrNone(em["duration"]),
			}
			// arc carries the fuller metadata (cover, stats, author, pubdate).
			if arc, ok := extractor.TraverseObj(em, "arc").(map[string]any); ok {
				if e.Title == "" {
					e.Title = extractor.StrOrNone(arc["title"])
				}
				e.Pic = extractor.StrOrNone(arc["pic"])
				e.View = int64(extractor.IntOrNone(extractor.TraverseObj(arc, "stat", "view")))
				e.Like = int64(extractor.IntOrNone(extractor.TraverseObj(arc, "stat", "like")))
				e.Reply = int64(extractor.IntOrNone(extractor.TraverseObj(arc, "stat", "reply")))
				e.Share = int64(extractor.IntOrNone(extractor.TraverseObj(arc, "stat", "share")))
				e.Author = extractor.StrOrNone(extractor.TraverseObj(arc, "author", "name"))
				if pub := extractor.IntOrNone(arc["pubdate"]); pub > 0 {
					e.PubDate = time.Unix(int64(pub), 0).UTC().Format("20060102")
				}
			}
			if e.BVID != "" && e.CID != 0 {
				s.Episodes = append(s.Episodes, e)
			}
		}
	}
	if len(s.Episodes) == 0 {
		return nil
	}
	return s
}

// ---- WBI signing ----

// mixinKeyEncTab is the fixed permutation Bilibili uses to derive the WBI mixin
// key from the img/sub keys.
var mixinKeyEncTab = []int{
	46, 47, 18, 2, 53, 8, 23, 32, 15, 50, 10, 31, 58, 3, 45, 35, 27, 43, 5, 49,
	33, 9, 42, 19, 29, 28, 14, 39, 12, 38, 41, 13, 37, 48, 7, 16, 24, 55, 40, 61,
	26, 17, 0, 1, 60, 51, 30, 4, 22, 25, 54, 21, 56, 59, 6, 63, 57, 62, 11, 36,
	20, 34, 44, 52,
}

// wbiSign builds the w_rid signature for a WBI-protected request.
//
// imgKey and subKey come from the nav API's wbi_img (the 32 hex-ish chars inside
// each .png URL). params are the request's other query parameters; wts is the
// current unix timestamp in seconds.
func wbiSign(imgKey, subKey string, params url.Values, wts int64) string {
	orig := imgKey + subKey
	var mixin strings.Builder
	for _, idx := range mixinKeyEncTab[:32] {
		if idx < len(orig) {
			mixin.WriteByte(orig[idx])
		}
	}
	mixinKey := mixin.String()

	// Append wts and build the canonical query string (sorted by key).
	params.Set("wts", fmt.Sprintf("%d", wts))
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var canon strings.Builder
	for i, k := range keys {
		if i > 0 {
			canon.WriteByte('&')
		}
		canon.WriteString(k)
		canon.WriteByte('=')
		canon.WriteString(params.Get(k))
	}
	canon.WriteString(mixinKey)
	sum := md5.Sum([]byte(canon.String()))
	return fmt.Sprintf("%x", sum)
}

// ---- playurl media resolution ----

// wbiKeys holds the two WBI signing keys derived from the nav API. They are
// stable across a short session, so a season playlist fetches them once and
// reuses them for every episode.
type wbiKeys struct{ imgKey, subKey string }

// fetchWbiKeys obtains the current WBI signing keys from the nav endpoint.
func fetchWbiKeys(ctx *extractor.Context) (*wbiKeys, error) {
	nav, err := extractor.DownloadJSON(ctx, "https://api.bilibili.com/x/web-interface/nav", nil, nil)
	if err != nil {
		return nil, fmt.Errorf("nav: %w", err)
	}
	imgURL := extractor.StrOrNone(extractor.TraverseObj(nav, "data", "wbi_img", "img_url"))
	subURL := extractor.StrOrNone(extractor.TraverseObj(nav, "data", "wbi_img", "sub_url"))
	imgKey := keyFromURL(imgURL)
	subKey := keyFromURL(subURL)
	if imgKey == "" || subKey == "" {
		return nil, fmt.Errorf("could not derive WBI keys")
	}
	return &wbiKeys{imgKey: imgKey, subKey: subKey}, nil
}

// fetchFormatsForCid requests the WBI-signed playurl API for a specific
// bvid/cid and converts the DASH/FLV response into extractor Formats. The keys
// must be fetched once via fetchWbiKeys and reused across calls.
func fetchFormatsForCid(ctx *extractor.Context, bvid string, cid int64, keys *wbiKeys) ([]extractor.Format, error) {
	if cid == 0 {
		return nil, fmt.Errorf("missing cid")
	}
	if keys == nil {
		return nil, fmt.Errorf("missing WBI keys")
	}
	params := url.Values{}
	params.Set("bvid", bvid)
	params.Set("cid", fmt.Sprintf("%d", cid))
	// Request the highest quality the account is entitled to. Bilibili downgrades
	// to what the cookie (SESSDATA) permits, so 127 is safe without login (it
	// still returns the 720p default) and unlocks 1080p+ / 4K / HDR when a
	// logged-in cookie is supplied via --cookies.
	params.Set("qn", "127")
	// fnval 4048 = DASH + 4K + HDR + Dolby; the API returns the full quality
	// ladder in dash.video so the format selector (-S) can pick among them.
	params.Set("fnval", "4048")
	params.Set("fourk", "1")
	params.Set("platform", "pc")
	wts := time.Now().Unix()
	params.Set("w_rid", wbiSign(keys.imgKey, keys.subKey, params, wts))

	body, err := extractor.DownloadJSON(ctx, "https://api.bilibili.com/x/player/wbi/playurl", nil, params)
	if err != nil {
		return nil, fmt.Errorf("playurl: %w", err)
	}
	return extractPlayurlFormats(body)
}

// fetchFormats resolves the media for a single video (meta holds its bvid/cid).
// Any failure returns an error (the caller treats it as best-effort).
func fetchFormats(ctx *extractor.Context, meta *initialState) ([]extractor.Format, error) {
	keys, err := fetchWbiKeys(ctx)
	if err != nil {
		return nil, err
	}
	return fetchFormatsForCid(ctx, meta.BVID, meta.CID, keys)
}

// keyFromURL extracts the 32-char key embedded in a wbi_img URL.
func keyFromURL(u string) string {
	m := regexp.MustCompile(`/([0-9a-f]+)\.png`).FindStringSubmatch(u)
	if m == nil {
		return ""
	}
	return m[1]
}

// httpsThumbnail upgrades a Bilibili cover URL to HTTPS, since the page embeds
// http:// covers that some clients refuse to fetch.
func httpsThumbnail(u string) string {
	if u == "" {
		return ""
	}
	if strings.HasPrefix(u, "http://") {
		return "https://" + u[len("http://"):]
	}
	return u
}

// extractPlayurlFormats converts a playurl API response body into formats.
// Supports the DASH (dash.audio/video) and legacy FLV (durl) shapes.
func extractPlayurlFormats(body any) ([]extractor.Format, error) {
	data, ok := extractor.TraverseObj(body, "data").(map[string]any)
	if !ok {
		return nil, fmt.Errorf("playurl: missing data")
	}
	var out []extractor.Format
	// Bilibili's video CDN enforces a Referer hotlink check on media URLs;
	// without it the DASH/FLV streams return HTTP 403.
	mediaHeaders := map[string]string{"Referer": "https://www.bilibili.com/"}

	// DASH streams.
	if dash := extractor.TraverseObj(data, "dash"); dash != nil {
		for _, key := range []string{"video", "audio"} {
			arr, ok := extractor.TraverseObj(dash, key).([]any)
			if !ok {
				continue
			}
			for _, item := range arr {
				m, ok := item.(map[string]any)
				if !ok {
					continue
				}
				u := extractor.StrOrNone(extractor.TraverseObj(m, "baseUrl"))
				if u == "" {
					continue
				}
				f := extractor.Format{
					FormatID: fmt.Sprintf("%s-%d", key, extractor.IntOrNone(extractor.TraverseObj(m, "id"))),
					URL:      u,
					Protocol: "http",
					Ext:      "m4s",
					TBR:      extractor.FloatOrNone(extractor.TraverseObj(m, "bandwidth")) / 1000,
					Width:    extractor.IntOrNone(extractor.TraverseObj(m, "width")),
					Height:   extractor.IntOrNone(extractor.TraverseObj(m, "height")),
					Filesize: int64(extractor.FloatOrNone(extractor.TraverseObj(m, "size"))),
					Headers:  mediaHeaders,
				}
				if key == "video" {
					f.VCodec = extractor.StrOrNone(extractor.TraverseObj(m, "codecs"))
				} else {
					f.ACodec = extractor.StrOrNone(extractor.TraverseObj(m, "codecs"))
				}
				out = append(out, f)
			}
		}
		return out, nil
	}

	// Legacy FLV progressive streams.
	if durls, ok := extractor.TraverseObj(data, "durl").([]any); ok {
		for i, item := range durls {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			u := extractor.StrOrNone(extractor.TraverseObj(m, "url"))
			if u == "" {
				continue
			}
			out = append(out, extractor.Format{
				FormatID: fmt.Sprintf("flv-%d", i),
				URL:      u,
				Protocol: "http",
				Ext:      "flv",
				Filesize: int64(extractor.FloatOrNone(extractor.TraverseObj(m, "size"))),
				Headers:  mediaHeaders,
			})
		}
		return out, nil
	}

	return nil, fmt.Errorf("playurl: no dash/durl in response")
}

// extractJSONAssign extracts the object assigned to `key = {...}` from raw HTML.
func extractJSONAssign(html, key string) (map[string]any, error) {
	marker := key + "="
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
				var v any
				if uerr := json.Unmarshal([]byte(html[start:j+1]), &v); uerr == nil {
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
