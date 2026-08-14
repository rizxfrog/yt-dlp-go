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

// fixtureNSigCallSite proves call-site localization: a decoy n-shaped function
// (decoy) appears EARLIER in the source and would be chosen by the old
// body-shape scan, but it is never self-reassigned. The real n-transform
// (nTransform) is applied by the player via `n = nTransform(n)`, which is the
// precise call-site signal. nTransform("abcdefgh") = "cde".
const fixtureNSigCallSite = `
function decoy(a){a=a.slice(1);return a.replace(/x/g,"")}
function nTransform(a){a=a.charAt(0)+a.slice(1);a=a.substr(2,3);return a}
function applyThrottle(n){n=nTransform(n);return n}
`

func TestDeobfuscateNSig_CallSite(t *testing.T) {
	got, err := DeobfuscateNSig(fixtureNSigCallSite, "abcdefgh")
	if err != nil {
		t.Fatalf("DeobfuscateNSig (call site): %v", err)
	}
	// Must pick the APPLIED transform (nTransform -> "cde"), not the decoy
	// (which would yield "bcdefgh"). This demonstrates call-site localization.
	if got != "cde" {
		t.Errorf("call-site nsig: got %q, want %q (decoy would give %q)", got, "cde", "bcdefgh")
	}
}

// fixtureNSigModule exercises the module-form call site `x = (0, MOD.NAME)(x)`.
// NMod.throttle("abcdefgh") = "edefgh".
const fixtureNSigModule = `
var NMod={throttle:function(a){a=a.slice(3);a=a.charAt(1)+a;return a}};
function apply(n){n=(0,NMod.throttle)(n);return n}
`

func TestDeobfuscateNSig_ModuleCallSite(t *testing.T) {
	got, err := DeobfuscateNSig(fixtureNSigModule, "abcdefgh")
	if err != nil {
		t.Fatalf("DeobfuscateNSig (module call site): %v", err)
	}
	if got != "edefgh" {
		t.Errorf("module call-site nsig: got %q, want %q", got, "edefgh")
	}
}
