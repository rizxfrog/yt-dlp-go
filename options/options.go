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
	"strconv"
	"strings"
	"time"
)

// Options is the parsed runtime configuration shared across the engine.
type Options struct {
	// Selection / output
	Format            string // -f / --format
	FormatSort        string // -S / --format-sort (e.g. res,fps,codec:vp9.2)
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
	ConcurrentFragments int    // --concurrent-fragments
	SkipDownload        bool   // --skip-download
	Simulate            bool   // --simulate / -s
	NoOverwrites        bool   // --no-overwrite
	Continue            bool   // --continue (resume; on by default)
	DownloadArchive     string // --download-archive FILE
	PlaylistItems       string // --playlist-items 1-5,8
	NoPlaylist          bool   // --no-playlist
	YesPlaylist         bool   // --yes-playlist

	// Date filtering
	DateAfter  string // --dateafter YYYYMMDD
	DateBefore string // --datebefore YYYYMMDD

	// Subtitles
	WriteSubs     bool   // --write-subs
	WriteAutoSubs bool   // --write-auto-subs
	SubLangs      string // --sub-langs en,zh-Hans
	ConvertSubs   string // --convert-subs srt

	// Post-processing
	ExtractAudio  bool   // -x / --extract-audio
	AudioFormat   string // --audio-format (mp3/aac/m4a/opus/flac/wav)
	AudioQuality  string // --audio-quality (320 / 5)
	RemuxVideo    string // --remux-video (mp4/mkv)
	TrimFilenames int    // --trim-filenames N
	NoColors      bool   // --no-colors
	PrintField    string // --print %(field)s

	// SponsorBlock / chapters
	SponsorblockMark       bool   // --sponsorblock-mark (mark segments as chapters)
	SponsorblockRemove     bool   // --sponsorblock-remove (cut segments out)
	SponsorblockCategories string // --sponsorblock-categories (comma-separated)
	EmbedChapters          bool   // --embed-chapters

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
	fs.StringVar(&o.FormatSort, "S", "", "sort formats by a comma-separated key list (e.g. res,fps,codec:vp9.2)")
	fs.StringVar(&o.FormatSort, "format-sort", "", "alias of -S")
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
	fs.BoolVar(&o.NoOverwrites, "no-overwrite", false, "skip files that already exist")
	fs.BoolVar(&o.NoOverwrites, "no-overwrites", false, "alias of --no-overwrite")
	fs.BoolVar(&o.Continue, "continue", true, "resume partially downloaded files (default on)")
	fs.StringVar(&o.DownloadArchive, "download-archive", "", "record downloaded IDs to skip them later")
	fs.StringVar(&o.PlaylistItems, "playlist-items", "", "items to download, e.g. 1-5,8")
	fs.BoolVar(&o.NoPlaylist, "no-playlist", false, "do not download playlists")
	fs.BoolVar(&o.YesPlaylist, "yes-playlist", false, "download playlists even for single videos")
	fs.StringVar(&o.DateAfter, "dateafter", "", "only videos uploaded on/after YYYYMMDD")
	fs.StringVar(&o.DateBefore, "datebefore", "", "only videos uploaded on/before YYYYMMDD")

	fs.StringVar(&o.FFmpegLocation, "ffmpeg-location", "", "path to ffmpeg/ffprobe binary")
	fs.BoolVar(&o.WriteInfoJSON, "write-info-json", false, "write the info JSON to disk")
	fs.StringVar(&o.PrintToStdout, "j", "", "dump info JSON to stdout")
	fs.StringVar(&o.PrintToStdout, "dump-json", "", "alias of -j")
	fs.BoolVar(&o.WriteSubs, "write-subs", false, "write subtitle files")
	fs.BoolVar(&o.WriteSubs, "w", false, "alias of --write-subs")
	fs.BoolVar(&o.WriteAutoSubs, "write-auto-subs", false, "write auto-generated subtitles")
	fs.StringVar(&o.SubLangs, "sub-langs", "", "languages of subs to download (en,zh-Hans,all)")
	fs.StringVar(&o.ConvertSubs, "convert-subs", "", "convert subtitles to this format (srt/vtt/ass)")
	fs.BoolVar(&o.ExtractAudio, "extract-audio", false, "extract audio track only")
	fs.BoolVar(&o.ExtractAudio, "x", false, "alias of --extract-audio")
	fs.StringVar(&o.AudioFormat, "audio-format", "best", "audio format for -x (mp3/aac/m4a/opus/flac/wav)")
	fs.StringVar(&o.AudioQuality, "audio-quality", "", "audio quality for -x (e.g. 320 or 5)")
	fs.StringVar(&o.RemuxVideo, "remux-video", "", "remux video into this container (mp4/mkv)")
	fs.IntVar(&o.TrimFilenames, "trim-filenames", 0, "limit filenames to N characters")
	fs.BoolVar(&o.NoColors, "no-colors", false, "disable ANSI colors in output")
	fs.StringVar(&o.PrintField, "print", "", "print a template field, e.g. %(title)s")

	fs.BoolVar(&o.SponsorblockMark, "sponsorblock-mark", false, "mark SponsorBlock segments as chapters")
	fs.BoolVar(&o.SponsorblockRemove, "sponsorblock-remove", false, "remove SponsorBlock segments from the video")
	fs.StringVar(&o.SponsorblockCategories, "sponsorblock-categories", "", "SponsorBlock categories to act on (comma-separated; default sponsor)")
	fs.BoolVar(&o.EmbedChapters, "embed-chapters", false, "embed chapter markers in the downloaded file")

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

// InDateRange reports whether uploadDate (YYYYMMDD) satisfies --dateafter /
// --datebefore constraints. Empty constraints are ignored.
func (o *Options) InDateRange(uploadDate string) bool {
	if len(uploadDate) < 8 {
		return true
	}
	if o.DateAfter != "" && uploadDate < o.DateAfter {
		return false
	}
	if o.DateBefore != "" && uploadDate > o.DateBefore {
		return false
	}
	return true
}

// playlistSet parses a --playlist-items spec like "1-5,8,10-12" into a set of
// 1-based indices.
func playlistSet(spec string) (map[int]bool, error) {
	set := map[int]bool{}
	if spec == "" {
		return set, nil
	}
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if dash := strings.Index(part, "-"); dash > 0 {
			lo, err1 := strconv.Atoi(strings.TrimSpace(part[:dash]))
			hi, err2 := strconv.Atoi(strings.TrimSpace(part[dash+1:]))
			if err1 != nil || err2 != nil {
				return nil, fmt.Errorf("invalid playlist range %q", part)
			}
			for i := lo; i <= hi; i++ {
				set[i] = true
			}
		} else {
			n, err := strconv.Atoi(part)
			if err != nil {
				return nil, fmt.Errorf("invalid playlist item %q", part)
			}
			set[n] = true
		}
	}
	return set, nil
}

// WantsPlaylistItem reports whether the 1-based index should be downloaded given
// --playlist-items. An empty spec means "all".
func (o *Options) WantsPlaylistItem(index int) bool {
	if o.PlaylistItems == "" {
		return true
	}
	set, err := playlistSet(o.PlaylistItems)
	if err != nil {
		return true
	}
	return set[index]
}
