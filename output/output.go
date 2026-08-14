// Package output implements yt-dlp's -o output template engine: rendering a
// filename template such as "%(title)s-%(id)s.%(ext)s" from an Info.
//
// Supported template syntax:
//
//	%(field)s                 insert a field (title, id, uploader, upload_date,
//	                            description, ext, webpage_url, thumbnail,
//	                            duration, epoch, ... or any path into Info.Raw)
//	%(field|default)s         fallback value when the field is empty
//	%(field)l / %(field)u     lowercase / uppercase the result
//	%(upload_date>%Y-%m-%d)s  strftime formatting of a date field
//	%(duration>%H:%M:%S)s     render a seconds value as HH:MM:SS
//	%%                         a literal percent sign
//
// Sanitize strips characters that are illegal in filenames.
package output

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"yt-dlp-go/extractor"
)

var tmplRE = regexp.MustCompile(`%\(([^)]*)\)([a-zA-Z])`)

// Render expands template against info and returns the resulting filename body
// (without directory; the caller joins it with the output directory).
func Render(template string, info *extractor.Info) (string, error) {
	var sb strings.Builder
	last := 0
	for _, m := range tmplRE.FindAllStringSubmatchIndex(template, -1) {
		spec := template[m[2]:m[3]]
		conv := template[m[4]:m[5]]
		sb.WriteString(template[last:m[0]])
		val, err := resolveSpec(info, spec, conv)
		if err != nil {
			return "", err
		}
		sb.WriteString(val)
		last = m[1]
	}
	sb.WriteString(template[last:])
	// literal %%
	return strings.ReplaceAll(sb.String(), "%%", "%"), nil
}

// resolveSpec handles one %(...)X token.
func resolveSpec(info *extractor.Info, spec, conv string) (string, error) {
	// split off a strftime-style format after '>'
	format := ""
	if i := strings.Index(spec, ">"); i >= 0 {
		format = spec[i+1:]
		spec = spec[:i]
	}
	// split off a default after '|'
	def := ""
	if i := strings.Index(spec, "|"); i >= 0 {
		def = spec[i+1:]
		spec = spec[:i]
	}

	val, ok := resolveField(info, spec)
	if !ok || val == "" {
		val = def
	}

	// Date / duration formatting.
	if format != "" {
		if spec == "duration" || strings.HasPrefix(spec, "duration") {
			if dur, perr := strconv.ParseFloat(val, 64); perr == nil {
				val = formatDuration(dur, format)
			}
		} else if t, terr := parseDate(val); terr == nil {
			val = t.Format(strftimeToGo(format))
		}
	}

	switch conv {
	case "u":
		val = strings.ToUpper(val)
	case "l":
		val = strings.ToLower(val)
	}
	return val, nil
}

// resolveField returns the string value of a field name from Info.
func resolveField(info *extractor.Info, field string) (string, bool) {
	switch field {
	case "id":
		return info.ID, info.ID != ""
	case "title":
		return info.Title, info.Title != ""
	case "uploader", "channel":
		return info.Uploader, info.Uploader != ""
	case "upload_date", "uploader_id":
		return info.UploadDate, info.UploadDate != ""
	case "description":
		return info.Description, info.Description != ""
	case "ext":
		return info.Ext, info.Ext != ""
	case "webpage_url":
		return info.WebpageURL, info.WebpageURL != ""
	case "thumbnail":
		return info.Thumbnail, info.Thumbnail != ""
	case "duration":
		if info.Duration > 0 {
			return strconv.FormatFloat(info.Duration, 'f', 0, 64), true
		}
		return "", false
	case "epoch":
		return strconv.FormatInt(time.Now().Unix(), 10), true
	}
	// Fall back to a path into Info.Raw (e.g. raw.videoDetails.author).
	if info.Raw != nil {
		if strings.HasPrefix(field, "raw.") {
			field = field[len("raw."):]
		}
		parts := strings.Split(field, ".")
		var cur any = info.Raw
		for _, p := range parts {
			m, ok := cur.(map[string]any)
			if !ok {
				return "", false
			}
			cur = m[p]
		}
		if cur != nil {
			return strconvAny(cur), true
		}
	}
	return "", false
}

func strconvAny(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		if x == float64(int64(x)) {
			return strconv.FormatInt(int64(x), 10)
		}
		return strconv.FormatFloat(x, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(x)
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", x)
	}
}

func parseDate(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	for _, l := range []string{"20060102", "2006-01-02", time.RFC3339, "2006-01-02T15:04:05"} {
		if t, err := time.Parse(l, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("not a date: %q", s)
}

// strftimeToGo converts a small subset of strftime specifiers to Go's reference
// layout. Supported: %Y %y %m %d %H %M %S %p.
func strftimeToGo(s string) string {
	repl := map[string]string{
		"%Y": "2006", "%y": "06", "%m": "01", "%d": "02",
		"%H": "15", "%M": "04", "%S": "05", "%p": "PM",
	}
	for k, v := range repl {
		s = strings.ReplaceAll(s, k, v)
	}
	return s
}

// formatDuration renders seconds as HH:MM:SS (or H:MM:SS), honouring the
// strftime-style %H:%M:%S pattern used by yt-dlp.
func formatDuration(sec float64, layout string) string {
	total := int(sec)
	h := total / 3600
	m := (total % 3600) / 60
	s := total % 60
	if strings.Contains(layout, "%H:%M:%S") {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	if strings.Contains(layout, "%M:%S") {
		return fmt.Sprintf("%d:%02d", m, s)
	}
	return fmt.Sprintf("%d:%02d:%02d", h, m, s)
}

// Sanitize replaces characters that are illegal in common filesystems. The path
// separators '/' and '\' ARE rewritten to '_', so this is only safe to apply to a
// single filename component — see SanitizePath for whole paths.
func Sanitize(s string) string {
	r := strings.NewReplacer("/", "_", "\\", "_", ":", "_", "*", "_", "?", "_", "\"", "_", "<", "_", ">", "_", "|", "_")
	return r.Replace(s)
}

// SanitizePath sanitizes a full output path while preserving directory
// separators, so templates such as "%(uploader)s/%(id)s.%(ext)s" keep their
// subdirectories instead of having '/' rewritten to '_'. Each path segment is
// cleaned individually; the separators themselves are left intact.
func SanitizePath(p string) string {
	split := func(r rune) bool { return r == '/' || r == '\\' }
	parts := strings.FieldsFunc(p, split)
	for i, part := range parts {
		parts[i] = Sanitize(part)
	}
	return strings.Join(parts, "/")
}
