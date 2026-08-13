// Package downloader implements yt-dlp-go's download backends.
//
// It provides a simple HTTP file downloader and a native HLS/DASH fragment
// downloader (m3u8 parsing, concurrent segment fetch via goroutines, and
// AES-128-CBC decryption for protected streams), mirroring yt-dlp's
// "hlsnative"/"dashnative" path. fMP4/segment remuxing to a final container is
// delegated to the ffmpeg-backed postprocessor.
package downloader

import (
	"context"
	"fmt"
	"io"
	"net/http"

	"yt-dlp-go/extractor"
)

// ProgressFunc is invoked with bytes downloaded so far and the total (or -1 if
// unknown).
type ProgressFunc func(downloaded, total int64)

// DownloadOpts carries per-download configuration.
type DownloadOpts struct {
	Client              *http.Client
	Headers             map[string]string
	Retries             int
	ConcurrentFragments int
	Progress            ProgressFunc
	IsLive              bool
	Format              extractor.Format // the format being fetched (used to pick DASH reps)
}

// Downloader is implemented by each backend.
type Downloader interface {
	Name() string
	Download(ctx context.Context, url, outPath string, opts DownloadOpts) error
}

// HTTPDownloader downloads a single resource to a file with retries.
type HTTPDownloader struct{}

func (HTTPDownloader) Name() string { return "http" }

// Download fetches url into outPath, retrying on transient failures.
func (HTTPDownloader) Download(ctx context.Context, url, outPath string, opts DownloadOpts) error {
	client := opts.Client
	if client == nil {
		client = http.DefaultClient
	}
	retries := opts.Retries
	if retries < 1 {
		retries = 1
	}

	var lastErr error
	for attempt := 0; attempt < retries; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		for k, v := range opts.Headers {
			req.Header.Set(k, v)
		}
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
			resp.Body.Close()
			lastErr = fmt.Errorf("unexpected status %d", resp.StatusCode)
			continue
		}
		err = writeStream(resp.Body, outPath, opts.Progress)
		resp.Body.Close()
		if err != nil {
			lastErr = err
			continue
		}
		return nil
	}
	return fmt.Errorf("download failed after %d attempts: %w", retries, lastErr)
}

func writeStream(r io.Reader, outPath string, prog ProgressFunc) error {
	f, err := createFile(outPath)
	if err != nil {
		return err
	}
	defer f.Close()

	buf := make([]byte, 1<<16)
	var written int64
	for {
		n, err := r.Read(buf)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				return werr
			}
			written += int64(n)
			if prog != nil {
				prog(written, -1)
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
	}
	return nil
}
