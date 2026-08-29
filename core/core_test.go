package core

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"yt-dlp-go/extractor"
	"yt-dlp-go/options"
)

// testExtractor is a tiny in-memory extractor used to exercise the core pipeline
// without any network dependency on real sites.
type testExtractor struct {
	name    string
	matches func(url string) bool
	handler func(ctx *extractor.Context, url string) (*extractor.Info, error)
}

func (t *testExtractor) Name() string { return t.name }
func (t *testExtractor) Match(url string) bool {
	if t.matches != nil {
		return t.matches(url)
	}
	return strings.HasPrefix(url, "test://")
}
func (t *testExtractor) Extract(ctx *extractor.Context, url string) (*extractor.Info, error) {
	return t.handler(ctx, url)
}

// newMediaServer returns a server that always serves a small binary body.
func newMediaServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "video/mp4")
		_, _ = w.Write([]byte("binary-data-binary-data"))
	}))
}

func newTestYDL(t *testing.T, dir string) *YoutubeDL {
	t.Helper()
	opts := &options.Options{
		OutputDir:           dir,
		AddHeaders:          map[string]string{},
		ConcurrentFragments: 1,
		Retries:             2,
	}
	ydl, err := New(opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return ydl
}

func countFiles(t *testing.T, dir, suffix string) int {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	n := 0
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), suffix) {
			n++
		}
	}
	return n
}

// TestDownload_Playlist verifies that a playlist Info (with Entries) causes each
// child to be downloaded into its own file.
func TestDownload_Playlist(t *testing.T) {
	srv := newMediaServer(t)
	defer srv.Close()

	ext := &testExtractor{
		name:    "test-playlist",
		matches: func(u string) bool { return u == "test://playlist" },
		handler: func(ctx *extractor.Context, url string) (*extractor.Info, error) {
			return &extractor.Info{
				ID:    "pl1",
				Title: "My Playlist",
				Entries: []*extractor.Info{
					{ID: "v1", Title: "Video One", Formats: []extractor.Format{{FormatID: "1", URL: srv.URL + "/v1", Ext: "mp4", Protocol: "http", VCodec: "h264", ACodec: "aac"}}},
					{ID: "v2", Title: "Video Two", Formats: []extractor.Format{{FormatID: "1", URL: srv.URL + "/v2", Ext: "mp4", Protocol: "http", VCodec: "h264", ACodec: "aac"}}},
				},
			}, nil
		},
	}
	extractor.Register(ext)

	dir := t.TempDir()
	ydl := newTestYDL(t, dir)

	if err := ydl.Download("test://playlist"); err != nil {
		t.Fatalf("playlist download: %v", err)
	}
	if got := countFiles(t, dir, ".mp4"); got != 2 {
		t.Errorf("expected 2 downloaded files, got %d", got)
	}
}

// TestDownload_PlaylistItems verifies --playlist-items restricts which entries
// are downloaded (and preserves 1-based indexing).
func TestDownload_PlaylistItems(t *testing.T) {
	srv := newMediaServer(t)
	defer srv.Close()

	ext := &testExtractor{
		name:    "test-playlist-items",
		matches: func(u string) bool { return u == "test://playlist-items" },
		handler: func(ctx *extractor.Context, url string) (*extractor.Info, error) {
			return &extractor.Info{
				ID:    "pl2",
				Title: "P",
				Entries: []*extractor.Info{
					{ID: "a", Title: "A", Formats: []extractor.Format{{FormatID: "1", URL: srv.URL + "/a", Ext: "mp4", Protocol: "http", VCodec: "h264", ACodec: "aac"}}},
					{ID: "b", Title: "B", Formats: []extractor.Format{{FormatID: "1", URL: srv.URL + "/b", Ext: "mp4", Protocol: "http", VCodec: "h264", ACodec: "aac"}}},
					{ID: "c", Title: "C", Formats: []extractor.Format{{FormatID: "1", URL: srv.URL + "/c", Ext: "mp4", Protocol: "http", VCodec: "h264", ACodec: "aac"}}},
				},
			}, nil
		},
	}
	extractor.Register(ext)

	dir := t.TempDir()
	opts := &options.Options{
		OutputDir:           dir,
		AddHeaders:          map[string]string{},
		ConcurrentFragments: 1,
		Retries:             2,
		PlaylistItems:       "1,3",
	}
	ydl, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	if err := ydl.Download("test://playlist-items"); err != nil {
		t.Fatalf("playlist-items download: %v", err)
	}
	if got := countFiles(t, dir, ".mp4"); got != 2 {
		t.Errorf("expected 2 (items 1 & 3) downloaded files, got %d", got)
	}
}

// TestDownloadURLs_Concurrency verifies that multiple URLs download in parallel
// and that a single failing URL does not abort the others (error isolation).
func TestDownloadURLs_Concurrency(t *testing.T) {
	srv := newMediaServer(t)
	defer srv.Close()

	ext := &testExtractor{
		name: "test-multi",
		matches: func(u string) bool {
			return u == "test://ok1" || u == "test://ok2" || u == "test://bad"
		},
		handler: func(ctx *extractor.Context, url string) (*extractor.Info, error) {
			switch url {
			case "test://ok1":
				return &extractor.Info{ID: "ok1", Title: "OK One", Formats: []extractor.Format{{FormatID: "1", URL: srv.URL + "/a", Ext: "mp4", Protocol: "http", VCodec: "h264", ACodec: "aac"}}}, nil
			case "test://ok2":
				return &extractor.Info{ID: "ok2", Title: "OK Two", Formats: []extractor.Format{{FormatID: "1", URL: srv.URL + "/b", Ext: "mp4", Protocol: "http", VCodec: "h264", ACodec: "aac"}}}, nil
			default:
				return nil, fmt.Errorf("boom: cannot extract %s", url)
			}
		},
	}
	extractor.Register(ext)

	dir := t.TempDir()
	ydl := newTestYDL(t, dir)

	errs := ydl.DownloadURLs([]string{"test://ok1", "test://bad", "test://ok2"})
	if len(errs) != 1 {
		t.Fatalf("expected exactly 1 error, got %d: %v", len(errs), errs)
	}
	if got := countFiles(t, dir, ".mp4"); got != 2 {
		t.Errorf("expected both OK urls downloaded (2 files), got %d", got)
	}
}

func TestWriteInlineThumbnail(t *testing.T) {
	dir := t.TempDir()
	base := dir + "/video"
	if err := writeInlineThumbnail("data:image/png;base64,aGVsbG8=", base); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(base + ".png")
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello" {
		t.Errorf("thumbnail content = %q", data)
	}
}
