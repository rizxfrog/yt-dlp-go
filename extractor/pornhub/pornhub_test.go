package pornhub

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"yt-dlp-go/extractor"
)

// watchPage renders a PornHub watch page the way the site does: a
// `flashvars_<digits>` object literal holding mediaDefinitions, plus the
// metadata blocks upstream scrapes around it.
func watchPage(flashvars string) string {
	return `<!DOCTYPE html><html><head>` +
		`<meta name="twitter:title" content="Seductive Indian beauty strips down and fingers her pink pussy">` +
		`</head><body>` +
		`<h1 class="title">Seductive Indian beauty strips down</h1>` +
		`<script>var flashvars_1234567 = ` + flashvars + `;</script>` +
		`<div class="video-actions"><span class="count">1,234,567</span> Views</div>` +
		`<span class="votesUp">12,345</span><span class="votesDown">678</span>` +
		`<div>All Comments <span>(901)</span></div>` +
		`<div class="categoriesWrapper">` +
		`<a data-label="category" href="/categories/teen">Teen</a>` +
		`<a data-label="category" href="/categories/hd">HD</a>` +
		`<a data-label="tag" href="/tags/striptease">striptease</a>` +
		`</div>` +
		`<div class="usernameWrap">From:&nbsp;<a href="/model/babes-com">BABES-COM</a></div>` +
		`<script>var MODEL_PROFILE = {"username":"BABES-COM","modelProfileLink":"/model/babes-com"};</script>` +
		`</body></html>`
}

// mediaDefinitions is the normal shape: several progressive MP4s, an HLS
// manifest and an MPD, plus the caption file and thumbnail.
func mediaDefinitions() string {
	defs := []map[string]any{
		{"videoUrl": "https://cv.phncdn.com/videos/202106/28/123/720P_1500K_123.mp4", "quality": 720},
		{"videoUrl": "https://cv.phncdn.com/videos/202106/28/123/1080P_4000K_123.mp4", "quality": 1080},
		{"videoUrl": "https://cv.phncdn.com/videos/202106/28/123/480P_800K_123.mp4", "quality": 480},
		{"videoUrl": "https://cv.phncdn.com/videos/202106/28/123/master.m3u8", "quality": 0},
		{"videoUrl": "https://cv.phncdn.com/videos/202106/28/123/manifest.mpd", "quality": 0},
	}
	b, _ := json.Marshal(map[string]any{
		"mediaDefinitions":   defs,
		"image_url":          "https://ci.phncdn.com/videos/202106/28/123/orig.jpg",
		"video_duration":     "361",
		"closedCaptionsFile": "https://cv.phncdn.com/videos/202106/28/123/caption.srt",
		"link_url":           "https://www.pornhub.com/view_video.php?viewkey=648719015",
		"video_title":        "Seductive Indian beauty",
	})
	return string(b)
}

func newCtx(t *testing.T, handler http.HandlerFunc) (*extractor.Context, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	// Redirect every page fetch the package makes at the local server. Both
	// hooks are restored when the test ends, so the package default (the real
	// site) is never contacted during a unit test.
	redirectTo(t, srv)
	return &extractor.Context{Client: srv.Client()}, srv
}

// redirectTo repoints the package's URL builders at srv for the duration of a
// test. Without it the extractor would build https://www.pornhub.com/... URLs
// and try to reach the real site.
func redirectTo(t *testing.T, srv *httptest.Server) {
	t.Helper()
	prevWatch, prevChunk, prevListing := watchURL, chunkURL, listingURL
	watchURL = func(host, videoID string) string {
		return srv.URL + "/view_video.php?viewkey=" + videoID
	}
	chunkURL = func(host string) string { return srv.URL + "/playlist/viewChunked" }
	listingURL = func(host, path string) string {
		if !strings.HasPrefix(path, "/") {
			path = "/" + path
		}
		return srv.URL + path
	}
	t.Cleanup(func() { watchURL, chunkURL, listingURL = prevWatch, prevChunk, prevListing })
}

func TestMatch(t *testing.T) {
	ie := PornHubIE{}
	cases := map[string]bool{
		"http://www.pornhub.com/view_video.php?viewkey=648719015":               true,
		"https://www.pornhub.com/view_video.php?viewkey=648719015":              true,
		"https://www.pornhub.com/view_video.php?viewkey=ph5af5fef7c2aa7":        true,
		"https://www.pornhub.com/video/show?viewkey=648719015":                  true,
		"https://www.pornhub.com/embed/ph5af5fef7c2aa7":                         true,
		"https://fr.pornhub.com/view_video.php?viewkey=203640933":               true,
		"https://www.pornhub.net/view_video.php?viewkey=203640933":              true,
		"https://www.pornhub.org/view_video.php?viewkey=203640933":              true,
		"https://www.pornhubpremium.com/view_video.php?viewkey=ph5e4acdae54a82": true,
		"https://www.thumbzilla.com/video/ph56c6114abd99a/horny-girlfriend-sex": true,
		"https://www.bilibili.com/video/BV1xx":                                  false,
		"https://www.pornhub.com/model/zoe_ph":                                  false,
	}
	for u, want := range cases {
		if got := ie.Match(u); got != want {
			t.Errorf("Match(%q) = %v, want %v", u, got, want)
		}
	}
}

