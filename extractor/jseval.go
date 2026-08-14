package extractor

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/dop251/goja"
)

// This file implements YouTube's "signature deobfuscation". We evaluate the
// player's transform function in an embedded JavaScript interpreter (goja, a
// pure-Go ES5+ engine) so that the obfuscation logic runs exactly as written by
// YouTube — no fragile regex classification is needed for the common case.
//
// The previous regex/classify pipeline is retained as a best-effort fallback for
// player sources that cannot be extracted/evaluated (it is only reached when the
// goja path errors). The DeobfuscateSignature entry point is unchanged, so the
// YouTube extractor and tests need no edits.

// DeobfuscateSignature reverses a YouTube player's signature transform.
// Primary path: evaluate the player function in goja. Fallback: regex pipeline.
// sts (the signature timestamp from the player response) is passed as the
// second argument when non-empty, matching modern player functions of the form
// function(a, b){ ... } where b is the timestamp.
func DeobfuscateSignature(playerJS, sig, sts string) (string, error) {
	var extra []string
	if sts != "" {
		extra = []string{sts}
	}
	if out, err := deobfuscateGoja(playerJS, sig, extra, findTransform); err == nil {
		return out, nil
	}
	return deobfuscateRegex(playerJS, sig)
}

// DeobfuscateNSig reverses a YouTube player's "n" (throttling) transform using
// the same goja engine. The n function is identified separately from the
// signature transform (see findNTransform).
func DeobfuscateNSig(playerJS, n string) (string, error) {
	return deobfuscateGoja(playerJS, n, nil, findNTransform)
}

// ---------------------------------------------------------------------------
// goja-based evaluation
// ---------------------------------------------------------------------------

var (
	reFuncDecl   = regexp.MustCompile(`function\s+([A-Za-z_$][\w$]*)\s*\(`)
	reVarFunc    = regexp.MustCompile(`var\s+([A-Za-z_$][\w$]*)\s*=\s*function`)
	reAssignFunc = regexp.MustCompile(`(?:^|[;{]\s*)([A-Za-z_$][\w$]*)\s*=\s*function`)
	reVarObj     = regexp.MustCompile(`var\s+([A-Za-z_$][\w$]*)\s*=\s*\{`)
	reAssignObj  = regexp.MustCompile(`(?:^|[;{]\s*)([A-Za-z_$][\w$]*)\s*=\s*\{`)
	reCall       = regexp.MustCompile(`([A-Za-z_$][\w$]*)\s*\(`)
	reMemberCall = regexp.MustCompile(`([A-Za-z_$][\w$]*)\.([A-Za-z_$][\w$]*)\s*\(`)
	// Call-site of the n-transform: an in-place self-reassigning call of the
	// form `x = NAME(x)` (same identifier on both sides, single argument). This
	// is the precise signal of where YouTube applies the n throttling transform.
	reSelfCall = regexp.MustCompile(`([A-Za-z_$][\w$]*)\s*=\s*([A-Za-z_$][\w$]*)\s*\(\s*([A-Za-z_$][\w$]*)\s*\)`)
	// Module form of the same call site: `x = (0, MOD.NAME)(x)`.
	reSelfCallMember = regexp.MustCompile(`([A-Za-z_$][\w$]*)\s*=\s*\(0,\s*([A-Za-z_$][\w$]*)\.([A-Za-z_$][\w$]*)\)\s*\(\s*([A-Za-z_$][\w$]*)\s*\)`)
)

// collectDefs scans the player source for top-level function and object
// definitions and returns name -> full definition text (brace-matched).
func collectDefs(src string) map[string]string {
	defs := map[string]string{}
	add := func(re *regexp.Regexp) {
		for _, m := range re.FindAllStringSubmatchIndex(src, -1) {
			name := src[m[2]:m[3]]
			headerStart := m[0]
			bodyOpen := strings.Index(src[headerStart:], "{")
			if bodyOpen < 0 {
				continue
			}
			openIdx := headerStart + bodyOpen
			end, ok := extractBlock(src, openIdx)
			if !ok {
				continue
			}
			defs[name] = src[headerStart : end+1]
		}
	}
	add(reFuncDecl)
	add(reVarFunc)
	add(reAssignFunc)
	add(reVarObj)
	add(reAssignObj)
	return defs
}

