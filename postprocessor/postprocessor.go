// Package postprocessor wraps external tools (primarily ffmpeg) that transform
// downloaded media: merging separate video+audio streams, remuxing raw HLS/DASH
// containers into a playable file, and embedding metadata.
//
// yt-dlp relies on ffmpeg as a subprocess; so does this engine. The Go code
// only orchestrates the command line. If ffmpeg is absent, the engine still
// produces the raw downloaded file and reports the limitation.
package postprocessor

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"yt-dlp-go/options"
)

// PostProcessor transforms a downloaded file, returning the new path.
type PostProcessor interface {
	Name() string
	Process(input string, opts *options.Options) (string, error)
}

// FindFFmpeg locates the ffmpeg binary using --ffmpeg-location, then PATH.
func FindFFmpeg(opts *options.Options) (string, error) {
	candidates := []string{}
	if opts != nil && opts.FFmpegLocation != "" {
		candidates = append(candidates,
			filepath.Join(opts.FFmpegLocation, "ffmpeg"),
			filepath.Join(opts.FFmpegLocation, "bin", "ffmpeg"),
		)
	}
	candidates = append(candidates, "ffmpeg")
	for _, c := range candidates {
		if p, err := exec.LookPath(c); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("ffmpeg not found (set --ffmpeg-location or install ffmpeg)")
}

// Exec runs ffmpeg with the given arguments, streaming output to stderr.
func Exec(ffmpeg string, args ...string) error {
	cmd := exec.Command(ffmpeg, args...)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ffmpeg failed: %w", err)
	}
	return nil
}

// Merge combines separate video and audio inputs into one file. When audio is
// empty only the video is remuxed into the requested container.
func Merge(video, audio, output, container, ffmpeg string) error {
	if container == "" {
		container = "mkv"
	}
	args := []string{"-y", "-i", video}
	if audio != "" {
		args = append(args, "-i", audio)
	}
	args = append(args, "-c", "copy", "-f", container, output)
	return Exec(ffmpeg, args...)
}

// Remux rewrites input into output (used for fMP4/TS segment sets).
func Remux(input, output, container, ffmpeg string) error {
	if container == "" {
		container = "mp4"
	}
	return Exec(ffmpeg, "-y", "-i", input, "-c", "copy", "-f", container, output)
}

// renameReplace atomically replaces dst with the contents of src.
func renameReplace(src, dst string) error {
	return os.Rename(src, dst)
}

// MergePP is a PostProcessor that merges a sibling audio file named
// "<base>.video.<ext>" and "<base>.audio.<ext>" into "<base>.<container>".
type MergePP struct {
	FFmpeg   string
	Container string
}

func (p MergePP) Name() string { return "FFmpegMerger" }

func (p MergePP) Process(input string, opts *options.Options) (string, error) {
	dir := filepath.Dir(input)
	base := strings.TrimSuffix(filepath.Base(input), filepath.Ext(input))
	video := filepath.Join(dir, base+".video"+filepath.Ext(input))
	audio := filepath.Join(dir, base+".audio"+filepath.Ext(input))
	if _, err := os.Stat(video); err != nil {
		// No separate streams; nothing to merge.
		return input, nil
	}
	container := p.Container
	if opts != nil && opts.MergeOutputFormat != "" {
		container = opts.MergeOutputFormat
	}
	out := filepath.Join(dir, base+"."+container)
	if err := Merge(video, audio, out, container, p.FFmpeg); err != nil {
		return input, err
	}
	_ = os.Remove(video)
	_ = os.Remove(audio)
	return out, nil
}
