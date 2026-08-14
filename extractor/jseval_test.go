package extractor

import (
	"testing"
)

// fixtureObjectHelper mirrors modern YouTube: the transform calls methods on a
// shared helper object literal (var V={...}). goja must extract that object and
// the transform, then evaluate the composed operation.
const fixtureObjectHelper = `
var V={fS:function(a){a.reverse()},Cu:function(a,b){return a.slice(b)},Hm:function(a,b){var c=a[0];a[0]=a[b%a.length];a[b]=c}};
function sig(a){a=a.split("");V.fS(a);a=V.Cu(a,1);V.Hm(a,2);return a.join("")}
`

// fixtureInline has no helper methods; all operations are inlined in the
// transform body (reverse / slice / swap).
const fixtureInline = `
function dec(a){a=a.split("");a.reverse();a=a.slice(2);var c=a[0];a[0]=a[3];a[3]=c;return a.join("")}
`

func TestDeobfuscate_GojaObjectHelper(t *testing.T) {
	got, err := DeobfuscateSignature(fixtureObjectHelper, "abcdef", "")
	if err != nil {
		t.Fatalf("goja eval: %v", err)
	}
	// split -> reverse -> slice(1) -> swap(a[0],a[2])
	if got != "cdeba" {
		t.Errorf("object-helper transform: got %q, want %q", got, "cdeba")
	}
}

func TestDeobfuscate_GojaInline(t *testing.T) {
	got, err := DeobfuscateSignature(fixtureInline, "abcdefgh", "")
	if err != nil {
		t.Fatalf("goja eval: %v", err)
	}
	// split -> reverse -> slice(2) -> swap(a[0],a[3])
	if got != "cedfba" {
		t.Errorf("inline transform: got %q, want %q", got, "cedfba")
	}
}

func TestDeobfuscate_GojaUnknownPlayer(t *testing.T) {
	// No signature transform present -> goja fails, regex fallback also fails.
	if _, err := DeobfuscateSignature("var x = 1;", "abc", ""); err == nil {
		t.Errorf("expected error for player without a transform")
	}
}

// fixtureNSig is a synthetic "n" throttling transform. It must NOT split on ""
// and rejoin (that marks the signature transform); instead it reverses the
// string via charAt, which is the characteristic n-function shape.
const fixtureNSig = `
function nsig(a){var r="";for(var i=a.length-1;i>=0;i--){r=r+a.charAt(i)}return r}
`

func TestDeobfuscateNSig_Goja(t *testing.T) {
	got, err := DeobfuscateNSig(fixtureNSig, "abcdef")
	if err != nil {
		t.Fatalf("DeobfuscateNSig: %v", err)
	}
	// charAt-based reverse of "abcdef"
	if got != "fedcba" {
		t.Errorf("nsig transform: got %q, want %q", got, "fedcba")
	}
}

func TestDeobfuscateNSig_NoTransform(t *testing.T) {
	// No n transform present -> goja returns an error (caller keeps original n).
	if _, err := DeobfuscateNSig("var x = 1;", "abc"); err == nil {
		t.Errorf("expected error when no n transform exists")
	}
}
