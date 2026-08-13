package output

import (
	"testing"

	"yt-dlp-go/extractor"
)

func sample() *extractor.Info {
	return &extractor.Info{
		ID:          "abc123",
		Title:       "My Cool Video",
		Uploader:    "Some Channel",
		UploadDate:  "20240115",
		Ext:         "mp4",
		Duration:    125,
		WebpageURL:  "https://example.com/v/abc123",
		Description: "hello world",
		Raw: map[string]any{
			"videoDetails": map[string]any{
				"author": "RawAuthor",
			},
		},
	}
}

func TestRender_Basic(t *testing.T) {
	got, err := Render("%(title)s-%(id)s.%(ext)s", sample())
	if err != nil {
		t.Fatal(err)
	}
	want := "My Cool Video-abc123.mp4"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestRender_Default(t *testing.T) {
	got, err := Render("%(nonexistent|fallback)s", sample())
	if err != nil {
		t.Fatal(err)
	}
	if got != "fallback" {
		t.Fatalf("got %q want fallback", got)
	}
}

func TestRender_Upper(t *testing.T) {
	got, err := Render("%(title)u", sample())
	if err != nil {
		t.Fatal(err)
	}
	if got != "MY COOL VIDEO" {
		t.Fatalf("got %q", got)
	}
}

func TestRender_DateFormat(t *testing.T) {
	got, err := Render("%(upload_date>%Y-%m-%d)s", sample())
	if err != nil {
		t.Fatal(err)
	}
	if got != "2024-01-15" {
		t.Fatalf("got %q want 2024-01-15", got)
	}
}

func TestRender_Duration(t *testing.T) {
	got, err := Render("%(duration>%H:%M:%S)s", sample())
	if err != nil {
		t.Fatal(err)
	}
	if got != "0:02:05" {
		t.Fatalf("got %q want 0:02:05", got)
	}
}

func TestRender_RawPath(t *testing.T) {
	got, err := Render("%(raw.videoDetails.author)s", sample())
	if err != nil {
		t.Fatal(err)
	}
	if got != "RawAuthor" {
		t.Fatalf("got %q want RawAuthor", got)
	}
}

func TestRender_LiteralPercent(t *testing.T) {
	got, err := Render("100%% done %(id)s", sample())
	if err != nil {
		t.Fatal(err)
	}
	if got != "100% done abc123" {
		t.Fatalf("got %q", got)
	}
}

func TestSanitize(t *testing.T) {
	got := Sanitize("a/b:c*d?e")
	if got != "a_b_c_d_e" {
		t.Fatalf("got %q want a_b_c_d_e", got)
	}
}
