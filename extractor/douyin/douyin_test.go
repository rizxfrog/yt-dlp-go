package douyin

import (
	"testing"

	"yt-dlp-go/extractor"
)

func TestExtractAwemeID(t *testing.T) {
	cases := []struct {
		url  string
		want string
	}{
		{"https://www.douyin.com/video/7666774315384372859", "7666774315384372859"},
		{"https://www.douyin.com/jingxuan?modal_id=7666774315384372859", "7666774315384372859"},
		{"https://www.douyin.com/jingxuan?foo=bar&modal_id=12345&x=1", "12345"},
		{"https://www.douyin.com/note/9988776655443322110", "9988776655443322110"},
		{"https://www.iesdouyin.com/share/video/1112223334445556667", "1112223334445556667"},
		{"https://www.douyin.com/user/MS4wLjABAAAAxxx", ""}, // profile page: no id
		{"https://www.bilibili.com/video/BV1xx", ""},
	}
	for _, c := range cases {
		if got := extractAwemeID(c.url); got != c.want {
			t.Errorf("extractAwemeID(%q) = %q, want %q", c.url, got, c.want)
		}
	}
}

func TestMatch(t *testing.T) {
	ie := DouyinIE{}
	if !ie.Match("https://www.douyin.com/video/7666774315384372859") {
		t.Error("video URL should match")
	}
	if !ie.Match("https://www.douyin.com/jingxuan?modal_id=123") {
		t.Error("jingxuan modal URL should match")
	}
	if ie.Match("https://www.douyin.com/user/abc") {
		t.Error("profile URL should not match")
	}
}

func TestParseURLKey(t *testing.T) {
	cases := []struct {
		key   string
		codec string
		res   int
		tbr   float64
	}{
		{"v2700fgi0000d9itd17og65v7dbo6jlg_bytevc1_720p_972977", "bytevc1", 720, 972.977},
		{"vxxx_h264_540p_1649701", "h264", 540, 1649.701},
		{"", "", 0, 0},
		{"garbage_without_pattern", "", 0, 0},
	}
	for _, c := range cases {
		codec, res, tbr := parseURLKey(c.key)
		if codec != c.codec || res != c.res || tbr != c.tbr {
			t.Errorf("parseURLKey(%q) = (%q,%d,%.3f), want (%q,%d,%.3f)",
				c.key, codec, res, tbr, c.codec, c.res, c.tbr)
		}
	}
}

func TestMapCodec(t *testing.T) {
	if mapCodec("bytevc1") != "h265" {
		t.Error("bytevc1 should map to h265")
	}
	if mapCodec("h264") != "h264" {
		t.Error("h264 should pass through")
	}
	if mapCodec("bytevc2") != "bytevc2" {
		t.Error("bytevc2 should pass through (flagged unplayable elsewhere)")
	}
}

func addr(urlKey, u string) map[string]any {
	return map[string]any{
		"url_key":   urlKey,
		"url_list":  []any{u},
		"data_size": float64(1000),
	}
}

func TestBuildFormats(t *testing.T) {
	video := map[string]any{
		"width":         float64(1080),
		"height":        float64(1920),
		"has_watermark": true,
		"play_addr":     addr("vxxx_h264_540p_1649701", "https://cdn.example/play.mp4"),
		"play_addr_265": addr("vxxx_bytevc1_720p_972977", "https://cdn.example/play265.mp4"),
		"download_addr": addr("", "https://cdn.example/watermarked.mp4"),
		"bit_rate": []any{
			map[string]any{
				"gear_name": "720_1_1",
				"bit_rate":  float64(972977),
				"FPS":       float64(30),
				"play_addr": addr("vxxx_bytevc1_720p_972977", "https://cdn.example/gear720.mp4"),
			},
		},
	}

	formats := buildFormats(video)
	if len(formats) == 0 {
		t.Fatal("expected formats")
	}

	byID := map[string]extractor.Format{}
	for _, f := range formats {
		byID[f.FormatID] = f
	}

	// No-watermark playback stream.
	p, ok := byID["play_addr"]
	if !ok {
		t.Fatal("missing play_addr format")
	}
	if p.VCodec != "h264" || p.Preference != -1 {
		t.Errorf("play_addr vcodec/pref = %q/%d, want h264/-1", p.VCodec, p.Preference)
	}
	if p.Width != 540 || p.Height != 960 {
		t.Errorf("play_addr dims = %dx%d, want 540x960", p.Width, p.Height)
	}

	// No-watermark H265 720p.
	if f, ok := byID["play_addr_265"]; ok {
		if f.VCodec != "h265" {
			t.Errorf("play_addr_265 vcodec = %q, want h265", f.VCodec)
		}
		if f.Width != 720 || f.Height != 1280 {
			t.Errorf("play_addr_265 dims = %dx%d, want 720x1280", f.Width, f.Height)
		}
	}

	// Multi-bitrate gear keeps its own bitrate/FPS.
	g, ok := byID["720_1_1"]
	if !ok {
		t.Fatal("missing bit_rate gear format")
	}
	if g.TBR != 972.977 || g.FPS != 30 {
		t.Errorf("gear tbr/fps = %.3f/%.1f, want 972.977/30", g.TBR, g.FPS)
	}

	// Watermarked download stream is de-prioritised and full-resolution.
	d, ok := byID["download_addr"]
	if !ok {
		t.Fatal("missing download_addr format")
	}
	if d.Preference != -2 {
		t.Errorf("download_addr pref = %d, want -2 (watermarked)", d.Preference)
	}
	if d.VCodec != "h264" {
		t.Errorf("download_addr vcodec = %q, want h264", d.VCodec)
	}
	if d.Width != 1080 || d.Height != 1920 {
		t.Errorf("download_addr dims = %dx%d, want 1080x1920 (no url_key)", d.Width, d.Height)
	}
}

func TestParseAwemeDetail(t *testing.T) {
	detail := map[string]any{
		"aweme_id":    "7666774315384372859",
		"desc":        "随机奖励一位幸运之子",
		"create_time": float64(1785066230),
		"duration":    float64(2202203),
		"author":      map[string]any{"unique_id": "weilai321", "nickname": "魏来"},
		"video": map[string]any{
			"width":         float64(1080),
			"height":        float64(1920),
			"has_watermark": true,
			"cover":         map[string]any{"url_list": []any{"https://cdn.example/cover.jpg"}},
			"play_addr":     addr("vxxx_h264_540p_1649701", "https://cdn.example/play.mp4"),
			"download_addr": addr("", "https://cdn.example/watermarked.mp4"),
		},
	}

	info := parseAwemeDetail(detail, "https://www.douyin.com/video/7666774315384372859")
	if info.ID != "7666774315384372859" {
		t.Errorf("id = %q", info.ID)
	}
	if info.Title != "随机奖励一位幸运之子" {
		t.Errorf("title = %q", info.Title)
	}
	if info.Uploader != "weilai321" {
		t.Errorf("uploader = %q", info.Uploader)
	}
	if info.UploadDate != "20260726" {
		t.Errorf("upload_date = %q, want 20260726", info.UploadDate)
	}
	if info.Duration != 2202.203 {
		t.Errorf("duration = %f, want 2202.203", info.Duration)
	}
	if info.Thumbnail != "https://cdn.example/cover.jpg" {
		t.Errorf("thumbnail = %q", info.Thumbnail)
	}
	if len(info.Formats) != 2 {
		t.Errorf("formats = %d, want 2", len(info.Formats))
	}
}
