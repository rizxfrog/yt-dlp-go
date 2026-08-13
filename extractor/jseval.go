package extractor

import (
	"fmt"
	"regexp"
	"strings"
)

// This file implements YouTube's "signature deobfuscation" the same way yt-dlp
// classically did it: NOT by running a full JavaScript engine, but by extracting
// the transformation function and the helper operations from the player source
// with regexes, classifying each operation (reverse / slice / swap), and
// replaying them over the signature string in Go.
//
// PRODUCTION NOTE: modern YouTube changes this structure frequently and the
// regex approach is brittle. For full coverage, swap this for an embedded JS
// engine (github.com/dop251/goja or cgo quickjs) and simply evaluate the
// player's function directly. The DeobfuscateSignature entry point is the seam
// where that swap would happen.

type sigOp struct {
	kind string // "reverse" | "slice" | "swap"
	arg  int
}

var (
	mainFuncRE = regexp.MustCompile(`function\s+([A-Za-z_$][\w$]*)\([a-z]\)\{[a-z]=[a-z]\.split\(""\);(.+?);return [a-z]\.join\(""\)\}`)
	callRE     = regexp.MustCompile(`([A-Za-z_$][\w$]*)\.([A-Za-z_$][\w$]*)\([a-z],\s*(\d+)\)`)
	helperRE   = regexp.MustCompile(`([A-Za-z_$][\w$]*)\.([A-Za-z_$][\w$]*)\s*=\s*function\(([a-z]),?\s*([a-z])?\)\{(.+?)\}`)
)

// DeobfuscateSignature applies the player's signature transform to sig.
func DeobfuscateSignature(playerJS, sig string) (string, error) {
	ops, err := extractOps(playerJS)
	if err != nil {
		return "", err
	}
	s := []rune(sig)
	for _, op := range ops {
		switch op.kind {
		case "reverse":
			for i, j := 0, len(s)-1; i < j; i, j = i+1, j-1 {
				s[i], s[j] = s[j], s[i]
			}
		case "slice":
			if op.arg < len(s) {
				s = s[op.arg:]
			}
		case "swap":
			if op.arg >= 0 && op.arg < len(s) {
				s[0], s[op.arg] = s[op.arg], s[0]
			}
		}
	}
	return string(s), nil
}

// extractOps pulls the ordered list of operations out of the player source.
func extractOps(js string) ([]sigOp, error) {
	m := mainFuncRE.FindStringSubmatch(js)
	if m == nil {
		return nil, fmt.Errorf("could not locate signature function (player format may have changed; use goja for full coverage)")
	}
	body := m[2]

	// Build a lookup of helper method -> body so we can classify each call.
	helpers := map[string]string{}
	for _, h := range helperRE.FindAllStringSubmatch(js, -1) {
		obj, method, hbody := h[1], h[2], h[5]
		helpers[obj+"."+method] = hbody
	}

	var ops []sigOp
	for _, c := range callRE.FindAllStringSubmatch(body, -1) {
		key := c[1] + "." + c[2]
		arg := atoiSafe(c[3])
		hbody, ok := helpers[key]
		if !ok {
			return nil, fmt.Errorf("unknown signature helper %q", key)
		}
		kind := classify(hbody)
		if kind == "" {
			return nil, fmt.Errorf("unrecognised signature operation in helper %q", key)
		}
		ops = append(ops, sigOp{kind: kind, arg: arg})
	}
	if len(ops) == 0 {
		return nil, fmt.Errorf("no signature operations extracted")
	}
	return ops, nil
}

// classify inspects a helper function body and returns its operation kind.
func classify(body string) string {
	b := strings.ToLower(body)
	switch {
	case strings.Contains(b, ".reverse()"):
		return "reverse"
	case strings.Contains(b, ".splice(") || strings.Contains(b, ".slice("):
		return "slice"
	case strings.Contains(b, "=a[0]") || strings.Contains(b, "=a[b") || strings.Contains(b, "[0]="):
		return "swap"
	default:
		return ""
	}
}

func atoiSafe(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			break
		}
		n = n*10 + int(r-'0')
	}
	return n
}
