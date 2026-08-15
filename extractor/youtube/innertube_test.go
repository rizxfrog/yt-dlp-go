package youtube

import (
	"net/http"
	"strings"
	"testing"

	"yt-dlp-go/extractor"
)

// --- extractVisitorData -------------------------------------------------------

func TestExtractVisitorData_Primary(t *testing.T) {
	html := `<script>ytcfg.set({"VISITOR_DATA":"CgstEWh...abc","INNERTUBE_API_KEY":"x"});</script>`
	if got := extractVisitorData(html); got != "CgstEWh...abc" {
		t.Errorf("extractVisitorData = %q, want CgstEWh...abc", got)
	}
}

func TestExtractVisitorData_Fallback(t *testing.T) {
	// No "VISITOR_DATA" key, but a context.client.visitorData-style occurrence.
	html := `{"responseContext":{"visitorData":"fallback-id-123"}}`
	if got := extractVisitorData(html); got != "fallback-id-123" {
		t.Errorf("extractVisitorData fallback = %q, want fallback-id-123", got)
	}
}

func TestExtractVisitorData_Empty(t *testing.T) {
	if got := extractVisitorData(`<html>no visitor data here</html>`); got != "" {
		t.Errorf("extractVisitorData = %q, want empty", got)
	}
}

// --- countResolvable ----------------------------------------------------------

func TestCountResolvable(t *testing.T) {
	raws := []any{
		map[string]any{"itag": 22, "url": "https://x/videoplayback?itag=22"},
		map[string]any{"itag": 248, "signatureCipher": "url=https%3A%2F%2Fx&s=ABC"},
		map[string]any{"itag": 251, "cipher": "url=https%3A%2F%2Fx&s=DEF"},
		map[string]any{"itag": 160, "mimeType": "video/mp4"}, // metadata only, no url/cipher
		"not-a-map",
	}
	if n := countResolvable(raws); n != 3 {
		t.Errorf("countResolvable = %d, want 3", n)
	}
}

// --- formatsNeedJS ------------------------------------------------------------

func TestFormatsNeedJS(t *testing.T) {
	tests := []struct {
		name    string
		formats []any
		want    bool
	}{
		{
			name:    "empty",
			formats: nil,
			want:    false,
		},
		{
			name:    "signatureCipher triggers",
			formats: []any{map[string]any{"itag": 248, "signatureCipher": "url=x&s=y"}},
			want:    true,
		},
		{
			name:    "cipher triggers",
			formats: []any{map[string]any{"itag": 248, "cipher": "url=x&s=y"}},
			want:    true,
		},
		{
			name:    "n param triggers",
			formats: []any{map[string]any{"itag": 22, "url": "https://x/videoplayback?itag=22&n=abc123"}},
			want:    true,
		},
		{
			name:    "fn param does NOT trigger (only bare n)",
			formats: []any{map[string]any{"itag": 22, "url": "https://x/videoplayback?itag=22&fn=abc123"}},
			want:    false,
		},
		{
			name:    "sn param does NOT trigger",
			formats: []any{map[string]any{"itag": 22, "url": "https://x/videoplayback?sn=abc123"}},
			want:    false,
		},
		{
			name:    "plain url without n does NOT trigger",
			formats: []any{map[string]any{"itag": 22, "url": "https://x/videoplayback?itag=22"}},
			want:    false,
		},
		{
			name:    "metadata-only format does NOT trigger",
			formats: []any{map[string]any{"itag": 160, "mimeType": "video/mp4"}},
			want:    false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatsNeedJS(tc.formats); got != tc.want {
				t.Errorf("formatsNeedJS = %v, want %v", got, tc.want)
			}
		})
	}
}

// --- summarizeSkips -----------------------------------------------------------

