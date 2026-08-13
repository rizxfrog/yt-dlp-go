package downloader

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// Fragment is a single media segment plus the IV used to decrypt it (if any).
type Fragment struct {
	URL string
	IV  []byte
}

// hlsManifest is a parsed m3u8.
type hlsManifest struct {
	variants []variant
	segments []Fragment
	keyURL   string
	keyIV    []byte // explicit IV from #EXT-X-KEY (applies to all segs if set)
	seqBase  uint64
	isLive   bool
}

type variant struct {
	bandwidth uint64
	uri       string
}

// FragmentDownloader downloads HLS/DASH manifests by fetching and (decrypting)
// segments concurrently, then concatenating them. fMP4 segment sets are written
// raw and are expected to be remuxed by the ffmpeg postprocessor.
type FragmentDownloader struct{}

func (FragmentDownloader) Name() string { return "hlsnative" }

// Download parses the manifest at url and writes the assembled stream to outPath.
// It dispatches to the HLS or DASH parser based on the manifest type.
func (FragmentDownloader) Download(ctx context.Context, manifestURL, outPath string, opts DownloadOpts) error {
	if strings.Contains(strings.ToLower(manifestURL), ".mpd") {
		return downloadDASH(ctx, manifestURL, opts, outPath)
	}
	client := opts.Client
	if client == nil {
		client = http.DefaultClient
	}
	concurrency := opts.ConcurrentFragments
	if concurrency < 1 {
		concurrency = 1
	}

	// Resolve variant playlists (depth-limited) to a media playlist.
	manifest, _, err := resolveManifest(ctx, client, opts.Headers, manifestURL, 0)
	if err != nil {
		return err
	}

	var key []byte
	if manifest.keyURL != "" {
		key, err = downloadBytes(ctx, client, opts.Headers, resolveURL(manifestURL, manifest.keyURL))
		if err != nil {
			return fmt.Errorf("downloading key: %w", err)
		}
		if len(key) != 16 {
			return fmt.Errorf("AES key has unexpected length %d", len(key))
		}
	}

	tmpDir, err := os.MkdirTemp("", "ytdlpfrag-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	segs := manifest.segments
	errs := make([]error, len(segs))
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	for i, seg := range segs {
		wg.Add(1)
		go func(i int, seg Fragment) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			dst := filepath.Join(tmpDir, strconv.Itoa(i))
			errs[i] = downloadFile(ctx, client, opts.Headers, resolveURL(manifestURL, seg.URL), dst)
		}(i, seg)
	}
	wg.Wait()
	for _, e := range errs {
		if e != nil {
			return fmt.Errorf("segment download failed: %w", e)
		}
	}

	out, err := createFile(outPath)
	if err != nil {
		return err
	}
	defer out.Close()

	for i := range segs {
		raw, err := os.ReadFile(filepath.Join(tmpDir, strconv.Itoa(i)))
		if err != nil {
			return err
		}
		if key != nil {
			iv := segs[i].IV
			if iv == nil {
				iv = manifest.keyIV
			}
			if iv == nil {
				iv = defaultIV(manifest.seqBase + uint64(i))
			}
			raw, err = decryptAES128(raw, key, iv)
			if err != nil {
				return fmt.Errorf("decrypting segment %d: %w", i, err)
			}
		}
		if _, err := out.Write(raw); err != nil {
			return err
		}
		if opts.Progress != nil {
			opts.Progress(int64(i+1)*int64(len(raw)), int64(len(segs))*int64(len(raw)))
		}
	}
	return nil
}

// resolveManifest follows variant playlists until a media playlist is found.
func resolveManifest(ctx context.Context, client *http.Client, headers map[string]string, manifestURL string, depth int) (*hlsManifest, int, error) {
	if depth > 4 {
		return nil, depth, fmt.Errorf("too many nested variant playlists")
	}
	body, err := downloadText(ctx, client, headers, manifestURL)
	if err != nil {
		return nil, depth, err
	}
	m, err := parseM3U8(body)
	if err != nil {
		return nil, depth, err
	}
	if len(m.variants) > 0 {
		// Pick the highest-bandwidth variant.
		best := m.variants[0]
		for _, v := range m.variants[1:] {
			if v.bandwidth > best.bandwidth {
				best = v
			}
		}
		return resolveManifest(ctx, client, headers, resolveURL(manifestURL, best.uri), depth+1)
	}
	return m, depth, nil
}