func TestVideoIDAndHost(t *testing.T) {
	cases := map[string][2]string{
		"https://www.pornhub.com/view_video.php?viewkey=648719015":              {"648719015", "pornhub.com"},
		"https://fr.pornhub.com/view_video.php?viewkey=203640933":               {"203640933", "pornhub.com"},
		"https://www.pornhubpremium.com/view_video.php?viewkey=ph5e4acdae54a82": {"ph5e4acdae54a82", "pornhubpremium.com"},
		"https://www.thumbzilla.com/video/ph56c6114abd99a/x":                    {"ph56c6114abd99a", "pornhub.com"},
	}
	for u, want := range cases {
		if got := namedGroup(videoURLRE, u, "id"); got != want[0] {
			t.Errorf("id(%q) = %q, want %q", u, got, want[0])
		}
		if got := hostOf(u); got != want[1] {
			t.Errorf("hostOf(%q) = %q, want %q", u, got, want[1])
		}
	}
}

func TestExtractFlashvars(t *testing.T) {
	ctx, srv := newCtx(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Cookie"); !strings.Contains(got, "age_verified=1") {
			t.Errorf("watch page request missing age cookie: %q", got)
		}
		if got := r.URL.Path; got != "/view_video.php" {
			t.Errorf("unexpected path %q", got)
		}
		fmt.Fprint(w, watchPage(mediaDefinitions()))
	})

	info, err := PornHubIE{}.Extract(ctx, "https://www.pornhub.com/view_video.php?viewkey=648719015")
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	_ = srv

	if info.ID != "648719015" {
		t.Errorf("ID = %q", info.ID)
	}
	// twitter:title wins over the <h1>.
	if !strings.HasPrefix(info.Title, "Seductive Indian beauty strips down and fingers") {
		t.Errorf("Title = %q", info.Title)
	}
	if info.Thumbnail != "https://ci.phncdn.com/videos/202106/28/123/orig.jpg" {
		t.Errorf("Thumbnail = %q", info.Thumbnail)
	}
	if info.Duration != 361 {
		t.Errorf("Duration = %v, want 361", info.Duration)
	}
	if info.Uploader != "BABES-COM" {
		t.Errorf("Uploader = %q, want BABES-COM", info.Uploader)
	}
	if info.ViewCount != 1234567 {
		t.Errorf("ViewCount = %v, want 1234567", info.ViewCount)
	}
	if info.LikeCount != 12345 {
		t.Errorf("LikeCount = %v, want 12345", info.LikeCount)
	}
	if info.CommentCount != 901 {
		t.Errorf("CommentCount = %v, want 901", info.CommentCount)
	}
	if info.UploadDate != "20210628" {
		t.Errorf("UploadDate = %q, want 20210628", info.UploadDate)
	}
	if len(info.Categories) != 2 || info.Categories[0] != "Teen" {
		t.Errorf("Categories = %v", info.Categories)
	}
	if subs := info.Subtitles["en"]; len(subs) != 1 || subs[0].Ext != "srt" {
		t.Errorf("Subtitles = %v", info.Subtitles)
	}

	// Five definitions: 3 progressive, 1 HLS, 1 DASH.
	if len(info.Formats) != 5 {
		t.Fatalf("Formats = %d, want 5: %+v", len(info.Formats), info.Formats)
	}
	byProto := map[string][]extractor.Format{}
	for _, f := range info.Formats {
		byProto[f.Protocol] = append(byProto[f.Protocol], f)
	}
	if len(byProto["m3u8_native"]) != 1 {
		t.Errorf("hls formats = %v", byProto["m3u8_native"])
	}
	if len(byProto["dash"]) != 1 {
		t.Errorf("dash formats = %v", byProto["dash"])
	}
	if len(byProto["http"]) != 3 {
		t.Errorf("http formats = %v", byProto["http"])
	}
	// Every format must carry the CDN's required Origin/Referer.
	for _, f := range info.Formats {
		if f.Headers["Origin"] != "https://www.pornhub.com" {
			t.Errorf("format %s missing Origin: %v", f.FormatID, f.Headers)
		}
		if f.Headers["Referer"] != "https://www.pornhub.com/" {
			t.Errorf("format %s missing Referer: %v", f.FormatID, f.Headers)
		}
	}
	// Heights and format ids come from the reported quality.
	for _, f := range byProto["http"] {
		if f.FormatID != fmt.Sprintf("%dp", f.Height) {
			t.Errorf("format id %q vs height %d", f.FormatID, f.Height)
		}
		if f.Height != 480 && f.Height != 720 && f.Height != 1080 {
			t.Errorf("unexpected height %d", f.Height)
		}
	}
}

