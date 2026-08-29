package extractor

import (
	"bytes"
	"testing"
)

func TestDecodeDataURLBase64(t *testing.T) {
	du, err := DecodeDataURL("data:image/png;base64,aGVsbG8=")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(du.Data, []byte("hello")) {
		t.Errorf("Data = %q", du.Data)
	}
	if du.MIME != "image/png" || du.Ext != "png" {
		t.Errorf("MIME/Ext = %q/%q", du.MIME, du.Ext)
	}
}

func TestDecodeDataURLPercentEncoded(t *testing.T) {
	du, err := DecodeDataURL("data:image/jpeg,hello%20world")
	if err != nil {
		t.Fatal(err)
	}
	if string(du.Data) != "hello world" {
		t.Errorf("Data = %q", du.Data)
	}
	if du.Ext != "jpg" {
		t.Errorf("Ext = %q", du.Ext)
	}
}

func TestDecodeDataURLUnpaddedBase64(t *testing.T) {
	// "hello" with padding removed.
	du, err := DecodeDataURL("data:image/png;base64,aGVsbG8")
	if err != nil {
		t.Fatal(err)
	}
	if string(du.Data) != "hello" {
		t.Errorf("Data = %q", du.Data)
	}
}

func TestDecodeDataURLRejectsNonData(t *testing.T) {
	if _, err := DecodeDataURL("https://example.com/a.png"); err == nil {
		t.Fatal("expected error for non-data URL")
	}
}

func TestExtForMIME(t *testing.T) {
	cases := map[string]string{
		"image/png":     "png",
		"image/webp":    "webp",
		"image/jpeg":    "jpg",
		"image/avif":    "avif",
		"image/svg+xml": "svg",
		"video/mp4":     "",
		"":              "",
	}
	for mime, want := range cases {
		if got := ExtForMIME(mime); got != want {
			t.Errorf("ExtForMIME(%q) = %q, want %q", mime, got, want)
		}
	}
}
