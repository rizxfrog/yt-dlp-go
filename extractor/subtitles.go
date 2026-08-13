package extractor

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// SubtitleRef is a downloadable subtitle track discovered from a manifest.
type SubtitleRef struct {
	Lang string // BCP-47 language code, e.g. "en", "zh-Hans"
	Name string // human-readable label
	URL  string
	Ext  string // vtt | srt | ass
}

var hlsSubRE = regexp.MustCompile(`(?m)^#EXT-X-MEDIA:TYPE=SUBTITLES([^\n]*)`)

// ParseHLSSubtitles extracts subtitle tracks declared via #EXT-X-MEDIA in an
// HLS manifest.
func ParseHLSSubtitles(body, baseURL string) []SubtitleRef {
	var out []SubtitleRef
	for _, m := range hlsSubRE.FindAllStringSubmatch(body, -1) {
		attrs := parseAttrLine(m[1])
		uri := strings.Trim(attrs["URI"], `"`)
		if uri == "" {
			continue
		}
		lang := attrs["LANGUAGE"]
		if lang == "" {
			lang = attrs["NAME"]
		}
		ext := "vtt"
		if strings.Contains(uri, ".srt") {
			ext = "srt"
		} else if strings.Contains(uri, ".ass") {
			ext = "ass"
		}
		out = append(out, SubtitleRef{
			Lang: lang,
			Name: attrs["NAME"],
			URL:  joinURL(baseURL, uri),
			Ext:  ext,
		})
	}
	return out
}

// parseAttrLine parses a comma-separated KEY=VALUE list that may contain quoted
// values (and commas inside quotes).
func parseAttrLine(s string) map[string]string {
	m := map[string]string{}
	re := regexp.MustCompile(`([A-Za-z-]+)=("([^"]*)"|([^,]*))`)
	for _, mm := range re.FindAllStringSubmatch(s, -1) {
		val := mm[3]
		if val == "" {
			val = mm[4]
		}
		m[mm[1]] = val
	}
	return m
}

// DownloadSubtitle fetches one subtitle track to dir/baseName.lang.ext and
// returns the written path.
func DownloadSubtitle(ctx context.Context, client *http.Client, sub SubtitleRef, dir, baseName string) (string, error) {
	if sub.URL == "" {
		return "", fmt.Errorf("subtitle %q has no URL", sub.Lang)
	}
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sub.URL, nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GET subtitle %s: status %d", sub.Lang, resp.StatusCode)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	lang := sub.Lang
	if lang == "" {
		lang = "und"
	}
	dst := filepath.Join(dir, fmt.Sprintf("%s.%s.%s", baseName, lang, sub.Ext))
	f, err := os.Create(dst)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := io.Copy(f, resp.Body); err != nil {
		return "", err
	}
	return dst, nil
}

func joinURL(base, ref string) string {
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