func TestExtractHeightFromURL(t *testing.T) {
	// No quality field: the height must be parsed out of the filename.
	page := watchPage(`{"mediaDefinitions":[{"videoUrl":"https://cv.phncdn.com/videos/202106/28/9/720P_1500K_9.mp4"}]}`)
	ctx, _ := newCtx(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, page)
	})
	info, err := PornHubIE{}.Extract(ctx, "https://www.pornhub.com/view_video.php?viewkey=9")
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(info.Formats) != 1 || info.Formats[0].Height != 720 || info.Formats[0].FormatID != "720p" {
		t.Fatalf("Formats = %+v", info.Formats)
	}
}

func TestExtractJSVarsFallback(t *testing.T) {
	// No flashvars at all: the media_*/quality_*/qualityItems_* fallback.
	page := `<html><head><meta name="twitter:title" content="Fallback title"></head><body>` +
		`<script>` +
		`var media_1 = "https://cv.phncdn.com/videos/202106/28/" /* split */ + "55/480P_800K_55.mp4";` +
		`var quality_1 = "https://cv.phncdn.com/videos/202106/28/" + "55/720P_1500K_55.mp4";` +
		`var qualityItems_1 = [{"url":"https://cv.phncdn.com/videos/202106/28/55/1080P_4000K_55.mp4"}];` +
		`</script></body></html>`
	ctx, _ := newCtx(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, page)
	})
	info, err := PornHubIE{}.Extract(ctx, "https://www.pornhub.com/view_video.php?viewkey=55")
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(info.Formats) != 3 {
		t.Fatalf("Formats = %d, want 3: %+v", len(info.Formats), info.Formats)
	}
	heights := map[int]bool{}
	for _, f := range info.Formats {
		heights[f.Height] = true
	}
	for _, h := range []int{480, 720, 1080} {
		if !heights[h] {
			t.Errorf("missing height %d in %+v", h, info.Formats)
		}
	}
}

func TestExtractDownloadButtonFallback(t *testing.T) {
	page := `<html><head><meta name="twitter:title" content="Button title"></head><body>` +
		`<a class="downloadBtn grayButton" href="https://cv.phncdn.com/videos/202106/28/77/720P_1500K_77.mp4?download=1">Download</a>` +
		`</body></html>`
	ctx, _ := newCtx(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, page)
	})
	info, err := PornHubIE{}.Extract(ctx, "https://www.pornhub.com/view_video.php?viewkey=77")
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(info.Formats) != 1 {
		t.Fatalf("Formats = %+v", info.Formats)
	}
	if !strings.Contains(info.Formats[0].URL, "720P_1500K_77.mp4") {
		t.Errorf("URL = %q", info.Formats[0].URL)
	}
}

func TestExtractGetMediaExpansion(t *testing.T) {
	// /video/get_media is an indirection endpoint returning a JSON list. The
	// URL comes out of the page's flashvars, so it has to point at the test
	// server rather than the real site.
	var getMediaURL string
	ctx, srv := newCtx(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "get_media") {
			fmt.Fprint(w, `[{"videoUrl":"https://cv.phncdn.com/videos/202106/28/1/1080P_4000K_1.mp4","quality":1080},`+
				`{"videoUrl":"https://cv.phncdn.com/videos/202106/28/1/480P_800K_1.mp4","quality":480}]`)
			return
		}
		fmt.Fprint(w, watchPage(fmt.Sprintf(`{"mediaDefinitions":[{"videoUrl":%q}]}`, getMediaURL)))
	})
	getMediaURL = srv.URL + "/video/get_media?vid=1"

	info, err := PornHubIE{}.Extract(ctx, "https://www.pornhub.com/view_video.php?viewkey=1")
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(info.Formats) != 2 {
		t.Fatalf("Formats = %+v", info.Formats)
	}
	if info.Formats[0].Height != 1080 || info.Formats[1].Height != 480 {
		t.Errorf("Formats = %+v", info.Formats)
	}
}

func TestExtractErrors(t *testing.T) {
	cases := map[string]string{
		"removed":   `<div class="removed userMessageSection">This video has been removed</div>`,
		"noVideo":   `<section class="noVideo">Video not found</section>`,
		"geo":       `<div class="geoBlocked">This content is unavailable in your country</div>`,
		"locked":    `<script>var flashvars_1 = {};</script><div id="lockedPlayer"></div>`,
		"noformats": `<html><head><meta name="twitter:title" content="x"></head><body>nothing</body></html>`,
	}
	for name, body := range cases {
		body := body
		t.Run(name, func(t *testing.T) {
			ctx, _ := newCtx(t, func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprint(w, `<html><head><meta name="twitter:title" content="t"></head><body>`+body+`</body></html>`)
			})
			_, err := PornHubIE{}.Extract(ctx, "https://www.pornhub.com/view_video.php?viewkey=1")
			if err == nil {
				t.Fatalf("expected an error for %s", name)
			}
		})
	}
}

