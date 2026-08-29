// Package core ties the engine together: it parses a URL to the right extractor,
// selects formats per the -f selector, downloads them with the appropriate
// backend, and runs postprocessors (ffmpeg merge/remux).
//
// It blank-imports the extractor subpackages so their init() registration runs,
// populating the global registry before any URL is dispatched.
package core

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"yt-dlp-go/downloader"
	"yt-dlp-go/extractor"
	_ "yt-dlp-go/extractor/acast"
	_ "yt-dlp-go/extractor/bilibili"
	_ "yt-dlp-go/extractor/cg51"
	_ "yt-dlp-go/extractor/douyin"
	_ "yt-dlp-go/extractor/generic"
	_ "yt-dlp-go/extractor/hongguo"
	_ "yt-dlp-go/extractor/tiktok"
	_ "yt-dlp-go/extractor/youtube"
	"yt-dlp-go/format"
	"yt-dlp-go/network"
	"yt-dlp-go/options"
	"yt-dlp-go/output"
	"yt-dlp-go/postprocessor"
	"yt-dlp-go/sponsorblock"
)

// printMu serialises log writes so concurrent downloads don't interleave their
// status lines on stdout/stderr.
var printMu sync.Mutex

func logPrintf(w io.Writer, format string, args ...any) {
	printMu.Lock()
	defer printMu.Unlock()
	fmt.Fprintf(w, format, args...)
}

func logPrintln(w io.Writer, s string) {
	printMu.Lock()
	defer printMu.Unlock()
	fmt.Fprintln(w, s)
}

// YoutubeDL is the top-level coordinator.
type YoutubeDL struct {
	Opts   *options.Options
	Client *http.Client
}

// New builds a YoutubeDL from parsed options.
func New(opts *options.Options) (*YoutubeDL, error) {
	cfg := network.FromOptions(opts)
	client, err := network.NewClient(cfg)
	if err != nil {
		return nil, err
	}
	return &YoutubeDL{Opts: opts, Client: client}, nil
}

// Download runs the full pipeline for a single URL. If the URL resolves to a
// playlist (an Info with populated Entries), every item is processed in order
// while honouring --playlist-items / --no-playlist.
func (y *YoutubeDL) Download(rawURL string) error {
	info, err := y.extract(rawURL)
	if err != nil {
		return err
	}
	if len(info.Entries) > 0 {
		if y.Opts.NoPlaylist {
			// Download only the first entry, matching yt-dlp's --no-playlist.
			if len(info.Entries) > 0 && info.Entries[0] != nil {
				return y.processInfo(info.Entries[0])
			}
			return nil
		}
		return y.downloadPlaylist(info)
	}
	return y.processInfo(info)
}

// DownloadURLs runs the pipeline for multiple URLs concurrently. A failure on
// one URL is reported but does not abort the others (error isolation). It
// returns the slice of errors (empty when every URL succeeded).
func (y *YoutubeDL) DownloadURLs(urls []string) []error {
	errs := make([]error, len(urls))
	var wg sync.WaitGroup
	for i, u := range urls {
		wg.Add(1)
		go func(i int, u string) {
			defer wg.Done()
			if e := y.Download(u); e != nil {
				errs[i] = e
			}
		}(i, u)
	}
	wg.Wait()

	out := make([]error, 0, len(urls))
	for _, e := range errs {
		if e != nil {
			out = append(out, e)
		}
	}
	return out
}

// downloadPlaylist iterates the playlist entries, applying --playlist-items and
// isolating per-item errors (one bad item does not stop the rest).
func (y *YoutubeDL) downloadPlaylist(pl *extractor.Info) error {
	var firstErr error
	idx := 0
	for _, entry := range pl.Entries {
		if entry == nil {
			continue
		}
		idx++
		if !y.Opts.WantsPlaylistItem(idx) {
			continue
		}
		if err := y.processInfo(entry); err != nil {
			logPrintf(os.Stderr, "[error] playlist item %d (%s): %v\n", idx, entry.Title, err)
			if firstErr == nil {
				firstErr = err
			}
			// error isolation: keep going through the remaining items.
		}
	}
	return firstErr
}

