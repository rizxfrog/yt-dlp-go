package youtube

import (
	"regexp"
	"sort"
	"strconv"
	"strings"

	"yt-dlp-go/extractor"
)

// chapterLineRE matches a timestamped chapter line inside a video description,
// e.g. "0:00 intro" or "1:02:03 Some chapter" (leading/trailing spaces allowed).
var chapterLineRE = regexp.MustCompile(`(?m)^\s*((?:\d{1,2}:){1,2}\d{1,2})\s+(.+?)\s*$`)

// parseChaptersFromDescription extracts chapters from a YouTube description's
// "MM:SS Title" / "HH:MM:SS Title" timestamp lines. Each chapter's EndTime is
// set to the next chapter's StartTime; the last one stays open-ended (0).
// Chapters are returned sorted by start time with consecutive duplicates of the
// same timestamp dropped.
func parseChaptersFromDescription(desc string) []extractor.Chapter {
	matches := chapterLineRE.FindAllStringSubmatch(desc, -1)
	if len(matches) == 0 {
		return nil
	}
	chapters := make([]extractor.Chapter, 0, len(matches))
	for _, m := range matches {
		title := strings.TrimSpace(m[2])
		if title == "" {
			continue
		}
		chapters = append(chapters, extractor.Chapter{
			Title:     title,
			StartTime: parseTimestamp(m[1]),
		})
	}
	if len(chapters) == 0 {
		return nil
	}
	sort.SliceStable(chapters, func(i, j int) bool {
		return chapters[i].StartTime < chapters[j].StartTime
	})
	// Drop consecutive entries that share a start time (description artefacts).
	out := chapters[:0]
	for i, c := range chapters {
		if i > 0 && c.StartTime == chapters[i-1].StartTime {
			continue
		}
		out = append(out, c)
	}
	for i := 0; i < len(out); i++ {
		if i+1 < len(out) {
			out[i].EndTime = out[i+1].StartTime
		}
	}
	return out
}

// parseTimestamp converts "MM:SS" or "HH:MM:SS" (each part possibly fractional)
// into seconds.
func parseTimestamp(s string) float64 {
	parts := strings.Split(strings.TrimSpace(s), ":")
	var sec float64
	for _, p := range parts {
		n, err := strconv.ParseFloat(strings.TrimSpace(p), 64)
		if err != nil {
			return 0
		}
		sec = sec*60 + n
	}
	return sec
}
