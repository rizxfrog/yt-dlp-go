// Package network builds the configured *http.Client used throughout yt-dlp-go.
//
// It supports the features the engine needs: default headers, HTTP(S)/SOCKS
// proxy, cookie-file loading (Netscape/Mozilla format), TLS certificate
// verification toggling, and browser "impersonation" via header sets.
//
// NOTE on TLS fingerprint impersonation: yt-dlp can mimic a real browser's
// TLS ClientHello (curve/extension order) through curl_cffi. The Go standard
// library cannot alter the ClientHello, so to reach parity you would swap the
// transport's TLS dialer for one backed by github.com/refraction-networking/utls
// (or bogdanfinn/tls-client). The Impersonate option here sets the matching
// *headers* and leaves a clearly marked hook for that dialer.
package network

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"yt-dlp-go/options"
)

// ClientConfig is the resolved configuration for a client.
type ClientConfig struct {
	UserAgent     string
	Proxy         string
	CookiesFile   string
	NoCheckCerts  bool
	Impersonate   string
	SocketTimeout time.Duration
	AddHeaders    map[string]string
}

// FromOptions derives a ClientConfig from parsed Options.
func FromOptions(o *options.Options) *ClientConfig {
	return &ClientConfig{
		UserAgent:     o.UserAgent,
		Proxy:         o.Proxy,
		CookiesFile:   o.CookiesFile,
		NoCheckCerts:  o.NoCheckCerts,
		Impersonate:   o.ImpersonateTarget(),
		SocketTimeout: o.SocketTimeout,
		AddHeaders:    o.AddHeaders,
	}
}

// impersonateHeaders returns the default header set used to mimic a browser.
// (TLS ClientHello parity requires the utls dialer mentioned in the package doc.)
func impersonateHeaders(target string) map[string]string {
	switch target {
	case "firefox":
		return map[string]string{
			"User-Agent":      "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:121.0) Gecko/20100101 Firefox/121.0",
			"Accept":          "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,*/*;q=0.8",
			"Accept-Language": "en-US,en;q=0.5",
		}
	case "safari":
		return map[string]string{
			"User-Agent":      "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Safari/605.1.15",
			"Accept":          "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8",
			"Accept-Language": "en-US,en;q=0.9",
		}
	case "chrome", "edge":
		fallthrough
	default:
		return map[string]string{
			"User-Agent":      "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
			"Accept":          "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8",
			"Accept-Language": "en-US,en;q=0.9",
		}
	}
}

// headerTransport injects default + per-request headers onto every request.
type headerTransport struct {
	rt      http.RoundTripper
	headers http.Header
}

func (t *headerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	for k := range t.headers {
		if req.Header.Get(k) == "" {
			req.Header.Set(k, t.headers.Get(k))
		}
	}
	return t.rt.RoundTrip(req)
}

// buildHeaderTransport wraps rt, injecting default and caller-supplied headers.
func buildHeaderTransport(rt http.RoundTripper, cfg *ClientConfig) http.RoundTripper {
	h := http.Header{}
	mergeInto(h, impersonateHeaders(cfg.Impersonate))
	if cfg.UserAgent != "" {
		h.Set("User-Agent", cfg.UserAgent)
	}
	mergeInto(h, cfg.AddHeaders)
	return &headerTransport{rt: rt, headers: h}
}

func mergeInto(h http.Header, m map[string]string) {
	for k, v := range m {
		h.Set(k, v)
	}
}

// timeoutDialer enforces a connect timeout, mirroring yt-dlp --socket-timeout.
type timeoutDialer struct{ timeout time.Duration }

func (d *timeoutDialer) dial(ctx context.Context, network, addr string) (net.Conn, error) {
	dialer := &net.Dialer{Timeout: d.timeout}
	return dialer.DialContext(ctx, network, addr)
}

// NewClient constructs the engine's HTTP client from a ClientConfig.
func NewClient(cfg *ClientConfig) (*http.Client, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	if cfg.CookiesFile != "" {
		if err := loadCookiesFile(jar, cfg.CookiesFile); err != nil {
			return nil, fmt.Errorf("loading cookies: %w", err)
		}
	}

	transport := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: cfg.NoCheckCerts},
		Proxy:           proxyFromConfig(cfg.Proxy),
		DialContext:     (&timeoutDialer{timeout: cfg.SocketTimeout}).dial,
	}
	// configureTLS is a build-tagged hook: with `-tags utls` it installs a
	// utls-backed DialTLSContext that mimics the impersonated browser's
	// ClientHello; without the tag it is a no-op (stdlib TLS).
	configureTLS(transport, cfg)

	client := &http.Client{
		Transport: buildHeaderTransport(transport, cfg),
		Jar:       jar,
	}
	return client, nil
}

