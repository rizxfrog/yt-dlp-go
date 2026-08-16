package bilibili

import "testing"

func TestSpaceMatch_URL(t *testing.T) {
	ie := BilibiliSpaceIE{}
	cases := []struct {
		url  string
		want bool
	}{
		{"https://space.bilibili.com/3706988932368570/upload/video", true},
		{"https://space.bilibili.com/3706988932368570/", true},
		{"https://space.bilibili.com/3706988932368570/video", true},
		{"https://space.bilibili.com/3706988932368570", true},
		{"http://space.bilibili.com/12345/upload/video", true},
		{"space.bilibili.com/67890/upload/video", true},
		// Negative cases: these belong to BilibiliIE, not the space IE.
		{"https://www.bilibili.com/video/BV1BYuo6JE1w", false},
		{"https://b23.tv/abc123", false},
		{"https://example.com/12345", false},
		{"", false},
	}
	for _, c := range cases {
		if got := ie.Match(c.url); got != c.want {
			t.Errorf("Match(%q) = %v, want %v", c.url, got, c.want)
		}
	}
}

func TestSpaceMatch_BareMid(t *testing.T) {
	ie := BilibiliSpaceIE{}
	if !ie.Match("3706988932368570") {
		t.Error("bare mid should match")
	}
	if ie.Match("123") {
		t.Error("too-short digit string should not match")
	}
	if ie.Match("abc123") {
		t.Error("non-digit string should not match")
	}
}

func TestExtractSpaceMid(t *testing.T) {
	cases := []struct {
		url      string
		wantMid  string
		wantOK   bool
	}{
		{"https://space.bilibili.com/3706988932368570/upload/video", "3706988932368570", true},
		{"https://space.bilibili.com/12345", "12345", true},
		{"  3706988932368570  ", "3706988932368570", true},
		{"https://www.bilibili.com/video/BV1xx", "", false},
	}
	for _, c := range cases {
		got, ok := extractSpaceMid(c.url)
		if ok != c.wantOK || got != c.wantMid {
			t.Errorf("extractSpaceMid(%q) = (%q, %v), want (%q, %v)", c.url, got, ok, c.wantMid, c.wantOK)
		}
	}
}
