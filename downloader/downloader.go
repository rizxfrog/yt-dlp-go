// Package downloader implements yt-dlp-go's download backends.
//
// It provides a simple HTTP file downloader (with resume, rate limiting and
// exponential-backoff retries) and a native HLS/DASH fragment downloader
// (m3u8/MPD parsing, concurrent segment fetch via goroutines, and AES-128-CBC
// decryption for protected HLS streams), mirroring yt-dlp's "hlsnative"/
// "dashnative" path. fMP4/segment remuxing to a final container is delegated to
// the ffmpeg-backed postprocessor.
package downloader

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"yt-dlp-go/extractor"
)

// ProgressFunc is invoked with bytes downloaded so far and the total (or -1 if
// unknown). It is called periodically during a transfer.
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
	RateLimit           string           // e.g. "50K", "1M" — parsed by parseRateLimit
}

// Downloader is implemented by each backend.
type Downloader interface {
	Name() string
	Download(ctx context.Context, url, outPath string, opts DownloadOpts) error
}

// HTTPDownloader downloads a single resource to a file, supporting resume
// (via Range requests), rate limiting, and exponential-backoff retries. The
// transfer is written to a ".part" file and atomically renamed on success so an
// interrupted download can be resumed on the next run.
type HTTPDownloader struct{}

func (HTTPDownloader) Name() string { return "http" }

// Download fetches url into outPath.
func (HTTPDownloader) Download(ctx context.Context, url, outPath string, opts DownloadOpts) error {
	client := opts.Client
	if client == nil {
		client = http.DefaultClient
	}
	retries := opts.Retries
	if retries < 1 {
		retries = 1
	}
	// Ensure the destination directory exists. Output templates may include
	// subdirectories (e.g. "%(id)s/%(title)s"), and the .part file is opened
	// below with os.OpenFile, which does not create parent dirs.
	if dir := filepath.Dir(outPath); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("creating output directory %q: %w", dir, err)
		}
	}
	part := outPath + ".part"

	// Resume: start from the size of any existing partial file.
	var offset int64
	if fi, err := os.Stat(part); err == nil {
		offset = fi.Size()
	}

	var lastErr error
	for attempt := 0; attempt <= retries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff(attempt)):
			}
		}
		err := httpDownloadRange(ctx, client, opts, url, part, offset)
		if err == nil {
			if rerr := os.Rename(part, outPath); rerr != nil {
				return fmt.Errorf("finalising download: %w", rerr)
			}
			return nil
		}
		lastErr = err
		if se, ok := err.(statusError); ok && !se.retryable {
			return fmt.Errorf("download failed (status %d): %w", se.code, err)
		}
		// Re-stat the partial in case the server honoured the range.
		if fi, ferr := os.Stat(part); ferr == nil {
			offset = fi.Size()
		}
	}
	return fmt.Errorf("download failed after %d retries: %w", retries, lastErr)
}

// httpDownloadRange performs one GET, optionally as a Range request resuming from
// offset. It appends to part (or overwrites if the server ignored the range).
func httpDownloadRange(ctx context.Context, client *http.Client, opts DownloadOpts, url, part string, offset int64) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	for k, v := range opts.Headers {
		req.Header.Set(k, v)
	}
	if offset > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		// Server ignored the range: restart from the beginning.
		offset = 0
	case http.StatusPartialContent:
		// Expected when resuming.
	case http.StatusTooManyRequests, http.StatusRequestTimeout:
		return statusError{code: resp.StatusCode, retryable: true}
	case http.StatusForbidden:
		// CDN anti-hotlink / rate-limit blocks are often transient 403s (e.g.
		// hongguoduanju's qznovelvod CDN when downloading several episodes back
		// to back); retry with backoff, as yt-dlp does for such CDNs.
		return statusError{code: resp.StatusCode, retryable: true}
	case http.StatusNotFound, http.StatusUnauthorized:
		return statusError{code: resp.StatusCode, retryable: false}
	case http.StatusServiceUnavailable, http.StatusBadGateway, http.StatusGatewayTimeout,
		http.StatusInternalServerError, http.StatusBadRequest:
		// 5xx and 502/503/504 are retryable; 400 generally is not but some CDNs
		// return it transiently, so we let the retry loop decide.
		return statusError{code: resp.StatusCode, retryable: resp.StatusCode != http.StatusBadRequest}
	default:
		if resp.StatusCode >= 500 {
			return statusError{code: resp.StatusCode, retryable: true}
		}
		return statusError{code: resp.StatusCode, retryable: false}
	}

	var total int64 = -1
	if cl := resp.Header.Get("Content-Length"); cl != "" {
		if n, perr := strconv.ParseInt(cl, 10, 64); perr == nil {
			if offset > 0 {
				total = offset + n
			} else {
				total = n
			}
		}
	}

	flags := os.O_WRONLY | os.O_CREATE
	if offset > 0 {
		flags |= os.O_APPEND
	} else {
		flags |= os.O_TRUNC
	}
	f, err := os.OpenFile(part, flags, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	reader := io.Reader(resp.Body)
	if bps := parseRateLimit(opts.RateLimit); bps > 0 {
		reader = &rateReader{r: resp.Body, bytesPerSec: bps}
	}

	var written int64
	buf := make([]byte, 1<<16)
	for {
		n, rerr := reader.Read(buf)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				return werr
			}
			written += int64(n)
			if opts.Progress != nil {
				opts.Progress(offset+written, total)
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return rerr
		}
	}
	return nil
}

// statusError carries an HTTP status code and whether the download should retry.
type statusError struct {
	code      int
	retryable bool
}

func (e statusError) Error() string { return http.StatusText(e.code) }

// rateReader throttles reads to bytesPerSec.
type rateReader struct {
	r           io.Reader
	bytesPerSec int64
}

func (rr *rateReader) Read(p []byte) (int, error) {
	n, err := rr.r.Read(p)
	if rr.bytesPerSec > 0 && n > 0 {
		expected := time.Duration(int64(time.Second) * int64(n) / rr.bytesPerSec)
		if expected > 0 {
			time.Sleep(expected)
		}
	}
	return n, err
}

// backoff returns the delay before the given retry attempt (capped).
func backoff(attempt int) time.Duration {
	d := time.Duration(1<<uint(attempt-1)) * 500 * time.Millisecond
	if d > 16*time.Second {
		d = 16 * time.Second
	}
	return d
}

// parseRateLimit parses a rate string like "50K" or "1M" into bytes/second.
func parseRateLimit(s string) int64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	mult := int64(1)
	upper := strings.ToUpper(s)
	switch {
	case strings.HasSuffix(upper, "K"):
		mult = 1024
		s = strings.TrimSpace(s[:len(s)-1])
	case strings.HasSuffix(upper, "M"):
		mult = 1024 * 1024
		s = strings.TrimSpace(s[:len(s)-1])
	case strings.HasSuffix(upper, "G"):
		mult = 1024 * 1024 * 1024
		s = strings.TrimSpace(s[:len(s)-1])
	}
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return 0
	}
	return n * mult
}

// writeStream writes an io.Reader to a file (used by internal fetchers).
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