// extractBlock returns the index of the brace matching the one at openIdx
// ('{' or '('), tracking strings, template literals, and comments.
func extractBlock(src string, openIdx int) (int, bool) {
	opener := src[openIdx]
	depth := 0
	inStr := false
	var quote byte
	for i := openIdx; i < len(src); i++ {
		c := src[i]
		if inStr {
			if c == '\\' {
				i++
				continue
			}
			if c == quote {
				inStr = false
			}
			continue
		}
		switch c {
		case '"', '\'', '`':
			inStr = true
			quote = c
		case '/':
			if i+1 < len(src) && src[i+1] == '/' {
				for i < len(src) && src[i] != '\n' {
					i++
				}
				i--
			} else if i+1 < len(src) && src[i+1] == '*' {
				i += 2
				for i+1 < len(src) && !(src[i] == '*' && src[i+1] == '/') {
					i++
				}
			}
		case '{':
			if opener == '{' {
				depth++
			}
		case '}':
			if opener == '{' {
				depth--
				if depth == 0 {
					return i, true
				}
			}
		case '(':
			if opener == '(' {
				depth++
			}
		case ')':
			if opener == '(' {
				depth--
				if depth == 0 {
					return i, true
				}
			}
		}
	}
	return -1, false
}

// isTransform reports whether a definition looks like the signature transform:
// it splits a string into a char array and rejoins it, optionally via inline
// reverse/slice/swap or via helper-object method calls (modern YouTube style).
func isTransform(text string) bool {
	if !strings.Contains(text, "split(\"\"") || !strings.Contains(text, "join(\"\"") {
		return false
	}
	b := strings.ToLower(text)
	if strings.Contains(b, ".reverse()") ||
		strings.Contains(b, ".slice(") ||
		strings.Contains(b, ".splice(") ||
		strings.Contains(b, "[0]=") ||
		strings.Contains(b, "[b]=") {
		return true
	}
	// helper-based transform: calls methods on an object (e.g. V.fS(a))
	return reMemberCall.MatchString(text)
}

// inlineMarker reports whether a definition performs the transform inline.
func inlineMarker(text string) bool {
	b := strings.ToLower(text)
	return strings.Contains(b, ".reverse()") ||
		strings.Contains(b, ".slice(") ||
		strings.Contains(b, ".splice(") ||
		strings.Contains(b, "[0]=") ||
		strings.Contains(b, "[b]=")
}

// findTransform returns the name and full text of the signature transform,
// scanning the source in order so the choice is deterministic.
func findTransform(src string) (name, text string) {
	scans := []*regexp.Regexp{reFuncDecl, reVarFunc, reAssignFunc}
	type cand struct {
		name, text string
		idx        int
	}
	var cands []cand
	for _, re := range scans {
		for _, m := range re.FindAllStringSubmatchIndex(src, -1) {
			nm := src[m[2]:m[3]]
			headerStart := m[0]
			bo := strings.Index(src[headerStart:], "{")
			if bo < 0 {
				continue
			}
			end, ok := extractBlock(src, headerStart+bo)
			if !ok {
				continue
			}
			t := src[headerStart : end+1]
			if isTransform(t) {
				cands = append(cands, cand{nm, t, m[0]})
			}
		}
	}
	if len(cands) == 0 {
		return "", ""
	}
	// Prefer an inline transform (its body contains the operations directly).
	for _, c := range cands {
		if inlineMarker(c.text) {
			return c.name, c.text
		}
	}
	return cands[0].name, cands[0].text
}

// findNTransform returns the name and full text of the "n" (throttling) transform.
// It is a different function from the signature transform: it operates on the n
// query parameter (typically with charAt / substr / substring / slice and a
// return), and crucially does NOT split a string on "" and rejoin it (that is the
// signature transform).
//
// Localization strategy:
//  1. Primary — CALL SITE. Rather than scanning every function definition and
//     guessing by body shape, we locate where the player actually APPLIES the
//     n-transform: an in-place self-reassigning call `x = NAME(x)` (or the module
//     form `x = (0, MOD.NAME)(x)`). This is a low-false-positive signal because
//     YouTube rewrites the n value in place. We resolve NAME to its definition and
//     confirm it is n-shaped.
//  2. Fallback — body shape. Only if no call site is found do we fall back to the
//     older scan-every-definition heuristic.
func findNTransform(src string) (name, text string) {
	if n, t := findNByCallSite(src); n != "" {
		return n, t
	}
	return findNByBody(src)
}

