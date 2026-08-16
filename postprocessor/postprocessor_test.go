package postprocessor

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestExtractAudio_Args(t *testing.T) {
	p := FFmpegExtractAudio{AudioFormat: "mp3", AudioQuality: "320"}
	got := p.Args("video.mkv")
	want := []string{"-y", "-i", "video.mkv", "-vn", "-acodec", "mp3", "-b:a", "320k", "video.mp3"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("mp3 args = %v want %v", got, want)
	}

	p2 := FFmpegExtractAudio{AudioFormat: "opus"}
	got2 := p2.Args("video.mp4")
	want2 := []string{"-y", "-i", "video.mp4", "-vn", "-acodec", "libopus", "video.opus"}
	if !reflect.DeepEqual(got2, want2) {
		t.Fatalf("opus args = %v want %v", got2, want2)
	}
}

func TestExtractAudio_OutputPath(t *testing.T) {
	p := FFmpegExtractAudio{AudioFormat: "m4a"}
	got := p.OutputPath("/a/b/video.webm")
	if filepath.Base(got) != "video.m4a" {
		t.Fatalf("output path base = %q want video.m4a", filepath.Base(got))
	}
}

func TestVideoRemux_Args(t *testing.T) {
	p := FFmpegVideoRemux{RemuxFormat: "mp4"}
	got := p.Args("clip.mkv")
	want := []string{"-y", "-i", "clip.mkv", "-c", "copy", "clip.mp4"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("remux args = %v want %v", got, want)
	}
}

func TestMetadata_Args(t *testing.T) {
	p := FFmpegMetadata{Title: "Hello", Artist: "Me", Date: "20240115"}
	got := p.Args("v.mp4")
	// Last element is the temp output; check key flags are present.
	joined := ""
	for _, a := range got {
		joined += a + " "
	}
	for _, want := range []string{"-metadata", "title=Hello", "artist=Me", "date=20240115", "-c", "copy"} {
		if !contains(got, want) {
			t.Fatalf("metadata args %v missing %q (joined: %s)", got, want, joined)
		}
	}
}

func TestMetadata_StatsArgs(t *testing.T) {
	p := FFmpegMetadata{ViewCount: 100, LikeCount: 50, CommentCount: 20, RepostCount: 5}
	got := p.Args("v.mkv")
	if last := got[len(got)-1]; last != "v.meta.tmp.mkv" {
		t.Errorf("output = %q, want v.meta.tmp.mkv", last)
	}
	for _, want := range []string{"view_count=100", "like_count=50", "comment_count=20", "repost_count=5"} {
		if !contains(got, want) {
			t.Errorf("metadata args missing %q: %v", want, got)
		}
	}
}

func TestEmbedSubtitle_Args(t *testing.T) {
	p := FFmpegEmbedSubtitle{SubtitleFile: "sub.srt", OutputFormat: "mkv"}
	got := p.Args("v.mp4")
	last := got[len(got)-1]
	if last != "v.mkv" {
		t.Fatalf("embed output = %q want v.mkv", last)
	}
	if !contains(got, "-map") || !contains(got, "sub.srt") {
		t.Fatalf("embed args %v missing map/subtitle", got)
	}
}

func TestSubtitlesConvertor_Args(t *testing.T) {
	p := FFmpegSubtitlesConvertor{OutputExt: "vtt"}
	got := p.Args("sub.srt")
	if got[len(got)-1] != "sub.vtt" {
		t.Fatalf("convert output = %q want sub.vtt", got[len(got)-1])
	}
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
