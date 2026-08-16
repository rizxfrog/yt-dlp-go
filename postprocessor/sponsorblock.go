package postprocessor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"yt-dlp-go/extractor"
	"yt-dlp-go/options"
)

// SponsorBlockRemove cuts sponsor segments out of a media file. It mirrors
// yt-dlp's ModifyChaptersPP.remove_chapters: the segments to remove are turned
// into a list of "keep" ranges, serialised as an ffconcat spec, and fed to
// ffmpeg's concat demuxer with stream copy (-c copy). Cuts snap to the nearest
// keyframe (exact cuts would require re-encoding / forced keyframes).
type SponsorBlockRemove struct {
	FFmpeg   string
	Segments []extractor.Chapter // ranges to remove (StartTime/EndTime), sorted
	Duration float64             // media duration in seconds
}

func (p SponsorBlockRemove) Name() string { return "SponsorBlockRemove" }

func (p SponsorBlockRemove) Process(input string, opts *options.Options) (string, error) {
	if len(p.Segments) == 0 {
		return input, nil
	}
	ff := p.FFmpeg
	if ff == "" {
		var err error
		if ff, err = FindFFmpeg(opts); err != nil {
			return input, err
		}
	}
	ranges := keepRanges(p.Segments, p.Duration)
	if len(ranges) == 0 {
		return input, nil
	}
	// The concat demuxer resolves relative file paths against the concat file's
	// directory, so reference the input by absolute path.
	abs, err := filepath.Abs(input)
	if err != nil {
		return input, err
	}
	concatPath, err := writeConcatSpec(abs, ranges)
	if err != nil {
		return input, err
	}
	defer os.Remove(concatPath)

	// Keep the original extension so ffmpeg infers the right muxer.
	out := strings.TrimSuffix(abs, filepath.Ext(abs)) + ".temp" + filepath.Ext(abs)
	args := []string{
		"-hide_banner", "-nostdin",
		"-f", "concat", "-safe", "0", "-i", concatPath,
		"-c", "copy", out,
	}
	if err := Exec(ff, args...); err != nil {
		return input, err
	}
	if err := renameReplace(out, input); err != nil {
		return input, err
	}
	return input, nil
}

// keepRange is one contiguous interval to keep, expressed as optional inpoint /
// outpoint directives for the concat demuxer.
type keepRange struct {
	hasIn, hasOut bool
	in, out       float64
}

// keepRanges converts sorted remove-segments into the intervals to keep,
// reproducing yt-dlp's _make_concat_opts.
func keepRanges(segments []extractor.Chapter, duration float64) []keepRange {
	ranges := []keepRange{{}} // first keep-range starts at 0
	for _, s := range segments {
		if s.StartTime <= 0 {
			// Segment starts at 0: keep from its end.
			ranges[len(ranges)-1] = keepRange{hasIn: true, in: s.EndTime}
			continue
		}
		cur := ranges[len(ranges)-1]
		cur.hasOut = true
		cur.out = s.StartTime
		ranges[len(ranges)-1] = cur
		if s.EndTime < duration {
			// Non-final segment: open a new keep-range at its end.
			ranges = append(ranges, keepRange{hasIn: true, in: s.EndTime})
		}
	}
	return ranges
}

// writeConcatSpec serialises an ffconcat file referencing input once per keep
// range, with the appropriate inpoint/outpoint directives.
func writeConcatSpec(input string, ranges []keepRange) (string, error) {
	var b strings.Builder
	b.WriteString("ffconcat version 1.0\n")
	for _, r := range ranges {
		b.WriteString("file '" + escapeConcatPath(input) + "'\n")
		if r.hasIn {
			b.WriteString(fmt.Sprintf("inpoint %.6f\n", r.in))
		}
		if r.hasOut {
			b.WriteString(fmt.Sprintf("outpoint %.6f\n", r.out))
		}
	}
	f, err := os.CreateTemp("", "ytdlp-cut-*.concat")
	if err != nil {
		return "", err
	}
	if _, err := f.WriteString(b.String()); err != nil {
		f.Close()
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	return f.Name(), nil
}

// escapeConcatPath escapes single quotes inside an ffconcat file path.
func escapeConcatPath(s string) string {
	return strings.ReplaceAll(s, "'", `'\''`)
}