// fetchSponsorChapters fetches SponsorBlock skip segments for the video and
// returns them as "[SponsorBlock]: <category>" chapters, sorted by start time.
// Failures are non-fatal (nil is returned on error or when no segments exist).
func (y *YoutubeDL) fetchSponsorChapters(info *extractor.Info) []extractor.Chapter {
	if info.ID == "" {
		return nil
	}
	cats := sponsorblock.ParseCategories(y.Opts.SponsorblockCategories)
	segs, err := sponsorblock.Fetch(y.Client, info.ID, cats)
	if err != nil {
		if y.Opts.Verbose {
			logPrintf(os.Stderr, "[sponsorblock] %v\n", err)
		}
		return nil
	}
	if len(segs) == 0 {
		return nil
	}
	chapters := make([]extractor.Chapter, 0, len(segs))
	for _, s := range segs {
		chapters = append(chapters, extractor.Chapter{
			Title:     "[SponsorBlock]: " + s.Category,
			StartTime: s.StartTime,
			EndTime:   s.EndTime,
		})
	}
	sort.SliceStable(chapters, func(i, j int) bool {
		return chapters[i].StartTime < chapters[j].StartTime
	})
	return chapters
}

// markSponsorblock appends pre-fetched SponsorBlock chapters to info.Chapters
// as "[SponsorBlock]: <category>" markers (the yt-dlp --sponsorblock-mark
// behaviour).
func (y *YoutubeDL) markSponsorblock(info *extractor.Info, chapters []extractor.Chapter) {
	if len(chapters) == 0 {
		return
	}
	info.Chapters = append(info.Chapters, chapters...)
	sort.SliceStable(info.Chapters, func(i, j int) bool {
		return info.Chapters[i].StartTime < info.Chapters[j].StartTime
	})
	// Log to stderr so -j/--dump-json stdout stays pure JSON.
	logPrintf(os.Stderr, "[sponsorblock] %d segments marked as chapters\n", len(chapters))
}

