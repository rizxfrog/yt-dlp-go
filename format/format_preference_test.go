package format

import (
	"testing"

	"yt-dlp-go/extractor"
)

// TestSelect_PreferenceDominatesQuality verifies that the extractor-assigned
// Preference outranks resolution/bitrate in the default "best" selection, so an
// unwatermarked 720p stream (pref -1) beats a watermarked 1080p (pref -2). This
// is what lets Douyin's clean playback stream win over the higher-resolution
// watermarked download stream.
func TestSelect_PreferenceDominatesQuality(t *testing.T) {
	watermarked := mk("download_addr", "avc1", "aac", 1920, 1080, 4000, 700_000_000, "mp4", "http")
	watermarked.Preference = -2
	clean := mk("play_addr_265", "h265", "aac", 1280, 720, 973, 270_000_000, "mp4", "http")
	clean.Preference = -1

	// watermarked is listed first on purpose: pickBest must still choose clean.
	groups, err := Select([]extractor.Format{watermarked, clean}, "best")
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 1 || len(groups[0]) != 1 {
		t.Fatalf("groups = %v, want a single combined format", groups)
	}
	if got := groups[0][0].FormatID; got != "play_addr_265" {
		t.Errorf("best picked %q, want the unwatermarked play_addr_265", got)
	}
}