// findNByCallSite locates the n-transform via its application call site.
func findNByCallSite(src string) (string, string) {
	defs := collectDefs(src)
	// Plain form: x = NAME(x)
	for _, m := range reSelfCall.FindAllStringSubmatch(src, -1) {
		lhs, fnName, arg := m[1], m[2], m[3]
		if lhs != arg {
			continue // must be a self-reassigning call
		}
		if t, ok := resolveNShaped(defs, fnName); ok {
			return fnName, t
		}
	}
	// Module form: x = (0, MOD.NAME)(x)
	for _, m := range reSelfCallMember.FindAllStringSubmatch(src, -1) {
		lhs, modName, fnName, arg := m[1], m[2], m[3], m[4]
		if lhs != arg {
			continue
		}
		member := modName + "." + fnName
		if objDef, ok := defs[modName]; ok {
			if t, ok := findMethodInObj(objDef, fnName); ok && isNTransform(t) {
				return member, t
			}
		}
	}
	return "", ""
}

// resolveNShaped returns the definition of name if it exists and is n-shaped.
func resolveNShaped(defs map[string]string, name string) (string, bool) {
	t, ok := defs[name]
	if !ok || !isNTransform(t) {
		return "", false
	}
	return t, true
}

// findMethodInObj returns the method body for `method` within an object-literal
// definition (e.g. `var M = { fo: function(a){...}, ... }`).
func findMethodInObj(objDef, method string) (string, bool) {
	re := regexp.MustCompile(`(?:^|[,{]\s*)` + regexp.QuoteMeta(method) + `\s*[:=]\s*function\s*\(`)
	ms := re.FindStringSubmatchIndex(objDef)
	if ms == nil {
		// ES6 shorthand `method(a){...}` without `function`
		re2 := regexp.MustCompile(`(?:^|[,{]\s*)` + regexp.QuoteMeta(method) + `\s*\(`)
		ms = re2.FindStringSubmatchIndex(objDef)
		if ms == nil {
			return "", false
		}
	}
	headerStart := ms[0]
	bo := strings.Index(objDef[headerStart:], "{")
	if bo < 0 {
		return "", false
	}
	end, ok := extractBlock(objDef, headerStart+bo)
	if !ok {
		return "", false
	}
	return objDef[headerStart : end+1], true
}

// findNByBody is the legacy fallback: scan function definitions in source order
// and pick the first whose body is n-shaped.
func findNByBody(src string) (string, string) {
	scans := []*regexp.Regexp{reFuncDecl, reVarFunc, reAssignFunc}
	type cand struct {
		name, text string
		idx        int
	}
	var cands []cand
	for _, re := range scans {
		for _, m := range re.FindAllStringSubmatchIndex(src, -1) {
			nm := src[m[2]:m[3]]
			headerStart := m[0]
			bo := strings.Index(src[headerStart:], "{")
			if bo < 0 {
				continue
			}
			end, ok := extractBlock(src, headerStart+bo)
			if !ok {
				continue
			}
			t := src[headerStart : end+1]
			if isNTransform(t) {
				cands = append(cands, cand{nm, t, m[0]})
			}
		}
	}
	if len(cands) == 0 {
		return "", ""
	}
	return cands[0].name, cands[0].text
}

// isNTransform reports whether a definition looks like the n throttling transform.
func isNTransform(text string) bool {
	b := strings.ToLower(text)
	// Exclude the signature transform (splits on "" and rejoins).
	if strings.Contains(b, "split(\"\"") && strings.Contains(b, "join(\"\"") {
		return false
	}
	hasCharOp := strings.Contains(b, "charat") ||
		strings.Contains(b, "substr") ||
		strings.Contains(b, "substring") ||
		strings.Contains(b, ".slice(")
	hasReturn := strings.Contains(b, "return")
	return hasCharOp && hasReturn
}

