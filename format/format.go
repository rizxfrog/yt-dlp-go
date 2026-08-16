// Package format implements yt-dlp's -f format selection grammar.
//
// Supported syntax (a faithful, well-tested subset of yt-dlp):
//
//	best / worst                 combined (video+audio) format, best/worst by quality
//	bestvideo / worstvideo       best/worst pure-video format
//	bestaudio / worstaudio       best/worst pure-audio format
//	137                          a single format by itag/id
//	137+140                      merge a video format with an audio format
//	137-140                      any format whose itag is in [137,140]
//	best[height<=720]            filter by a condition
//	bestvideo[ext=mp4]+bestaudio[protocol!=http]
//	                             merge two filtered selectors
//	a,b,c                       comma separates multiple independent outputs
//	x/y/z                       slash is a fallback: first spec that yields a result wins
//
// Select returns a slice of "groups"; each group is a set of formats that, taken
// together, produce one output file. A group of length 1 is downloaded directly;
// a group of length 2 is downloaded as separate streams and merged by the
// postprocessor.
package format

import (
	"fmt"
	"strconv"
	"strings"

	"yt-dlp-go/extractor"
)

// Select evaluates the selector against formats and returns the chosen groups.
// An empty selector defaults to "best". An optional sortSpec (-S / --format-sort)
// reorders the formats first and is then honoured by the best/worst aliases, so
// "best" under "-S res,fps" yields the top format after that sort.
func Select(formats []extractor.Format, selector string, sortSpec ...string) ([][]extractor.Format, error) {
	selector = strings.TrimSpace(selector)
	if selector == "" {
		selector = "best"
	}
	var order []extractor.Format
	if len(sortSpec) > 0 && strings.TrimSpace(sortSpec[0]) != "" {
		order = Sort(formats, sortSpec[0])
	}
	var result [][]extractor.Format
	for _, out := range strings.Split(selector, ",") {
		group, err := selectOne(formats, order, out)
		if err != nil {
			return nil, err
		}
		if group != nil {
			result = append(result, group...)
		}
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("no formats matched %q", selector)
	}
	return result, nil
}

