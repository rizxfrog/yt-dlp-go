package extractor

import (
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
)

// DataURL describes a decoded data: URI.
type DataURL struct {
	Data []byte
	MIME string // e.g. "image/png"; empty when the URI carried no type
	Ext  string // file extension without the dot, e.g. "png"
}

// DecodeDataURL parses a data: URI into its bytes and media type.
//
// Some sites inline preview images as data: URIs instead of serving them at a
// real URL: they are only materialised by the page's own JavaScript. Accepting
// them lets extractors expose those images as thumbnails, which the core can
// then write to disk directly instead of downloading.
//
// Both encodings are handled: base64 (the common case) and percent-encoded
// bytes. Non-image payloads still decode; Ext is then empty.
func DecodeDataURL(dataURL string) (*DataURL, error) {
	const prefix = "data:"
	if !strings.HasPrefix(dataURL, prefix) {
		return nil, fmt.Errorf("not a data URI")
	}
	header, payload, ok := strings.Cut(dataURL[len(prefix):], ",")
	if !ok {
		return nil, fmt.Errorf("malformed data URI")
	}
	parts := strings.Split(header, ";")
	mime := strings.TrimSpace(parts[0])

	isBase64 := false
	for _, p := range parts[1:] {
		if strings.EqualFold(strings.TrimSpace(p), "base64") {
			isBase64 = true
		}
	}

	var (
		data []byte
		err  error
	)
	if isBase64 {
		// Tolerate the padding and stray whitespace some pages emit. Python's
		// base64.b64decode also accepts unpadded payloads, so pad to a multiple
		// of 4 before decoding when necessary.
		clean := strings.Map(func(r rune) rune {
			switch r {
			case ' ', '\n', '\r', '\t':
				return -1
			}
			return r
		}, payload)
		if rem := len(clean) % 4; rem != 0 {
			clean += strings.Repeat("=", 4-rem)
		}
		data, err = base64.StdEncoding.DecodeString(clean)
	} else {
		data, err = percentDecodeBytes(payload)
	}
	if err != nil {
		return nil, err
	}
	return &DataURL{Data: data, MIME: mime, Ext: ExtForMIME(mime)}, nil
}

// ExtForMIME maps a media type to the file extension yt-dlp would use.
// Unknown image types fall back to "jpg"; non-image types return "".
func ExtForMIME(mime string) string {
	switch strings.ToLower(strings.TrimSpace(mime)) {
	case "image/png":
		return "png"
	case "image/webp":
		return "webp"
	case "image/gif":
		return "gif"
	case "image/bmp":
		return "bmp"
	case "image/svg+xml":
		return "svg"
	case "image/avif":
		return "avif"
	case "image/jpeg", "image/jpg", "image/jp2", "image/pjpeg":
		return "jpg"
	}
	if strings.HasPrefix(mime, "image/") {
		return "jpg"
	}
	return ""
}

// percentDecodeBytes decodes %XX escapes into raw bytes. Unlike
// url.PathUnescape it never mangles non-UTF-8 payloads, which matters for
// arbitrary binary content.
func percentDecodeBytes(s string) ([]byte, error) {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] != '%' {
			out = append(out, s[i])
			continue
		}
		if i+2 >= len(s) {
			return nil, fmt.Errorf("truncated escape at offset %d", i)
		}
		v, err := strconv.ParseUint(s[i+1:i+3], 16, 8)
		if err != nil {
			return nil, fmt.Errorf("bad escape %q", s[i:i+3])
		}
		out = append(out, byte(v))
		i += 2
	}
	return out, nil
}
