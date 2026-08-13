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
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"yt-dlp-go/downloader"
	"yt-dlp-go/extractor"
	_ "yt-dlp-go/extractor/acast"
	_ "yt-dlp-go/extractor/generic"
	_ "yt-dlp-go/extractor/youtube"
	"yt-dlp-go/format"
	"yt-dlp-go/network"
	"yt-dlp-go/options"
	"yt-dlp-go/postprocessor"
)

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

// Download runs the full pipeline for a single URL.
func (y *YoutubeDL) Download(rawURL string) error {
	if y.Opts.Simulate || y.Opts.PrintToStdout != "" {
		info, err := y.extract(rawURL)
		if err != nil {
			return err
		}
		if y.Opts.PrintToStdout != "" {
			b, _ := json.MarshalIndent(info, "", "  ")
			fmt.Println(string(b))
		} else {
			fmt.Printf("[simulate] %s (%d formats)\n", info.Title, len(info.Formats))
		}
		return nil
	}

	info, err := y.extract(rawURL)
	if err != nil {
		return err
	}
	if y.Opts.Verbose {
		fmt.Printf("[info] title=%q id=%s formats=%d\n", info.Title, info.ID, len(info.Formats))
	}

	groups, err := format.Select(info.Formats, y.Opts.Format)
	if err != nil {
		return err
	}

	base := y.outputBase(info)
	for _, group := range groups {
		var videoPath, audioPath string
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
			if err := y.downloadFormat(rawURL, f, path); err != nil {
				return fmt.Errorf("downloading format %s: %w", f.FormatID, err)
			}
			fmt.Printf("[download] %s -> %s\n", f.FormatID, path)
			switch kind {
			case "video":
				videoPath = path
			case "audio":
				audioPath = path
			}
		}

		// Merge separate video+audio streams into one container via ffmpeg.
		if videoPath != "" && audioPath != "" {
			ff, ferr := postprocessor.FindFFmpeg(y.Opts)
			if ferr == nil {
				container := y.Opts.MergeOutputFormat
				if container == "" {
					container = "mkv"
				}
				final := fmt.Sprintf("%s.%s", base, container)
				if merr := postprocessor.Merge(videoPath, audioPath, final, container, ff); merr == nil {
					fmt.Printf("[postprocess] merged -> %s\n", final)
					_ = os.Remove(videoPath)
					_ = os.Remove(audioPath)
				} else if y.Opts.Verbose {
					fmt.Printf("[warn] merge failed: %v\n", merr)
				}
			} else if y.Opts.Verbose {
				fmt.Printf("[warn] %v\n", ferr)
			}
		}
	}

	if y.Opts.WriteInfoJSON {
		b, _ := json.MarshalIndent(info, "", "  ")
		_ = os.WriteFile(base+".info.json", b, 0o644)
	}
	return nil
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
		Headers:             y.Opts.AddHeaders,
		Retries:             y.Opts.Retries,
		ConcurrentFragments: y.Opts.ConcurrentFragments,
	}
	return dl.Download(context.Background(), f.URL, path, dopts)
}

// ---- output naming ----

func (y *YoutubeDL) outputBase(info *extractor.Info) string {
	name := info.Title
	if name == "" {
		name = info.ID
	}
	name = sanitize(name)
	dir := y.Opts.OutputDir
	if dir == "" {
		dir = "."
	}
	return filepath.Join(dir, name)
}

func sanitize(s string) string {
	r := strings.NewReplacer("/", "_", "\\", "_", ":", "_", "*", "_", "?", "_", "\"", "_", "<", "_", ">", "_", "|", "_")
	return r.Replace(s)
}

func outPath(base, kind, ext string) string {
	if kind == "" {
		return base + "." + ext
	}
	return fmt.Sprintf("%s.%s.%s", base, kind, ext)
}
