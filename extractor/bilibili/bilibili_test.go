package bilibili

import (
	"crypto/md5"
	"fmt"
	"net/url"
	"testing"

	"yt-dlp-go/extractor"
)

const fixtureHTML = `<!doctype html><html><head><title>test</title></head><body>
<script>window.__INITIAL_STATE__={"videoData":{"bvid":"BV1xx411c7XD","aid":12345,"cid":67890,"title":"演示视频","desc":"这是描述","owner":{"name":"UP主小明"},"pic":"https://i0.hdslb.com/cover.jpg","duration":123,"pubdate":1700000000},"error":null};</script>
</body></html>`

func TestParseInitialState(t *testing.T) {
	meta, err := parseInitialState(fixtureHTML)
	if err != nil {
		t.Fatalf("parseInitialState: %v", err)
	}
	if meta.BVID != "BV1xx411c7XD" {
		t.Errorf("BVID = %q", meta.BVID)
	}
	if meta.Title != "演示视频" {
		t.Errorf("Title = %q", meta.Title)
	}
	if meta.Owner != "UP主小明" {
		t.Errorf("Owner = %q", meta.Owner)
	}
	if meta.CID != 67890 {
		t.Errorf("CID = %d", meta.CID)
	}
	if meta.Duration != 123 {
		t.Errorf("Duration = %v", meta.Duration)
	}
	if meta.PubDate != "20231114" {
		t.Errorf("PubDate = %q (want 20231114)", meta.PubDate)
	}
}

func TestParseInitialState_NotFound(t *testing.T) {
	if _, err := parseInitialState("<html>no state here</html>"); err == nil {
		t.Fatal("expected error for missing __INITIAL_STATE__")
	}
}

func TestWbiSign_IsDeterministic(t *testing.T) {
	params := url.Values{}
	params.Set("bvid", "BV1xx411c7XD")
	params.Set("cid", "67890")
	params.Set("qn", "64")
	params.Set("fnval", "16")

	a := wbiSign("abcdef0123456789abcdef0123456789", "0123456789abcdef0123456789abcdef", params, 1700000000)
	b := wbiSign("abcdef0123456789abcdef0123456789", "0123456789abcdef0123456789abcdef", params, 1700000000)
	if a != b {
		t.Fatalf("wbiSign not deterministic: %q vs %q", a, b)
	}
	if len(a) != 32 {
		t.Fatalf("wbiSign length = %d, want 32", len(a))
	}

	// Cross-check against a manual recomputation of the documented algorithm.
	orig := "abcdef0123456789abcdef0123456789" + "0123456789abcdef0123456789abcdef"
	var mixin []byte
	for _, idx := range mixinKeyEncTab[:32] {
		mixin = append(mixin, orig[idx])
	}
	p := url.Values{}
	p.Set("bvid", "BV1xx411c7XD")
	p.Set("cid", "67890")
	p.Set("qn", "64")
	p.Set("fnval", "16")
	p.Set("wts", "1700000000")
	keys := []string{"bvid", "cid", "fnval", "qn", "wts"}
	sortKeys(keys)
	var canon string
	for i, k := range keys {
		if i > 0 {
			canon += "&"
		}
		canon += k + "=" + p.Get(k)
	}
	canon += string(mixin)
	want := fmt.Sprintf("%x", md5.Sum([]byte(canon)))
	if a != want {
		t.Fatalf("wbiSign = %q, manual = %q", a, want)
	}
}

func sortKeys(keys []string) {
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j-1] > keys[j]; j-- {
			keys[j-1], keys[j] = keys[j], keys[j-1]
		}
	}
}

func TestKeyFromURL(t *testing.T) {
	got := keyFromURL("https://i0.hdslb.com/bfs/wbi/abcdef0123456789abcdef0123456789.png")
	if got != "abcdef0123456789abcdef0123456789" {
		t.Errorf("keyFromURL = %q", got)
	}
}

func TestExtractPlayurlFormats_DASH(t *testing.T) {
	body := map[string]any{
		"data": map[string]any{
			"dash": map[string]any{
				"video": []any{
					map[string]any{"id": 80, "baseUrl": "https://v.example/v.m4s", "codecs": "avc1.64001f", "width": 1920, "height": 1080, "bandwidth": 3000000, "size": 123456},
				},
				"audio": []any{
					map[string]any{"id": 30280, "baseUrl": "https://a.example/a.m4s", "codecs": "mp4a.40.2", "bandwidth": 192000, "size": 23456},
				},
			},
		},
	}
	formats, err := extractPlayurlFormats(body)
	if err != nil {
		t.Fatalf("extractPlayurlFormats: %v", err)
	}
	if len(formats) != 2 {
		t.Fatalf("got %d formats, want 2", len(formats))
	}
	v := formats[0]
	if v.VCodec != "avc1.64001f" || v.Height != 1080 || v.URL != "https://v.example/v.m4s" {
		t.Errorf("video format wrong: %+v", v)
	}
	a := formats[1]
	if a.ACodec != "mp4a.40.2" || a.URL != "https://a.example/a.m4s" {
		t.Errorf("audio format wrong: %+v", a)
	}
}

func TestExtractPlayurlFormats_DURL(t *testing.T) {
	body := map[string]any{
		"data": map[string]any{
			"durl": []any{
				map[string]any{"url": "https://f.example/1.flv", "size": 999},
				map[string]any{"url": "https://f.example/2.flv", "size": 888},
			},
		},
	}
	formats, err := extractPlayurlFormats(body)
	if err != nil {
		t.Fatalf("extractPlayurlFormats: %v", err)
	}
	if len(formats) != 2 || formats[0].Ext != "flv" {
		t.Fatalf("got %+v", formats)
	}
}

