package youtube

import "testing"

func TestParseTimestamp(t *testing.T) {
	cases := map[string]float64{
		"0:00":    0,
		"1:23":    83,
		"10:00":   600,
		"1:02:03": 3723,
		"0:00.5":  0.5,
	}
	for in, want := range cases {
		if got := parseTimestamp(in); got != want {
			t.Errorf("parseTimestamp(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestParseChaptersFromDescription(t *testing.T) {
	desc := `这是一个视频

0:00 开场
1:23 正片开始
5:00 结尾
感谢观看`
	chapters := parseChaptersFromDescription(desc)
	if len(chapters) != 3 {
		t.Fatalf("chapters = %d, want 3", len(chapters))
	}
	if chapters[0].Title != "开场" || chapters[0].StartTime != 0 {
		t.Errorf("chapters[0] = %+v", chapters[0])
	}
	if chapters[0].EndTime != 83 {
		t.Errorf("chapters[0].EndTime = %v, want 83", chapters[0].EndTime)
	}
	if chapters[1].Title != "正片开始" || chapters[1].StartTime != 83 || chapters[1].EndTime != 300 {
		t.Errorf("chapters[1] = %+v", chapters[1])
	}
	if chapters[2].Title != "结尾" || chapters[2].StartTime != 300 || chapters[2].EndTime != 0 {
		t.Errorf("chapters[2] = %+v", chapters[2])
	}
}

func TestParseChaptersFromDescription_None(t *testing.T) {
	if got := parseChaptersFromDescription("no timestamps here\njust text"); got != nil {
		t.Fatalf("got %v, want nil", got)
	}
}

func TestParseChaptersFromDescription_Dedupe(t *testing.T) {
	desc := "0:00 a\n0:00 b\n1:00 c\n"
	chapters := parseChaptersFromDescription(desc)
	if len(chapters) != 2 {
		t.Fatalf("chapters = %d, want 2 (dedup same start)", len(chapters))
	}
	if chapters[0].Title != "a" || chapters[1].Title != "c" {
		t.Errorf("chapters = %+v", chapters)
	}
}