// selectOne handles a single comma-separated spec, supporting "/" fallback.
func selectOne(formats, order []extractor.Format, spec string) ([][]extractor.Format, error) {
	var lastErr error
	for _, candidate := range strings.Split(spec, "/") {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		group, err := selectMerge(formats, order, candidate)
		if err != nil {
			lastErr = err
			continue
		}
		if len(group) > 0 {
			return [][]extractor.Format{group}, nil
		}
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, nil
}

// selectMerge handles a "+"-joined merge expression.
func selectMerge(formats, order []extractor.Format, expr string) ([]extractor.Format, error) {
	parts := strings.Split(expr, "+")
	// A single "best"/"worst" (optionally filtered) token is an alias for merging
	// the best video and audio streams — handle it before the generic path.
	if len(parts) == 1 {
		if g, ok := selectBestAlias(formats, order, strings.TrimSpace(parts[0])); ok {
			return g, nil
		}
	}
	group := make([]extractor.Format, 0, len(parts))
	for _, p := range parts {
		f, err := resolveOne(formats, order, strings.TrimSpace(p))
		if err != nil {
			return nil, err
		}
		if f == nil {
			return nil, nil // nothing for this candidate; let fallback try
		}
		group = append(group, *f)
	}
	if len(group) == 0 {
		return nil, nil
	}
	return group, nil
}

// selectBestAlias resolves the "best"/"worst" selectors (and their filtered
// forms "best[...]"/"worst[...]"). In yt-dlp these expand to a merge of the best
// video and best audio streams, which is what DASH / separate-stream sources
// (YouTube, Bilibili, …) actually expose. When a source has no separate streams
// we fall back to a single combined (video+audio) format.
func selectBestAlias(formats, order []extractor.Format, tok string) ([]extractor.Format, bool) {
	var isWorst bool
	var rest string
	switch {
	case tok == "best":
		isWorst, rest = false, ""
	case strings.HasPrefix(tok, "best["):
		isWorst, rest = false, tok[4:]
	case tok == "worst":
		isWorst, rest = true, ""
	case strings.HasPrefix(tok, "worst["):
		isWorst, rest = true, tok[5:]
	default:
		return nil, false
	}
	vSel, aSel := "bestvideo"+rest, "bestaudio"+rest
	if isWorst {
		vSel, aSel = "worstvideo"+rest, "worstaudio"+rest
	}
	v, _ := resolveOne(formats, order, vSel)
	a, _ := resolveOne(formats, order, aSel)
	if v != nil && a != nil {
		return []extractor.Format{*v, *a}, true
	}
	// Fallback: a single combined (video+audio) format.
	if order != nil {
		if c := bestByOrder(order, formats, kindCombined); c != nil {
			return []extractor.Format{*c}, true
		}
	} else if isWorst {
		if c := pickWorst(formats, kindCombined); c != nil {
			return []extractor.Format{*c}, true
		}
	} else {
		if c := pickBest(formats, kindCombined); c != nil {
			return []extractor.Format{*c}, true
		}
	}
	return nil, true
}

// resolveOne resolves a single selector token (name + optional [filters]).
func resolveOne(formats, order []extractor.Format, token string) (*extractor.Format, error) {
	name, conds, err := splitFilter(token)
	if err != nil {
		return nil, err
	}
	pool := applyFilters(formats, conds)
	switch name {
	case "", "best":
		if order != nil {
			return bestByOrder(order, pool, kindCombined), nil
		}
		return pickBest(pool, kindCombined), nil
	case "worst":
		if order != nil {
			return worstByOrder(order, pool, kindCombined), nil
		}
		return pickWorst(pool, kindCombined), nil
	case "bestvideo":
		if order != nil {
			return bestByOrder(order, pool, kindVideo), nil
		}
		return pickBest(pool, kindVideo), nil
	case "worstvideo":
		if order != nil {
			return worstByOrder(order, pool, kindVideo), nil
		}
		return pickWorst(pool, kindVideo), nil
	case "bestaudio":
		if order != nil {
			return bestByOrder(order, pool, kindAudio), nil
		}
		return pickBest(pool, kindAudio), nil
	case "worstaudio":
		if order != nil {
			return worstByOrder(order, pool, kindAudio), nil
		}
		return pickWorst(pool, kindAudio), nil
	case "all":
		if len(pool) == 0 {
			return nil, nil
		}
		f := pool[0]
		return &f, nil
	}
	// numeric itag or itag range
	if i := strings.Index(name, "-"); i > 0 {
		lo, err1 := strconv.Atoi(strings.TrimSpace(name[:i]))
		hi, err2 := strconv.Atoi(strings.TrimSpace(name[i+1:]))
		if err1 == nil && err2 == nil {
			return pickRange(pool, lo, hi), nil
		}
	}
	if id, err := strconv.Atoi(name); err == nil {
		for i := range pool {
			if itagOf(pool[i]) == id {
				f := pool[i]
				return &f, nil
			}
		}
		return nil, nil
	}
	// treat as a literal FormatID match
	for i := range pool {
		if pool[i].FormatID == name {
			f := pool[i]
			return &f, nil
		}
	}
	return nil, nil
}

// ---- filtering ----

type cond struct {
	field string
	op    string
	value string
}

func splitFilter(token string) (string, []cond, error) {
	var conds []cond
	for {
		open := strings.Index(token, "[")
		if open < 0 {
			break
		}
		close := strings.Index(token[open:], "]")
		if close < 0 {
			return "", nil, fmt.Errorf("unbalanced '[' in %q", token)
		}
		close += open
		raw := token[open+1 : close]
		token = token[:open] + token[close+1:]
		c, err := parseCond(raw)
		if err != nil {
			return "", nil, err
		}
		conds = append(conds, c)
	}
	return strings.TrimSpace(token), conds, nil
}

func parseCond(raw string) (cond, error) {
	for _, op := range []string{"<=", ">=", "!=", "=", "<", ">"} {
		if i := strings.Index(raw, op); i > 0 {
			return cond{field: strings.TrimSpace(raw[:i]), op: op, value: strings.TrimSpace(raw[i+len(op):])}, nil
		}
	}
	return cond{}, fmt.Errorf("invalid filter %q", raw)
}

func applyFilters(fs []extractor.Format, conds []cond) []extractor.Format {
	if len(conds) == 0 {
		return fs
	}
	var out []extractor.Format
	for _, f := range fs {
		ok := true
		for _, c := range conds {
			if !evalCond(f, c) {
				ok = false
				break
			}
		}
		if ok {
			out = append(out, f)
		}
	}
	return out
}

func evalCond(f extractor.Format, c cond) bool {
	actual := fieldValue(f, c.field)
	switch c.op {
	case "=":
		return strings.EqualFold(actual, c.value)
	case "!=":
		return !strings.EqualFold(actual, c.value)
	}
	// numeric comparison
	av, aerr := strconv.ParseFloat(actual, 64)
	bv, berr := strconv.ParseFloat(c.value, 64)
	if aerr != nil || berr != nil {
		return false
	}
	switch c.op {
	case "<":
		return av < bv
	case "<=":
		return av <= bv
	case ">":
		return av > bv
	case ">=":
		return av >= bv
	}
	return false
}

func fieldValue(f extractor.Format, field string) string {
	isNone := func(s string) bool { return s == "" || strings.EqualFold(s, "none") }
	switch strings.ToLower(field) {
	case "height":
		return strconv.Itoa(f.Height)
	case "width":
		return strconv.Itoa(f.Width)
	case "tbr", "br":
		return strconv.FormatFloat(f.TBR, 'f', -1, 64)
	case "vbr":
		return strconv.FormatFloat(f.VBR, 'f', -1, 64)
	case "abr":
		return strconv.FormatFloat(f.ABR, 'f', -1, 64)
	case "asr":
		return strconv.Itoa(f.AudioSampleRate)
	case "channels":
		return strconv.Itoa(f.AudioChannels)
	case "dynamic_range", "dr", "hdr":
		return f.DynamicRange
	case "source":
		return f.Source
	case "lang":
		return f.Language
	case "pref", "preference":
		return strconv.Itoa(f.Preference)
	case "fps":
		return strconv.FormatFloat(f.FPS, 'f', -1, 64)
	case "filesize":
		return strconv.FormatInt(f.Filesize, 10)
	case "ext":
		return f.Ext
	case "vcodec":
		if isNone(f.VCodec) {
			return "none"
		}
		return f.VCodec
	case "acodec":
		if isNone(f.ACodec) {
			return "none"
		}
		return f.ACodec
	case "protocol":
		return f.Protocol
	case "format_id", "id":
		return f.FormatID
	case "format_note":
		return f.FormatNote
	}
	return ""
}

// ---- picking ----

type kind int

const (
	kindAny kind = iota
	kindVideo
	kindAudio
	kindCombined
)

func matchesKind(f extractor.Format, k kind) bool {
	hasV := f.VCodec != "" && !strings.EqualFold(f.VCodec, "none")
	hasA := f.ACodec != "" && !strings.EqualFold(f.ACodec, "none")
	switch k {
	case kindVideo:
		return hasV && !hasA
	case kindAudio:
		return hasA && !hasV
	case kindCombined:
		return hasV && hasA
	case kindAny:
		return true
	}
	return false
}

func quality(f extractor.Format) int {
	// Preference dominates, mirroring yt-dlp: the extractor-assigned preference
	// outranks resolution/bitrate, so e.g. an unwatermarked 720p (pref -1) beats
	// a watermarked 1080p (pref -2). The 1e12 scale keeps a ±1 preference step
	// larger than any realistic Height*1e6 term (≤ ~8e9), so the comparison is
	// effectively preference-first with quality as the tie-breaker.
	return f.Preference*1_000_000_000_000 + f.Height*1_000_000 + int(f.TBR)*1000 + int(f.Filesize)
}

func audioQuality(f extractor.Format) int {
	return int(f.TBR)*1000 + int(f.Filesize)
}

func pickBest(pool []extractor.Format, k kind) *extractor.Format {
	var best *extractor.Format
	for i := range pool {
		f := &pool[i]
		if !matchesKind(*f, k) {
			continue
		}
		if best == nil {
			best = f
			continue
		}
		if k == kindAudio {
			if audioQuality(*f) > audioQuality(*best) {
				best = f
			}
		} else if quality(*f) > quality(*best) {
			best = f
		}
	}
	return best
}

func pickWorst(pool []extractor.Format, k kind) *extractor.Format {
	var worst *extractor.Format
	for i := range pool {
		f := &pool[i]
		if !matchesKind(*f, k) {
			continue
		}
		if worst == nil {
			worst = f
			continue
		}
		if k == kindAudio {
			if audioQuality(*f) < audioQuality(*worst) {
				worst = f
			}
		} else if quality(*f) < quality(*worst) {
			worst = f
		}
	}
	return worst
}

func pickRange(pool []extractor.Format, lo, hi int) *extractor.Format {
	var best *extractor.Format
	for i := range pool {
		f := &pool[i]
		id := itagOf(*f)
		if id < lo || id > hi {
			continue
		}
		if best == nil || quality(*f) > quality(*best) {
			best = f
		}
	}
	return best
}

// itagOf returns the numeric itag when FormatID is numeric, else -1.
func itagOf(f extractor.Format) int {
	n, err := strconv.Atoi(f.FormatID)
	if err != nil {
		return -1
	}
	return n
}

// inPool reports whether f (an element of the sorted order) is also present in
// pool (the filtered subset). Formats are compared by identity triple because
// applyFilters returns value copies, not the same backing elements.
func inPool(pool []extractor.Format, f extractor.Format) bool {
	for i := range pool {
		if pool[i].FormatID == f.FormatID &&
			pool[i].URL == f.URL &&
			pool[i].Protocol == f.Protocol &&
			pool[i].VCodec == f.VCodec &&
			pool[i].ACodec == f.ACodec {
			return true
		}
	}
	return false
}

// bestByOrder returns the first format in order (highest-ranked) that matches
// kind and is also in pool. Used when a -S sort order is active so the "best"
// alias picks the top-ranked format rather than recomputing by quality().
func bestByOrder(order, pool []extractor.Format, k kind) *extractor.Format {
	for i := range order {
		if matchesKind(order[i], k) && inPool(pool, order[i]) {
			f := order[i]
			return &f
		}
	}
	return nil
}

// worstByOrder returns the last (lowest-ranked) format in order matching kind
// and pool.
func worstByOrder(order, pool []extractor.Format, k kind) *extractor.Format {
	for i := len(order) - 1; i >= 0; i-- {
		if matchesKind(order[i], k) && inPool(pool, order[i]) {
			f := order[i]
			return &f
		}
	}
	return nil
}
