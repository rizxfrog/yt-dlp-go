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
	"strings"
	"time"

	"yt-dlp-go/options"
)

// ClientConfig is the resolved configuration for a client.
type ClientConfig struct {
	UserAgent     string
	Proxy         string
	CookiesFile   string
	NoCheckCerts bool
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
func loadCookiesFile(jar http.CookieJar, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 7 {
			continue
		}
		domain := strings.TrimPrefix(fields[0], "#HttpOnly_")
		domain = strings.TrimPrefix(domain, ".")
		pathField := fields[2]
		secure := strings.EqualFold(fields[3], "TRUE")
		name := fields[5]
		value := fields[6]

		u := &url.URL{Scheme: "https", Host: domain, Path: pathField}
		jar.SetCookies(u, []*http.Cookie{{Name: name, Value: value, Path: pathField, Domain: domain, Secure: secure}})
	}
	return sc.Err()
}