// referencedNames returns top-level identifiers referenced inside text, either
// as `foo(` calls or `obj.method(` member calls. These are candidates for the
// helper definitions that must be embedded alongside the transform.
func referencedNames(text string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(n string) {
		if n != "" && !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	for _, m := range reMemberCall.FindAllStringSubmatch(text, -1) {
		add(m[1]) // the object whose method is called
	}
	for _, m := range reCall.FindAllStringSubmatch(text, -1) {
		switch m[1] {
		case "if", "for", "while", "function", "return", "switch", "catch",
			"typeof", "do", "else", "var", "new", "with", "await", "delete":
			continue
		}
		add(m[1])
	}
	return out
}

// deobfuscateGoja evaluates a player transform (located by findFn) in goja.
// arg is the primary input (signature or n); extra holds optional additional
// arguments such as the sts timestamp. The function extracts the transform and
// its (recursively) referenced helpers, defines them, then calls the transform.
func deobfuscateGoja(playerJS, arg string, extra []string, findFn func(string) (string, string)) (string, error) {
	name, _ := findFn(playerJS)
	if name == "" {
		return "", fmt.Errorf("no transform found")
	}
	defs := collectDefs(playerJS)

	included := map[string]bool{}
	var prog strings.Builder
	var expand func(n string)
	expand = func(n string) {
		if included[n] {
			return
		}
		included[n] = true
		if d, ok := defs[n]; ok {
			prog.WriteString(d)
			prog.WriteString("\n")
			for _, ref := range referencedNames(d) {
				expand(ref)
			}
			return
		}
		// Member call such as "M.fo": the definition lives on the parent object
		// M, so embed that object instead (its method is reachable as M.fo).
		if dot := strings.Index(n, "."); dot > 0 {
			expand(n[:dot])
		}
	}
	expand(name)

	vm := goja.New()
	if _, err := vm.RunString(prog.String()); err != nil {
		return "", fmt.Errorf("goja define: %w", err)
	}
	args := append([]string{jsonString(arg)}, quoteAll(extra)...)
	v, err := vm.RunString(fmt.Sprintf("%s(%s)", name, strings.Join(args, ",")))
	if err != nil {
		return "", fmt.Errorf("goja call: %w", err)
	}
	return v.String(), nil
}

func quoteAll(ss []string) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = jsonString(s)
	}
	return out
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// ---------------------------------------------------------------------------
// regex/classify fallback (best effort)
// ---------------------------------------------------------------------------

type sigOp struct {
	kind string // "reverse" | "slice" | "swap"
	arg  int
}

var (
	mainFuncRE = regexp.MustCompile(`function\s+([A-Za-z_$][\w$]*)\([a-z]\)\{[a-z]=[a-z]\.split\(""\);(.+?);return [a-z]\.join\(""\)\}`)
	callRE     = regexp.MustCompile(`([A-Za-z_$][\w$]*)\.([A-Za-z_$][\w$]*)\([a-z],\s*(\d+)\)`)
	helperRE   = regexp.MustCompile(`([A-Za-z_$][\w$]*)\.([A-Za-z_$][\w$]*)\s*=\s*function\(([a-z]),?\s*([a-z])?\)\{(.+?)\}`)
	reSlice    = regexp.MustCompile(`\.slice\((\d+)\)`)
)

// deobfuscateRegex replays the transform via regex classification. Used only
// when the goja path fails.
func deobfuscateRegex(playerJS, sig string) (string, error) {
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

func extractOps(js string) ([]sigOp, error) {
	m := mainFuncRE.FindStringSubmatch(js)
	if m == nil {
		return nil, fmt.Errorf("could not locate signature function")
	}
	body := m[2]

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
		// No helper calls inside the body: modern players inline everything.
		// Fall back to scanning the body for inlined operations.
		return scanInlineOps(body)
	}
	return ops, nil
}

// scanInlineOps handles transforms that inline reverse/slice/swap directly
// (no helper method calls), preserving textual order.
func scanInlineOps(body string) ([]sigOp, error) {
	var ops []sigOp
	for i := 0; i < len(body); {
		switch {
		case strings.HasPrefix(body[i:], ".reverse()"):
			ops = append(ops, sigOp{kind: "reverse"})
			i += len(".reverse()")
		case body[i] == '.':
			if mm := reSlice.FindStringSubmatch(body[i:]); mm != nil {
				ops = append(ops, sigOp{kind: "slice", arg: atoiSafe(mm[1])})
				i += len(mm[0])
				continue
			}
			i++
		case body[i] == '[' && i+1 < len(body) && body[i+1] == '0':
			// [0]=... swap marker
			ops = append(ops, sigOp{kind: "swap", arg: 0})
			i++
		default:
			i++
		}
	}
	if len(ops) == 0 {
		return nil, fmt.Errorf("no signature operations extracted")
	}
	return ops, nil
}

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
	n, _ := strconv.Atoi(s)
	return n
}
