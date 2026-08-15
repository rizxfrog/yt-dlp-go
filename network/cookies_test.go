package network

import (
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

// writeTempCookies writes a cookies.txt file and returns its path.
func writeTempCookies(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cookies.txt")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// names returns the set of cookie names the jar would send for the URL.
func names(t *testing.T, jar http.CookieJar, rawurl string) map[string]bool {
	t.Helper()
	u, err := url.Parse(rawurl)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	for _, c := range jar.Cookies(u) {
		out[c.Name] = true
	}
	return out
}

func TestLoadCookiesFile(t *testing.T) {
	content := `# Netscape HTTP Cookie File
# This is a generated file! Do not edit.

.example.com	TRUE	/	TRUE	0	SID	abcdef123
#HttpOnly_.example.com	TRUE	/	FALSE	1893456000	HSID	secret-token
example.com	FALSE	/account	FALSE	1893456000	theme	dark
`
	path := writeTempCookies(t, content)

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := loadCookiesFile(jar, path); err != nil {
		t.Fatal(err)
	}

	// Domain cookie (include_subdomains=TRUE): sent to a subdomain over https.
	sub := names(t, jar, "https://www.example.com/")
	if !sub["SID"] {
		t.Error("domain cookie SID should be sent to www.example.com")
	}
	if !sub["HSID"] {
		t.Error("HttpOnly domain cookie HSID should be sent to www.example.com")
	}
	// host-only cookie 'theme' must NOT leak to the subdomain.
	if sub["theme"] {
		t.Error("host-only cookie 'theme' should not be sent to subdomain")
	}

	// Secure cookie (SID) must NOT be sent over plain http.
	subHTTP := names(t, jar, "http://www.example.com/")
	if subHTTP["SID"] {
		t.Error("secure cookie SID should not be sent over http")
	}
	// non-secure HSID may still be sent over http.
	if !subHTTP["HSID"] {
		t.Error("non-secure HSID should be sent over http")
	}

	// Host-only cookie (include_subdomains=FALSE): sent to the exact host only.
	host := names(t, jar, "https://example.com/account")
	if !host["theme"] {
		t.Error("host-only cookie theme should be present for example.com/account")
	}
	// Path matching: /account-scoped cookie is not sent for /.
	root := names(t, jar, "https://example.com/")
	if root["theme"] {
		t.Error("cookie with /account path should not be sent to /")
	}
}

func TestLoadCookiesFile_Expired(t *testing.T) {
	// expires=1 (past) should be rejected by the jar.
	content := ".example.com	TRUE	/	FALSE	1	old	gone\n"
	path := writeTempCookies(t, content)

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := loadCookiesFile(jar, path); err != nil {
		t.Fatal(err)
	}
	if names(t, jar, "https://www.example.com/")["old"] {
		t.Error("expired cookie should not be present")
	}
}

func TestLoadCookiesFile_SessionCookie(t *testing.T) {
	// expires=0 => session cookie, still stored and sent.
	content := ".example.com	TRUE	/	FALSE	0	SID	session-val\n"
	path := writeTempCookies(t, content)

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := loadCookiesFile(jar, path); err != nil {
		t.Fatal(err)
	}
	if !names(t, jar, "https://www.example.com/")["SID"] {
		t.Error("session cookie (expires=0) should be sent")
	}
}

func TestLoadCookiesFile_Missing(t *testing.T) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := loadCookiesFile(jar, "/nonexistent/cookies.txt"); err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestNewClient_LoadsCookies(t *testing.T) {
	content := ".example.com	TRUE	/	FALSE	0	SID	abcdef123\n"
	path := writeTempCookies(t, content)

	client, err := NewClient(&ClientConfig{CookiesFile: path})
	if err != nil {
		t.Fatal(err)
	}
	got := names(t, client.Jar, "https://www.example.com/")
	if !got["SID"] {
		t.Fatalf("client jar should contain SID cookie, got %v", got)
	}
}

func TestNewClient_BadCookiesFile(t *testing.T) {
	_, err := NewClient(&ClientConfig{CookiesFile: "/nonexistent/cookies.txt"})
	if err == nil {
		t.Fatal("expected error for bad cookies file")
	}
}
