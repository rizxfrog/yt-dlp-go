// Package sponsorblock implements a client for the SponsorBlock API
// (https://sponsor.ajay.app), which crowd-sources timestamp ranges that viewers
// typically want to skip: sponsor reads, self-promotion, filler, intros, etc.
//
// The client fetches "skip segments" for a video id and returns them as a
// normalised Segment slice. It is used by the core to mark those segments as
// chapters (--sponsorblock-mark) or to cut them out of the download
// (--sponsorblock-remove, implemented in the postprocessor).
package sponsorblock

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// Segment is one SponsorBlock skip segment.
type Segment struct {
	Category  string  // sponsor | selfpromo | interaction | intro | outro | preview | music_offtopic | filler
	StartTime float64 // seconds
	EndTime   float64 // seconds
	UUID      string
}

// Known SponsorBlock categories.
const (
	CatSponsor       = "sponsor"
	CatSelfpromo     = "selfpromo"
	CatInteraction   = "interaction"
	CatIntro         = "intro"
	CatOutro         = "outro"
	CatPreview       = "preview"
	CatMusicOfftopic = "music_offtopic"
	CatFiller        = "filler"
	CatHook          = "hook"
	CatPOIHighlight  = "poi_highlight"
	CatChapter       = "chapter"
)

// AllCategories lists every category in canonical order.
var AllCategories = []string{
	CatSponsor, CatSelfpromo, CatInteraction, CatIntro, CatOutro,
	CatPreview, CatMusicOfftopic, CatFiller, CatHook, CatPOIHighlight, CatChapter,
}

// DefaultCategories mirrors yt-dlp's default: sponsor only.
var DefaultCategories = []string{CatSponsor}

// ParseCategories turns a comma-separated category string (e.g.
// "sponsor,selfpromo") into a normalised, de-duplicated slice. "all" selects
// every known category. Unknown names are dropped.
func ParseCategories(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return DefaultCategories
	}
	known := map[string]bool{}
	for _, c := range AllCategories {
		known[c] = true
	}
	var out []string
	seen := map[string]bool{}
	for _, part := range strings.Split(s, ",") {
		name := strings.TrimSpace(part)
		if name == "" || seen[name] {
			continue
		}
		if name == "all" {
			out = append([]string{}, AllCategories...)
			return out
		}
		if !known[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	if len(out) == 0 {
		return DefaultCategories
	}
	return out
}

// Filter keeps only segments whose category is in categories (order-preserving).
func Filter(segs []Segment, categories []string) []Segment {
	want := map[string]bool{}
	for _, c := range categories {
		want[c] = true
	}
	var out []Segment
	for _, s := range segs {
		if want[s.Category] {
			out = append(out, s)
		}
	}
	return out
}

// apiBase is the SponsorBlock API origin. Exposed as a var so tests can point
// it at a local httptest server.
var apiBase = "https://sponsor.ajay.app"

// apiResponse is the raw shape returned by /api/skipSegments/<hash4>: a list of
// entries, each keyed by videoID (several ids can share a 4-char hash prefix).
type apiResponse struct {
	VideoID  string       `json:"videoID"`
	Segments []apiSegment `json:"segments"`
}

type apiSegment struct {
	Segment  []float64 `json:"segment"`
	Category string    `json:"category"`
	UUID     string    `json:"UUID"`
}

// Fetch retrieves skip segments for videoID via the SponsorBlock API. The
// current API (privacy-preserving) keys on the first 4 hex chars of
// sha256(videoID) rather than the raw id, so the response may cover several
// videos and is filtered back down to videoID. A nil client falls back to
// http.DefaultClient.
func Fetch(client *http.Client, videoID string, categories []string) ([]Segment, error) {
	if client == nil {
		client = http.DefaultClient
	}
	if len(categories) == 0 {
		categories = DefaultCategories
	}
	catJSON, err := json.Marshal(categories)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256([]byte(videoID))
	prefix := hex.EncodeToString(sum[:])[:4]

	q := url.Values{}
	q.Set("service", "YouTube")
	q.Set("categories", string(catJSON))
	q.Set("actionTypes", `["skip","poi","chapter"]`)
	u := apiBase + "/api/skipSegments/" + prefix + "?" + q.Encode()

	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "yt-dlp-go (sponsorblock client)")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("sponsorblock: status %d", resp.StatusCode)
	}
	var raw []apiResponse
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("sponsorblock: decode: %w", err)
	}
	var segs []Segment
	for _, r := range raw {
		if r.VideoID != videoID {
			continue
		}
		for _, s := range r.Segments {
			if len(s.Segment) != 2 || s.Category == "" {
				continue
			}
			// (0, 0) marks the entire video; not a skip segment.
			if s.Segment[0] == 0 && s.Segment[1] == 0 {
				continue
			}
			segs = append(segs, Segment{
				Category:  s.Category,
				StartTime: s.Segment[0],
				EndTime:   s.Segment[1],
				UUID:      s.UUID,
			})
		}
	}
	return segs, nil
}
