// InnerTube support for the YouTube extractor.
//
// # WHY THIS EXISTS
//
// Scraping ytInitialPlayerResponse out of the watch page used to be enough to
// get stream URLs. It no longer is: YouTube now serves the web player response
// with streamingData entries that carry only metadata (itag, mimeType,
// contentLength, initRange...) and NO "url" and NO "signatureCipher". Every
// format is therefore unresolvable and extraction fails with "no resolvable
// formats found".
//
// Real yt-dlp solves this by not using the webpage for formats at all. It calls
// the InnerTube player endpoint (POST /youtubei/v1/player) once per "client",
// where a client is a spoofed YouTube app identity (visionOS app, Oculus Quest
// VR app, iOS app, TV/Cobalt...). Some of those clients still receive plain,
// unciphered URLs.
//
// The client table below mirrors yt_dlp/extractor/youtube/_base.py
// (INNERTUBE_CLIENTS) and the try-order mirrors _video.py's
// _DEFAULT_CLIENTS = ('visionos', 'android_vr', 'web').
//
// Verified empirically against a live video: visionos returned 28/28 formats
// with plain URLs, android_vr 23/23, both with no signature cipher and no "n"
// throttling parameter — i.e. these clients need no JS player at all
// (yt-dlp marks them REQUIRE_JS_PLAYER: False). ios currently returns formats
// without URLs (it now needs a PO token) and tv returns UNPLAYABLE, so they are
// defined for completeness but not tried by default.
package youtube

import (
	"fmt"
	"regexp"
	"strings"

	"yt-dlp-go/extractor"
)

// innertubeClient is one spoofed client identity for the InnerTube API.
type innertubeClient struct {
	// Label is the yt-dlp name for this client (e.g. "android_vr").
	Label string
	// ClientName / ClientVersion go into context.client and the
	// X-YouTube-Client-Version header.
	ClientName    string
	ClientVersion string
	// ClientID is INNERTUBE_CONTEXT_CLIENT_NAME, sent as X-YouTube-Client-Name.
	ClientID int
	// UserAgent must match the impersonated app, or YouTube rejects the call.
	UserAgent string
	// Extra holds the remaining context.client fields (deviceMake, osName, ...).
	Extra map[string]any
}

// contextClient assembles the context.client object for a request. hl/timeZone/
// utcOffsetMinutes are pinned exactly as yt-dlp's _extract_context does, so
// responses are stable and locale-independent.
func (c innertubeClient) contextClient(visitorData string) map[string]any {
	client := map[string]any{
		"clientName":       c.ClientName,
		"clientVersion":    c.ClientVersion,
		"hl":               "en",
		"timeZone":         "UTC",
		"utcOffsetMinutes": 0,
	}
	for k, v := range c.Extra {
		client[k] = v
	}
	if visitorData != "" {
		client["visitorData"] = visitorData
	}
	return client
}

// innertubeClients is the known client table, keyed by yt-dlp's label.
var innertubeClients = map[string]innertubeClient{
	// "Made for kids" videos aren't available with this client.
	"visionos": {
		Label:         "visionos",
		ClientName:    "VISIONOS",
		ClientVersion: "1.02",
		ClientID:      101,
		UserAgent:     "Mozilla/5.0 (Macintosh; Intel Mac OS X 15_7_3) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.0 Safari/605.1.15",
		Extra: map[string]any{
			"deviceMake":  "Apple",
			"deviceModel": "RealityDevice17,1",
			"osName":      "visionOS",
			"osVersion":   "26.5.23O471",
		},
	},
	// Using a clientVersion > 1.65 may return SABR-only streams.
	"android_vr": {
		Label:         "android_vr",
		ClientName:    "ANDROID_VR",
		ClientVersion: "1.65.10",
		ClientID:      28,
		UserAgent:     "com.google.android.apps.youtube.vr.oculus/1.65.10 (Linux; U; Android 12L; eureka-user Build/SQ3A.220605.009.A1) gzip",
		Extra: map[string]any{
			"deviceMake":        "Oculus",
			"deviceModel":       "Quest 3",
			"androidSdkVersion": 32,
			"osName":            "Android",
			"osVersion":         "12L",
		},
	},
	// Defined for completeness / manual override. As of writing, ios returns
	// formats without URLs (PO token required).
	"ios": {
		Label:         "ios",
		ClientName:    "IOS",
		ClientVersion: "21.26.4",
		ClientID:      5,
		UserAgent:     "com.google.ios.youtube/21.26.4 (iPhone16,2; U; CPU iOS 18_3_2 like Mac OS X;)",
		Extra: map[string]any{
			"deviceMake":  "Apple",
			"deviceModel": "iPhone16,2",
			"osName":      "iPhone",
			"osVersion":   "18.3.2.22D82",
		},
	},
	"tv": {
		Label:         "tv",
		ClientName:    "TVHTML5",
		ClientVersion: "7.20260707.07.00",
		ClientID:      7,
		UserAgent:     "Mozilla/5.0 (ChromiumStylePlatform) Cobalt/25.lts.30.1034943-gold (unlike Gecko), Unknown_TV_Unknown_0/Unknown (Unknown, Unknown)",
	},
	"web": {
		Label:         "web",
		ClientName:    "WEB",
		ClientVersion: "2.20260708.00.00",
		ClientID:      1,
		UserAgent:     "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
	},
}