// When fnval=4048 the API returns the whole quality ladder: multiple video
// streams (including HDR) plus audio. All must be exposed so -S can pick.
func TestExtractPlayurlFormats_MultiQualityHDR(t *testing.T) {
	body := map[string]any{
		"data": map[string]any{
			"dash": map[string]any{
				"video": []any{
					map[string]any{"id": 80, "baseUrl": "https://v.example/1080.m4s", "codecs": "avc1.64001f", "width": 1920, "height": 1080, "bandwidth": 3000000, "size": 123456},
					map[string]any{"id": 112, "baseUrl": "https://v.example/1080h.m4s", "codecs": "hev1.1.6.L120.90", "width": 1920, "height": 1080, "bandwidth": 4000000, "size": 200000},
					map[string]any{"id": 120, "baseUrl": "https://v.example/4k.m4s", "codecs": "av01.0.12M.10.0.110.09.18.0", "width": 3840, "height": 2160, "bandwidth": 12000000, "size": 900000},
				},
				"audio": []any{
					map[string]any{"id": 30280, "baseUrl": "https://a.example/a.m4s", "codecs": "mp4a.40.2", "bandwidth": 192000, "size": 23456},
				},
			},
		},
	}
	formats, err := extractPlayurlFormats(body)
	if err != nil {
		t.Fatalf("extractPlayurlFormats: %v", err)
	}
	if len(formats) != 4 {
		t.Fatalf("got %d formats, want 4: %+v", len(formats), formats)
	}
	// HDR/4K stream must be present with correct resolution.
	var uhd extractor.Format
	found := false
	for _, f := range formats {
		if f.Height == 2160 {
			uhd = f
			found = true
		}
	}
	if !found {
		t.Fatalf("4K video stream missing: %+v", formats)
	}
	if uhd.Width != 3840 || uhd.VCodec != "av01.0.12M.10.0.110.09.18.0" {
		t.Errorf("4K stream metadata wrong: %+v", uhd)
	}
}

func TestHttpsThumbnail(t *testing.T) {
	if got := httpsThumbnail("http://i0.hdslb.com/cover.jpg"); got != "https://i0.hdslb.com/cover.jpg" {
		t.Errorf("got %q", got)
	}
	if got := httpsThumbnail("https://i0.hdslb.com/cover.jpg"); got != "https://i0.hdslb.com/cover.jpg" {
		t.Errorf("https URL should be unchanged, got %q", got)
	}
	if got := httpsThumbnail(""); got != "" {
		t.Errorf("empty should stay empty, got %q", got)
	}
}

const fixtureSeasonHTML = `<!doctype html><html><body>
<script>window.__INITIAL_STATE__={"videoData":{"bvid":"BV1BYuo6JE1w","aid":117072134733981,"cid":40789936120,"title":"演示视频","owner":{"name":"UP主"},"pic":"https://i0.hdslb.com/cover.jpg","duration":905,"pubdate":1700000000,"ugc_season":{"id":8804028,"title":"测试合集","sections":[{"title":"正片","episodes":[
{"bvid":"BV1BYuo6JE1w","cid":40789936120,"title":"第一集","duration":905,"arc":{"pic":"https://p1.jpg","stat":{"view":100,"like":10,"reply":2,"share":1},"author":{"name":"UP主"},"pubdate":1700000000}},
{"bvid":"BV1D2ue63EoN","cid":40823360504,"title":"第二集","duration":509,"arc":{"pic":"https://p2.jpg","stat":{"view":200,"like":20,"reply":3,"share":2},"author":{"name":"UP主"},"pubdate":1700000000}}
]}]}}};</script>
</body></html>`

func TestParseSeason(t *testing.T) {
	meta, err := parseInitialState(fixtureSeasonHTML)
	if err != nil {
		t.Fatalf("parseInitialState: %v", err)
	}
	if meta.Season == nil {
		t.Fatal("Season should be populated")
	}
	if meta.Season.ID != 8804028 || meta.Season.Title != "测试合集" {
		t.Errorf("season id/title = %d/%q", meta.Season.ID, meta.Season.Title)
	}
	if len(meta.Season.Episodes) != 2 {
		t.Fatalf("episodes = %d, want 2", len(meta.Season.Episodes))
	}
	ep0 := meta.Season.Episodes[0]
	if ep0.BVID != "BV1BYuo6JE1w" || ep0.CID != 40789936120 {
		t.Errorf("ep0 bvid/cid = %s/%d", ep0.BVID, ep0.CID)
	}
	if ep0.Title != "第一集" || ep0.Duration != 905 {
		t.Errorf("ep0 title/duration = %q/%v", ep0.Title, ep0.Duration)
	}
	if ep0.View != 100 || ep0.Like != 10 || ep0.Reply != 2 || ep0.Share != 1 {
		t.Errorf("ep0 stats = %d/%d/%d/%d", ep0.View, ep0.Like, ep0.Reply, ep0.Share)
	}
	if ep0.Author != "UP主" || ep0.PubDate != "20231114" {
		t.Errorf("ep0 author/pubdate = %q/%q", ep0.Author, ep0.PubDate)
	}
	ep1 := meta.Season.Episodes[1]
	if ep1.BVID != "BV1D2ue63EoN" || ep1.CID != 40823360504 {
		t.Errorf("ep1 bvid/cid = %s/%d", ep1.BVID, ep1.CID)
	}
}

func TestParseSeason_NoSeason(t *testing.T) {
	meta, err := parseInitialState(fixtureHTML)
	if err != nil {
		t.Fatalf("parseInitialState: %v", err)
	}
	if meta.Season != nil {
		t.Errorf("Season should be nil for a non-season video, got %+v", meta.Season)
	}
}
