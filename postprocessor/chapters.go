package postprocessor

import (
	"fmt"
	"os"
	"strings"

	"yt-dlp-go/extractor"
	"yt-dlp-go/options"
)

// FFmpegEmbedChapters writes chapter markers into a media file using ffmpeg's
// FFMETADATA input. ffmpeg cannot rewrite a file in place, so it streams to a
// temp file which is then atomically renamed over the input.
type FFmpegEmbedChapters struct {
	FFmpeg   string
	Chapters []extractor.Chapter
	Duration float64 // media duration in seconds; fills the last chapter's END
}

func (p FFmpegEmbedChapters) Name() string { return "FFmpegEmbedChapters" }

// Args builds the ffmpeg argument list. metaFile is a pre-written FFMETADATA
// file (see WriteFFMetadataFile). Pure so it is testable without ffmpeg.
func (p FFmpegEmbedChapters) Args(input, metaFile string) []string {
	return []string{
		"-y", "-i", input,
		"-i", metaFile,
		"-map", "0",
		"-map_metadata", "1",
		"-c", "copy",
		input + ".chap.tmp",
	}
}

func (p FFmpegEmbedChapters) Process(input string, opts *options.Options) (string, error) {
	if len(p.Chapters) == 0 {
		return input, nil
	}
	ff := p.FFmpeg
	if ff == "" {
		var err error
		if ff, err = FindFFmpeg(opts); err != nil {
			return input, err
		}
	}
	meta, err := WriteFFMetadataFile(p.Chapters, p.Duration)
	if err != nil {
		return input, err
	}
	defer os.Remove(meta)

	tmp := input + ".chap.tmp"
	if err := Exec(ff, p.Args(input, meta)...); err != nil {
		return input, err
	}
	if err := renameReplace(tmp, input); err != nil {
		return input, err
	}
	return input, nil
}

// WriteFFMetadataFile serialises chapters into an FFMETADATA text file and
// returns its path. TIMEBASE is 1/1000 (millisecond timestamps). A chapter with
// a zero EndTime is open-ended; it is closed at `duration` (when known) so
// ffmpeg does not complain about a missing END.
func WriteFFMetadataFile(chapters []extractor.Chapter, duration float64) (string, error) {
	var b strings.Builder
	b.WriteString(";FFMETADATA1\n")
	for _, c := range chapters {
		b.WriteString("[CHAPTER]\n")
		b.WriteString("TIMEBASE=1/1000\n")
		b.WriteString(fmt.Sprintf("START=%d\n", int64(c.StartTime*1000)))
		end := c.EndTime
		if end <= 0 {
			end = duration
		}
		if end > 0 {
			b.WriteString(fmt.Sprintf("END=%d\n", int64(end*1000)))
		}
		b.WriteString("title=" + ffMetadataEscape(c.Title) + "\n")
	}
	f, err := os.CreateTemp("", "ytdlp-chapters-*.ffmeta")
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

// ffMetadataEscape escapes a metadata value per the FFMETADATA spec: backslash,
// equals, semicolon and hash must be escaped, and newlines folded to spaces.
func ffMetadataEscape(s string) string {
	r := strings.NewReplacer(
		"\\", "\\\\",
		"=", "\\=",
		";", "\\;",
		"#", "\\#",
		"\n", " ",
		"\r", " ",
	)
	return r.Replace(s)
}
