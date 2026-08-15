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

	info := &extractor.Info{
		ID:          meta.BVID,
		Title:       meta.Title,
		Description: meta.Desc,
		Uploader:    meta.Owner,
		UploadDate:  meta.PubDate,
		Thumbnail:   httpsThumbnail(meta.Pic),
		WebpageURL:  pageURL,
		Ext:         "mp4",
		Duration:    meta.Duration,
		Subtitles:   map[string][]extractor.Subtitle{},
		Raw:         meta.Raw,
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
	Raw      map[string]any
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
	return s, nil
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

// fetchFormats requests the WBI-signed playurl API and converts the DASH/FLV
// response into extractor Formats. Any failure returns an error (the caller
// treats it as best-effort).
func fetchFormats(ctx *extractor.Context, meta *initialState) ([]extractor.Format, error) {
	if meta.CID == 0 {
		return nil, fmt.Errorf("missing cid")
	}
	// Fetch the nav endpoint to obtain the WBI keys.
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

	params := url.Values{}
	params.Set("bvid", meta.BVID)
	params.Set("cid", fmt.Sprintf("%d", meta.CID))
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
	params.Set("w_rid", wbiSign(imgKey, subKey, params, wts))

	body, err := extractor.DownloadJSON(ctx, "https://api.bilibili.com/x/player/wbi/playurl", nil, params)
	if err != nil {
		return nil, fmt.Errorf("playurl: %w", err)
	}
	return extractPlayurlFormats(body)
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
