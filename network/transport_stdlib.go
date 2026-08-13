//go:build !utls

package network

import "net/http"

// configureTLS is a no-op for the default (stdlib-only) build. TLS fingerprint
// impersonation is provided by transport_utls.go when built with `-tags utls`.
func configureTLS(t *http.Transport, cfg *ClientConfig) {}
