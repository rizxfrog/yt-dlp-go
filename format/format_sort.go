// Package format implements yt-dlp's -f / -S selection grammar.
//
// This file implements --format-sort (-S): a multi-key stable sort of the
// format list. The sorted order is then honoured by Select so that the "best" /
// "worst" / "bestvideo" / "bestaudio" aliases resolve to the top (or bottom)
// format under that ordering, exactly like yt-dlp.

package format

import (
	"math"
	"sort"
	"strconv"
	"strings"

	"yt-dlp-go/extractor"
)

// sortKey describes one comma-separated sort criterion, e.g. "res",
// "!fps", "-tbr", "codec:vp9.2". dir==true means descending (higher ranks
// first); the modifiers +/- force a direction and ! flips the default.
type sortKey struct {
	field string
	value string // for codec:vp9.2 style preference keys
	dir   bool   // true = descending
}

// parseSortSpec parses a -S value into ordered criteria. Unknown fields are
// silently ignored (they contribute no ordering, leaving stable order intact).
func parseSortSpec(spec string) []sortKey {
	var out []sortKey
	for _, raw := range strings.Split(spec, ",") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		key := sortKey{}
		// Modifiers: leading '!' reverses; '+'/'-' force direction.
		reverse := false
		for strings.HasPrefix(raw, "!") {
			reverse = !reverse
			raw = raw[1:]
		}
		switch {
		case strings.HasPrefix(raw, "+"):
			raw = raw[1:]
			key.dir = false // ascending
		case strings.HasPrefix(raw, "-"):
			raw = raw[1:]
			key.dir = true // descending
		default:
			key.dir = defaultDesc(raw)
		}
		if reverse {
			key.dir = !key.dir
		}
		// Optional ":value" preference (codec:vp9.2, lang:en, ...).
		if i := strings.Index(raw, ":"); i > 0 {
			key.field = strings.ToLower(strings.TrimSpace(raw[:i]))
			key.value = strings.TrimSpace(raw[i+1:])
		} else {
			key.field = strings.ToLower(raw)
		}
		if _, ok := knownSortField[key.field]; !ok {
			continue // skip unknown field
		}
		out = append(out, key)
	}
	return out
}

// defaultDesc reports the default sort direction for a field: true = higher is
// better (descending), false = lower is better / alphabetical (ascending).
func defaultDesc(field string) bool {
	switch field {
	case "id", "format_id":
		return false // ascending
	default:
		return true
	}
}

var knownSortField = map[string]bool{
	"res": true, "height": true, "width": true, "fps": true,
	"tbr": true, "br": true, "vbr": true, "abr": true,
	"size": true, "filesize": true, "asr": true, "channels": true,
	"quality": true, "has_video": true, "has_audio": true,
	"pref": true, "preference": true,
	"proto": true, "protocol": true, "source": true, "ext": true,
	"vcodec": true, "acodec": true, "codec": true,
	"dynamic_range": true, "hdr": true, "dr": true,
	"lang": true, "id": true, "format_id": true,
}

// Sort returns a new slice of formats ordered by the -S spec. It is a stable
// sort so ties preserve the original (extractor) order. An empty/whitespace
// spec returns a shallow copy of the input unchanged.
func Sort(formats []extractor.Format, spec string) []extractor.Format {
	keys := parseSortSpec(spec)
	if len(keys) == 0 {
		out := make([]extractor.Format, len(formats))
		copy(out, formats)
		return out
	}
	out := make([]extractor.Format, len(formats))
	copy(out, formats)
	sort.SliceStable(out, func(i, j int) bool {
		for _, k := range keys {
			si, sj := scoreOf(k, out[i]), scoreOf(k, out[j])
			if si == sj {
				continue
			}
			if k.dir {
				return si > sj // descending
			}
			return si < sj // ascending
		}
		return false // all equal -> keep stable order
	})
	return out
}

// scoreOf computes a numeric score for a format under one sort key. Higher is
// "better" only relative to the same key; the direction is applied by the caller.
func scoreOf(k sortKey, f extractor.Format) float64 {
	switch k.field {
	case "res", "height":
		return float64(f.Height)
	case "width":
		return float64(f.Width)
	case "fps":
		return f.FPS
	case "tbr", "br":
		return f.TBR
	case "vbr":
		return f.VBR
	case "abr":
		return f.ABR
	case "size", "filesize":
		return float64(f.Filesize)
	case "asr":
		return float64(f.AudioSampleRate)
	case "channels":
		return float64(f.AudioChannels)
	case "quality":
		return float64(qualityRank(f))
	case "has_video":
		if hasVideo(f) {
			return 1
		}
		return 0
	case "has_audio":
		if hasAudio(f) {
			return 1
		}
		return 0
	case "pref", "preference":
		return float64(f.Preference)
	case "proto", "protocol":
		return float64(protoRank(f.Protocol))
	case "source":
		return float64(sourceRank(f))
	case "ext":
		return float64(extRank(f.Ext))
	case "vcodec":
		return codecScore(f.VCodec, k.value)
	case "acodec":
		return codecScore(f.ACodec, k.value)
	case "codec":
		return math.Max(codecScore(f.VCodec, k.value), codecScore(f.ACodec, k.value))
	case "dynamic_range", "hdr", "dr":
		return float64(hdrRank(f.DynamicRange))
	case "lang":
		return langScore(f.Language, k.value)
	case "id", "format_id":
		return idScore(f.FormatID)
	}
	return 0
}

