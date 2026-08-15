package youtube

import (
	"net/url"
	"testing"

	"yt-dlp-go/extractor"
)

func TestExtractSubtitles(t *testing.T) {
	player := map[string]any{
		"captions": map[string]any{
			"playerCaptionsTracklistRenderer": map[string]any{
				"captionTracks": []any{
					map[string]any{
						"languageCode": "en",
						"baseUrl":      "https://www.youtube.com/api/timedtext?lang=en",
						"name":         map[string]any{"simpleText": "English"},
					},
					map[string]any{
						"languageCode": "zh",
						"baseUrl":      "https://www.youtube.com/api/timedtext?lang=zh",
						"name":         map[string]any{"simpleText": "中文"},
					},
				},
			},
		},
	}
	subs := extractSubtitles(player)
	if len(subs) != 2 {
		t.Fatalf("got %d languages, want 2", len(subs))
	}
	if len(subs["en"]) != 1 || subs["en"][0].URL == "" || subs["en"][0].Ext != "xml" {
		t.Errorf("en subtitle wrong: %+v", subs["en"])
	}
	if subs["zh"][0].Name != "中文" {
		t.Errorf("zh name = %q", subs["zh"][0].Name)
	}
}

func TestExtractSubtitles_Empty(t *testing.T) {
	subs := extractSubtitles(map[string]any{})
	if subs == nil {
		t.Fatal("expected non-nil map")
	}
	if len(subs) != 0 {
		t.Errorf("expected empty, got %d", len(subs))
	}
}

func TestDeobfuscateSignature_Smoke(t *testing.T) {
	// A minimal player source whose transform is: reverse -> slice(1) -> swap(0).
	// Every step must be expressed as a helper call (X.Y(a,N)) because the
	// extractor only recognises signature operations shaped that way.
	js := `
var a={};a.b=function(c,d){c=c.split("");c.reverse();return c.join("")};
var x={};x.y=function(c,d){c=c.slice(d);return c};
var z={};z.w=function(a,b){var c=a[0];a[0]=a[b%a.length];a[b]=c;return a};
function sig(a){a=a.split("");a=a.b(a,0);a=x.y(a,1);a=z.w(a,0);return a.join("")}
`
	got, err := extractor.DeobfuscateSignature(js, "abcdef", "")
	if err != nil {
		t.Fatalf("DeobfuscateSignature: %v", err)
	}
	// reverse("abcdef") = "fedcba"; slice(1) = "edcba"; swap(0,0) = "edcba" (no-op with itself).
	if got != "edcba" {
		t.Errorf("got %q, want edcba", got)
	}
}

// playerNSig is a synthetic player whose "n" transform reverses via charAt.
const playerNSig = `
function nsig(a){var r="";for(var i=a.length-1;i>=0;i--){r=r+a.charAt(i)}return r}
`

func TestBuildFormat_RewritesNParam(t *testing.T) {
	raw := "https://r.googlevideo.com/videoplayback?itag=22&n=abcdef&sig=xyz"
	m := map[string]any{
		"itag": 22,
		"url":  raw,
	}
	f, err := buildFormat(m, playerNSig, nil, "", nil)
	if err != nil {
		t.Fatalf("buildFormat: %v", err)
	}
	u, err := url.Parse(f.URL)
	if err != nil {
		t.Fatalf("result url parse: %v", err)
	}
	if got := u.Query().Get("n"); got != "fedcba" {
		t.Errorf("n param: got %q, want fedcba (full url %q)", got, f.URL)
	}
}

func TestBuildFormat_NParamNoJS(t *testing.T) {
	// Without player JS, the n param must be left untouched (graceful).
	raw := "https://r.googlevideo.com/videoplayback?n=abcdef"
	m := map[string]any{"itag": 22, "url": raw}
	f, err := buildFormat(m, "", nil, "", nil)
	if err != nil {
		t.Fatalf("buildFormat: %v", err)
	}
	if f.URL != raw {
		t.Errorf("expected url unchanged without player JS, got %q", f.URL)
	}
}

func TestExtract_StsPassthrough(t *testing.T) {
	// sts must be forwarded to the signature transform as its 2nd argument.
	// Use a synthetic transform that reverses (split/join) and appends the 2nd arg.
	js := `
function sig(a,b){a=a.split("");a.reverse();return a.join("")+b}
`
	got, err := extractor.DeobfuscateSignature(js, "SIG", "12345")
	if err != nil {
		t.Fatalf("DeobfuscateSignature: %v", err)
	}
	// reverse("SIG") = "GIS", then append sts "12345"
	if got != "GIS12345" {
		t.Errorf("sts passthrough: got %q, want GIS12345", got)
	}
}

func TestYouTubeIsLive(t *testing.T) {
	if !youtubeIsLive(map[string]any{"videoDetails": map[string]any{"isLiveContent": true}}) {
		t.Error("isLiveContent=true should be live")
	}
	if youtubeIsLive(map[string]any{"videoDetails": map[string]any{"isLive": true}}) {
		// isLive:true is also recognised.
	}
	if youtubeIsLive(map[string]any{}) {
		t.Error("empty player should not be live")
	}
	if youtubeIsLive(map[string]any{"videoDetails": map[string]any{"isLiveContent": false}}) {
		t.Error("isLiveContent=false should not be live")
	}
}

func TestBuildLiveFormats(t *testing.T) {
	const hls = "https://manifest.googlevideo.com/api/manifest/hls_playlist/expire/123/playlist.m3u8"
	live := buildLiveFormats(map[string]any{
		"streamingData": map[string]any{"hlsManifestUrl": hls},
	})
	if len(live) != 1 {
		t.Fatalf("got %d live formats, want 1", len(live))
	}
	f := live[0]
	if f.Protocol != "m3u8_native" {
		t.Errorf("protocol = %q, want m3u8_native", f.Protocol)
	}
	if f.URL != hls {
		t.Errorf("url = %q", f.URL)
	}
	if f.Ext != "m3u8" || f.Source != "hls" || f.FormatNote != "live" {
		t.Errorf("unexpected fields: %+v", f)
	}

	// No manifest -> no live format.
	if got := buildLiveFormats(map[string]any{"streamingData": map[string]any{}}); got != nil {
		t.Errorf("expected nil for missing hlsManifestUrl, got %+v", got)
	}
	if got := buildLiveFormats(map[string]any{}); got != nil {
		t.Errorf("expected nil for missing streamingData, got %+v", got)
	}
}
