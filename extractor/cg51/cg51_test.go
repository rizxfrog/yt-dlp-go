package cg51

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"yt-dlp-go/extractor"
)

func TestMatch(t *testing.T) {
	ie := Cg51IE{}
	cases := map[string]bool{
		"https://51cg1.com/archives/234404/":         true,
		"https://www.51cg1.com/archives/234404":      true,
		"http://51cg1.com/archives/999999":           true,
		"https://51cg1.com/archives/234404/?foo=bar": true,
		"https://51cg1.com/other/234404/":            false,
		"https://www.bilibili.com/video/BV1xx":       false,
	}
	for u, want := range cases {
		if got := ie.Match(u); got != want {
			t.Errorf("Match(%q) = %v, want %v", u, got, want)
		}
	}
}

func TestFindManifestURL(t *testing.T) {
	cases := map[string]string{
		`<script>var u="https://cdn.example.com/video/abc/index.m3u8";</script>`: "https://cdn.example.com/video/abc/index.m3u8",
		`<video src="https://cdn.example.com/a.m3u8"></video>`:                   "https://cdn.example.com/a.m3u8",
		`<iframe src="https://cdn.example.com/embed/x.m3u8?t=1"></iframe>`:       "https://cdn.example.com/embed/x.m3u8?t=1",
		`var cfg = {"url":"https:\/\/cdn.example.com\/escaped.m3u8"};`:           "https://cdn.example.com/escaped.m3u8",
		`<a href="https://cdn.example.com/query.m3u8&amp;token=1">`:              "https://cdn.example.com/query.m3u8&token=1",
		`<div>nothing here</div>`:                                                "",
	}
	for html, want := range cases {
		if got := findManifestURL(html); got != want {
			t.Errorf("findManifestURL(%q) = %q, want %q", html, got, want)
		}
	}
}

func TestSanitizeDescription(t *testing.T) {
	got := sanitizeDescription("  正文内容\n\n热门吃瓜  版权声明：本文著作权归 51吃瓜网所有， 任何媒体、网站或个人未经授权不得复制、转载、摘编或以其他方式使用， 否则将依法追究其法律责任。  ", "https://51cg1.com/archives/1/")
	want := "正文内容\n\nSource: https://51cg1.com/archives/1/"
	if got != want {
		t.Errorf("sanitizeDescription = %q, want %q", got, want)
	}

	if got := sanitizeDescription("", "https://51cg1.com/archives/2/"); got != "Source: https://51cg1.com/archives/2/" {
		t.Errorf("empty body = %q", got)
	}
	if got := sanitizeDescription("仅正文", ""); got != "仅正文" {
		t.Errorf("no source = %q", got)
	}
}

func TestExtractPostContentBalanced(t *testing.T) {
	html := `
	<div class="post-content" itemprop="articleBody">
		<p>段落一</p>
		<div class="dplayer">
			<div class="nested">player config</div>
		</div>
		<p>段落二</p>
	</div>
	<div class="footer">should not be included</div>`
	body := extractPostContent(html)
	if !strings.Contains(body, "段落一") || !strings.Contains(body, "段落二") {
		t.Fatalf("body missing text: %q", body)
	}
	if strings.Contains(body, "footer") {
		t.Fatalf("body leaked footer: %q", body)
	}
}

func TestStripNoiseBlocksNestedDiv(t *testing.T) {
	html := `<p>前</p>
<div class="dplayer"><div class="nested">播放器</div><p>内层</p></div>
<p>后</p>`
	got := stripNoiseBlocks(html)
	if strings.Contains(got, "播放器") || strings.Contains(got, "内层") {
		t.Fatalf("nested noise block not fully removed: %q", got)
	}
	if !strings.Contains(got, "前") || !strings.Contains(got, "后") {
		t.Fatalf("lost surrounding text: %q", got)
	}
}

func TestStripNoiseBlocksBlockquote(t *testing.T) {
	got := stripNoiseBlocks(`<p>前</p><blockquote>引用</blockquote><p>后</p>`)
	if strings.Contains(got, "引用") {
		t.Fatalf("blockquote not removed: %q", got)
	}
	if !strings.Contains(got, "前") || !strings.Contains(got, "后") {
		t.Fatalf("lost surrounding text: %q", got)
	}
}

