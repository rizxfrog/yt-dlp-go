# Repository Guidelines

## Project Structure & Module Organization

Go module `yt-dlp-go` — an independent Go reimplementation of the yt-dlp engine.

- `main.go` — CLI entry: flag parsing, then core dispatch.
- `core/` — `YoutubeDL` orchestrator (`Download`, `DownloadURLs`); extract → select → download → postprocess.
- `options/` — `Options` struct and yt-dlp-flavoured flag parser.
- `network/` — `*http.Client` factory (headers, proxy, cookies, impersonation); `transport_utls.go` is `-tags utls` only, `transport_stdlib.go` the default.
- `extractor/` — `Info`/`Format` types, `Extractor` interface, registry, shared helpers, `jseval.go` (goja JS evaluator), `datauri.go` (data: URI decoding). One subpackage per site: `youtube/`, `bilibili/`, `tiktok/`, `douyin/`, `hongguo/`, `cg51/`, `pornhub/`, `acast/`, `generic/`. A new site's subpackage must also be blank-imported in `core/core.go` so its `init()` registers it.
- `format/`, `output/`, `downloader/`, `postprocessor/`, `sponsorblock/` — `-f` grammar, `-o` templates, HTTP/HLS/DASH transfer, ffmpeg post-processing, SponsorBlock.
- `scripts/crosscheck.py` — regenerates `extractor/testdata/realistic_player.expected.json`.
- `PLAN.md`, `docs/使用手册.md` — roadmap and user manual (Chinese).

## Build, Test, and Development Commands

```bash
go build -o yt-dlp-go .                  # default (pure Go, no cgo)
go build -tags utls -o yt-dlp-go-utls .  # adds TLS ClientHello impersonation
go test ./...                            # offline unit tests (no network)
go test -tags utls ./network/            # utls transport tests
go vet ./...                             # run before committing
```

Go 1.25+ (`go.mod`); CI uses 1.26 on push/PR to `main`.

## Coding Style & Naming Conventions

- `gofmt` formatting: tabs, standard Go layout. No linter config is checked in — `gofmt`/`go vet` clean is the bar.
- Exported extractor types use yt-dlp's `_IE` suffix: `YouTubeIE`, `BilibiliSpaceIE`.
- Tests live beside code as `<file>_test.go`.
- Keep dependencies pure Go (no cgo).

## Testing Guidelines

- Standard `testing` package, table-driven where practical. No coverage threshold is enforced; new logic ships with a unit test.
- Tests must run **offline**: use `httptest` servers and a test extractor (see `core/core_test.go`) instead of live sites.
- Fixtures: `extractor/testdata/realistic_player.js`; regenerate gold values with `python scripts/crosscheck.py`.
- Naming: `Test<Subject>` or `Test<Subject>_<Case>` (e.g. `TestWbiSign_IsDeterministic`).

## Commit & Pull Request Guidelines

- Conventional commits matching history: `feat(youtube): ...`, `fix(network): ...`, `ci: ...`, `docs: ...`, `chore: ...`.
- Name the affected package or site in the scope; keep the subject imperative and lowercase.
- PRs: describe behaviour change, link the issue, note live-verification steps (site, flags, ffmpeg version), and confirm `go build ./...` + `go test ./...` pass. Add terminal output or screenshots for user-visible output changes.
- Tags `v*` trigger `.github/workflows/release.yml` (linux/windows/darwin binaries).

## Adding an Extractor

Copy the shape of `extractor/acast` — small, self-contained — then register it:

```go
func init() { extractor.Register(MySiteIE{}) }
```

Keep `Match()` strict so registry order (first match wins) stays predictable. Prefer a curated, well-tested subset over yt-dlp's full extractor set.
