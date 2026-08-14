# yt-dlp-go

A Go reimplementation of the [yt-dlp](https://github.com/yt-dlp/yt-dlp) video
downloader engine.

> **Status: working foundation, build-verified, not a 1:1 port.**
> This repository mirrors yt-dlp's *architecture* and implements a substantial,
> tested subset of its core features: a format-selection engine (`-f`), an output
> template engine (`-o`), native HTTP / HLS / DASH downloaders with resume, rate
> limiting and retry, an ffmpeg postprocessor collection, subtitle download, a
> playlist + concurrent multi-URL pipeline, and a curated extractor set
> (YouTube, Bilibili, TikTok, Acast, and a direct-URL fallback). The engine is
> built on the **Go standard library** plus a few small, pure-Go dependencies:
> `github.com/dop251/goja` (an embedded ECMAScript engine) evaluates YouTube's
> signature transform exactly as written, and an optional `-tags utls` build adds
> real TLS ClientHello impersonation. All dependencies are pure Go, so the binary
> still compiles without cgo.

## Why a foundation and not the full 225k-line port?

yt-dlp is ~225,000 lines of Python; **~87% is ~2000 site-specific extractors
that break constantly as websites change.** Python's "edit text, ship a release"
model is exactly what keeps that project alive. A Go rewrite changes the
maintenance model (every fix needs a recompile + redistributed binary), which is
why a literal 1:1 port is not advisable for the same community-maintenance use
case. This project instead proves the engine in Go and provides a clean
extension point for a *curated* set of sites.

## Architecture

```
main.go                 CLI entry, flag parsing, concurrent dispatch
core/                   YoutubeDL orchestrator: extract -> select -> download -> postprocess
  ├─ Download()         single URL; branches to playlist vs single
  ├─ DownloadURLs()     concurrent multi-URL download with error isolation
options/                Options struct + flag parser (yt-dlp-flavoured switches)
network/                *http.Client factory: headers, proxy, cookies, impersonation
  └─ transport_utls.go  (-tags utls) real ClientHello impersonation via utls
downloader/             HTTP downloader + native HLS/DASH fragment downloader
format/                 -f selection grammar (best/worst/+merge//fallback/[filter])
output/                 -o template engine (%(field)s, defaults, date, duration)
postprocessor/          ffmpeg orchestration (merge / remux / extract-audio / metadata / subs)
extractor/              Info/Format types, Extractor interface, registry, helpers, JS evaluator
  /youtube              YouTube (ytInitialPlayerResponse + signature deobfuscation + captions)
  /bilibili             Bilibili (window.__INITIAL_STATE__ + WBI-signed playurl)
  /tiktok               TikTok (og:video meta + __NEXT_DATA__)
  /acast                Acast (JSON-API pattern)
  /generic              Direct media-URL fallback
```

The extractor dispatch mirrors yt-dlp's explicit registry: each subpackage
registers itself in `init()` via `extractor.Register(...)`, and `core` dispatches
a URL to the first extractor whose `Match()` returns true.

## Build & run

Requires Go 1.25+.

```bash
cd yt-dlp-go
go build -o yt-dlp-go .                 # stdlib-only build (default)

# Optional: real TLS fingerprint impersonation (Chrome/Firefox/Safari/Edge)
go build -tags utls -o yt-dlp-go-utls .
```

> **Note on module downloads.** The core build pulls `github.com/dop251/goja`
> (pure Go) and, for the optional impersonation build,
> `github.com/refraction-networking/utls`. If the default Go proxy is unreachable,
> use a mirror, e.g.
> `GOPROXY=https://goproxy.cn,direct GOSUMDB=off go get github.com/dop251/goja github.com/refraction-networking/utls`.

```bash
./yt-dlp-go --version
./yt-dlp-go -f best "https://www.youtube.com/watch?v=VIDEO_ID"
./yt-dlp-go -f "bestvideo+bestaudio" -o "%(title)s.%(ext)s" "URL"
./yt-dlp-go -s "https://youtu.be/VIDEO_ID"            # simulate, no download
./yt-dlp-go -j "https://shows.acast.com/..."           # dump info JSON
./yt-dlp-go --write-subs --sub-langs en,zh-Hans "URL"  # subtitles
./yt-dlp-go --playlist-items 1-3 "PLAYLIST_URL"       # playlist slice
./yt-dlp-go URL1 URL2 URL3                            # concurrent downloads
./yt-dlp-go -x --audio-format mp3 "URL"               # extract audio
./yt-dlp-go "https://www.bilibili.com/video/BV1xx411c7XD"
./yt-dlp-go "https://www.tiktok.com/@user/video/123"
```

## What works today

- **Format selection (`-f`)**: `best` / `worst` / `bestvideo` / `bestaudio`,
  `+` merge, `/` fallback, `,` multiple outputs, `[height<=720]` / `[ext=mp4]` /
  `[protocol!=http]` filters, itag and itag-range matching.
- **Output templates (`-o`)**: `%(field)s`, default values `%(title|id)s`,
  case transforms `%(title)l`, date `%(upload_date>%Y-%m-%d)s`, duration
  `%(duration>%H:%M:%S)s`, `raw.*` nested paths, literal `%%`, filesystem
  sanitisation.
- **Downloaders**: HTTP with resume (`.part` + Range + atomic rename), rate
  limiting (`-r 50K`), exponential-backoff retry, non-retryable 4xx; native
  **HLS** and **DASH (MPD)** fragment downloaders (SegmentTemplate/Base/List,
  concurrent fragments, AES-128-CBC decryption, TS/m4s concatenation).
- **Postprocessors**: ffmpeg merge, video remux (`--remux-video`), audio
  extraction (`-x` with bitrate/quality), metadata + embedded thumbnail, subtitle
  embed/convert.
- **Subtitles**: HLS `#EXT-X-MEDIA` parsing + download, YouTube caption-track
  extraction, language filtering (`--sub-langs`), format conversion.
- **Playlists & concurrency**: `Info.Entries` playlist model; `--playlist-items`
  1-based slicing; `--no-playlist` takes the first entry; `DownloadURLs` runs
  multiple URLs concurrently with per-URL error isolation.
- **Options**: `--download-archive`, `--no-overwrite`, `--dateafter/before`,
  `--print`, `--trim-filenames`, `--simulate`, `--dump-json`, proxy, cookies,
  impersonation, retries, etc.
- **Extractors**: YouTube (incl. ciphered-signature deobfuscation **and** the `n`
  throttling-parameter transform, both evaluated in an embedded `goja` JS engine,
  with the `sts` timestamp injected as the signature function's second argument;
  plus caption extraction), **Bilibili** (metadata from
  `window.__INITIAL_STATE__` + pure-Go WBI-signed `playurl` for DASH/FLV),
  **TikTok** (og:video meta + `__NEXT_DATA__`), Acast, and a direct-URL
  generic fallback.
- **TLS impersonation (optional)**: `-tags utls` swaps the TLS dialer for
  `utls`, reproducing the impersonated browser's ClientHello.

## Testing

The pure-logic modules are unit-tested offline (no network):

```bash
go test ./...
go test -tags utls ./network/   # only with the utls build
```

## Known limitations / next steps

1. **Real-site extraction needs online verification.** YouTube / Bilibili /
   TikTok internals change often; the extractors are structurally faithful and
   their parsing functions are unit-tested, but live behaviour may drift (as with
   upstream yt-dlp). Bilibili media URLs additionally require the WBI-signed
   `playurl` API, which needs a live session.
2. **Signature & n-parameter deobfuscation (done).** `extractor.DeobfuscateSignature`
   and `extractor.DeobfuscateNSig` both evaluate the player's transform functions
   in an embedded `goja` ECMAScript engine (pure Go), so the obfuscation runs
   exactly as YouTube wrote it; the previous regex/classify pipeline is retained
   as a best-effort fallback for the signature. The `sts` timestamp from the
   player response is injected as the signature function's second argument. The
   `n` transform is identified heuristically (string ops + `return`, excluding the
   signature transform); if it cannot be evaluated the `n` query param is left
   unchanged so the download still proceeds.
3. **Extractor coverage.** More sites can be added by copying the shape of
   `extractor/acast` / `extractor/bilibili` and calling `extractor.Register`
   from `init()`. Aim for a curated, well-tested subset rather than all 2000.
4. **Postprocessors.** SponsorBlock, more fixups, and richer metadata mapping
   remain incremental additions.

## License

Same spirit as upstream yt-dlp (Unlicense); this is an independent reimplementation.
