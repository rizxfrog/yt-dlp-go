// Package douyin implements an InfoExtractor for douyin.com (the Chinese
// TikTok).
//
// Douyin serves a video's data through a web JSON endpoint
// (https://www.douyin.com/aweme/v1/web/aweme/detail/) that — unlike the search
// and feed endpoints — does NOT require the a_bogus / X-Bogus JS signature. It
// only needs a set of fresh cookies: an unauthenticated s_v_web_id is usually
// enough, and a logged-in sid_tt (via --cookies) can unlock higher qualities.
// This mirrors yt-dlp's DouyinIE, which relies on the same "fresh cookies"
// behaviour rather than implementing the JS verification challenge.
//
// Watermark handling: Douyin exposes two families of streams — the playback
// streams (play_addr / play_addr_265 / play_addr_h264 / bit_rate[]) carry no
// watermark, while download_addr has the creator watermark burned in. We assign
// a lower preference to download_addr so the default "best" selector favours
// the unwatermarked source, exactly like yt-dlp.
package douyin

import (
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"time"

	"yt-dlp-go/extractor"
)

// DouyinIE extracts from douyin.com / iesdouyin.com.
type DouyinIE struct{}

func init() { extractor.Register(DouyinIE{}) }

func (DouyinIE) Name() string { return "douyin" }

var (
	// douyin.com/video/<id>, douyin.com/note/<id>, iesdouyin.com/share/video/<id>.
	douyinVideoRE = regexp.MustCompile(`(?i)(?:douyin|iesdouyin)\.com/(?:share/)?(?:video|note)/([0-9]+)`)
	// douyin.com/jingxuan?modal_id=<id> (and any other path carrying modal_id).
	douyinModalRE = regexp.MustCompile(`(?i)douyin\.com[^\s#]*[?&]modal_id=([0-9]+)`)
	// url_key format: <v-hash>_<codec>_<res>p_<bitrate>, e.g.
	// v2700fgi0000d9itd17og65v7dbo6jlg_bytevc1_720p_972977.
	urlKeyRE = regexp.MustCompile(`v[^_]+_([^_]+)_(\d+)p_(\d+)`)
)

// extractAwemeID pulls the numeric aweme id out of the supported URL forms.
func extractAwemeID(u string) string {
	if m := douyinModalRE.FindStringSubmatch(u); m != nil {
		return m[1]
	}
	if m := douyinVideoRE.FindStringSubmatch(u); m != nil {
		return m[1]
	}
	return ""
}

func (DouyinIE) Match(u string) bool { return extractAwemeID(u) != "" }

// Extract fetches the aweme detail JSON and normalises it into an Info.
func (DouyinIE) Extract(ctx *extractor.Context, pageURL string) (*extractor.Info, error) {
	awemeID := extractAwemeID(pageURL)
	if awemeID == "" {
		return nil, fmt.Errorf("could not parse Douyin aweme id from %q", pageURL)
	}

	query := url.Values{}
	query.Set("aweme_id", awemeID)
	body, err := extractor.DownloadJSON(ctx,
		"https://www.douyin.com/aweme/v1/web/aweme/detail/",
		map[string]string{"Referer": "https://www.douyin.com/"}, query)
	if err != nil {
		return nil, err
	}
	detail, ok := extractor.TraverseObj(body, "aweme_detail").(map[string]any)
	if !ok {
		return nil, fmt.Errorf("douyin: aweme_detail missing from API response (are the cookies fresh?)")
	}
	return parseAwemeDetail(detail, pageURL), nil
}

// parseAwemeDetail converts a raw aweme_detail object into a normalised Info.
func parseAwemeDetail(detail map[string]any, pageURL string) *extractor.Info {
	desc := extractor.StrOrNone(detail["desc"])
	info := &extractor.Info{
		ID:          extractor.StrOrNone(detail["aweme_id"]),
		Title:       desc,
		Description: desc,
		Uploader:    extractor.StrOrNone(extractor.TraverseObj(detail, "author", "unique_id")),
		WebpageURL:  pageURL,
		Ext:         "mp4",
		Subtitles:   map[string][]extractor.Subtitle{},
		Raw:         detail,
	}
	if ct := extractor.IntOrNone(detail["create_time"]); ct > 0 {
		info.UploadDate = time.Unix(int64(ct), 0).UTC().Format("20060102")
	}
	if dur := extractor.IntOrNone(detail["duration"]); dur > 0 {
		info.Duration = float64(dur) / 1000
	}
	if video, ok := detail["video"].(map[string]any); ok {
		info.Thumbnail = coverURL(video)
		info.Formats = buildFormats(video)
	}
	return info
}