func TestSummarizeSkips(t *testing.T) {
	if got := summarizeSkips(30, nil); got != "30 formats present, none usable" {
		t.Errorf("summarizeSkips(nil) = %q", got)
	}
	got := summarizeSkips(30, map[string]int{
		"format has neither url nor cipher":     28,
		"incomplete cipher (url=\"\" sig=\"\")": 2,
	})
	// reasons are sorted, so "format has neither..." sorts before "incomplete...".
	want := "all 30 formats skipped: format has neither url nor cipher (x28); incomplete cipher (url=\"\" sig=\"\") (x2)"
	if got != want {
		t.Errorf("summarizeSkips =\n  %q\nwant\n  %q", got, want)
	}
}

// --- playabilityError ---------------------------------------------------------

func TestPlayabilityError(t *testing.T) {
	if err := playabilityError(map[string]any{}); err != nil {
		t.Errorf("empty status: err = %v, want nil", err)
	}
	if err := playabilityError(map[string]any{"playabilityStatus": map[string]any{"status": "OK"}}); err != nil {
		t.Errorf("OK status: err = %v, want nil", err)
	}
	loginReq := map[string]any{"playabilityStatus": map[string]any{
		"status": "LOGIN_REQUIRED",
		"reason": "Sign in to confirm you're not a bot",
	}}
	if err := playabilityError(loginReq); err == nil || !strings.Contains(err.Error(), "LOGIN_REQUIRED") {
		t.Errorf("LOGIN_REQUIRED: err = %v, want error containing LOGIN_REQUIRED", err)
	}
	// reason can also live in the errorScreen renderer.
	unplay := map[string]any{"playabilityStatus": map[string]any{
		"status": "UNPLAYABLE",
		"errorScreen": map[string]any{
			"playerErrorMessageRenderer": map[string]any{
				"reason": map[string]any{"simpleText": "Video unavailable"},
			},
		},
	}}
	if err := playabilityError(unplay); err == nil || !strings.Contains(err.Error(), "UNPLAYABLE") {
		t.Errorf("UNPLAYABLE: err = %v, want error containing UNPLAYABLE", err)
	}
}

// --- innertubeClient.contextClient -------------------------------------------

func TestContextClientPins(t *testing.T) {
	c := innertubeClients["visionos"]
	cc := c.contextClient("visitor-xyz")
	if cc["clientName"] != "VISIONOS" {
		t.Errorf("clientName = %v", cc["clientName"])
	}
	if cc["hl"] != "en" || cc["timeZone"] != "UTC" || cc["utcOffsetMinutes"] != 0 {
		t.Errorf("locale pins wrong: %+v", cc)
	}
	if cc["visitorData"] != "visitor-xyz" {
		t.Errorf("visitorData = %v, want visitor-xyz", cc["visitorData"])
	}
	// Extra (deviceMake etc.) must be merged in.
	if cc["deviceMake"] != "Apple" {
		t.Errorf("deviceMake not merged: %+v", cc["deviceMake"])
	}
	// Without a visitor id, the key must be absent (not empty string).
	cc2 := c.contextClient("")
	if _, ok := cc2["visitorData"]; ok {
		t.Errorf("visitorData key present when empty: %+v", cc2)
	}
}

// --- collectRawFormats --------------------------------------------------------

func TestCollectRawFormats(t *testing.T) {
	player := map[string]any{
		"streamingData": map[string]any{
			"formats":         []any{map[string]any{"itag": 22}},
			"adaptiveFormats": []any{map[string]any{"itag": 248}, map[string]any{"itag": 251}},
		},
	}
	got := collectRawFormats(player)
	if len(got) != 3 {
		t.Fatalf("collectRawFormats len = %d, want 3", len(got))
	}
	if got := collectRawFormats(map[string]any{}); len(got) != 0 {
		t.Errorf("missing streamingData should yield empty slice, got %d", len(got))
	}
	if got := collectRawFormats(map[string]any{"streamingData": map[string]any{}}); len(got) != 0 {
		t.Errorf("empty streamingData should yield empty slice, got %d", len(got))
	}
}

