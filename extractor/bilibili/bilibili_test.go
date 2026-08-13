package bilibili

import (
	"crypto/md5"
	"fmt"
	"net/url"
	"testing"
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