func proxyFromConfig(raw string) func(*http.Request) (*url.URL, error) {
	if raw == "" {
		return http.ProxyFromEnvironment
	}
	u, err := url.Parse(raw)
	if err != nil {
		return func(*http.Request) (*url.URL, error) { return nil, nil }
	}
	return func(*http.Request) (*url.URL, error) { return u, nil }
}

// loadCookiesFile parses a Netscape/Mozilla cookies.txt and installs it in jar.
//
// The Netscape format is tab-separated with seven fields per record:
//
//	domain  include_subdomains  path  secure  expires  name  value
//
// Comments (leading '#') and blank lines are ignored. A '#HttpOnly_' prefix on
// the domain column marks an HTTP-only cookie. This mirrors yt-dlp's
// _really_load / load behaviour (yt_dlp/cookies.py) so that exported cookie
// files — including the session cookies (expires == 0) that are common with
// "remember me" logins — are applied faithfully.
func loadCookiesFile(jar http.CookieJar, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		// Strip the HttpOnly prefix before the comment check: a record like
		// "#HttpOnly_.example.com	TRUE	..." is a real cookie, not a comment.
		// (This mirrors yt-dlp's prepare_line in yt_dlp/cookies.py.)
		httpOnly := strings.HasPrefix(line, "#HttpOnly_")
		if httpOnly {
			line = strings.TrimPrefix(line, "#HttpOnly_")
		}
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) != 7 {
			// Skip malformed records rather than failing the whole file.
			continue
		}

		domain := fields[0]
		includeSubdomains := strings.EqualFold(fields[1], "TRUE")
		pathField := fields[2]
		secure := strings.EqualFold(fields[3], "TRUE")
		expiresRaw := fields[4]
		name := fields[5]
		value := fields[6]

		cookie := &http.Cookie{
			Name:     name,
			Value:    sanitizeCookieValue(value),
			Path:     pathField,
			Secure:   secure,
			HttpOnly: httpOnly,
		}

		// include_subdomains maps to the cookie's Domain attribute: a non-empty
		// Domain makes it a "domain cookie" (sent to subdomains), while an empty
		// Domain makes it host-only. Go's cookiejar strips any leading dot, so we
		// pass the raw domain through and let it normalise.
		if includeSubdomains {
			cookie.Domain = domain
		}

		// expires is a Unix timestamp; 0 or empty means a session cookie. Go's
		// cookiejar treats a zero Expires as a session (non-persistent) cookie,
		// so we only set Expires for genuine, still-valid timestamps.
		if expiresRaw != "" && expiresRaw != "0" {
			if sec, err := strconv.ParseInt(expiresRaw, 10, 64); err == nil {
				cookie.Expires = time.Unix(sec, 0)
			}
		}

		// SetCookies needs a URL whose host matches the cookie domain; the scheme
		// is only used to decide the default path, which we always supply.
		u := &url.URL{Scheme: "https", Host: domain, Path: pathField}
		jar.SetCookies(u, []*http.Cookie{cookie})
	}
	return sc.Err()
}

// cookieValueOctet reports whether b is a valid RFC 6265 cookie-octet: the
// byte set net/http accepts in a Cookie.Value without percent-encoding. Bytes
// outside this set (e.g. '"', ',', ';', '\\', control chars, and any byte
// >= 0x7f) trigger net/http's "invalid byte in Cookie.Value" warning and cause
// the whole cookie to be dropped by the jar.
func cookieValueOctet(b byte) bool {
	return b == 0x21 ||
		(b >= 0x23 && b <= 0x2b) ||
		(b >= 0x2d && b <= 0x3a) ||
		(b >= 0x3c && b <= 0x5b) ||
		(b >= 0x5d && b <= 0x7e)
}

// sanitizeCookieValue percent-encodes any byte in a cookie value that is not a
// valid cookie-octet. Cookies exported by browsers (e.g. Bilibili's bmg_af_sc,
// whose value is a JSON object containing double quotes) otherwise make
// net/http log "invalid byte '"' in Cookie.Value" and silently drop the cookie.
// Encoding preserves the value through the jar instead of discarding it.
func sanitizeCookieValue(v string) string {
	needsEscape := false
	for i := 0; i < len(v); i++ {
		if !cookieValueOctet(v[i]) {
			needsEscape = true
			break
		}
	}
	if !needsEscape {
		return v
	}
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	b.Grow(len(v))
	for i := 0; i < len(v); i++ {
		c := v[i]
		if cookieValueOctet(c) {
			b.WriteByte(c)
		} else {
			b.WriteByte('%')
			b.WriteByte(hex[c>>4])
			b.WriteByte(hex[c&0xf])
		}
	}
	return b.String()
}
