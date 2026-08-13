// Package options defines the runtime configuration for yt-dlp-go and a
// flag-based parser that mirrors the most commonly used yt-dlp CLI switches.
//
// This is a faithful-but-scoped subset of yt-dlp's option surface. The full
// CLI exposes hundreds of flags; here we cover the ones that drive the core
// engine so the architecture is demonstrable end-to-end.
package options

import (
	"flag"
	"fmt"
	"strings"
	"time"
)

// Options is the parsed runtime configuration shared across the engine.
type Options struct {
	// Selection / output
	Format            string // -f / --format
	OutputTemplate    string // -o / --output
	OutputDir         string // -P / --paths
	MergeOutputFormat string // --merge-output-format

	// Network
	Proxy         string // --proxy
	CookiesFile   string // --cookies
	NoCheckCerts  bool   // --no-check-certificates
	UserAgent     string // --user-agent
	Impersonate   string // --impersonate (e.g. chrome, firefox, safari)
	Retries       int    // -R / --retries
	SocketTimeout time.Duration
	LimitRate     string // -r / --limit-rate
	AddHeaders    map[string]string

	// Auth
	Username string // -u / --username
	Password string // -p / --password
	Netrc    bool   // --netrc

	// Download behaviour
	ConcurrentFragments int  // --concurrent-fragments
	SkipDownload        bool // --skip-download
	Simulate            bool // --simulate / -s

	// Post / output controls
	FFmpegLocation string // --ffmpeg-location
	WriteInfoJSON  bool   // --write-info-json
	PrintToStdout  string // -j / --dump-json / --print

	// Logging
	Verbose    bool // -v
	NoWarnings bool // --no-warnings
	Quiet      bool // -q

	// Meta
	Version bool // --version
	Help    bool // -h / --help
}

// headerMap implements flag.Value so "-H Key: Value" can be repeated.
type headerMap map[string]string

func (h headerMap) String() string { return "map of HTTP headers" }

func (h headerMap) Set(v string) error {
	i := strings.Index(v, ":")
	if i < 0 {
		return fmt.Errorf("header must be in 'Key: Value' form, got %q", v)
	}
	h[strings.TrimSpace(v[:i])] = strings.TrimSpace(v[i+1:])
	return nil
}

// Parse parses the given argument slice (typically os.Args[1:]) into Options and
// returns the remaining positional arguments (the URLs to process).
func Parse(args []string) (*Options, []string, error) {
	fs := flag.NewFlagSet("yt-dlp-go", flag.ContinueOnError)
	o := &Options{
		AddHeaders:          map[string]string{},
		ConcurrentFragments: 1,
		Retries:             10,
		SocketTimeout:       30 * time.Second,
		Format:              "best",
		UserAgent:           "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0 Safari/537.36",
	}

	fs.StringVar(&o.Format, "f", o.Format, "video format selector (e.g. best, worst, bestvideo+bestaudio)")
	fs.StringVar(&o.Format, "format", o.Format, "alias of -f")
	fs.StringVar(&o.OutputTemplate, "o", "", "output filename template")
	fs.StringVar(&o.OutputTemplate, "output", "", "alias of -o")
	fs.StringVar(&o.OutputDir, "P", "", "base directory for output files")
	fs.StringVar(&o.OutputDir, "paths", "", "alias of -P")
	fs.StringVar(&o.MergeOutputFormat, "merge-output-format", "", "container for merged outputs (e.g. mkv, mp4)")

	fs.StringVar(&o.Proxy, "proxy", "", "proxy URL (http/https/socks5)")
	fs.StringVar(&o.CookiesFile, "cookies", "", "path to Netscape/Mozilla cookies.txt")
	fs.BoolVar(&o.NoCheckCerts, "no-check-certificates", false, "ignore TLS certificate errors")
	fs.StringVar(&o.UserAgent, "user-agent", o.UserAgent, "set the User-Agent header")
	fs.StringVar(&o.Impersonate, "impersonate", "", "browser to impersonate (chrome/firefox/safari/edge)")
	fs.IntVar(&o.Retries, "R", o.Retries, "number of retries")
	fs.IntVar(&o.Retries, "retries", o.Retries, "alias of -R")
	fs.DurationVar(&o.SocketTimeout, "socket-timeout", o.SocketTimeout, "socket timeout")
	fs.StringVar(&o.LimitRate, "r", "", "limit download rate (e.g. 50K, 1M)")
	fs.StringVar(&o.LimitRate, "limit-rate", "", "alias of -r")
	fs.Var(headerMap(o.AddHeaders), "H", "add a HTTP header (repeatable): 'Key: Value'")
	fs.Var(headerMap(o.AddHeaders), "add-header", "alias of -H")

	fs.StringVar(&o.Username, "u", "", "account username")
	fs.StringVar(&o.Username, "username", "", "alias of -u")
	fs.StringVar(&o.Password, "p", "", "account password")
	fs.StringVar(&o.Password, "password", "", "alias of -p")
	fs.BoolVar(&o.Netrc, "netrc", false, "use .netrc for credentials")

	fs.IntVar(&o.ConcurrentFragments, "concurrent-fragments", o.ConcurrentFragments, "number of concurrent fragment downloads")
	fs.BoolVar(&o.SkipDownload, "skip-download", false, "do not download the video")
	fs.BoolVar(&o.Simulate, "simulate", false, "do not download, only simulate")
	fs.BoolVar(&o.Simulate, "s", false, "alias of --simulate")

	fs.StringVar(&o.FFmpegLocation, "ffmpeg-location", "", "path to ffmpeg/ffprobe binary")
	fs.BoolVar(&o.WriteInfoJSON, "write-info-json", false, "write the info JSON to disk")
	fs.StringVar(&o.PrintToStdout, "j", "", "dump info JSON to stdout")
	fs.StringVar(&o.PrintToStdout, "dump-json", "", "alias of -j")

	fs.BoolVar(&o.Verbose, "v", false, "verbose logging")
	fs.BoolVar(&o.Verbose, "verbose", false, "alias of -v")
	fs.BoolVar(&o.NoWarnings, "no-warnings", false, "ignore warnings")
	fs.BoolVar(&o.Quiet, "q", false, "quiet mode")
	fs.BoolVar(&o.Quiet, "quiet", false, "alias of -q")

	fs.BoolVar(&o.Version, "version", false, "print version and exit")
	fs.BoolVar(&o.Help, "h", false, "show help")
	fs.BoolVar(&o.Help, "help", false, "alias of -h")

	if err := fs.Parse(args); err != nil {
		return nil, nil, err
	}
	return o, fs.Args(), nil
}

// ImpersonateTarget normalises a --impersonate value into a browser key.
func (o *Options) ImpersonateTarget() string {
	switch strings.ToLower(o.Impersonate) {
	case "chrome", "chromium":
		return "chrome"
	case "firefox":
		return "firefox"
	case "safari", "webkit":
		return "safari"
	case "edge":
		return "edge"
	default:
		return ""
	}
}
