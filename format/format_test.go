package format

import (
	"reflect"
	"testing"

	"yt-dlp-go/extractor"
)

func mk(id, vcodec, acodec string, h, w int, tbr float64, fs int64, ext, proto string) extractor.Format {
	return extractor.Format{
		FormatID: id, VCodec: vcodec, ACodec: acodec,
		Height: h, Width: w, TBR: tbr, Filesize: fs, Ext: ext, Protocol: proto,
	}
}

// A realistic mixed set: a combined progressive, a 1080p video-only, a 720p
// video-only, and several audio-only formats.
func sampleFormats() []extractor.Format {
	return []extractor.Format{
		mk("18", "avc1", "mp4a", 360, 640, 700, 10_000_000, "mp4", "http"),
		mk("137", "avc1", "", 1080, 1920, 2500, 40_000_000, "mp4", "http"),
		mk("248", "vp9", "", 1080, 1920, 1800, 35_000_000, "webm", "http"),
		mk("136", "avc1", "", 720, 1280, 1200, 25_000_000, "mp4", "http"),
		mk("140", "", "mp4a", 0, 0, 128, 3_000_000, "m4a", "http"),
		mk("251", "", "opus", 0, 0, 160, 4_000_000, "webm", "http"),
		mk("22", "avc1", "mp4a", 720, 1280, 1500, 30_000_000, "mp4", "http"),
		mk("92", "avc1", "", 240, 426, 300, 5_000_000, "mp4", "http"),
	}
}

func ids(groups [][]extractor.Format) [][]string {
	out := make([][]string, len(groups))
	for i, g := range groups {
		for _, f := range g {
			out[i] = append(out[i], f.FormatID)
		}
	}
	return out
}

func TestSelect_Best(t *testing.T) {
	g, err := Select(sampleFormats(), "best")
	if err != nil {
		t.Fatal(err)
	}
	// best combined: id 22 (720p) outranks id 18 (360p) by quality.
	if got := ids(g); !reflect.DeepEqual(got, [][]string{{"22"}}) {
		t.Fatalf("best = %v, want [[22]]", got)
	}
}

func TestSelect_BestVideo(t *testing.T) {
	g, err := Select(sampleFormats(), "bestvideo")
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(g); !reflect.DeepEqual(got, [][]string{{"137"}}) {
		t.Fatalf("bestvideo = %v, want [[137]]", got)
	}
}

func TestSelect_BestAudio(t *testing.T) {
	g, err := Select(sampleFormats(), "bestaudio")
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(g); !reflect.DeepEqual(got, [][]string{{"251"}}) {
		t.Fatalf("bestaudio = %v, want [[251]]", got)
	}
}

func TestSelect_Merge(t *testing.T) {
	g, err := Select(sampleFormats(), "bestvideo+bestaudio")
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(g); !reflect.DeepEqual(got, [][]string{{"137", "251"}}) {
		t.Fatalf("merge = %v, want [[137 251]]", got)
	}
}

func TestSelect_FilterHeight(t *testing.T) {
	g, err := Select(sampleFormats(), "bestvideo[height<=720]+bestaudio")
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(g); !reflect.DeepEqual(got, [][]string{{"136", "251"}}) {
		t.Fatalf("filtered merge = %v, want [[136 251]]", got)
	}
}

func TestSelect_FilterExt(t *testing.T) {
	g, err := Select(sampleFormats(), "bestvideo[ext=webm]")
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(g); !reflect.DeepEqual(got, [][]string{{"248"}}) {
		t.Fatalf("webm bestvideo = %v, want [[248]]", got)
	}
}

func TestSelect_ProtocolNotHTTP(t *testing.T) {
	fs := append(sampleFormats(), mk("999", "avc1", "", 720, 1280, 1000, 20_000_000, "mp4", "m3u8_native"))
	g, err := Select(fs, "bestvideo[protocol!=http]")
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(g); !reflect.DeepEqual(got, [][]string{{"999"}}) {
		t.Fatalf("protocol!=http = %v, want [[999]]", got)
	}
}

func TestSelect_Itag(t *testing.T) {
	g, err := Select(sampleFormats(), "136")
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(g); !reflect.DeepEqual(got, [][]string{{"136"}}) {
		t.Fatalf("itag 136 = %v, want [[136]]", got)
	}
}

func TestSelect_Range(t *testing.T) {
	g, err := Select(sampleFormats(), "136-140")
	if err != nil {
		t.Fatal(err)
	}
	// Among itags in [136,140]: 136,137,140 -> highest quality is 137 (1080p).
	if got := ids(g); !reflect.DeepEqual(got, [][]string{{"137"}}) {
		t.Fatalf("range 136-140 = %v, want [[137]]", got)
	}
}

func TestSelect_Worst(t *testing.T) {
	g, err := Select(sampleFormats(), "worst")
	if err != nil {
		t.Fatal(err)
	}
	// worst combined: only "18" and "22" are combined; 18 is smaller.
	if got := ids(g); !reflect.DeepEqual(got, [][]string{{"18"}}) {
		t.Fatalf("worst = %v, want [[18]]", got)
	}
}

func TestSelect_Fallback(t *testing.T) {
	// first candidate yields nothing (no 4k), fallback to bestvideo.
	g, err := Select(sampleFormats(), "bestvideo[height>=2160]/bestvideo")
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(g); !reflect.DeepEqual(got, [][]string{{"137"}}) {
		t.Fatalf("fallback = %v, want [[137]]", got)
	}
}

func TestSelect_MultipleOutputs(t *testing.T) {
	g, err := Select(sampleFormats(), "137,140")
	if err != nil {
		t.Fatal(err)
	}
	if len(g) != 2 {
		t.Fatalf("expected 2 outputs, got %d", len(g))
	}
	if got := ids(g); !reflect.DeepEqual(got, [][]string{{"137"}, {"140"}}) {
		t.Fatalf("multi = %v, want [[137],[140]]", got)
	}
}

func TestSelect_NoMatch(t *testing.T) {
	_, err := Select(sampleFormats(), "bestvideo[height>=4000]")
	if err == nil {
		t.Fatal("expected error for no match")
	}
}
