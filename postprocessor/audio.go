package postprocessor

import (
	"path/filepath"
	"strings"

	"yt-dlp-go/options"
)

// FFmpegExtractAudio re-encodes a media file to a standalone audio track.
type FFmpegExtractAudio struct {
	FFmpeg       string
	AudioFormat  string // mp3 | aac | m4a | opus | flac | wav | best | copy
	AudioQuality string // e.g. "320" (kbps) or "5" (VBR index)
}

func (p FFmpegExtractAudio) Name() string { return "FFmpegExtractAudio" }

// OutputPath returns the destination filename for the extracted audio.
func (p FFmpegExtractAudio) OutputPath(input string) string {
	ext := p.AudioFormat
	if ext == "" || ext == "best" {
		ext = "m4a"
	}
	return replaceExt(input, ext)
}

// Args builds the ffmpeg argument list (testable without ffmpeg present).
func (p FFmpegExtractAudio) Args(input string) []string {
	out := p.OutputPath(input)
	args := []string{"-y", "-i", input, "-vn"}
	switch strings.ToLower(p.AudioFormat) {
	case "mp3":
		args = append(args, "-acodec", "mp3")
	case "aac", "m4a":
		args = append(args, "-acodec", "aac")
	case "opus":
		args = append(args, "-acodec", "libopus")
	case "flac":
		args = append(args, "-acodec", "flac")
	case "wav":
		args = append(args, "-acodec", "pcm_s16le")
	default:
		args = append(args, "-acodec", "copy")
	}
	if q := strings.TrimSpace(p.AudioQuality); q != "" {
		if isDigits(q) {
			args = append(args, "-b:a", q+"k")
		} else {
			args = append(args, "-q:a", q)
		}
	}
	args = append(args, out)
	return args
}

func (p FFmpegExtractAudio) Process(input string, opts *options.Options) (string, error) {
	ff := p.FFmpeg
	if ff == "" {
		var err error
		if ff, err = FindFFmpeg(opts); err != nil {
			return input, err
		}
	}
	if err := Exec(ff, p.Args(input)...); err != nil {
		return input, err
	}
	return p.OutputPath(input), nil
}

// FFmpegVideoRemux rewrites a file into another container without re-encoding.
type FFmpegVideoRemux struct {
	FFmpeg      string
	RemuxFormat string // mp4 | mkv | ...
}

func (p FFmpegVideoRemux) Name() string { return "FFmpegVideoRemux" }

func (p FFmpegVideoRemux) OutputPath(input string) string {
	ext := p.RemuxFormat
	if ext == "" {
		ext = "mp4"
	}
	return replaceExt(input, ext)
}

func (p FFmpegVideoRemux) Args(input string) []string {
	out := p.OutputPath(input)
	return []string{"-y", "-i", input, "-c", "copy", out}
}

func (p FFmpegVideoRemux) Process(input string, opts *options.Options) (string, error) {
	ff := p.FFmpeg
	if ff == "" {
		var err error
		if ff, err = FindFFmpeg(opts); err != nil {
			return input, err
		}
	}
	if err := Exec(ff, p.Args(input)...); err != nil {
		return input, err
	}
	return p.OutputPath(input), nil
}

func replaceExt(path, ext string) string {
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	if e := filepath.Ext(base); e != "" {
		base = base[:len(base)-len(e)]
	}
	return filepath.Join(dir, base+"."+ext)
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