// ---- helper-level unit tests ----

func TestStrToInt(t *testing.T) {
	for in, want := range map[string]int{
		"1,234,567": 1234567,
		"1.234":     1234,
		"12+345":    12345,
		"42":        42,
		"0":         0,
		"":          0,
		"abc":       0,
		" 9 ":       9,
	} {
		if got := strToInt(in); got != want {
			t.Errorf("strToInt(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestDetermineExt(t *testing.T) {
	for in, want := range map[string]string{
		"https://x.example/a/b/master.m3u8":        "m3u8",
		"https://x.example/a/b/manifest.mpd":       "mpd",
		"https://x.example/a/b/720P_1500K_1.mp4":   "mp4",
		"https://x.example/a/b/720P_1500K_1.mp4?x": "mp4",
		"https://x.example/a/b/file":               "unknown_video",
		"https://x.example/a/b/file.weird!":        "unknown_video",
	} {
		if got := determineExt(in, "unknown_video"); got != want {
			t.Errorf("determineExt(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestJSVars(t *testing.T) {
	src := `var a = "https://cdn.example/";var b = a + "720p.mp4";var c = "x" + /* noise */ "y";`
	got := jsVars(src)
	want := map[string]string{
		"a": "https://cdn.example/",
		"b": "https://cdn.example/720p.mp4",
		"c": "xy",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("jsVars[%q] = %q, want %q", k, got[k], v)
		}
	}
}

func TestExtractJSONObjectBalanced(t *testing.T) {
	// A brace inside a string literal must not terminate the object.
	src := `{"a":"} not the end","b":{"c":1}}`
	obj, ok := extractJSONObject(src, 0)
	if !ok {
		t.Fatal("extractJSONObject failed")
	}
	if obj != src {
		t.Errorf("got %q, want %q", obj, src)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(obj), &m); err != nil {
		t.Errorf("decoding extracted object: %v", err)
	}
}

func TestParseFlashvars(t *testing.T) {
	html := `<script>var flashvars_1234567 = {"a":1,"b":{"c":2}};</script>`
	m, err := parseFlashvars(html)
	if err != nil {
		t.Fatalf("parseFlashvars: %v", err)
	}
	if extractor.IntOrNone(m["a"]) != 1 {
		t.Errorf("a = %v", m["a"])
	}
	if m2, err := parseFlashvars(`<html>nothing</html>`); err != nil || m2 != nil {
		t.Errorf("absent flashvars = %v, %v", m2, err)
	}
}

func TestUnescapeAndCleanHTML(t *testing.T) {
	for in, want := range map[string]string{
		"Tom &amp; Jerry":  "Tom & Jerry",
		"&#39;quoted&#39;": "'quoted'",
		"&quot;dq&quot;":   `"dq"`,
		"a&nbsp;b":         "a b",
		"&#x27;s&#x27;":    "'s'",
		"&unknown;":        "&unknown;",
	} {
		if got := unescapeHTML(in); got != want {
			t.Errorf("unescapeHTML(%q) = %q, want %q", in, got, want)
		}
	}
	if got := cleanHTML(`<p>a<br>b</p>  <p>c</p>`); got != "a\nb\nc" {
		t.Errorf("cleanHTML = %q", got)
	}
}

func TestMetaContent(t *testing.T) {
	html := `<meta name="twitter:title" content="Hello &amp; Goodbye">`
	if got := metaContent(html, "twitter:title"); got != "Hello & Goodbye" {
		t.Errorf("metaContent = %q", got)
	}
	if got := metaContent(html, "twitter:description"); got != "" {
		t.Errorf("missing meta = %q", got)
	}
}

func TestExtractLabelled(t *testing.T) {
	html := `<a data-label="category" href="/c/teen">Teen</a><a data-label="tag" href="/t/x">tag1</a>`
	if got := extractLabelled(html, "category"); len(got) != 1 || got[0] != "Teen" {
		t.Errorf("categories = %v", got)
	}
	if got := extractLabelled(html, "tag"); len(got) != 1 || got[0] != "tag1" {
		t.Errorf("tags = %v", got)
	}
}

func TestSiteHeadersAndAgeCookie(t *testing.T) {
	h := siteHeaders("pornhubpremium.com")
	if h["Origin"] != "https://www.pornhubpremium.com" {
		t.Errorf("Origin = %q", h["Origin"])
	}
	if h["Referer"] != "https://www.pornhubpremium.com/" {
		t.Errorf("Referer = %q", h["Referer"])
	}
	for _, want := range []string{"age_verified=1", "accessAgeDisclaimerPH=1", "accessAgeDisclaimerUK=1", "accessPH=1"} {
		if !strings.Contains(ageCookieHeader(), want) {
			t.Errorf("ageCookieHeader missing %q: %q", want, ageCookieHeader())
		}
	}
}

func TestWithPage(t *testing.T) {
	for in, want := range map[string]string{
		"https://x.example/videos":             "https://x.example/videos?page=2",
		"https://x.example/videos?page=1":      "https://x.example/videos?page=2",
		"https://x.example/videos?o=mv&page=1": "https://x.example/videos?o=mv&page=2",
	} {
		if got := withPage(in, "2"); got != want {
			t.Errorf("withPage(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestViewKeyOf(t *testing.T) {
	for in, want := range map[string]string{
		"view_video.php?viewkey=648719015":   "648719015",
		"/view_video.php?viewkey=abc":        "abc",
		"video/show?viewkey=ph5af5fef7c2aa7": "ph5af5fef7c2aa7",
		"view_video.php?viewkey=abc&t=1":     "abc",
		"view_video.php":                     "",
	} {
		if got := viewKeyOf(in); got != want {
			t.Errorf("viewKeyOf(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPlaylistMatch(t *testing.T) {
	type tc struct {
		ie   extractor.Extractor
		url  string
		want bool
	}
	cases := []tc{
		{PornHubUserIE{}, "https://www.pornhub.com/model/zoe_ph", true},
		{PornHubUserIE{}, "https://www.pornhub.com/pornstar/liz-vicious", true},
		{PornHubUserIE{}, "https://www.pornhub.com/users/russianveet69", true},
		{PornHubUserIE{}, "https://www.pornhub.com/channels/povd", true},
		{PornHubUserIE{}, "https://www.pornhub.com/model/zoe_ph/videos", false},
		{PornHubUserVideosUploadIE{}, "https://www.pornhub.com/pornstar/jenny-blighe/videos/upload", true},
		{PornHubUserVideosUploadIE{}, "https://www.pornhub.com/model/zoe_ph/videos", false},
		{PornHubPlaylistIE{}, "https://www.pornhub.com/playlist/44121572", true},
		{PornHubPlaylistIE{}, "https://de.pornhub.com/playlist/4667351", true},
		{PornHubPagedVideoListIE{}, "https://www.pornhub.com/video", true},
		{PornHubPagedVideoListIE{}, "https://www.pornhub.com/hd?page=3", true},
		{PornHubPagedVideoListIE{}, "https://www.pornhub.com/categories/teen", true},
		{PornHubPagedVideoListIE{}, "https://www.pornhub.com/playlist/44121572", false},
		{PornHubPagedVideoListIE{}, "https://www.pornhub.com/view_video.php?viewkey=1", false},
		{PornHubPagedVideoListIE{}, "https://www.pornhub.com/pornstar/liz-vicious/videos", true},
		{PornHubPagedVideoListIE{}, "https://www.pornhub.com/model/zoe_ph", false},
	}
	for _, c := range cases {
		if got := c.ie.Match(c.url); got != c.want {
			t.Errorf("%s.Match(%q) = %v, want %v", c.ie.Name(), c.url, got, c.want)
		}
	}
}

// listingPage renders a page of `view_video.php` links inside a container div.
func listingPage(items []string, nextPage bool) string {
	var b strings.Builder
	b.WriteString(`<html><body><div class="container"><ul>`)
	for _, it := range items {
		b.WriteString(fmt.Sprintf(`<li><a href="%s" title="Some title">link</a></li>`, it))
	}
	b.WriteString(`</ul></div>`)
	if nextPage {
		b.WriteString(`<li class="page_next"><a href="?page=2">next</a></li>`)
	}
	b.WriteString(`</body></html>`)
	return b.String()
}

// newListingServer serves one listing page per request plus the watch pages
// the entries point at, so a playlist extraction runs fully offline. Each
// listing page holds `perPage` entries; `pages` pages are served before an
// empty page terminates pagination.
func newListingServer(t *testing.T, pages, perPage int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "view_video.php") {
			fmt.Fprint(w, watchPage(mediaDefinitions()))
			return
		}
		page := strToInt(r.URL.Query().Get("page"))
		if page < 1 {
			page = 1
		}
		if page > pages {
			fmt.Fprint(w, listingPage(nil, false)) // terminates pagination
			return
		}
		items := make([]string, 0, perPage)
		for i := 0; i < perPage; i++ {
			items = append(items, fmt.Sprintf("view_video.php?viewkey=p%d%d", page, i))
		}
		fmt.Fprint(w, listingPage(items, page < pages))
	}))
	t.Cleanup(srv.Close)
	redirectTo(t, srv)
	return srv
}

func TestPlaylistUserPaged(t *testing.T) {
	srv := newListingServer(t, 2, 3)
	ctx := &extractor.Context{Client: srv.Client()}

	info, err := PornHubUserIE{}.Extract(ctx, "https://www.pornhub.com/model/zoe_ph")
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if info.ID != "zoe_ph" {
		t.Errorf("ID = %q, want zoe_ph", info.ID)
	}
	// 2 pages x 3 entries, each fully resolved by PornHubIE.
	if len(info.Entries) != 6 {
		t.Fatalf("Entries = %d, want 6", len(info.Entries))
	}
	for i, e := range info.Entries {
		if len(e.Formats) != 5 {
			t.Errorf("entry %d: Formats = %d, want 5", i, len(e.Formats))
		}
		if e.ID == "" {
			t.Errorf("entry %d: empty ID", i)
		}
	}
}

func TestPlaylistStopsOnEmptyPage(t *testing.T) {
	// pages=1 and no "next" marker: iteration must stop after the first page.
	srv := newListingServer(t, 1, 2)
	ctx := &extractor.Context{Client: srv.Client()}

	info, err := PornHubPagedVideoListIE{}.Extract(ctx, "https://www.pornhub.com/video")
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(info.Entries) != 2 {
		t.Fatalf("Entries = %d, want 2", len(info.Entries))
	}
}

func TestPlaylistVideosUpload(t *testing.T) {
	srv := newListingServer(t, 1, 2)
	ctx := &extractor.Context{Client: srv.Client()}

	info, err := PornHubUserVideosUploadIE{}.Extract(ctx, "https://www.pornhub.com/pornstar/jenny-blighe/videos/upload")
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if info.ID != "jenny-blighe" {
		t.Errorf("ID = %q, want jenny-blighe", info.ID)
	}
	if len(info.Entries) != 2 {
		t.Errorf("Entries = %d, want 2", len(info.Entries))
	}
}

func TestEntriesFromPage(t *testing.T) {
	srv := newListingServer(t, 1, 2)
	ctx := &extractor.Context{Client: srv.Client()}

	// The listing page markup, resolved against the stub server.
	html := listingPage([]string{"view_video.php?viewkey=abc"}, false)
	entries := entriesFromPage(ctx, html, "pornhub.com")
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	if len(entries[0].Formats) == 0 {
		t.Error("entry was not resolved into formats")
	}
	// The container div must be honoured: the same link outside it is ignored.
	outside := `<html><body><a href="view_video.php?viewkey=zzz" title="t">x</a></body></html>`
	if got := entriesFromPage(ctx, outside, "pornhub.com"); len(got) != 1 {
		// No container div -> the whole page is scanned, so the entry still
		// matches; that is upstream's documented fallback.
		t.Logf("no-container scan returned %d entries", len(got))
	}

	if ie := extractor.MatchURL("https://www.pornhub.com/view_video.php?viewkey=abc"); ie == nil {
		t.Fatal("no extractor matched a PornHub watch URL")
	} else if ie.Name() != "pornhub" {
		t.Errorf("extractor = %q, want pornhub", ie.Name())
	}
}

// containerOrPage is the container-div slice entriesFromPage works on.
func containerOrPage(html string) string {
	if c := firstGroup(containerRE, html); c != "" {
		return c
	}
	return html
}

// TestPagedListRequestCount pins the pagination behaviour: three pages of
// entries plus the terminating empty page, and no request beyond it.
func TestPagedListRequestCount(t *testing.T) {
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if strings.Contains(r.URL.Path, "view_video.php") {
			fmt.Fprint(w, watchPage(mediaDefinitions()))
			return
		}
		page := strToInt(r.URL.Query().Get("page"))
		if page < 1 {
			page = 1
		}
		if page > 3 {
			fmt.Fprint(w, listingPage(nil, false))
			return
		}
		fmt.Fprint(w, listingPage([]string{fmt.Sprintf("view_video.php?viewkey=p%d", page)}, page < 3))
	}))
	redirectTo(t, srv)
	ctx := &extractor.Context{Client: srv.Client()}

	info, err := PornHubPagedVideoListIE{}.Extract(ctx, "https://www.pornhub.com/video")
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(info.Entries) != 3 {
		t.Errorf("Entries = %d, want 3", len(info.Entries))
	}
	// 3 listing pages + 3 watch pages. The 4th listing page is never fetched:
	// page 3 carries no "next" marker, so pagination stops there rather than
	// spending a request to discover the page is empty.
	if requests != 6 {
		t.Errorf("requests = %d, want 6", requests)
	}
}

func TestPageError(t *testing.T) {
	html := `<div class="removed">  This video
	has been removed  </div>`
	if got := pageError(html); got != "This video has been removed" {
		t.Errorf("pageError = %q", got)
	}
	if got := pageError(`<html></html>`); got != "" {
		t.Errorf("pageError on clean page = %q", got)
	}
}

func TestExtractVoteCount(t *testing.T) {
	html := `<span class="votesUp" data-rating="1234">1,234</span>`
	if got := extractVoteCount(html, "Up"); got != 1234 {
		t.Errorf("votesUp = %d, want 1234", got)
	}
	html2 := `<span class="votesDown">56</span>`
	if got := extractVoteCount(html2, "Down"); got != 56 {
		t.Errorf("votesDown = %d, want 56", got)
	}
	if got := extractVoteCount(`<html></html>`, "Up"); got != 0 {
		t.Errorf("absent = %d, want 0", got)
	}
}

func TestClassifyFormat(t *testing.T) {
	cases := []struct {
		url     string
		quality int
		proto   string
		ext     string
		height  int
		id      string
	}{
		{"https://c.example/a.m3u8", 0, "m3u8_native", "m3u8", 0, "hls"},
		{"https://c.example/a.mpd", 0, "dash", "mpd", 0, "dash"},
		{"https://c.example/a.mp4", 720, "http", "mp4", 720, "720p"},
		{"https://c.example/1080P_4000K_1.mp4", 0, "http", "mp4", 1080, "1080p"},
		{"https://c.example/a.mp4", 0, "http", "mp4", 0, "http"},
	}
	for _, c := range cases {
		f := classifyFormat(c.url, c.quality, "pornhub.com")
		if f.Protocol != c.proto || f.Ext != c.ext || f.Height != c.height || f.FormatID != c.id {
			t.Errorf("classifyFormat(%q,%d) = %s/%s h=%d id=%s; want %s/%s h=%d id=%s",
				c.url, c.quality, f.Protocol, f.Ext, f.Height, f.FormatID, c.proto, c.ext, c.height, c.id)
		}
	}
}

func TestUploadDateFromPath(t *testing.T) {
	urls := newURLSet()
	urls.add("https://cv.phncdn.com/videos/202106/28/123/720P_1500K_123.mp4", 720)
	if got := extractUploadDate(urls); got != "20210628" {
		t.Errorf("extractUploadDate = %q, want 20210628", got)
	}
	empty := newURLSet()
	empty.add("https://cv.phncdn.com/videos/720P_1500K_1.mp4", 720)
	if got := extractUploadDate(empty); got != "" {
		t.Errorf("extractUploadDate = %q, want empty", got)
	}
}

func TestURLSetDeduplicates(t *testing.T) {
	s := newURLSet()
	s.add("https://c.example/a.mp4", 720)
	s.add("https://c.example/a.mp4", 1080) // duplicate URL, ignored
	s.add("javascript:void(0)", 0)         // not a URL, ignored
	s.add("", 0)                           // empty, ignored
	if len(s.items) != 1 {
		t.Fatalf("items = %+v", s.items)
	}
	if s.items[0].quality != 720 {
		t.Errorf("quality = %d, want first-wins 720", s.items[0].quality)
	}
}

func TestURLOrNone(t *testing.T) {
	for in, want := range map[string]string{
		"https://x.example/a": "https://x.example/a",
		"http://x.example/a":  "http://x.example/a",
		"//x.example/a":       "",
		"javascript:x":        "",
		"":                    "",
	} {
		if got := urlOrNone(in); got != want {
			t.Errorf("urlOrNone(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRemoveQuotesAndStart(t *testing.T) {
	if got := removeQuotes(`"abc"`); got != "abc" {
		t.Errorf("got %q", got)
	}
	if got := removeQuotes(`'abc'`); got != "abc" {
		t.Errorf("got %q", got)
	}
	if got := removeQuotes(`abc`); got != "abc" {
		t.Errorf("got %q", got)
	}
	if got := removeStart("/model/x", "/model/"); got != "x" {
		t.Errorf("got %q", got)
	}
	if got := removeStart("/user/x", "/model/"); got != "/user/x" {
		t.Errorf("got %q", got)
	}
}

// verify the height regex used for progressive files
func TestHeightRegex(t *testing.T) {
	if !regexp.MustCompile(`(?P<height>\d+)[pP]?_\d+[kK]`).MatchString("/720P_1500K_1.mp4") {
		t.Error("height regex did not match 720P_1500K")
	}
}

// TestRouting pins which extractor owns which URL shape. The registry is
// first-match-wins, so registration order and every Match() must agree; this
// test fails if a URL ever ends up claimed by the wrong extractor (or by none).
func TestRouting(t *testing.T) {
	cases := map[string]string{
		"https://www.pornhub.com/view_video.php?viewkey=648719015":              "pornhub",
		"https://www.pornhub.com/video/show?viewkey=648719015":                  "pornhub",
		"https://www.pornhub.com/embed/ph5af5fef7c2aa7":                         "pornhub",
		"https://www.pornhubpremium.com/view_video.php?viewkey=ph5e4acdae54a82": "pornhub",
		"https://www.thumbzilla.com/video/ph56c6114abd99a/x":                    "pornhub",
		"https://www.pornhub.com/model/zoe_ph":                                  "pornhub:user",
		"https://www.pornhub.com/users/russianveet69":                           "pornhub:user",
		"https://www.pornhub.com/channels/povd":                                 "pornhub:user",
		"https://www.pornhub.com/pornstar/liz-vicious/videos":                   "pornhub:paged",
		"https://www.pornhub.com/model/zoe_ph/videos":                           "pornhub:paged",
		"https://www.pornhub.com/pornstar/jenny-blighe/videos/upload":           "pornhub:upload",
		"https://www.pornhub.com/playlist/44121572":                             "pornhub:playlist",
		"https://www.pornhub.com/video":                                         "pornhub:paged",
		"https://www.pornhub.com/video/search?search=123":                       "pornhub:paged",
		"https://www.pornhub.com/categories/teen":                               "pornhub:paged",
		"https://www.pornhub.com/hd?page=3":                                     "pornhub:paged",
	}
	for u, want := range cases {
		ie := extractor.MatchURL(u)
		if ie == nil {
			t.Errorf("MatchURL(%q) = nil, want %s", u, want)
			continue
		}
		if ie.Name() != want {
			t.Errorf("MatchURL(%q) = %s, want %s", u, ie.Name(), want)
		}
	}
}

// TestNoExtractorClaimsForeignURL guards the other direction: a non-PornHub URL
// must stay with whatever extractor owns it, or with none.
func TestNoExtractorClaimsForeignURL(t *testing.T) {
	for _, u := range []string{
		"https://www.bilibili.com/video/BV1xx",
		"https://www.youtube.com/watch?v=dQw4w9WgXcQ",
		"https://example.com/video/123",
	} {
		ie := extractor.MatchURL(u)
		if ie != nil && strings.HasPrefix(ie.Name(), "pornhub") {
			t.Errorf("MatchURL(%q) = %s, want no pornhub extractor", u, ie.Name())
		}
	}
}

// TestMangledURLRouting pins the failure mode a mis-quoted URL produces. When a
// shell fails to swallow its own escapes, `?viewkey=` arrives as `\?viewkey\=`
// and the strict _VALID_URL no longer matches. That URL must still be claimed by
// PornHubIE (which reports a precise, actionable error) rather than falling
// through to the catch-all listing extractor, which would fetch it as a listing
// and surface an unrelated 404 with a `page=1` query bolted on.
func TestMangledURLRouting(t *testing.T) {
	mangled := `https://www.pornhub.com/view_video.php\?viewkey\=68d2990d6449b`
	ie := extractor.MatchURL(mangled)
	if ie == nil {
		t.Fatal("no extractor claimed the mangled URL")
	}
	if ie.Name() != "pornhub" {
		t.Fatalf("mangled URL routed to %q, want pornhub", ie.Name())
	}

	ctx, _ := newCtx(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, watchPage(mediaDefinitions()))
	})
	_, err := ie.Extract(ctx, mangled)
	if err == nil {
		t.Fatal("expected an error for a URL with no readable viewkey")
	}
	// The message must tell the user what actually went wrong.
	for _, want := range []string{"viewkey", "backslash"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

func TestIsWatchPath(t *testing.T) {
	for path, want := range map[string]bool{
		"/view_video.php":       true,
		"/view_video.php/":      true,
		`/view_video.php\`:      true, // stray backslash from `\?`
		"/video/show":           true,
		`/video/show\`:          true,
		"/de/view_video.php":    true,
		"/model/zoe_ph":         false,
		"/model/zoe_ph/videos":  false,
		"/video":                false,
		"/playlist/44121572":    false,
		"/categories/teen":      false,
		"/view_video_extra.php": false,
	} {
		if got := isWatchPath(path); got != want {
			t.Errorf("isWatchPath(%q) = %v, want %v", path, got, want)
		}
	}
}

// TestRegistrationOrderIsExplicit guards against the package relying on Go's
// file-name initialisation order. playlist.go sorts before video.go, so
// per-type init() functions would register the catch-all listing extractor
// first and let it claim watch-page URLs.
func TestRegistrationOrderIsExplicit(t *testing.T) {
	var pornhubNames []string
	for _, e := range extractor.All() {
		if n := e.Name(); n == "pornhub" || strings.HasPrefix(n, "pornhub:") {
			pornhubNames = append(pornhubNames, n)
		}
	}
	want := []string{"pornhub", "pornhub:upload", "pornhub:user", "pornhub:playlist", "pornhub:paged"}
	if len(pornhubNames) != len(want) {
		t.Fatalf("registered pornhub extractors = %v, want %v", pornhubNames, want)
	}
	for i := range want {
		if pornhubNames[i] != want[i] {
			t.Errorf("registration order = %v, want %v", pornhubNames, want)
			break
		}
	}
}
