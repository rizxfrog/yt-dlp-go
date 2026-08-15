package downloader

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHTTPDownload_Basic(t *testing.T) {
	body := []byte("hello world, this is a test payload for the http downloader")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(body)
	}))
	defer srv.Close()

	dir := t.TempDir()
	out := filepath.Join(dir, "out.bin")
	err := (HTTPDownloader{}).Download(context.Background(), srv.URL, out, DownloadOpts{Retries: 1})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(out)
	if string(got) != string(body) {
		t.Fatalf("got %q want %q", got, body)
	}
}

func TestHTTPDownload_CreatesParentDir(t *testing.T) {
	// Regression: a -o template with a subdirectory (e.g. "%(id)s/%(title)s")
	// must have its target directory created; otherwise the .part open fails with
	// "The system cannot find the path specified".
	body := []byte("payload written into a freshly created subdirectory")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(body)
	}))
	defer srv.Close()

	dir := t.TempDir()
	out := filepath.Join(dir, "nested", "deeper", "out.bin")
	if _, err := os.Stat(filepath.Dir(out)); !os.IsNotExist(err) {
		t.Fatalf("precondition: parent dir should not exist yet")
	}
	err := (HTTPDownloader{}).Download(context.Background(), srv.URL, out, DownloadOpts{Retries: 1})
	if err != nil {
		t.Fatal(err)
	}
	got, rerr := os.ReadFile(out)
	if rerr != nil {
		t.Fatalf("read downloaded file: %v", rerr)
	}
	if string(got) != string(body) {
		t.Fatalf("got %q want %q", got, body)
	}
}

func TestHTTPDownload_Resume(t *testing.T) {
	content := []byte("0123456789ABCDEF") // 16 bytes
	half := 8
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rg := r.Header.Get("Range")
		if strings.HasPrefix(rg, "bytes=8-") {
			w.WriteHeader(http.StatusPartialContent)
			w.Header().Set("Content-Length", fmt.Sprintf("%d", len(content)-half))
			w.Write(content[half:])
			return
		}
		w.Write(content)
	}))
	defer srv.Close()

	dir := t.TempDir()
	out := filepath.Join(dir, "out.bin")
	// Simulate a previous partial download of the first half.
	if err := os.WriteFile(out+".part", content[:half], 0o644); err != nil {
		t.Fatal(err)
	}
	err := (HTTPDownloader{}).Download(context.Background(), srv.URL, out, DownloadOpts{Retries: 1})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(out)
	if string(got) != string(content) {
		t.Fatalf("resumed content = %q want %q", got, content)
	}
}

func TestHTTPDownload_RetryOn503(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if hits == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Write([]byte("ok after retry"))
	}))
	defer srv.Close()

	dir := t.TempDir()
	out := filepath.Join(dir, "out.bin")
	err := (HTTPDownloader{}).Download(context.Background(), srv.URL, out, DownloadOpts{Retries: 2})
	if err != nil {
		t.Fatal(err)
	}
	if hits != 2 {
		t.Fatalf("expected 2 server hits (1 fail + 1 success), got %d", hits)
	}
}

func TestHTTPDownload_404NotRetryable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	dir := t.TempDir()
	out := filepath.Join(dir, "out.bin")
	err := (HTTPDownloader{}).Download(context.Background(), srv.URL, out, DownloadOpts{Retries: 3})
	if err == nil {
		t.Fatal("expected error for 404")
	}
}

func TestParseRateLimit(t *testing.T) {
	cases := map[string]int64{
		"":    0,
		"50K": 50 * 1024,
		"1M":  1024 * 1024,
		"2G":  2 * 1024 * 1024 * 1024,
		"100": 100,
	}
	for in, want := range cases {
		if got := parseRateLimit(in); got != want {
			t.Fatalf("parseRateLimit(%q) = %d want %d", in, got, want)
		}
	}
}
