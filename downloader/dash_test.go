package downloader

import (
	"reflect"
	"testing"
)

const segTemplateMPD = `<?xml version="1.0"?>
<MPD mediaPresentationDuration="PT10M">
  <Period>
    <AdaptationSet contentType="video" mimeType="video/mp4">
      <Representation id="1" bandwidth="3000000" width="1280" height="720" codecs="avc1">
        <BaseURL>video/</BaseURL>
        <SegmentTemplate duration="6000" timescale="1000" startNumber="1"
          initialization="$RepresentationID$-init.mp4" media="$RepresentationID$-$Number$.m4s"/>
      </Representation>
    </AdaptationSet>
    <AdaptationSet contentType="audio" mimeType="audio/mp4">
      <Representation id="2" bandwidth="128000" codecs="mp4a">
        <BaseURL>audio/</BaseURL>
        <SegmentTemplate duration="6000" timescale="1000" startNumber="1"
          initialization="$RepresentationID$-init.mp4" media="$RepresentationID$-$Number$.m4s"/>
      </Representation>
    </AdaptationSet>
  </Period>
</MPD>`

const segListMPD = `<?xml version="1.0"?>
<MPD>
  <Period>
    <AdaptationSet contentType="audio" mimeType="audio/mp4">
      <Representation id="a1" bandwidth="64000" codecs="mp4a">
        <SegmentList>
          <SegmentURL media="seg1.m4s"/>
          <SegmentURL media="seg2.m4s"/>
          <SegmentURL media="seg3.m4s"/>
        </SegmentList>
      </Representation>
    </AdaptationSet>
  </Period>
</MPD>`

func TestParseMPD_SegmentTemplate(t *testing.T) {
	reps, err := parseMPD(segTemplateMPD, "https://example.com/manifest.mpd")
	if err != nil {
		t.Fatal(err)
	}
	if len(reps) != 2 {
		t.Fatalf("want 2 representations, got %d", len(reps))
	}
	var video *dashRepresentation
	for i := range reps {
		if reps[i].ContentType == "video" {
			video = &reps[i]
		}
	}
	if video == nil {
		t.Fatal("no video representation")
	}
	if video.InitURL != "https://example.com/video/1-init.mp4" {
		t.Fatalf("init url = %q", video.InitURL)
	}
	if len(video.SegmentURLs) == 0 {
		t.Fatal("expected segments from duration, got 0")
	}
	wantFirst := "https://example.com/video/1-1.m4s"
	if video.SegmentURLs[0] != wantFirst {
		t.Fatalf("first segment = %q want %q", video.SegmentURLs[0], wantFirst)
	}
}

func TestParseMPD_SegmentList(t *testing.T) {
	reps, err := parseMPD(segListMPD, "https://h.com/m.mpd")
	if err != nil {
		t.Fatal(err)
	}
	audio := reps[0]
	if audio.ContentType != "audio" {
		t.Fatalf("content type = %q", audio.ContentType)
	}
	want := []string{
		"https://h.com/seg1.m4s",
		"https://h.com/seg2.m4s",
		"https://h.com/seg3.m4s",
	}
	if !reflect.DeepEqual(audio.SegmentURLs, want) {
		t.Fatalf("segments = %v want %v", audio.SegmentURLs, want)
	}
}

func TestParseISO8601Duration(t *testing.T) {
	cases := map[string]float64{
		"PT10M":    600,
		"PT1H2M3S": 3723,
		"PT30S":    30,
		"":         0,
	}
	for in, want := range cases {
		if got := parseISO8601Duration(in); got != want {
			t.Fatalf("parseISO8601Duration(%q) = %v want %v", in, got, want)
		}
	}
}
