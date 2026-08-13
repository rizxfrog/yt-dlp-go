package downloader

import (
	"context"
	"encoding/xml"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// dashRepresentation is one playable stream inside an MPD.
type dashRepresentation struct {
	ID          string
	ContentType string // "video" | "audio" | "text"
	MimeType    string
	Codecs      string
	Bandwidth   uint64
	Width       int
	Height      int
	InitURL     string   // initialization segment (may be empty)
	SegmentURLs []string // media segment URLs (may be empty for SegmentBase)
	// SegmentBase single-file mode:
	MediaURL   string // the full media file (when SegmentBase has no list)
	IndexRange string // byte range of the sidx inside MediaURL
}

type mpdRoot struct {
	XMLName         xml.Name `xml:"MPD"`
	MediaPresentDur string   `xml:"mediaPresentationDuration,attr"`
	Period          []mpdPeriod
}

type mpdPeriod struct {
	Duration      string `xml:"duration,attr"`
	AdaptationSet []struct {
		ContentType    string `xml:"contentType,attr"`
		MimeType       string `xml:"mimeType,attr"`
		BaseURL        string `xml:"BaseURL"`
		Representation []struct {
			ID                string `xml:"id,attr"`
			Bandwidth         uint64 `xml:"bandwidth,attr"`
			Width             int    `xml:"width,attr"`
			Height            int    `xml:"height,attr"`
			Codecs            string `xml:"codecs,attr"`
			AudioSamplingRate string `xml:"audioSamplingRate,attr"`
			BaseURL           string `xml:"BaseURL"`
			SegmentBase       *struct {
				IndexRange     string `xml:"indexRange,attr"`
				Initialization struct {
					SourceURL string `xml:"sourceURL,attr"`
				} `xml:"Initialization"`
			} `xml:"SegmentBase"`
			SegmentTemplate *struct {
				Duration       uint64 `xml:"duration,attr"`
				Timescale      uint64 `xml:"timescale,attr"`
				StartNumber    uint64 `xml:"startNumber,attr"`
				Initialization string `xml:"initialization,attr"`
				Media          string `xml:"media,attr"`
				Timeline       *struct {
					S []struct {
						D uint64 `xml:"d,attr"`
						R uint64 `xml:"r,attr"`
					} `xml:"S"`
				} `xml:"SegmentTimeline"`
			} `xml:"SegmentTemplate"`
			SegmentList *struct {
				SegmentURL []struct {
					Media string `xml:"media,attr"`
				} `xml:"SegmentURL"`
			} `xml:"SegmentList"`
		} `xml:"Representation"`
	} `xml:"AdaptationSet"`
}

// parseMPD deserialises an MPD document into a flat list of representations.
func parseMPD(body, manifestURL string) ([]dashRepresentation, error) {
	var root mpdRoot
	if err := xml.Unmarshal([]byte(body), &root); err != nil {
		return nil, fmt.Errorf("parse MPD: %w", err)
	}
	if len(root.Period) == 0 {
		return nil, fmt.Errorf("MPD has no Period")
	}

	var reps []dashRepresentation
	for _, p := range root.Period {
		periodDur := parseISO8601Duration(p.Duration)
		if periodDur == 0 {
			periodDur = parseISO8601Duration(root.MediaPresentDur)
		}
		for _, as := range p.AdaptationSet {
			ct := as.ContentType
			if ct == "" && as.MimeType != "" {
				if strings.HasPrefix(as.MimeType, "audio") {
					ct = "audio"
				} else if strings.HasPrefix(as.MimeType, "video") {
					ct = "video"
				} else if strings.HasPrefix(as.MimeType, "text") {
					ct = "text"
				}
			}
			asBase := as.BaseURL
			for _, r := range as.Representation {
				rep := dashRepresentation{
					ID:          r.ID,
					ContentType: ct,
					MimeType:    as.MimeType,
					Codecs:      r.Codecs,
					Bandwidth:   r.Bandwidth,
					Width:       r.Width,
					Height:      r.Height,
				}
				repBase := joinURLs(manifestURL, asBase, r.BaseURL)
				buildDashSegments(&rep, manifestURL, repBase, r, periodDur)
				reps = append(reps, rep)
			}
		}
	}
	if len(reps) == 0 {
		return nil, fmt.Errorf("MPD contained no representations")
	}
	return reps, nil
}

func buildDashSegments(rep *dashRepresentation, manifestURL, repBase string, r struct {
	ID                string `xml:"id,attr"`
	Bandwidth         uint64 `xml:"bandwidth,attr"`
	Width             int    `xml:"width,attr"`
	Height            int    `xml:"height,attr"`
	Codecs            string `xml:"codecs,attr"`
	AudioSamplingRate string `xml:"audioSamplingRate,attr"`
	BaseURL           string `xml:"BaseURL"`
	SegmentBase       *struct {
		IndexRange     string `xml:"indexRange,attr"`
		Initialization struct {
			SourceURL string `xml:"sourceURL,attr"`
		} `xml:"Initialization"`
	} `xml:"SegmentBase"`
	SegmentTemplate *struct {
		Duration       uint64 `xml:"duration,attr"`
		Timescale      uint64 `xml:"timescale,attr"`
		StartNumber    uint64 `xml:"startNumber,attr"`
		Initialization string `xml:"initialization,attr"`
		Media          string `xml:"media,attr"`
		Timeline       *struct {
			S []struct {
				D uint64 `xml:"d,attr"`
				R uint64 `xml:"r,attr"`
			} `xml:"S"`
		} `xml:"SegmentTimeline"`
	} `xml:"SegmentTemplate"`
	SegmentList *struct {
		SegmentURL []struct {
			Media string `xml:"media,attr"`
		} `xml:"SegmentURL"`
	} `xml:"SegmentList"`
}, periodDur float64) {
	switch {
	case r.SegmentBase != nil:
		if r.SegmentBase.Initialization.SourceURL != "" {
			rep.InitURL = joinURLs(manifestURL, repBase, r.SegmentBase.Initialization.SourceURL)
		}
		// The representation BaseURL (or media) is one big file; ranges are
		// resolved at download time using IndexRange.
		rep.MediaURL = joinURLs(manifestURL, repBase, "")
		rep.IndexRange = r.SegmentBase.IndexRange
	case r.SegmentTemplate != nil:
		st := r.SegmentTemplate
		init := st.Initialization
		init = strings.ReplaceAll(init, "$RepresentationID$", r.ID)
		rep.InitURL = joinURLs(manifestURL, repBase, init)
		start := st.StartNumber
		if start == 0 {
			start = 1
		}
		if st.Timeline != nil {
			n := uint64(0)
			for _, s := range st.Timeline.S {
				count := s.R + 1 // r is the number of *additional* repeats
				n += count
			}
			for i := uint64(0); i < n; i++ {
				rep.SegmentURLs = append(rep.SegmentURLs, dashSegmentURL(manifestURL, repBase, st.Media, r.ID, start+i, 0))
			}
		} else if st.Duration > 0 && st.Timescale > 0 && periodDur > 0 {
			segDur := float64(st.Duration) / float64(st.Timescale)
			n := uint64(periodDur/segDur) + 1
			for i := uint64(0); i < n; i++ {
				rep.SegmentURLs = append(rep.SegmentURLs, dashSegmentURL(manifestURL, repBase, st.Media, r.ID, start+i, 0))
			}
		}
	case r.SegmentList != nil:
		for _, su := range r.SegmentList.SegmentURL {
			rep.SegmentURLs = append(rep.SegmentURLs, joinURLs(manifestURL, repBase, su.Media))
		}
	}
}

// dashSegmentURL expands a SegmentTemplate media string ($Number$/$RepresentationID$).
func dashSegmentURL(manifestURL, repBase, media, repID string, number uint64, t uint64) string {
	media = strings.ReplaceAll(media, "$RepresentationID$", repID)
	media = strings.ReplaceAll(media, "$Number%0Nd$", strconv.FormatUint(number, 10))
	media = strings.ReplaceAll(media, "$Number$", strconv.FormatUint(number, 10))
	media = strings.ReplaceAll(media, "$Time$", strconv.FormatUint(t, 10))
	return joinURLs(manifestURL, repBase, media)
}

// joinURLs resolves a possibly-empty chain of base URLs against the manifest.
func joinURLs(manifestURL string, bases ...string) string {
	u := manifestURL
	for _, b := range bases {
		if b == "" {
			continue
		}
		u = resolveURL(u, b)
	}
	return u
}

// parseISO8601Duration parses a limited subset of xs:duration (e.g. "PT10M30S").
func parseISO8601Duration(s string) float64 {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "PT") && !strings.HasPrefix(s, "P") {
		return 0
	}
	s = strings.TrimPrefix(s, "P")
	s = strings.TrimPrefix(s, "T")
	var total, cur float64
	num := ""
	multiplier := map[string]float64{"H": 3600, "M": 60, "S": 1}
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9' || r == '.':
			num += string(r)
		case r == 'H' || r == 'M' || r == 'S':
			v, err := strconv.ParseFloat(num, 64)
			if err == nil {
				total += v * multiplier[string(r)]
			}
			num = ""
		default:
			num = ""
		}
	}
	_ = cur
	return total
}

