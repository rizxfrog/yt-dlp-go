// Package acast implements an InfoExtractor for acast.com, demonstrating the
// common "call a JSON API, build Info from the response" pattern that most
// podcast/radio extractors follow.
package acast

import (
	"fmt"
	"regexp"
	"strings"

	"yt-dlp-go/extractor"
)

// AcastIE extracts from acast.com.
type AcastIE struct{}

func init() { extractor.Register(AcastIE{}) }

func (AcastIE) Name() string { return "acast" }

var acastURLRE = regexp.MustCompile(`(?i)https?://(?:www\.|shows\.|play\.)?acast\.com/([^/?#]+)/([^/?#]+)`)

func (AcastIE) Match(u string) bool {
	return acastURLRE.MatchString(u)
}

// Extract fetches the episode metadata from Acast's public API.
func (AcastIE) Extract(ctx *extractor.Context, pageURL string) (*extractor.Info, error) {
	m := acastURLRE.FindStringSubmatch(pageURL)
	if m == nil {
		return nil, fmt.Errorf("not an acast URL: %q", pageURL)
	}
	channel, episode := m[1], m[2]

	// If the page itself does not reveal the API ids, fall back to scanning the
	// HTML for the feeder API call Acast embeds.
	apiURL := fmt.Sprintf("https://feeder.acast.com/api/v1/shows/%s/episodes/%s", channel, episode)
	var data any
	var err error
	if data, err = extractor.DownloadJSON(ctx, apiURL, nil, nil); err != nil {
		html, herr := extractor.DownloadWebpage(ctx, pageURL, nil, nil)
		if herr != nil {
			return nil, err
		}
		re := regexp.MustCompile(`"https://feeder\.acast\.com/api/v1/shows/([^/"']+)/episodes/([^/"']+)"`)
		if mm := re.FindStringSubmatch(html); mm != nil {
			apiURL = fmt.Sprintf("https://feeder.acast.com/api/v1/shows/%s/episodes/%s", mm[1], mm[2])
			data, err = extractor.DownloadJSON(ctx, apiURL, nil, nil)
			if err != nil {
				return nil, err
			}
		} else {
			return nil, err
		}
	}

	root, ok := data.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("unexpected API response shape")
	}

	info := &extractor.Info{
		ID:         extractor.StrOrNone(root["id"]),
		Title:      extractor.StrOrNone(root["title"]),
		WebpageURL: pageURL,
		Ext:        "mp3",
		Raw:        root,
	}
	info.Description = extractor.CleanHTML(extractor.StrOrNone(root["description"]))
	if ts := extractor.StrOrNone(root["publishDate"]); ts != "" {
		if t, terr := extractor.ParseISO8601(ts); terr == nil {
			info.UploadDate = t.Format("20060102")
		}
	}
	info.Duration = extractor.FloatOrNone(root["duration"])

	mediaURL := extractor.StrOrNone(root["url"])
	if mediaURL == "" {
		mediaURL = extractor.StrOrNone(root["audio"])
	}
	if mediaURL == "" {
		return nil, fmt.Errorf("no media url in acast response")
	}
	info.Formats = append(info.Formats, extractor.Format{
		FormatID: "1",
		URL:      mediaURL,
		Protocol: "http",
		Ext:      extOf(mediaURL),
	})
	return info, nil
}

func extOf(u string) string {
	e := strings.TrimPrefix(strings.ToLower(urlForExt(u)), ".")
	switch {
	case strings.Contains(e, "mp3"):
		return "mp3"
	case strings.Contains(e, "m4a"):
		return "m4a"
	case strings.Contains(e, "opus"):
		return "opus"
	default:
		return "mp3"
	}
}

func urlForExt(u string) string {
	if i := strings.Index(u, "?"); i >= 0 {
		u = u[:i]
	}
	if i := strings.LastIndex(u, "."); i >= 0 {
		return u[i:]
	}
	return ""
}
