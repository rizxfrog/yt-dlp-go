//go:build utls

// Package network TLS impersonation via github.com/refraction-networking/utls.
//
// Built only with `-tags utls`. When --impersonate is set, this replaces the
// transport's TLS dialer with one that performs the handshake using the selected
// browser's ClientHello (curve/extension order), giving real TLS-fingerprint
// parity with yt-dlp's curl_cffi backend. Without --impersonate the stdlib TLS
// path is used (DialTLSContext is left unset).
package network

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"strings"

	utls "github.com/refraction-networking/utls"
)

func configureTLS(t *http.Transport, cfg *ClientConfig) {
	if cfg.Impersonate == "" {
		return // stdlib TLS when not impersonating
	}
	id := clientHelloID(cfg.Impersonate)
	t.DialTLSContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		host := addr
		if h, _, err := net.SplitHostPort(addr); err == nil {
			host = h
		}
		dialer := &net.Dialer{Timeout: cfg.SocketTimeout}
		rawConn, err := dialer.DialContext(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		tlsConn := utls.UClient(rawConn, &utls.Config{
			ServerName:         host,
			InsecureSkipVerify: cfg.NoCheckCerts,
		}, id)
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			rawConn.Close()
			return nil, err
		}
		return tlsConn, nil
	}
}

// clientHelloID maps an --impersonate target to a utls ClientHello.
func clientHelloID(target string) utls.ClientHelloID {
	switch strings.ToLower(target) {
	case "firefox":
		return utls.HelloFirefox_Auto
	case "safari", "webkit":
		return utls.HelloSafari_Auto
	case "edge":
		return utls.HelloEdge_Auto
	default:
		return utls.HelloChrome_Auto
	}
}

// ensure tls import is used even if utls constants change across versions.
var _ = tls.Config{}