// processInfo runs the download + postprocess pipeline for one already-extracted
// Info (a single video or a playlist entry).
func (y *YoutubeDL) processInfo(info *extractor.Info) error {
	// SponsorBlock runs first so --dump-json / --print see the marked chapters.
	var sponsorChapters []extractor.Chapter
	if y.Opts.SponsorblockMark || y.Opts.SponsorblockRemove {
		sponsorChapters = y.fetchSponsorChapters(info)
		if y.Opts.SponsorblockMark {
			y.markSponsorblock(info, sponsorChapters)
		}
	}

	// --print: render a single field and exit.
	if y.Opts.PrintField != "" {
		s, err := output.Render(y.Opts.PrintField, info)
		if err != nil {
			return err
		}
		logPrintln(os.Stdout, s)
		return nil
	}

	// dump-json.
	if y.Opts.PrintToStdout != "" {
		b, _ := json.MarshalIndent(info, "", "  ")
		logPrintln(os.Stdout, string(b))
		return nil
	}

	// Simulate / skip-download.
	if y.Opts.Simulate || y.Opts.SkipDownload {
		logPrintf(os.Stdout, "[simulate] %s (%d formats)\n", info.Title, len(info.Formats))
		return nil
	}

	if y.Opts.Verbose {
		logPrintf(os.Stdout, "[info] title=%q id=%s formats=%d\n", info.Title, info.ID, len(info.Formats))
	}

	// --download-archive: skip already-recorded IDs.
	if y.Opts.DownloadArchive != "" && archiveHas(y.Opts.DownloadArchive, info.ID) {
		logPrintf(os.Stdout, "[download] %s already in archive, skipping\n", info.ID)
		return nil
	}

	groups, err := format.Select(info.Formats, y.Opts.Format, y.Opts.FormatSort)
	if err != nil {
		return err
	}

	base := y.outputBase(info)
	if y.Opts.TrimFilenames > 0 {
		base = trimPath(base, y.Opts.TrimFilenames)
	}

	for _, group := range groups {
		final := groupFinalPath(base, group, y.Opts)
		if y.Opts.NoOverwrites && fileExists(final) {
			encrypted := false
			for _, f := range group {
				if f.DecryptionKey != "" {
					encrypted = true
					break
				}
			}
			if !encrypted {
				logPrintf(os.Stdout, "[download] %s exists, skipping (--no-overwrite)\n", final)
				continue
			}
		}
		videoPath, audioPath, downloadErr := y.downloadGroup(group, base)
		if downloadErr != nil {
			return downloadErr
		}
		if videoPath == "" && audioPath == "" {
			continue
		}

		// Merge separate video+audio streams into one container via ffmpeg.
		if videoPath != "" && audioPath != "" {
			ff, ferr := postprocessor.FindFFmpeg(y.Opts)
			if ferr == nil {
				container := y.Opts.MergeOutputFormat
				if container == "" {
					container = "mkv"
				}
				final = fmt.Sprintf("%s.%s", base, container)
				if merr := postprocessor.Merge(videoPath, audioPath, final, container, ff); merr == nil {
					logPrintf(os.Stdout, "[postprocess] merged -> %s\n", final)
					_ = os.Remove(videoPath)
					_ = os.Remove(audioPath)
				} else if y.Opts.Verbose {
					logPrintf(os.Stderr, "[warn] merge failed: %v\n", merr)
				}
			} else if y.Opts.Verbose {
				logPrintf(os.Stderr, "[warn] %v\n", ferr)
			}
		} else if videoPath != "" {
			final = videoPath
		} else {
			final = audioPath
		}

		// --remux-video
		if y.Opts.RemuxVideo != "" {
			if ff, ferr := postprocessor.FindFFmpeg(y.Opts); ferr == nil {
				pp := postprocessor.FFmpegVideoRemux{FFmpeg: ff, RemuxFormat: y.Opts.RemuxVideo}
				if out, rerr := pp.Process(final, y.Opts); rerr == nil {
					logPrintf(os.Stdout, "[postprocess] remuxed -> %s\n", out)
					final = out
				} else if y.Opts.Verbose {
					logPrintf(os.Stderr, "[warn] remux failed: %v\n", rerr)
				}
			}
		}

		// --sponsorblock-remove: cut the sponsor segments out of the video.
		if y.Opts.SponsorblockRemove && len(sponsorChapters) > 0 {
			if ff, ferr := postprocessor.FindFFmpeg(y.Opts); ferr == nil {
				pp := postprocessor.SponsorBlockRemove{FFmpeg: ff, Segments: sponsorChapters, Duration: info.Duration}
				if out, rerr := pp.Process(final, y.Opts); rerr == nil {
					logPrintf(os.Stdout, "[postprocess] sponsor segments removed -> %s\n", out)
					final = out
				} else if y.Opts.Verbose {
					logPrintf(os.Stderr, "[warn] sponsorblock remove failed: %v\n", rerr)
				}
			}
		}

		// --extract-audio
		if y.Opts.ExtractAudio {
			if ff, ferr := postprocessor.FindFFmpeg(y.Opts); ferr == nil {
				pp := postprocessor.FFmpegExtractAudio{FFmpeg: ff, AudioFormat: y.Opts.AudioFormat, AudioQuality: y.Opts.AudioQuality}
				if out, aerr := pp.Process(final, y.Opts); aerr == nil {
					logPrintf(os.Stdout, "[postprocess] audio -> %s\n", out)
					final = out
				} else if y.Opts.Verbose {
					logPrintf(os.Stderr, "[warn] audio extract failed: %v\n", aerr)
				}
			}
		}

		// --embed-chapters
		if y.Opts.EmbedChapters && len(info.Chapters) > 0 {
			if ff, ferr := postprocessor.FindFFmpeg(y.Opts); ferr == nil {
				pp := postprocessor.FFmpegEmbedChapters{FFmpeg: ff, Chapters: info.Chapters, Duration: info.Duration}
				if out, cerr := pp.Process(final, y.Opts); cerr == nil {
					logPrintf(os.Stdout, "[postprocess] chapters -> %s\n", out)
					final = out
				} else if y.Opts.Verbose {
					logPrintf(os.Stderr, "[warn] embed chapters failed: %v\n", cerr)
				}
			}
		}

		// --embed-metadata
		if y.Opts.EmbedMetadata {
			if ff, ferr := postprocessor.FindFFmpeg(y.Opts); ferr == nil {
				pp := postprocessor.FFmpegMetadata{
					FFmpeg:       ff,
					Title:        info.Title,
					Artist:       info.Uploader,
					Date:         info.UploadDate,
					Description:  info.Description,
					ViewCount:    info.ViewCount,
					LikeCount:    info.LikeCount,
					CommentCount: info.CommentCount,
					RepostCount:  info.RepostCount,
				}
				if out, merr := pp.Process(final, y.Opts); merr == nil {
					logPrintf(os.Stdout, "[postprocess] metadata -> %s\n", out)
					final = out
				} else if y.Opts.Verbose {
					logPrintf(os.Stderr, "[warn] embed metadata failed: %v\n", merr)
				}
			}
		}
	}

	// Write thumbnail.
	if y.Opts.WriteThumbnail && info.Thumbnail != "" {
		if err := y.writeThumbnail(info.Thumbnail, base); err != nil && y.Opts.Verbose {
			logPrintf(os.Stderr, "[warn] write thumbnail failed: %v\n", err)
		}
	}

	// Write description.
	if y.Opts.WriteDescription && info.Description != "" {
		_ = os.WriteFile(base+".description", []byte(info.Description), 0o644)
	}

	// Subtitles.
	if y.Opts.WriteSubs {
		y.writeSubtitles(info, base)
	}

	// Write info JSON.
	if y.Opts.WriteInfoJSON {
		b, _ := json.MarshalIndent(info, "", "  ")
		_ = os.WriteFile(base+".info.json", b, 0o644)
	}

	// Record to archive.
	if y.Opts.DownloadArchive != "" {
		_ = archiveAppend(y.Opts.DownloadArchive, info.ID)
	}
	return nil
}