func hasVideo(f extractor.Format) bool {
	return f.VCodec != "" && !strings.EqualFold(f.VCodec, "none")
}
func hasAudio(f extractor.Format) bool {
	return f.ACodec != "" && !strings.EqualFold(f.ACodec, "none")
}

// qualityRank mirrors yt-dlp's "quality" pseudo-field: combined streams beat
// separate ones.
func qualityRank(f extractor.Format) int {
	v, a := hasVideo(f), hasAudio(f)
	switch {
	case v && a:
		return 3
	case v:
		return 2
	case a:
		return 1
	default:
		return 0
	}
}

// protoRank: https > http > m3u8/dash > ftp > rtmp > other.
func protoRank(p string) int {
	switch strings.ToLower(p) {
	case "https":
		return 5
	case "http":
		return 4
	case "m3u8", "m3u8_native", "dash":
		return 3
	case "ftp":
		return 2
	case "rtmp", "rtmps":
		return 1
	default:
		return 0
	}
}

// sourceRank derives provenance from the Source field, falling back to Protocol.
func sourceRank(f extractor.Format) int {
	s := strings.ToLower(f.Source)
	if s == "" {
		s = strings.ToLower(f.Protocol)
	}
	switch {
	case strings.Contains(s, "web"):
		return 5
	case strings.Contains(s, "dash"):
		return 4
	case strings.Contains(s, "hls"), strings.Contains(s, "m3u8"):
		return 3
	case strings.Contains(s, "http"):
		return 2
	default:
		return 0
	}
}

// extRank prefers container formats that survive remux/merge well.
func extRank(e string) int {
	switch strings.ToLower(e) {
	case "mkv":
		return 6
	case "mp4":
		return 5
	case "webm":
		return 4
	case "mov":
		return 3
	case "m4a", "mp3", "aac", "opus", "flac", "wav", "ogg":
		return 2
	default:
		return 1
	}
}

// codecScore ranks a single codec string. With a preference value it boosts
// formats whose codec matches it; without one it uses a codec-quality ladder.
func codecScore(codec, pref string) float64 {
	c := strings.ToLower(codec)
	if pref != "" {
		if codecMatches(c, strings.ToLower(pref)) {
			return 1000
		}
		return 0
	}
	switch {
	case strings.HasPrefix(c, "av01"):
		return 5
	case strings.Contains(c, "vp9.2"):
		return 4
	case strings.Contains(c, "vp9"):
		return 3
	case strings.HasPrefix(c, "avc"), strings.HasPrefix(c, "h264"):
		return 2
	case strings.HasPrefix(c, "hev"), strings.HasPrefix(c, "h265"):
		return 2
	case c == "" || c == "none":
		return 0
	default:
		return 1
	}
}

// codecMatches reports whether codec c satisfies the preference p. A trailing
// ".N" / "-N" in p is treated as a prefix so "vp9" matches both "vp9" and
// "vp9.2", while "vp9.2" only matches "vp9.2".
func codecMatches(c, p string) bool {
	if c == "" || c == "none" {
		return false
	}
	if c == p {
		return true
	}
	return strings.HasPrefix(c, p+".") || strings.HasPrefix(c, p+"-")
}

// hdrRank orders dynamic-range labels.
func hdrRank(d string) int {
	switch strings.ToUpper(d) {
	case "DOLBY_VISION", "DOLBYVISION":
		return 4
	case "HDR10+":
		return 3
	case "HDR10", "HDR":
		return 2
	case "HLG":
		return 1
	default:
		return 0
	}
}

// langScore boosts a matching language tag when a preference is given.
func langScore(lang, pref string) float64 {
	if pref == "" {
		return 0
	}
	if strings.EqualFold(lang, pref) {
		return 1000
	}
	// prefix match (e.g. "zh" matches "zh-Hans")
	if strings.HasPrefix(strings.ToLower(lang), strings.ToLower(pref)) {
		return 500
	}
	return 0
}

// idScore parses a numeric format id; non-numeric ids compare as a hash so the
// sort stays deterministic but is effectively a tie (stable order preserved).
func idScore(id string) float64 {
	if n, err := strconv.Atoi(id); err == nil {
		return float64(n)
	}
	var h float64
	for i := 0; i < len(id); i++ {
		h = h*31 + float64(id[i])
	}
	return h
}
