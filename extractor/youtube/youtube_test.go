package youtube

import (
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
						"name":        map[string]any{"simpleText": "English"},
					},
					map[string]any{
						"languageCode": "zh",
						"baseUrl":      "https://www.youtube.com/api/timedtext?lang=zh",
						"name":        map[string]any{"simpleText": "中文"},
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
	got, err := extractor.DeobfuscateSignature(js, "abcdef")
	if err != nil {
		t.Fatalf("DeobfuscateSignature: %v", err)
	}
	// reverse("abcdef") = "fedcba"; slice(1) = "edcba"; swap(0,0) = "edcba" (no-op with itself).
	if got != "edcba" {
		t.Errorf("got %q, want edcba", got)
	}
}