// downloadGroup downloads all formats in a group, returning the video and audio
// file paths (empty when that stream was not present).
func (y *YoutubeDL) downloadGroup(group []extractor.Format, base string) (videoPath, audioPath string, retErr error) {
	for _, f := range group {
		kind := ""
		if len(group) > 1 {
			if f.ACodec != "" && f.VCodec == "" {
				kind = "audio"
			} else if f.VCodec != "" {
				kind = "video"
			}
		}
		path := outPath(base, kind, f.Ext)
		if f.DecryptionKey != "" && fileExists(path) {
			ffmpeg, err := postprocessor.FindFFmpeg(y.Opts)
			if err != nil {
				return "", "", fmt.Errorf("decrypting %s: %w", f.FormatID, err)
			}
			if postprocessor.IsPlayable(path, ffmpeg) {
				logPrintf(os.Stdout, "[download] %s already decrypted, skipping\n", path)
			} else if err := postprocessor.DecryptCENC(path, f.DecryptionKey, ffmpeg); err != nil {
				return "", "", fmt.Errorf("decrypting existing %s: %w", f.FormatID, err)
			} else {
				logPrintf(os.Stdout, "[postprocess] decrypted existing file -> %s\n", path)
			}
			switch kind {
			case "video":
				videoPath = path
			case "audio":
				audioPath = path
			default:
				videoPath = path
			}
			continue
		}
		if err := y.downloadFormat(f.URL, f, path); err != nil {
			return "", "", fmt.Errorf("downloading %s: %w", f.FormatID, err)
		}
		if f.DecryptionKey != "" {
			ffmpeg, err := postprocessor.FindFFmpeg(y.Opts)
			if err != nil {
				return "", "", fmt.Errorf("decrypting %s: %w", f.FormatID, err)
			}
			if err := postprocessor.DecryptCENC(path, f.DecryptionKey, ffmpeg); err != nil {
				return "", "", fmt.Errorf("decrypting %s: %w", f.FormatID, err)
			}
			logPrintf(os.Stdout, "[postprocess] decrypted -> %s\n", path)
		}
		logPrintf(os.Stdout, "[download] %s -> %s\n", f.FormatID, path)
		switch kind {
		case "video":
			videoPath = path
		case "audio":
			audioPath = path
		default:
			videoPath = path // single-format group
		}
	}
	return videoPath, audioPath, nil
}

