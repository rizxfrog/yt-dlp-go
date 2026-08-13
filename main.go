// Command yt-dlp-go is a Go reimplementation of the yt-dlp download engine.
//
// This is a faithful, self-contained foundation: it mirrors yt-dlp's
// architecture (network -> extractor -> downloader -> postprocessor) using only
// the Go standard library so it builds offline. It ships with a representative
// extractor subset (YouTube, Acast, and a direct-URL fallback); the remaining
// ~2000 site extractors slot in via the same Extractor interface + registry.
package main

import (
	"fmt"
	"os"

	"yt-dlp-go/core"
	"yt-dlp-go/options"
)

func main() {
	opts, urls, err := options.Parse(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, "yt-dlp-go:", err)
		os.Exit(2)
	}
	if opts.Help {
		printHelp()
		return
	}
	if opts.Version {
		fmt.Println("yt-dlp-go (foundation build) — Go reimplementation of yt-dlp")
		return
	}
	if len(urls) == 0 {
		fmt.Fprintln(os.Stderr, "usage: yt-dlp-go [options] URL [URL...]")
		fmt.Fprintln(os.Stderr, "try 'yt-dlp-go --help' for options")
		os.Exit(2)
	}

	ydl, err := core.New(opts)
	if err != nil {
		fmt.Fprintln(os.Stderr, "yt-dlp-go:", err)
		os.Exit(1)
	}

	hadError := false
	for _, u := range urls {
		if err := ydl.Download(u); err != nil {
			hadError = true
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
		}
	}
	if hadError {
		os.Exit(1)
	}
}

func printHelp() {
	fmt.Print(`yt-dlp-go — Go reimplementation of the yt-dlp download engine (foundation build)

Usage: yt-dlp-go [options] URL [URL...]

Options (subset of yt-dlp):
  -f, --format FMT          format selector (best, worst, bestvideo, bestaudio, or itag)
  -o, --output TEMPLATE     output filename (template support is minimal)
  -P, --paths DIR           base directory for output files
  --merge-output-format FMT container for merged outputs (mkv/mp4)
  --proxy URL               HTTP(S)/SOCKS proxy
  --cookies FILE            Netscape/Mozilla cookies.txt
  --no-check-certificates   ignore TLS errors
  --user-agent UA           set User-Agent
  --impersonate BROWSER     chrome|firefox|safari|edge (header set)
  -R, --retries N           retry count
  --socket-timeout SECS      connection timeout
  -r, --limit-rate RATE      bandwidth cap
  -H, --add-header K:V       extra HTTP header (repeatable)
  -u, --username USER        account username
  -p, --password PASS        account password
  --concurrent-fragments N   parallel fragment downloads
  --skip-download           do not download
  -s, --simulate            simulate only
  --ffmpeg-location PATH    path to ffmpeg/ffprobe
  --write-info-json         write .info.json
  -j, --dump-json           print info JSON to stdout
  -v, --verbose             verbose logging
  -q, --quiet               quiet mode
  --version                 print version
  -h, --help                show this help

Note: built with the Go standard library only. For TLS fingerprint
impersonation (utls) and a full JS signature engine (goja), see README.md.
`)
}
