package hongguo

import (
	"strings"
	"testing"
)

func TestMatch(t *testing.T) {
	ie := HongguoIE{}
	cases := map[string]bool{
		"https://hongguoduanju.com/player/7404776999618612286": true,
		"https://www.hongguoduanju.com/player/123":             true,
		"https://www.bilibili.com/video/BV1xx":                 false,
		"https://hongguoduanju.com/some/other/page":            false,
	}
	for u, want := range cases {
		if got := ie.Match(u); got != want {
			t.Errorf("Match(%q) = %v, want %v", u, got, want)
		}
	}
}

func TestExtractJSONAssign(t *testing.T) {
	html := `<script>var x=1;</script><script data-script-src="modern-inline">_ROUTER_DATA = {"loaderData":{"a":{"video_player_info":{"main_url":"https://cdn.example/v.mp4"}}}}</script>`
	obj, err := extractJSONAssign(html, "_ROUTER_DATA")
	if err != nil {
		t.Fatal(err)
	}
	u := obj["loaderData"].(map[string]any)["a"].(map[string]any)["video_player_info"].(map[string]any)["main_url"]
	if u != "https://cdn.example/v.mp4" {
		t.Errorf("main_url = %v", u)
	}
}

func TestParseRouterData(t *testing.T) {
	obj := map[string]any{
		"loaderData": map[string]any{
			"player_(7404776999618612286)/page": map[string]any{
				"vid": "7404780627477662745",
				"video_player_info": map[string]any{
					"main_url":   "https://cdn.example/v1.mp4",
					"duration":   float64(90.976),
					"width":      "720",
					"height":     "1280",
					"poster_url": "https://cdn.example/poster.jpg",
				},
				"seriesDetail": map[string]any{
					"series_name":  "朝阳似火",
					"series_intro": "一部关于复仇的短剧",
					"series_cover": "https://cdn.example/cover.jpg",
					"vid_list": []any{
						"7404780627477662745",
						"7404780649162230846",
					},
				},
			},
		},
	}

	info, err := parseRouterData(obj, "https://hongguoduanju.com/player/7404776999618612286")
	if err != nil {
		t.Fatal(err)
	}
	if info.ID != "7404780627477662745" {
		t.Errorf("id = %q", info.ID)
	}
	if info.Title != "朝阳似火 第1集" {
		t.Errorf("title = %q", info.Title)
	}
	if info.Description != "一部关于复仇的短剧" {
		t.Errorf("description = %q", info.Description)
	}
	if info.Thumbnail != "https://cdn.example/cover.jpg" {
		t.Errorf("thumbnail = %q", info.Thumbnail)
	}
	if info.Duration != 90.976 {
		t.Errorf("duration = %f", info.Duration)
	}
	if len(info.Formats) != 1 {
		t.Fatalf("formats = %d, want 1", len(info.Formats))
	}
	f := info.Formats[0]
	if f.URL != "https://cdn.example/v1.mp4" || f.Width != 720 || f.Height != 1280 {
		t.Errorf("format = %+v", f)
	}
	if f.VCodec != "h264" || f.ACodec != "aac" {
		t.Errorf("codec = %s/%s, want h264/aac", f.VCodec, f.ACodec)
	}
}

func TestParseRouterData_NoVideo(t *testing.T) {
	obj := map[string]any{
		"loaderData": map[string]any{
			"player_(1)/page": map[string]any{
				"vid":               "2",
				"video_player_info": map[string]any{},
			},
		},
	}
	_, err := parseRouterData(obj, "https://hongguoduanju.com/player/1")
	if err == nil || !strings.Contains(err.Error(), "no playable video") {
		t.Errorf("err = %v, want no-playable-video error", err)
	}
}