// buildFormats derives the download formats from the aweme_detail.video object.
func buildFormats(video map[string]any) []extractor.Format {
	width := extractor.IntOrNone(video["width"])
	height := extractor.IntOrNone(video["height"])
	ratio := 0.5625 // 9:16 fallback
	if width > 0 && height > 0 {
		ratio = float64(width) / float64(height)
	}
	hasWatermark := false
	if b, ok := video["has_watermark"].(bool); ok {
		hasWatermark = b
	}

	var out []extractor.Format
	seen := map[string]bool{}
	add := func(f extractor.Format) {
		if f.URL == "" || seen[f.URL] {
			return
		}
		seen[f.URL] = true
		out = append(out, f)
	}

	// addAddr builds one format from a video.<key> object. When vcodecOverride is
	// non-empty it wins over the codec encoded in the url_key (used for fields
	// whose url_key is missing or ambiguous).
	addAddr := func(key, note, vcodecOverride string, pref int, formatID string) {
		addr, ok := video[key].(map[string]any)
		if !ok {
			return
		}
		u := firstURL(addr)
		if u == "" {
			return
		}
		codec, res, tbr := parseURLKey(extractor.StrOrNone(addr["url_key"]))
		if vcodecOverride != "" {
			codec = vcodecOverride
		}
		f := extractor.Format{
			FormatID:   formatID,
			URL:        u,
			Protocol:   "http",
			Ext:        "mp4",
			VCodec:     mapCodec(codec),
			ACodec:     "aac",
			FormatNote: note,
			Preference: pref,
			TBR:        tbr,
			Filesize:   int64(extractor.FloatOrNone(addr["data_size"])),
		}
		// bytevc2 is Bytedance's own H266/VVC codec, not yet playable.
		if codec == "bytevc2" {
			f.FormatNote = note + " (UNPLAYABLE)"
			f.Preference = -100
		}
		if res > 0 {
			f.Width, f.Height = dimsFor(res, ratio)
		} else {
			f.Width, f.Height = width, height
		}
		add(f)
	}

	// No-watermark playback streams first, so dedup keeps them over mirrors.
	addAddr("play_addr", "Direct video", "", -1, "play_addr")
	addAddr("play_addr_265", "Direct video", "h265", -1, "play_addr_265")
	addAddr("play_addr_h264", "Direct video", "h264", -1, "play_addr_h264")
	addAddr("play_addr_bytevc1", "Direct video", "h265", -1, "play_addr_bytevc1")

	// Multi-bitrate no-watermark gears (the highest quality lives here).
	if gears, ok := video["bit_rate"].([]any); ok {
		for _, g := range gears {
			gear, ok := g.(map[string]any)
			if !ok {
				continue
			}
			pa, ok := gear["play_addr"].(map[string]any)
			if !ok {
				continue
			}
			u := firstURL(pa)
			if u == "" {
				continue
			}
			codec, res, _ := parseURLKey(extractor.StrOrNone(pa["url_key"]))
			f := extractor.Format{
				FormatID:   extractor.StrOrNone(gear["gear_name"]),
				URL:        u,
				Protocol:   "http",
				Ext:        "mp4",
				VCodec:     mapCodec(codec),
				ACodec:     "aac",
				FormatNote: "Playback video",
				Preference: -1,
				TBR:        extractor.FloatOrNone(gear["bit_rate"]) / 1000,
				FPS:        extractor.FloatOrNone(gear["FPS"]),
				Filesize:   int64(extractor.FloatOrNone(pa["data_size"])),
			}
			if codec == "bytevc2" {
				f.FormatNote = "Playback video (UNPLAYABLE)"
				f.Preference = -100
			}
			if res > 0 {
				f.Width, f.Height = dimsFor(res, ratio)
			} else {
				f.Width, f.Height = width, height
			}
			add(f)
		}
	}

	// Watermarked download stream: full source quality but with the creator's
	// watermark burned in, so it is de-prioritised below the clean streams.
	note := "Download video"
	pref := -1
	if hasWatermark {
		note += ", watermarked"
		pref = -2
	}
	addAddr("download_addr", note, "h264", pref, "download_addr")

	return out
}

// parseURLKey decodes a Douyin url_key into (codec, short-side resolution,
// total bitrate in kbps).
func parseURLKey(urlKey string) (codec string, res int, tbr float64) {
	m := urlKeyRE.FindStringSubmatch(urlKey)
	if m == nil {
		return "", 0, 0
	}
	codec = m[1]
	res, _ = strconv.Atoi(m[2])
	if b, err := strconv.Atoi(m[3]); err == nil {
		tbr = float64(b) / 1000
	}
	return codec, res, tbr
}

// mapCodec normalises Bytedance's codec names: bytevc1 is H.265, bytevc2 is the
// unplayable H.266/VVC, everything else (h264, ...) passes through unchanged.
func mapCodec(c string) string {
	if c == "bytevc1" {
		return "h265"
	}
	return c
}

// dimsFor turns a short-side resolution ("720p") into a (width, height) pair
// using the video's aspect ratio, mirroring yt-dlp's web-format handling.
func dimsFor(res int, ratio float64) (w, h int) {
	if ratio < 1 { // portrait: the resolution names the width (short side)
		w = res
		h = int(float64(res)/ratio + 0.5)
		h -= h % 2
	} else { // landscape: the resolution names the height
		h = res
		w = int(float64(res)*ratio + 0.5)
		w += w % 2
	}
	return w, h
}

// firstURL returns the first (primary) CDN URL of an addr/cover object. Douyin
// provides several mirror URLs per stream; we keep the first one for a clean
// format table (mirrors are equivalent, and the downloader retries on failure).
func firstURL(v any) string {
	m, ok := v.(map[string]any)
	if !ok {
		return ""
	}
	list, ok := m["url_list"].([]any)
	if !ok || len(list) == 0 {
		return ""
	}
	return extractor.StrOrNone(list[0])
}

// coverURL picks the best available cover image.
func coverURL(video map[string]any) string {
	for _, k := range []string{"cover", "origin_cover", "dynamic_cover"} {
		if u := firstURL(video[k]); u != "" {
			return u
		}
	}
	return ""
}