// --- defaultPlayerClients integrity -------------------------------------------

func TestDefaultClientTable(t *testing.T) {
	if len(defaultPlayerClients) == 0 {
		t.Fatal("defaultPlayerClients is empty")
	}
	for _, label := range defaultPlayerClients {
		c, ok := innertubeClients[label]
		if !ok {
			t.Errorf("default client %q not present in innertubeClients", label)
			continue
		}
		if c.ClientName == "" || c.ClientVersion == "" || c.UserAgent == "" {
			t.Errorf("client %q missing required identity fields: %+v", label, c)
		}
		if c.ClientID == 0 {
			t.Errorf("client %q has zero ClientID", label)
		}
	}
}

// --- resolvePlayerResponse fallback (offline) ---------------------------------

// stubAPI lets a test drive resolvePlayerResponse without network access.
func stubAPI(results map[string]map[string]any, errs map[string]error) func(*extractor.Context, innertubeClient, string, string) (map[string]any, error) {
	return func(_ *extractor.Context, c innertubeClient, _, _ string) (map[string]any, error) {
		if e, ok := errs[c.Label]; ok {
			return nil, e
		}
		if r, ok := results[c.Label]; ok {
			return r, nil
		}
		return nil, http.ErrNotSupported // treat unknown label as a failed request
	}
}

func TestResolvePlayerResponse_ApiWins(t *testing.T) {
	orig := playerAPIFn
	defer func() { playerAPIFn = orig }()

	good := map[string]map[string]any{
		"visionos": {
			"streamingData": map[string]any{
				"formats": []any{map[string]any{"itag": 22, "url": "https://x/v?itag=22"}},
			},
		},
	}
	playerAPIFn = stubAPI(good, nil)

	player, label, err := resolvePlayerResponse(&extractor.Context{}, "VID", "vd", nil)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if label != "visionos" {
		t.Errorf("label = %q, want visionos", label)
	}
	if player == nil {
		t.Errorf("player is nil")
	}
}

func TestResolvePlayerResponse_ApiFailsFallsBackToWebpage(t *testing.T) {
	orig := playerAPIFn
	defer func() { playerAPIFn = orig }()

	// Every API client fails; the webpage player response still carries formats.
	playerAPIFn = stubAPI(nil, map[string]error{
		"visionos":   http.ErrNotSupported,
		"android_vr": http.ErrNotSupported,
		"web":        http.ErrNotSupported,
	})
	htmlPlayer := map[string]any{
		"streamingData": map[string]any{
			"formats": []any{map[string]any{"itag": 22, "url": "https://x/v?itag=22"}},
		},
	}

	player, label, err := resolvePlayerResponse(&extractor.Context{}, "VID", "vd", htmlPlayer)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if label != "webpage" {
		t.Errorf("label = %q, want webpage", label)
	}
	if player == nil {
		t.Errorf("expected non-nil player from webpage fallback")
	}
}

func TestResolvePlayerResponse_AllFail(t *testing.T) {
	orig := playerAPIFn
	defer func() { playerAPIFn = orig }()

	playerAPIFn = stubAPI(nil, map[string]error{
		"visionos":   http.ErrNotSupported,
		"android_vr": http.ErrNotSupported,
		"web":        http.ErrNotSupported,
	})
	// Webpage player response present but also has no usable formats.
	htmlPlayer := map[string]any{
		"streamingData": map[string]any{
			"formats": []any{map[string]any{"itag": 160, "mimeType": "video/mp4"}},
		},
	}
	_, _, err := resolvePlayerResponse(&extractor.Context{}, "VID", "vd", htmlPlayer)
	if err == nil {
		t.Fatal("expected error when no client yields formats, got nil")
	}
	if !strings.Contains(err.Error(), "any client") {
		t.Errorf("error should mention clients failed: %v", err)
	}
}