func TestExtract(t *testing.T) {
	html := `<!doctype html>
<html><head><title>x</title></head><body>
<h1 class="post-title">  测试标题 <span>副标</span>  </h1>
<div class="post-content" itemprop="articleBody">
	<p>这是正文。</p>
	<div class="dplayer"><script>var cfg={"url":"https://cdn.example.com/media/234404/index.m3u8"}</script></div>
	<div class="btn-download">下载</div>
	<p>版权声明：本文著作权归 51吃瓜网所有， 任何媒体、网站或个人未经授权不得复制、转载、摘编或以其他方式使用， 否则将依法追究其法律责任。</p>
	<img src="https://img.example.com/placeholder.jpg" data-original="data:image/png;base64,aGVsbG8=">
	<img data-src="data:image/webp;base64,d29ybGQ=">
</div>
</body></html>`

	info, err := extractHTML(html, "https://51cg1.com/archives/234404/")
	if err != nil {
		t.Fatal(err)
	}
	if info.ID != "234404" {
		t.Errorf("ID = %q", info.ID)
	}
	if info.Title != "测试标题 副标" {
		t.Errorf("Title = %q", info.Title)
	}
	if !strings.Contains(info.Description, "这是正文") {
		t.Errorf("Description = %q", info.Description)
	}
	if strings.Contains(info.Description, "版权声明") || strings.Contains(info.Description, "下载") {
		t.Errorf("Description not cleaned: %q", info.Description)
	}
	if !strings.HasSuffix(info.Description, "Source: https://51cg1.com/archives/234404/") {
		t.Errorf("Description source missing: %q", info.Description)
	}
	if len(info.Formats) != 1 {
		t.Fatalf("Formats = %+v", info.Formats)
	}
	f := info.Formats[0]
	if f.URL != "https://cdn.example.com/media/234404/index.m3u8" {
		t.Errorf("manifest URL = %q", f.URL)
	}
	if f.Protocol != "m3u8_native" || f.Ext != "m3u8" {
		t.Errorf("format = %+v", f)
	}
	if f.Headers["Origin"] != "https://51cg1.com" || f.Headers["Referer"] == "" || f.Headers["User-Agent"] == "" {
		t.Errorf("format headers = %+v", f.Headers)
	}
	// The remote placeholder img is a real URL; it must win over data: URIs.
	if !strings.HasPrefix(info.Thumbnail, "https://img.example.com/placeholder.jpg") {
		t.Errorf("Thumbnail = %q", info.Thumbnail)
	}
}

func TestExtractDataURIThumbnailFallback(t *testing.T) {
	html := `<html><body>
<h1 class="post-title">标题</h1>
<div class="post-content" itemprop="articleBody">
	<img src="data:image/jpeg;base64,aGVsbG8=">
</div>
<script>const u="https://cdn.example.com/v.m3u8";</script>
</body></html>`

	info, err := extractHTML(html, "https://51cg1.com/archives/1")
	if err != nil {
		t.Fatal(err)
	}
	if info.Thumbnail != "data:image/jpeg;base64,aGVsbG8=" {
		t.Errorf("Thumbnail = %q", info.Thumbnail)
	}
}

func TestExtractMissingManifest(t *testing.T) {
	if _, err := extractHTML(`<h1 class="post-title">标题</h1>`, "https://51cg1.com/archives/1"); err == nil {
		t.Fatal("expected error for page without .m3u8")
	}
}

// TestExtractNetwork exercises the real download path (headers + cookie) without
// touching the network: a RoundTripper intercepts the 51cg1.com request.
func TestExtractNetwork(t *testing.T) {
	html := `<h1 class="post-title">网络测试</h1>
<div class="post-content" itemprop="articleBody"><p>正文</p></div>
<script>var u="https://cdn.example.com/n.m3u8";</script>`

	var gotCookie string
	rt := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		gotCookie = r.Header.Get("Cookie")
		if got := r.Header.Get("User-Agent"); got != cg51UserAgent {
			t.Errorf("User-Agent = %q", got)
		}
		if got := r.Header.Get("Referer"); got == "" {
			t.Error("Referer missing")
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/html"}},
			Body:       io.NopCloser(strings.NewReader(html)),
			Request:    r,
		}, nil
	})
	client := &http.Client{Transport: rt}

	info, err := (Cg51IE{}).Extract(
		&extractor.Context{Client: client, Headers: map[string]string{}},
		"https://51cg1.com/archives/234404/",
	)
	if err != nil {
		t.Fatal(err)
	}
	if gotCookie != "user-choose=true" {
		t.Errorf("Cookie = %q, want user-choose=true", gotCookie)
	}
	if info.Title != "网络测试" {
		t.Errorf("Title = %q", info.Title)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