// groupFinalPath estimates the on-disk path produced for a group (used for the
// --no-overwrite check).
func groupFinalPath(base string, group []extractor.Format, o *options.Options) string {
	if len(group) > 1 {
		container := o.MergeOutputFormat
		if container == "" {
			container = "mkv"
		}
		return fmt.Sprintf("%s.%s", base, container)
	}
	f := group[0]
	ext := f.Ext
	if o.ExtractAudio && o.AudioFormat != "" && o.AudioFormat != "best" {
		ext = o.AudioFormat
	}
	if o.RemuxVideo != "" {
		ext = o.RemuxVideo
	}
	return fmt.Sprintf("%s.%s", base, ext)
}

func trimPath(base string, n int) string {
	dir := filepath.Dir(base)
	name := filepath.Base(base)
	r := []rune(name)
	if len(r) > n {
		name = string(r[:n])
	}
	return filepath.Join(dir, name)
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// writeThumbnail saves the cover image next to the media file. A data: URI is
// decoded locally (some sites only expose inline images); anything else is
// downloaded.
func (y *YoutubeDL) writeThumbnail(thumbURL, base string) error {
	if strings.HasPrefix(thumbURL, "data:") {
		return writeInlineThumbnail(thumbURL, base)
	}
	return y.downloadThumbnail(thumbURL, base)
}

// writeInlineThumbnail decodes a data: URI and writes it with an extension
// derived from its media type.
func writeInlineThumbnail(thumbURL, base string) error {
	du, err := extractor.DecodeDataURL(thumbURL)
	if err != nil {
		return err
	}
	if len(du.Data) == 0 {
		return fmt.Errorf("empty inline thumbnail")
	}
	ext := du.Ext
	if ext == "" {
		ext = "jpg"
	}
	return os.WriteFile(base+"."+ext, du.Data, 0o644)
}

// downloadThumbnail fetches the cover image to base + a suitable extension.
func (y *YoutubeDL) downloadThumbnail(thumbURL, base string) error {
	req, err := http.NewRequest(http.MethodGet, thumbURL, nil)
	if err != nil {
		return err
	}
	resp, err := y.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: status %d", thumbURL, resp.StatusCode)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	ext := thumbnailExt(resp.Header.Get("Content-Type"), thumbURL)
	return os.WriteFile(base+ext, data, 0o644)
}

// thumbnailExt picks a file extension from a Content-Type header, falling back
// to the URL's path suffix, then to .jpg.
func thumbnailExt(contentType, u string) string {
	switch {
	case strings.Contains(contentType, "png"):
		return ".png"
	case strings.Contains(contentType, "webp"):
		return ".webp"
	case strings.Contains(contentType, "avif"):
		return ".avif"
	case strings.Contains(contentType, "jpeg"), strings.Contains(contentType, "jpg"):
		return ".jpg"
	}
	low := strings.ToLower(u)
	for _, ext := range []string{".png", ".webp", ".avif", ".jpg", ".jpeg"} {
		if strings.Contains(low, ext) {
			if ext == ".jpeg" {
				return ".jpg"
			}
			return ext
		}
	}
	return ".jpg"
}

// writeSubtitles downloads (and optionally converts) the requested subtitle tracks.
func (y *YoutubeDL) writeSubtitles(info *extractor.Info, base string) {
	dir := filepath.Dir(base)
	name := filepath.Base(base)
	wanted := langFilter(y.Opts.SubLangs)
	for lang, subs := range info.Subtitles {
		if !langWanted(wanted, lang) {
			continue
		}
		for _, s := range subs {
			if s.URL == "" {
				continue
			}
			ref := extractor.SubtitleRef{Lang: lang, URL: s.URL, Ext: s.Ext}
			dst, derr := extractor.DownloadSubtitle(context.Background(), y.Client, ref, dir, name)
			if derr != nil {
				if y.Opts.Verbose {
					logPrintf(os.Stderr, "[warn] subtitle %s: %v\n", lang, derr)
				}
				continue
			}
			if y.Opts.ConvertSubs != "" && s.Ext != y.Opts.ConvertSubs {
				if ff, ferr := postprocessor.FindFFmpeg(y.Opts); ferr == nil {
					pp := postprocessor.FFmpegSubtitlesConvertor{FFmpeg: ff, OutputExt: y.Opts.ConvertSubs}
					if out, cerr := pp.Process(dst, y.Opts); cerr == nil {
						_ = os.Remove(dst)
						dst = out
					}
				}
			}
			logPrintf(os.Stdout, "[subtitle] %s -> %s\n", lang, dst)
		}
	}
}

func langFilter(spec string) []string {
	if spec == "" {
		return nil
	}
	parts := strings.Split(spec, ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

func langWanted(wanted []string, lang string) bool {
	if len(wanted) == 0 {
		return true // none specified -> all
	}
	for _, w := range wanted {
		if w == "all" || w == lang || strings.HasPrefix(lang, w+"-") || strings.HasPrefix(w, lang+"-") {
			return true
		}
	}
	return false
}

func archiveHas(path, id string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == id {
			return true
		}
	}
	return false
}

func archiveAppend(path, id string) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(id + "\n")
	return err
}

func (y *YoutubeDL) extract(rawURL string) (*extractor.Info, error) {
	ie := extractor.MatchURL(rawURL)
	if ie == nil {
		return nil, fmt.Errorf("no extractor found for %q", rawURL)
	}
	ctx := &extractor.Context{Client: y.Client, Options: y.Opts, Headers: y.Opts.AddHeaders}
	return ie.Extract(ctx, rawURL)
}

func (y *YoutubeDL) downloadFormat(refURL string, f extractor.Format, path string) error {
	var dl downloader.Downloader
	if f.Protocol == "m3u8_native" || f.Protocol == "dash" {
		dl = downloader.FragmentDownloader{}
	} else {
		dl = downloader.HTTPDownloader{}
	}
	dopts := downloader.DownloadOpts{
		Client:              y.Client,
		Headers:             mergeHeaders(y.Opts.AddHeaders, f.Headers),
		Retries:             y.Opts.Retries,
		ConcurrentFragments: y.Opts.ConcurrentFragments,
		RateLimit:           y.Opts.LimitRate,
		Format:              f,
	}
	return dl.Download(context.Background(), f.URL, path, dopts)
}

// mergeHeaders overlays per-format headers on top of the global add-headers.
// Format-level headers (e.g. a Referer for a specific CDN) win on conflict.
func mergeHeaders(base, override map[string]string) map[string]string {
	if len(override) == 0 {
		return base
	}
	out := make(map[string]string, len(base)+len(override))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range override {
		out[k] = v
	}
	return out
}

// ---- output naming ----

func (y *YoutubeDL) outputBase(info *extractor.Info) string {
	dir := y.Opts.OutputDir
	if dir == "" {
		dir = "."
	}
	if y.Opts.OutputTemplate != "" {
		name, err := output.Render(y.Opts.OutputTemplate, info)
		if err == nil && name != "" {
			// If the template already embedded the extension via %(ext)s, drop
			// that trailing extension so the per-format extension (e.g. m4s) is
			// not appended a second time by outPath.
			if info.Ext != "" && strings.HasSuffix(name, "."+info.Ext) {
				name = strings.TrimSuffix(name, "."+info.Ext)
			}
			return filepath.Join(dir, output.SanitizePath(name))
		}
		// On template error, fall through to the default naming.
	}
	// Default naming: the title is a single filename component, not a path.
	// Apply Sanitize (not SanitizePath) so that '/' (and other punctuation) in
	// the title is rewritten to '_' instead of creating subdirectories.
	name := info.Title
	if name == "" {
		name = info.ID
	}
	return filepath.Join(dir, output.Sanitize(name))
}

func outPath(base, kind, ext string) string {
	if kind == "" {
		return base + "." + ext
	}
	return fmt.Sprintf("%s.%s.%s", base, kind, ext)
}