// defaultPlayerClients is the try-order, mirroring yt-dlp's _DEFAULT_CLIENTS.
// The first client that yields formats carrying real URLs wins.
var defaultPlayerClients = []string{"visionos", "android_vr", "web"}

const innertubePlayerURL = "https://www.youtube.com/youtubei/v1/player"

var (
	// VISITOR_DATA lives in the ytcfg.set({...}) blob on the watch page.
	visitorDataRE = regexp.MustCompile(`"VISITOR_DATA"\s*:\s*"([^"]+)"`)
	// Fallback: the same value also appears as context.client.visitorData or
	// responseContext.visitorData.
	visitorDataAltRE = regexp.MustCompile(`"visitorData"\s*:\s*"([^"]+)"`)
)

// extractVisitorData pulls the session's visitor id out of the watch page.
//
// This is essential: without X-Goog-Visitor-Id the player endpoint answers
// LOGIN_REQUIRED / "Sign in to confirm you're not a bot" even for public
// videos. (Verified: identical requests succeed with it and fail without it.)
func extractVisitorData(html string) string {
	if m := visitorDataRE.FindStringSubmatch(html); m != nil {
		return m[1]
	}
	if m := visitorDataAltRE.FindStringSubmatch(html); m != nil {
		return m[1]
	}
	return ""
}

// playerAPIFn performs one InnerTube player request. It is a variable rather
// than a direct call to callPlayerAPI so tests can substitute a stub without
// reaching the network. Override it in tests and restore afterwards.
var playerAPIFn = callPlayerAPI

// callPlayerAPI performs one InnerTube player request and returns the decoded
// player response.
func callPlayerAPI(ctx *extractor.Context, c innertubeClient, videoID, visitorData string) (map[string]any, error) {
	payload := map[string]any{
		"context": map[string]any{
			"client": c.contextClient(visitorData),
		},
		"videoId": videoID,
		// Skip the "this may be inappropriate" and racy-content interstitials,
		// which would otherwise suppress streamingData.
		"contentCheckOk": true,
		"racyCheckOk":    true,
	}

	headers := map[string]string{
		"X-YouTube-Client-Name":    fmt.Sprintf("%d", c.ClientID),
		"X-YouTube-Client-Version": c.ClientVersion,
		"Origin":                   "https://www.youtube.com",
	}
	if c.UserAgent != "" {
		headers["User-Agent"] = c.UserAgent
	}
	if visitorData != "" {
		headers["X-Goog-Visitor-Id"] = visitorData
	}

	raw, err := extractor.PostJSON(ctx, innertubePlayerURL, headers, payload)
	if err != nil {
		return nil, err
	}
	player, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("player response was not a JSON object")
	}
	return player, nil
}

// playabilityError converts a non-OK playabilityStatus into an error. Statuses
// like LOGIN_REQUIRED / UNPLAYABLE / AGE_VERIFICATION_REQUIRED mean this client
// cannot serve the video, so the caller should try the next one.
func playabilityError(player map[string]any) error {
	status := extractor.StrOrNone(extractor.TraverseObj(player, "playabilityStatus", "status"))
	if status == "" || strings.EqualFold(status, "OK") {
		return nil
	}
	reason := extractor.StrOrNone(extractor.TraverseObj(player, "playabilityStatus", "reason"))
	if reason == "" {
		reason = extractor.StrOrNone(extractor.TraverseObj(player,
			"playabilityStatus", "errorScreen", "playerErrorMessageRenderer", "reason", "simpleText"))
	}
	if reason != "" {
		return fmt.Errorf("%s: %s", status, reason)
	}
	return fmt.Errorf("%s", status)
}

// collectRawFormats flattens streamingData.formats (progressive) and
// streamingData.adaptiveFormats (separate video/audio) into one slice.
func collectRawFormats(player map[string]any) []any {
	streaming := extractor.TraverseObj(player, "streamingData")
	if streaming == nil {
		return nil
	}
	out := []any{}
	for _, key := range []string{"formats", "adaptiveFormats"} {
		if arr, ok := extractor.TraverseObj(streaming, key).([]any); ok {
			out = append(out, arr...)
		}
	}
	return out
}

// countResolvable reports how many raw formats carry something we can turn into
// a media URL (a plain url, or a cipher we can decode).
func countResolvable(raws []any) int {
	n := 0
	for _, f := range raws {
		m, ok := f.(map[string]any)
		if !ok {
			continue
		}
		if extractor.StrOrNone(m["url"]) != "" ||
			extractor.StrOrNone(m["signatureCipher"]) != "" ||
			extractor.StrOrNone(m["cipher"]) != "" {
			n++
		}
	}
	return n
}
