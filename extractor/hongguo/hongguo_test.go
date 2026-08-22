package hongguo

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"yt-dlp-go/extractor"
)

func TestMatch(t *testing.T) {
	ie := HongguoIE{}
	cases := map[string]bool{
		"https://hongguoduanju.com/player/7404776999618612286":                     true,
		"https://hongguoduanju.com/player/7404776999618612286/7404780649162230846": true,
		"https://hongguoduanju.com/detail?series_id=7671213206915779646":           true,
		"https://novelquickapp.com/s/OyZtu4aCveY/":                                 true,
		"hongguo:7671213206915779646":                                              true,
		"https://www.bilibili.com/video/BV1xx":                                     false,
	}
	for u, want := range cases {
		if got := ie.Match(u); got != want {
			t.Errorf("Match(%q) = %v, want %v", u, got, want)
		}
	}
}

func TestInputVideoID(t *testing.T) {
	for raw, want := range map[string]string{
		"hongguo:7671213206915779646":                                              "7671213206915779646",
		"https://hongguoduanju.com/detail?series_id=7671213206915779646":           "7671213206915779646",
		"https://hongguoduanju.com/player/7671213206915779646/7671229514868853784": "7671229514868853784",
	} {
		got, err := inputVideoID(nil, raw)
		if err != nil || got != want {
			t.Errorf("inputVideoID(%q) = %q, %v; want %q", raw, got, err, want)
		}
	}
}

func TestExpandShareOneRedirect(t *testing.T) {
	scheme, _ := json.Marshal(map[string]any{"video_id": "7671213206915779646"})
	z := "snssdk1128://video-detail-share?schemeParams=" + url.QueryEscape(string(scheme))
	target := "https://example.test/video-detail-share?zlink=" + url.QueryEscape(z)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target, http.StatusFound)
	}))
	defer srv.Close()
	ctx := testContext(srv.Client())
	got, err := expandShare(ctx, srv.URL)
	if err != nil || got != "7671213206915779646" {
		t.Fatalf("expandShare = %q, %v", got, err)
	}
}

func TestExpandShareEscapedReportParams(t *testing.T) {
	report, _ := json.Marshal(map[string]any{"content_id": "7671213206915779646"})
	scheme, _ := json.Marshal(map[string]any{"report_params": url.QueryEscape(string(report))})
	z := "snssdk1128://video-detail-share?schemeParams=" + url.QueryEscape(string(scheme))
	target := "https://example.test/video-detail-share?zlink=" + url.QueryEscape(z)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target, http.StatusFound)
	}))
	defer srv.Close()
	got, err := expandShare(testContext(srv.Client()), srv.URL)
	if err != nil || got != "7671213206915779646" {
		t.Fatalf("expandShare = %q, %v", got, err)
	}
}

func TestExtractSeriesExactBlock(t *testing.T) {
	html := `x {"series_id":"9999999999999999999","series_name":"推荐剧","vid_list":["9111111111111111111"]}
	{"series_id":"7671213206915779646","series_name":"开局一碗泡面","vid_list":["7671229514868853784","7671229514868853785"]}
	{"series_id":"8888888888888888888","series_name":"下一个推荐","vid_list":["8222222222222222222"]}`
	name, vids, err := extractSeries(html, "7671213206915779646")
	if err != nil {
		t.Fatal(err)
	}
	if name != "开局一碗泡面" || strings.Join(vids, ",") != "7671229514868853784,7671229514868853785" {
		t.Fatalf("name=%q vids=%v", name, vids)
	}
}

func TestMakeVideoPayloadCompact(t *testing.T) {
	got := string(makeVideoPayload("1004", "7671229514868853784"))
	want := `{"biz_param":{"detail_page_version":0,"device_level":3,"disable_digg_stat":false,"need_all_video_definition":true,"need_mp4_align":false,"use_os_player":false,"use_server_dns":false,"video_platform":1024},"mixed_video_id_map":{"1004":["7671229514868853784"]}}`
	if got != want {
		t.Fatalf("payload=%s", got)
	}
}

func TestExtractFallbackVariants(t *testing.T) {
	root := map[string]any{"data": map[string]any{"v": map[string]any{"video_model": `{"fallback_api":{"fallback_api":"https://example.test/fallback"}}`}}}
	fallback, model, err := extractFallback(root, "v")
	if err != nil || fallback != "https://example.test/fallback" || model == nil {
		t.Fatalf("fallback=%q model=%v err=%v", fallback, model, err)
	}
}

func TestExtractFallbackRejectsWrongVideo(t *testing.T) {
	root := map[string]any{"data": map[string]any{"other": map[string]any{"video_model": `{"fallback_api":"https://example.test/wrong"}`}}}
	if _, _, err := extractFallback(root, "wanted"); err == nil {
		t.Fatal("expected missing requested video to be rejected")
	}
}

func TestSelectBestVariant(t *testing.T) {
	got := selectBestVariant(map[string]any{
		"a": map[string]any{"vheight": float64(720), "bitrate": float64(500)},
		"b": map[string]any{"vheight": float64(1080), "bitrate": float64(800)},
		"c": map[string]any{"vheight": float64(1080), "bitrate": float64(900)},
	})
	if extractorInt(got["bitrate"]) != 900 {
		t.Fatalf("best=%v", got)
	}
}

func testContext(client *http.Client) *extractor.Context {
	return &extractor.Context{Client: client, Headers: map[string]string{}}
}
func extractorInt(v any) int {
	if n, ok := v.(float64); ok {
		return int(n)
	}
	return 0
}
