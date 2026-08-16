package postprocessor

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"yt-dlp-go/extractor"
)

func TestKeepRanges(t *testing.T) {
	cases := []struct {
		name     string
		segments []extractor.Chapter
		duration float64
		want     string // "0-10|20-50|60-100" (in-out pairs; "100-" = open at end)
	}{
		{
			name:     "middle segments",
			segments: []extractor.Chapter{{StartTime: 10, EndTime: 20}, {StartTime: 50, EndTime: 60}},
			duration: 100,
			want:     "0-10|20-50|60-100",
		},
		{
			name:     "leading segment",
			segments: []extractor.Chapter{{StartTime: 0, EndTime: 20}},
			duration: 100,
			want:     "20-100",
		},
		{
			name:     "trailing segment",
			segments: []extractor.Chapter{{StartTime: 80, EndTime: 100}},
			duration: 100,
			want:     "0-80",
		},
		{
			name:     "no segments keeps everything",
			segments: nil,
			duration: 100,
			want:     "0-100",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ranges := keepRanges(c.segments, c.duration)
			var parts []string
			for _, r := range ranges {
				in := "0"
				if r.hasIn {
					in = fmt.Sprintf("%g", r.in)
				}
				out := fmt.Sprintf("%g", c.duration)
				if r.hasOut {
					out = fmt.Sprintf("%g", r.out)
				}
				parts = append(parts, in+"-"+out)
			}
			got := strings.Join(parts, "|")
			if got != c.want {
				t.Errorf("keepRanges() = %q, want %q", got, c.want)
			}
		})
	}
}

func TestKeepRanges_Structure(t *testing.T) {
	ranges := keepRanges([]extractor.Chapter{{StartTime: 10, EndTime: 20}, {StartTime: 50, EndTime: 60}}, 100)
	if len(ranges) != 3 {
		t.Fatalf("len = %d, want 3", len(ranges))
	}
	if ranges[0].hasIn || !ranges[0].hasOut || ranges[0].out != 10 {
		t.Errorf("ranges[0] = %+v, want outpoint 10", ranges[0])
	}
	if !ranges[1].hasIn || ranges[1].in != 20 || !ranges[1].hasOut || ranges[1].out != 50 {
		t.Errorf("ranges[1] = %+v, want inpoint 20 outpoint 50", ranges[1])
	}
	if !ranges[2].hasIn || ranges[2].in != 60 || ranges[2].hasOut {
		t.Errorf("ranges[2] = %+v, want inpoint 60 open-ended", ranges[2])
	}
}

func TestWriteConcatSpec(t *testing.T) {
	ranges := keepRanges([]extractor.Chapter{{StartTime: 10, EndTime: 20}}, 100)
	path, err := writeConcatSpec("/path/to/vid.mp4", ranges)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(path)
	b, _ := os.ReadFile(path)
	s := string(b)
	for _, want := range []string{
		"ffconcat version 1.0\n",
		"file '/path/to/vid.mp4'\n",
		"outpoint 10.000000\n",
		"inpoint 20.000000\n",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("concat spec missing %q:\n%s", want, s)
		}
	}
}

func TestEscapeConcatPath(t *testing.T) {
	if got := escapeConcatPath("/a'b.mp4"); got != `/a'\''b.mp4` {
		t.Errorf("escapeConcatPath = %q", got)
	}
}
