package extractor

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

const subManifest = `#EXTM3U
#EXT-X-MEDIA:TYPE=SUBTITLES,LANGUAGE="en",NAME="English",URI="sub_en.vtt"
#EXT-X-MEDIA:TYPE=SUBTITLES,LANGUAGE="zh-Hans",NAME="Chinese",URI="sub_zh.vtt"
#EXT-X-STREAM-INF:BANDWIDTH=1000000
video.m3u8`

func TestParseHLSSubtitles(t *testing.T) {
	subs := ParseHLSSubtitles(subManifest, "https://h.com/playlist.m3u8")
	if len(subs) != 2 {
		t.Fatalf("want 2 subtitles, got %d", len(subs))
	}
	if subs[0].Lang != "en" || subs[0].URL != "https://h.com/sub_en.vtt" {
		t.Fatalf("first sub = %+v", subs[0])
	}
	if subs[1].Lang != "zh-Hans" || subs[1].Ext != "vtt" {
		t.Fatalf("second sub = %+v", subs[1])
	}
}

func TestDownloadSubtitle(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/vtt")
		w.Write([]byte("WEBVTT\n\n1\n00:00:01.000 --> 00:00:02.000\nHello"))
	}))
	defer srv.Close()

	sub := SubtitleRef{Lang: "en", URL: srv.URL + "/sub_en.vtt", Ext: "vtt"}
	dir := t.TempDir()
	dst, err := DownloadSubtitle(context.Background(), srv.Client(), sub, dir, "video")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(dst) != "video.en.vtt" {
		t.Fatalf("dst = %q", dst)
	}
	data, _ := os.ReadFile(dst)
	if len(data) == 0 {
		t.Fatal("subtitle file empty")
	}
}
