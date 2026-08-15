package format

import (
	"testing"

	"yt-dlp-go/extractor"
)

// singleIDs returns the FormatIDs of a slice in order, for easy assertions.
func singleIDs(fs []extractor.Format) []string {
	out := make([]string, len(fs))
	for i, f := range fs {
		out[i] = f.FormatID
	}
	return out
}

func TestSort_ResDescending(t *testing.T) {
	fs := []extractor.Format{
		mk("1", "avc1", "mp4a", 360, 640, 500, 0, "mp4", "https"),
		mk("2", "avc1", "mp4a", 1080, 1920, 3000, 0, "mp4", "https"),
		mk("3", "avc1", "mp4a", 720, 1280, 1500, 0, "mp4", "https"),
	}
	got := singleIDs(Sort(fs, "res"))
	want := []string{"2", "3", "1"}
	if !equalStr(got, want) {
		t.Fatalf("res sort = %v, want %v", got, want)
	}
}

func TestSort_ResAscendingBang(t *testing.T) {
	fs := []extractor.Format{
		mk("1", "avc1", "mp4a", 360, 640, 500, 0, "mp4", "https"),
		mk("2", "avc1", "mp4a", 1080, 1920, 3000, 0, "mp4", "https"),
		mk("3", "avc1", "mp4a", 720, 1280, 1500, 0, "mp4", "https"),
	}
	got := singleIDs(Sort(fs, "!res"))
	want := []string{"1", "3", "2"}
	if !equalStr(got, want) {
		t.Fatalf("!res sort = %v, want %v", got, want)
	}
}

func TestSort_CodecPreference(t *testing.T) {
	fs := []extractor.Format{
		mk("avc", "avc1", "mp4a", 1080, 1920, 3000, 0, "mp4", "https"),
		mk("vp9", "vp9", "mp4a", 1080, 1920, 3000, 0, "webm", "https"),
		mk("vp92", "vp9.2", "mp4a", 1080, 1920, 3000, 0, "webm", "https"),
	}
	// Default yt-dlp order: prefer vp9.2, then vp9, then avc1.
	got := singleIDs(Sort(fs, "codec:vp9.2,codec:vp9"))
	want := []string{"vp92", "vp9", "avc"}
	if !equalStr(got, want) {
		t.Fatalf("codec sort = %v, want %v", got, want)
	}
}

func TestSort_TwoKeysResThenFps(t *testing.T) {
	fs := []extractor.Format{
		mk("a", "avc1", "mp4a", 1080, 1920, 3000, 0, "mp4", "https"), // 30fps
		mk("b", "avc1", "mp4a", 1080, 1920, 3000, 0, "mp4", "https"), // 60fps
		mk("c", "avc1", "mp4a", 720, 1280, 1500, 0, "mp4", "https"),  // lower res
	}
	fs[0].FPS = 30
	fs[1].FPS = 60
	got := singleIDs(Sort(fs, "res,fps"))
	want := []string{"b", "a", "c"} // 1080@60 first, then 1080@30, then 720
	if !equalStr(got, want) {
		t.Fatalf("res,fps sort = %v, want %v", got, want)
	}
}

func TestSort_StableOnTie(t *testing.T) {
	// All equal height -> original order preserved (stable sort).
	fs := []extractor.Format{
		mk("first", "avc1", "mp4a", 720, 1280, 1000, 0, "mp4", "https"),
		mk("second", "avc1", "mp4a", 720, 1280, 1000, 0, "mp4", "https"),
		mk("third", "avc1", "mp4a", 720, 1280, 1000, 0, "mp4", "https"),
	}
	got := singleIDs(Sort(fs, "res"))
	want := []string{"first", "second", "third"}
	if !equalStr(got, want) {
		t.Fatalf("stable sort = %v, want %v", got, want)
	}
}

func TestSort_EmptySpecIsNoop(t *testing.T) {
	fs := []extractor.Format{
		mk("1", "avc1", "mp4a", 360, 640, 500, 0, "mp4", "https"),
		mk("2", "avc1", "mp4a", 1080, 1920, 3000, 0, "mp4", "https"),
	}
	got := singleIDs(Sort(fs, "   "))
	if got[0] != "1" || got[1] != "2" {
		t.Fatalf("empty spec changed order: %v", got)
	}
}

func TestSelect_WithSortPicksTopRes(t *testing.T) {
	fs := []extractor.Format{
		mk("lo", "avc1", "none", 360, 640, 500, 0, "mp4", "https"),
		mk("hi", "avc1", "none", 1080, 1920, 3000, 0, "mp4", "https"),
		mk("mid", "avc1", "none", 720, 1280, 1500, 0, "mp4", "https"),
		mk("a", "none", "mp4a", 0, 0, 128, 0, "m4a", "https"),
	}
	g, err := Select(fs, "best", "res")
	if err != nil {
		t.Fatal(err)
	}
	if len(g) != 1 || len(g[0]) != 2 {
		t.Fatalf("expected one merged group of 2, got %v", idsOf(g))
	}
	if g[0][0].FormatID != "hi" {
		t.Errorf("best video under -S res = %q, want hi", g[0][0].FormatID)
	}
	if g[0][1].FormatID != "a" {
		t.Errorf("best audio = %q, want a", g[0][1].FormatID)
	}
}

func TestSelect_WithSortWorstLowestRes(t *testing.T) {
	fs := []extractor.Format{
		mk("lo", "avc1", "none", 360, 640, 500, 0, "mp4", "https"),
		mk("hi", "avc1", "none", 1080, 1920, 3000, 0, "mp4", "https"),
		mk("a", "none", "mp4a", 0, 0, 128, 0, "m4a", "https"),
	}
	g, err := Select(fs, "best", "!res")
	if err != nil {
		t.Fatal(err)
	}
	if g[0][0].FormatID != "lo" {
		t.Errorf("-S !res best video = %q, want lo", g[0][0].FormatID)
	}
}

func TestSelect_SortDoesNotAffectExplicitItag(t *testing.T) {
	fs := []extractor.Format{
		mk("137", "avc1", "none", 1080, 1920, 3000, 0, "mp4", "https"),
		mk("136", "avc1", "none", 720, 1280, 1500, 0, "mp4", "https"),
	}
	g, err := Select(fs, "136", "res")
	if err != nil {
		t.Fatal(err)
	}
	if g[0][0].FormatID != "136" {
		t.Errorf("explicit itag ignored by sort: got %q want 136", g[0][0].FormatID)
	}
}

func equalStr(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// idsOf flattens a group slice (each group is a slice of Formats).
func idsOf(groups [][]extractor.Format) []string {
	var out []string
	for _, g := range groups {
		for _, f := range g {
			out = append(out, f.FormatID)
		}
	}
	return out
}
