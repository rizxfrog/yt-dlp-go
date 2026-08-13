//go:build utls

package network

import (
	"testing"

	"yt-dlp-go/options"
)

// TestNewClient_Impersonate verifies the utls-backed client constructs without
// error when an impersonation target is requested.
func TestNewClient_Impersonate(t *testing.T) {
	opts := &options.Options{Impersonate: "chrome", AddHeaders: map[string]string{}}
	cfg := FromOptions(opts)
	client, err := NewClient(cfg)
	if err != nil {
		t.Fatalf("NewClient (utls, chrome): %v", err)
	}
	if client == nil {
		t.Fatal("nil client")
	}
	// A non-impersonated config must still build (stdlib TLS path).
	opts2 := &options.Options{AddHeaders: map[string]string{}}
	if _, err := NewClient(FromOptions(opts2)); err != nil {
		t.Fatalf("NewClient (utls, none): %v", err)
	}
}
