package tiktok

import "testing"

const tiktokHTML = `<!doctype html><html><head>
<meta property="og:title" content="Cool dance &#39;2024&#39;">
<meta property="og:video:secure_url" content="https://v16-web.tiktok.com/video.mp4">
<meta property="og:video" content="https://fallback.tiktok.com/video.flv">
</head><body>
<script id="__NEXT_DATA__" type="application/json">{"props":{"pageProps":{"videoData":{"id":"7000000000000000001","desc":"a fun clip","author":{"uniqueId":"dancer99"}}}}}</script>
</body></html>`

func TestParsePage_OGVideo(t *testing.T) {
	info, err := ParsePage(tiktokHTML, "https://www.tiktok.com/@dancer99/video/7000000000000000001")
	if err != nil {
		t.Fatalf("ParsePage: %v", err)
	}
	if len(info.Formats) != 1 {
		t.Fatalf("formats = %d, want 1", len(info.Formats))
	}
	if info.Formats[0].URL != "https://v16-web.tiktok.com/video.mp4" {
		t.Errorf("URL = %q", info.Formats[0].URL)
	}
	if info.Title != "Cool dance '2024'" {
		t.Errorf("Title = %q (html unescape failed)", info.Title)
	}
	if info.ID != "7000000000000000001" {
		t.Errorf("ID = %q", info.ID)
	}
	if info.Uploader != "dancer99" {
		t.Errorf("Uploader = %q", info.Uploader)
	}
	if info.Description != "a fun clip" {
		t.Errorf("Description = %q", info.Description)
	}
}

func TestParsePage_ReversedMetaOrder(t *testing.T) {
	html := `<meta content="https://x.tiktok.com/v.mp4" property="og:video:secure_url">`
	if got := metaContent(html, "og:video:secure_url"); got != "https://x.tiktok.com/v.mp4" {
		t.Errorf("metaContent (reversed) = %q", got)
	}
}

func TestParsePage_NoVideo(t *testing.T) {
	html := `<meta property="og:title" content="no video here">`
	if _, err := ParsePage(html, "https://www.tiktok.com/@x/1"); err == nil {
		t.Fatal("expected error when no video URL present")
	}
}

func TestParseNextData(t *testing.T) {
	_, err := parseNextData(tiktokHTML)
	if err != nil {
		t.Fatalf("parseNextData: %v", err)
	}
}
