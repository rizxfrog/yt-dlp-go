# yt-dlp-go

A Go reimplementation of the [yt-dlp](https://github.com/yt-dlp/yt-dlp) video
downloader engine.

> **Status: working foundation, build-verified, not a 1:1 port.**
> This repository mirrors yt-dlp's *architecture* and implements a substantial,
> tested subset of its core features: a format-selection engine (`-f`), an output
> template engine (`-o`), native HTTP / HLS / DASH downloaders with resume, rate
> limiting and retry, an ffmpeg postprocessor collection, subtitle download, a
> playlist + concurrent multi-URL pipeline, and a curated extractor set
> (YouTube, Bilibili, TikTok, Douyin, 红果短剧, Acast, and a direct-URL fallback). The engine is
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
  /youtube              YouTube (InnerTube player API + sig/nsig deobfuscation + captions)
  /bilibili             Bilibili (window.__INITIAL_STATE__ + WBI-signed playurl)
  /tiktok               TikTok (og:video meta + __NEXT_DATA__)
  /douyin               Douyin 抖音 (aweme/v1/web/aweme/detail + no-watermark preference)
  /hongguo              红果短剧 hongguoduanju (_ROUTER_DATA SSR payload)
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
- **Extractors**: YouTube — stream URLs are taken from the **InnerTube player API**
  (`POST /youtubei/v1/player`) rather than the watch page, because YouTube no
  longer ships stream URLs in `ytInitialPlayerResponse` (its `adaptiveFormats`
  carry only metadata now, which used to surface as `no resolvable formats found`).
  The extractor tries spoofed clients in the order `visionos → android_vr → web`
  and falls back to the webpage player response; `visionos`/`android_vr` return
  plain, unciphered URLs that need **no JS engine**, so the goja evaluator is only
  loaded for the rare ciphered fallback. Captions, the `sts`-stamped signature
  transform and the `n` throttling transform (located by its **player call site**,
  `x = NAME(x)` / the module form `x = (0, MOD.NAME)(x)`) are also supported via
  the embedded `goja` engine. **Bilibili** (metadata from
  `window.__INITIAL_STATE__` + pure-Go WBI-signed `playurl` for DASH/FLV),
  **TikTok** (og:video meta + `__NEXT_DATA__`), **Douyin** (抖音, the Chinese
  TikTok: `aweme/v1/web/aweme/detail` JSON with fresh `s_v_web_id`/`sid_tt`
  cookies — no `a_bogus` JS signature needed — and no-watermark playback
  streams preferred over the watermarked `download_addr` via the format
  `Preference` field), **红果短剧** (`hongguoduanju.com` — the SSR page embeds
  the playable mp4 in a `_ROUTER_DATA` JSON blob; no API signature needed.
  `/player/<sid>` expands the whole drama into a playlist (free episodes only),
  `/player/<sid>/<vid>` grabs a single episode), Acast, and a direct-URL
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

1. **Bilibili is real-verified end-to-end** (tested in this sandbox against a live
   `BV` URL): `window.__INITIAL_STATE__` parsing → pure-Go WBI signing →
   `x/player/wbi/playurl` DASH all work. The playurl call now requests the full
   quality ladder (`fnval=4048`, `qn=127`), so a `best` download picks the highest
   resolution the account is entitled to; Bilibili downgrades to the 720p default
   with no login, and unlocks 1080p+ / 4K / HDR when a logged-in `SESSDATA` cookie
   is supplied via `--cookies`. A `best` download merges separate video+audio
   streams through ffmpeg into a playable file. Covers are upgraded `http→https`.
2. **YouTube is now live-verified end-to-end** (this sandbox, via the corporate
   proxy) using the InnerTube player API: the watch page no longer carries stream
   URLs, so extraction goes through spoofed clients (`visionos`/`android_vr`/`web`),
   which return plain URLs and need no JS engine. A `best` download merges 1080p
   video + audio through ffmpeg into a playable file, and `-o` templates with
   subdirectories (e.g. `%(id)s/%(title)s.%(ext)s`) now auto-create their target
   directory (previously the `.part` open failed with "The system cannot find the
   path specified"). **Live broadcasts** are also handled: when `isLiveContent` is
   set, the extractor emits the `streamingData.hlsManifestUrl` as an `m3u8_native`
   format that the fragment downloader assembles. **Cookies are not required** for
   these public-URL clients; supplied account cookies may be silently stale
   (YouTube rotates them) — the InnerTube path works without them, so ignore the
   "cookies no longer valid" warning unless you specifically need
   age/restricted content.
3. **Signature & n-parameter deobfuscation (done).** `extractor.DeobfuscateSignature`
   and `extractor.DeobfuscateNSig` both evaluate the player's transform functions
   in an embedded `goja` ECMAScript engine (pure Go), so the obfuscation runs
   exactly as YouTube wrote it; the previous regex/classify pipeline is retained
   as a best-effort fallback for the signature. The `sts` timestamp from the
   player response is injected as the signature function's second argument. The
   `n` transform is located by its **player call site** (`x = NAME(x)` / the module
   form `x = (0, MOD.NAME)(x)`), resolved to its definition and validated, with a
   body-shape scan kept only as a last-resort fallback; if it cannot be evaluated
   the `n` query param is left unchanged so the download still proceeds.
4. **Extractor coverage.** More sites can be added by copying the shape of
   `extractor/acast` / `extractor/bilibili` and calling `extractor.Register`
   from `init()`. Aim for a curated, well-tested subset rather than all 2000.
5. **Postprocessors.** SponsorBlock, more fixups, and richer metadata mapping
   remain incremental additions.
6. **Format sorting (`-S` / `--format-sort`) is implemented** in the `format`
   package: a multi-key stable sort (res, width, fps, tbr/vbr/abr, size, asr,
   channels, proto, source, ext, vcodec/acodec/codec with `codec:vp9.2` style
   preferences, dynamic_range/hdr, lang, id, has_audio/has_video, quality, pref)
   with `+`/`-` direction and `!` reverse modifiers. When given, `Select` honours
   the sorted order so `best`/`bestvideo`/`bestaudio` pick the top-ranked format.
   Unit-tested; the binary builds and parses the flag (live YouTube fetch is
   environment-dependent).

## License

Same spirit as upstream yt-dlp (Unlicense); this is an independent reimplementation.
