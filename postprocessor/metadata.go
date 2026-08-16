package postprocessor

import (
	"path/filepath"
	"strconv"
	"strings"

	"yt-dlp-go/options"
)

// FFmpegMetadata writes title/artist/date/description and (optionally) an
// embedded thumbnail into the media file, plus engagement statistics.
type FFmpegMetadata struct {
	FFmpeg       string
	Title        string
	Artist       string
	Date         string // YYYYMMDD
	Description  string
	Thumbnail    string // path to a thumbnail image to embed
	ViewCount    int64
	LikeCount    int64
	CommentCount int64
	RepostCount  int64
}

func (p FFmpegMetadata) Name() string { return "FFmpegMetadata" }

// Args builds the ffmpeg argument list (testable without ffmpeg present).
// input is rewritten in place.
func (p FFmpegMetadata) Args(input string) []string {
	args := []string{"-y", "-i", input}
	if p.Thumbnail != "" {
		args = append(args, "-i", p.Thumbnail, "-map", "0", "-map", "1")
	} else {
		// noop map to keep stream selection explicit
		args = append(args, "-map", "0")
	}
	if p.Title != "" {
		args = append(args, "-metadata", "title="+p.Title)
	}
	if p.Artist != "" {
		args = append(args, "-metadata", "artist="+p.Artist)
	}
	if p.Date != "" {
		args = append(args, "-metadata", "date="+p.Date)
	}
	if p.Description != "" {
		args = append(args, "-metadata", "description="+p.Description)
	}
	if p.ViewCount > 0 {
		args = append(args, "-metadata", "view_count="+strconv.FormatInt(p.ViewCount, 10))
	}
	if p.LikeCount > 0 {
		args = append(args, "-metadata", "like_count="+strconv.FormatInt(p.LikeCount, 10))
	}
	if p.CommentCount > 0 {
		args = append(args, "-metadata", "comment_count="+strconv.FormatInt(p.CommentCount, 10))
	}
	if p.RepostCount > 0 {
		args = append(args, "-metadata", "repost_count="+strconv.FormatInt(p.RepostCount, 10))
	}
	if p.Thumbnail != "" {
		args = append(args, "-c", "copy", "-disposition:v:1", "attached_pic")
	} else {
		args = append(args, "-c", "copy")
	}
	args = append(args, metaTmpPath(input))
	return args
}

// metaTmpPath returns a temp path that keeps the original extension so ffmpeg
// can infer the muxer (e.g. in.mkv → in.meta.tmp.mkv).
func metaTmpPath(input string) string {
	return strings.TrimSuffix(input, filepath.Ext(input)) + ".meta.tmp" + filepath.Ext(input)
}

func (p FFmpegMetadata) Process(input string, opts *options.Options) (string, error) {
	ff := p.FFmpeg
	if ff == "" {
		var err error
		if ff, err = FindFFmpeg(opts); err != nil {
			return input, err
		}
	}
	// ffmpeg cannot rewrite a file in place; write to a temp then replace.
	tmp := metaTmpPath(input)
	args := p.Args(input)
	// Args already appends the temp output as the last element.
	if err := Exec(ff, args...); err != nil {
		return input, err
	}
	if err := renameReplace(tmp, input); err != nil {
		return input, err
	}
	return input, nil
}

// FFmpegEmbedSubtitle muxes an external subtitle file into the video container.
type FFmpegEmbedSubtitle struct {
	FFmpeg       string
	SubtitleFile string
	OutputFormat string // mkv | mp4 | ...
}

func (p FFmpegEmbedSubtitle) Name() string { return "FFmpegEmbedSubtitle" }

func (p FFmpegEmbedSubtitle) Args(input string) []string {
	ext := p.OutputFormat
	if ext == "" {
		ext = "mkv"
	}
	out := replaceExt(input, ext)
	return []string{"-y", "-i", input, "-i", p.SubtitleFile, "-c", "copy", "-map", "0", "-map", "1", out}
}

func (p FFmpegEmbedSubtitle) Process(input string, opts *options.Options) (string, error) {
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
	return p.Args(input)[len(p.Args(input))-1], nil
}

// FFmpegSubtitlesConvertor transcodes a subtitle file between formats.
type FFmpegSubtitlesConvertor struct {
	FFmpeg    string
	OutputExt string // srt | vtt | ass
}

func (p FFmpegSubtitlesConvertor) Name() string { return "FFmpegSubtitlesConvertor" }

func (p FFmpegSubtitlesConvertor) Args(input string) []string {
	out := replaceExt(input, p.OutputExt)
	return []string{"-y", "-i", input, out}
}

func (p FFmpegSubtitlesConvertor) Process(input string, opts *options.Options) (string, error) {
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
	return p.Args(input)[len(p.Args(input))-1], nil
}
