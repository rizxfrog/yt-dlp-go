package postprocessor

import (
	"os"
	"strings"
	"testing"

	"yt-dlp-go/extractor"
)

func TestFFMetadataEscape(t *testing.T) {
	cases := map[string]string{
		"plain":        "plain",
		"a=b":          `a\=b`,
		"a;b":          `a\;b`,
		"a#b":          `a\#b`,
		`a\b`:          `a\\b`,
		"line1\nline2": "line1 line2",
	}
	for in, want := range cases {
		if got := ffMetadataEscape(in); got != want {
			t.Errorf("ffMetadataEscape(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestWriteFFMetadataFile(t *testing.T) {
	chapters := []extractor.Chapter{
		{Title: "intro", StartTime: 0, EndTime: 10},
		{Title: "main", StartTime: 10},
	}
	path, err := WriteFFMetadataFile(chapters, 120)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(path)

	b, _ := os.ReadFile(path)
	s := string(b)
	for _, want := range []string{
		";FFMETADATA1",
		"[CHAPTER]",
		"TIMEBASE=1/1000",
		"START=0",
		"END=10000",
		"title=intro",
		"START=10000",
		"title=main",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("metadata missing %q in:\n%s", want, s)
		}
	}
	// The open-ended chapter must be closed at duration (120s → 120000ms).
	if !strings.Contains(s, "END=120000") {
		t.Errorf("open-ended chapter should be closed at duration:\n%s", s)
	}
}

func TestWriteFFMetadataFile_NoDuration(t *testing.T) {
	path, err := WriteFFMetadataFile([]extractor.Chapter{{Title: "x", StartTime: 1}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(path)
	b, _ := os.ReadFile(path)
	if strings.Contains(string(b), "END=") {
		t.Errorf("expected no END line, got:\n%s", string(b))
	}
}

func TestFFmpegEmbedChapters_Args(t *testing.T) {
	pp := FFmpegEmbedChapters{Chapters: []extractor.Chapter{{Title: "x", StartTime: 1, EndTime: 2}}}
	args := pp.Args("in.mp4", "meta.ffmeta")
	joined := strings.Join(args, " ")
	for _, want := range []string{"-i in.mp4", "-i meta.ffmeta", "-map 0", "-map_metadata 1", "-c copy", "in.mp4.chap.tmp"} {
		if !strings.Contains(joined, want) {
			t.Errorf("args missing %q: %v", want, args)
		}
	}
}