func parseM3U8(body string) (*hlsManifest, error) {
	m := &hlsManifest{isLive: true}
	lines := strings.Split(body, "\n")
	var pendingINF bool
	var seq uint64
	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" || strings.HasPrefix(line, "#EXTM3U") {
			continue
		}
		switch {
		case strings.HasPrefix(line, "#EXT-X-STREAM-INF:"):
			uri, ok := nextNonComment(lines, i)
			if !ok {
				continue
			}
			m.variants = append(m.variants, variant{bandwidth: bandwidthOf(line), uri: uri})
		case strings.HasPrefix(line, "#EXT-X-MEDIA-SEQUENCE:"):
			if v, err := strconv.ParseUint(strings.TrimPrefix(line, "#EXT-X-MEDIA-SEQUENCE:"), 10, 64); err == nil {
				seq = v
				m.seqBase = v
			}
		case strings.HasPrefix(line, "#EXT-X-KEY:"):
			method := attr(line, "METHOD")
			if !strings.EqualFold(method, "AES-128") {
				continue
			}
			m.keyURL = attr(line, "URI")
			m.keyURL = strings.Trim(m.keyURL, "\"")
			if iv := attr(line, "IV"); iv != "" {
				m.keyIV = parseIV(iv)
			}
		case strings.HasPrefix(line, "#EXTINF:"):
			pendingINF = true
		case strings.HasPrefix(line, "#EXT-X-ENDLIST"):
			m.isLive = false
		case !strings.HasPrefix(line, "#"):
			if pendingINF {
				m.segments = append(m.segments, Fragment{URL: line})
				pendingINF = false
			}
		}
	}
	// Assign default IVs when none were explicitly provided.
	if m.keyURL != "" && len(m.segments) > 0 && m.keyIV == nil {
		for i := range m.segments {
			m.segments[i].IV = defaultIV(seq + uint64(i))
		}
	}
	return m, nil
}

// defaultIV returns the 16-byte big-endian representation of n (RFC 8216 default).
func defaultIV(n uint64) []byte {
	iv := make([]byte, 16)
	for i := 15; i >= 8; i-- {
		iv[i] = byte(n & 0xff)
		n >>= 8
	}
	return iv
}

func parseIV(s string) []byte {
	s = strings.TrimPrefix(strings.TrimSpace(s), "0x")
	s = strings.TrimPrefix(s, "0X")
	b, err := hex.DecodeString(s)
	if err != nil || len(b) != 16 {
		return nil
	}
	return b
}

func bandwidthOf(attrLine string) uint64 {
	v := attr(attrLine, "BANDWIDTH")
	n, _ := strconv.ParseUint(v, 10, 64)
	return n
}

// attr extracts a quoted/unquoted attribute value from a tag line.
func attr(line, key string) string {
	idx := strings.Index(line, key+"=")
	if idx < 0 {
		return ""
	}
	rest := line[idx+len(key)+1:]
	if strings.HasPrefix(rest, "\"") {
		if end := strings.Index(rest[1:], "\""); end >= 0 {
			return rest[1 : end+1]
		}
	}
	if comma := strings.Index(rest, ","); comma >= 0 {
		return rest[:comma]
	}
	return rest
}

// nextNonComment returns the first non-comment, non-empty line after index i.
func nextNonComment(lines []string, i int) (string, bool) {
	for j := i + 1; j < len(lines); j++ {
		l := strings.TrimSpace(lines[j])
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		return l, true
	}
	return "", false
}

func resolveURL(base, ref string) string {
	b, err := url.Parse(base)
	if err != nil {
		return ref
	}
	r, err := url.Parse(ref)
	if err != nil {
		return ref
	}
	return b.ResolveReference(r).String()
}

func decryptAES128(data, key, iv []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	if len(data)%aes.BlockSize != 0 {
		return nil, fmt.Errorf("ciphertext not a multiple of block size (%d)", len(data))
	}
	dec := cipher.NewCBCDecrypter(block, iv)
	out := make([]byte, len(data))
	dec.CryptBlocks(out, data)
	return out, nil
}

// --- low-level fetchers ---

func downloadText(ctx context.Context, client *http.Client, headers map[string]string, u string) (string, error) {
	b, err := downloadBytes(ctx, client, headers, u)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func downloadBytes(ctx context.Context, client *http.Client, headers map[string]string, u string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: status %d", u, resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

func downloadFile(ctx context.Context, client *http.Client, headers map[string]string, u, dst string) error {
	data, err := downloadBytes(ctx, client, headers, u)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o600)
}