// pickRepresentation chooses the DASH representation that best matches a Format.
func pickRepresentation(reps []dashRepresentation, wantContentType string, bandwidth uint64) *dashRepresentation {
	var best *dashRepresentation
	for i := range reps {
		rep := &reps[i]
		if wantContentType != "" && rep.ContentType != wantContentType {
			continue
		}
		if best == nil || rep.Bandwidth > best.Bandwidth {
			best = rep
		}
	}
	if best != nil {
		return best
	}
	// Fallback: highest-bandwidth representation of any type.
	for i := range reps {
		rep := &reps[i]
		if best == nil || rep.Bandwidth > best.Bandwidth {
			best = rep
		}
	}
	return best
}

// downloadDASH parses the MPD at manifestURL, selects a representation, and
// writes the assembled media to outPath. init + segments are fetched concurrently
// then concatenated; SegmentBase single-file mode downloads the whole resource.
func downloadDASH(ctx context.Context, manifestURL string, opts DownloadOpts, outPath string) error {
	f := opts.Format
	client := opts.Client
	if client == nil {
		client = http.DefaultClient
	}
	body, err := downloadText(ctx, client, opts.Headers, manifestURL)
	if err != nil {
		return err
	}
	reps, err := parseMPD(body, manifestURL)
	if err != nil {
		return err
	}
	want := ""
	if f.VCodec != "" && f.ACodec == "" {
		want = "video"
	} else if f.ACodec != "" && f.VCodec == "" {
		want = "audio"
	}
	rep := pickRepresentation(reps, want, 0)
	if rep == nil {
		return fmt.Errorf("no DASH representation matched")
	}

	tmpDir, err := os.MkdirTemp("", "ytdldash-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	var parts []string // ordered file paths to concatenate

	// Initialization segment.
	if rep.InitURL != "" {
		dst := filepath.Join(tmpDir, "init")
		if err := downloadFile(ctx, client, opts.Headers, rep.InitURL, dst); err != nil {
			return fmt.Errorf("downloading init: %w", err)
		}
		parts = append(parts, dst)
	}

	// Media segments (or the single SegmentBase file).
	segURLs := rep.SegmentURLs
	if len(segURLs) == 0 && rep.MediaURL != "" {
		segURLs = []string{rep.MediaURL}
	}
	if len(segURLs) > 0 {
		concurrency := opts.ConcurrentFragments
		if concurrency < 1 {
			concurrency = 1
		}
		errs := make([]error, len(segURLs))
		sem := make(chan struct{}, concurrency)
		var wg sync.WaitGroup
		for i, u := range segURLs {
			wg.Add(1)
			go func(i int, u string) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				dst := filepath.Join(tmpDir, "seg"+strconv.Itoa(i))
				errs[i] = downloadFile(ctx, client, opts.Headers, u, dst)
			}(i, u)
		}
		wg.Wait()
		for _, e := range errs {
			if e != nil {
				return fmt.Errorf("segment download failed: %w", e)
			}
		}
		for i := range segURLs {
			parts = append(parts, filepath.Join(tmpDir, "seg"+strconv.Itoa(i)))
		}
	}

	out, err := createFile(outPath)
	if err != nil {
		return err
	}
	defer out.Close()
	for _, p := range parts {
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if _, err := out.Write(raw); err != nil {
			return err
		}
	}
	if opts.Progress != nil {
		opts.Progress(1, 1)
	}
	return nil
}
