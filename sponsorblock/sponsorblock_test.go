package sponsorblock

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseCategories(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", []string{"sponsor"}},
		{"sponsor", []string{"sponsor"}},
		{"sponsor,selfpromo", []string{"sponsor", "selfpromo"}},
		{"sponsor,sponsor,selfpromo", []string{"sponsor", "selfpromo"}},
		{"all", AllCategories},
		{"garbage", []string{"sponsor"}},
		{" sponsor , selfpromo ", []string{"sponsor", "selfpromo"}},
	}
	for _, c := range cases {
		got := ParseCategories(c.in)
		if len(got) != len(c.want) {
			t.Fatalf("ParseCategories(%q) = %v, want %v", c.in, got, c.want)
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Fatalf("ParseCategories(%q) = %v, want %v", c.in, got, c.want)
			}
		}
	}
}

func TestFilter(t *testing.T) {
	segs := []Segment{
		{Category: "sponsor", StartTime: 10, EndTime: 20},
		{Category: "selfpromo", StartTime: 30, EndTime: 35},
		{Category: "sponsor", StartTime: 50, EndTime: 55},
	}
	got := Filter(segs, []string{"sponsor"})
	if len(got) != 2 || got[0].StartTime != 10 || got[1].StartTime != 50 {
		t.Fatalf("Filter(sponsor) = %v", got)
	}
}

func TestFetch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Path is /api/skipSegments/<sha256(videoID)[:4]>.
		if !strings.HasPrefix(r.URL.Path, "/api/skipSegments/") {
			t.Errorf("path = %q", r.URL.Path)
		}
		if r.URL.Query().Get("service") != "YouTube" {
			t.Errorf("service = %q", r.URL.Query().Get("service"))
		}
		var cats []string
		if err := json.Unmarshal([]byte(r.URL.Query().Get("categories")), &cats); err != nil {
			t.Errorf("categories not JSON array: %v", err)
		} else if len(cats) != 1 || cats[0] != "sponsor" {
			t.Errorf("categories = %v", cats)
		}
		// Response covers several videoIDs sharing a hash prefix; only "abc"
		// (the requested id) should be returned by Fetch.
		json.NewEncoder(w).Encode([]map[string]any{
			{"videoID": "other", "segments": []map[string]any{
				{"segment": []float64{1, 2}, "category": "sponsor", "UUID": "u0"},
			}},
			{"videoID": "abc", "segments": []map[string]any{
				{"segment": []float64{10, 20}, "category": "sponsor", "UUID": "u1"},
				{"segment": []float64{30, 35}, "category": "selfpromo", "UUID": "u2"},
				{"segment": []float64{0, 0}, "category": "sponsor", "UUID": "u3"}, // whole-video, skipped
			}},
		})
	}))
	defer srv.Close()

	orig := apiBase
	apiBase = srv.URL
	defer func() { apiBase = orig }()

	segs, err := Fetch(srv.Client(), "abc", []string{"sponsor"})
	if err != nil {
		t.Fatal(err)
	}
	if len(segs) != 2 {
		t.Fatalf("segs = %d, want 2", len(segs))
	}
	if segs[0].Category != "sponsor" || segs[0].StartTime != 10 || segs[0].EndTime != 20 {
		t.Errorf("segs[0] = %+v", segs[0])
	}
	if segs[1].Category != "selfpromo" || segs[1].UUID != "u2" {
		t.Errorf("segs[1] = %+v", segs[1])
	}
}
