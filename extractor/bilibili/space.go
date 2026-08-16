// BilibiliSpaceIE extracts a UP master's full video-upload list from
// https://space.bilibili.com/<mid>/upload/video and the /video equivalent.
//
// The endpoint it talks to is the same WBI-signed arc/search used by yt-dlp's
// BilibiliSpaceIE: GET https://api.bilibili.com/x/space/wbi/arc/search with
// mid/pn/ps/order/tid. vlist items lack cid (they're archive metadata, not
// video page state), so for each entry we issue one extra view call to fetch
// the cid before resolving the playurl ladder.
//
// The resulting Info is a playlist: each upload becomes an Entry with its own
// Formats. Options.WantsPlaylistItem (--playlist-items) is honoured here so
// users can request a subset (e.g. "--playlist-items 1-5") of a long upload
// list without downloading everything.
package bilibili

import (
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"yt-dlp-go/extractor"
)

// BilibiliSpaceIE matches and extracts UP-master space (upload list) pages.
type BilibiliSpaceIE struct{}

func init() { extractor.Register(BilibiliSpaceIE{}) }

func (BilibiliSpaceIE) Name() string { return "bilibili:space" }

// spaceURLRE matches https://space.bilibili.com/<mid> with optional trailing
// path segments (/upload/video, /upload, /video, ...). It captures the mid.
var spaceURLRE = regexp.MustCompile(`(?i)space\.bilibili\.com/(\d+)(?:/.*)?$`)

// bareMidRE matches a bare decimal string so users can pass the mid directly
// (no URL needed).
var bareMidRE = regexp.MustCompile(`^\d{4,}$`)

func (BilibiliSpaceIE) Match(u string) bool {
	return spaceURLRE.MatchString(u) || bareMidRE.MatchString(strings.TrimSpace(u))
}

func extractSpaceMid(u string) (string, bool) {
	u = strings.TrimSpace(u)
	if m := spaceURLRE.FindStringSubmatch(u); m != nil {
		return m[1], true
	}
	if bareMidRE.MatchString(u) {
		return u, true
	}
	return "", false
}

// Extract resolves a UP-master upload list into a playlist Info.
//
//   - WBI keys are fetched once and reused across pages and entries.
//   - arc/search is paginated with pn=1..N, ps=30, stopping when vlist is
//     empty or page.count indicates the end.
//   - For each upload we fetch view?bvid=… once (to recover the cid) before
//     falling through to the existing playurl resolver. A failed entry is
//     skipped with a verbose log; the rest of the playlist still downloads.
//   - --playlist-items is honoured by indexing into the assembled entry list
//     so a partial range (e.g. "1-5") works without downloading everything.
func (BilibiliSpaceIE) Extract(ctx *extractor.Context, pageURL string) (*extractor.Info, error) {
	mid, ok := extractSpaceMid(pageURL)
	if !ok {
		return nil, fmt.Errorf("could not parse Bilibili space mid from %q", pageURL)
	}

	keys, err := fetchWbiKeys(ctx)
	if err != nil {
		return nil, fmt.Errorf("bilibili space %s: WBI keys: %w", mid, err)
	}

	uploads, err := fetchSpaceUploads(ctx, mid, keys)
	if err != nil {
		return nil, err
	}
	if len(uploads) == 0 {
		return nil, fmt.Errorf("bilibili space %s: no videos found", mid)
	}

	pl := &extractor.Info{
		ID:         fmt.Sprintf("space-%s", mid),
		Title:      fmt.Sprintf("UP %s uploads", mid),
		WebpageURL: fmt.Sprintf("https://space.bilibili.com/%s/upload/video", mid),
		Entries:    make([]*extractor.Info, 0, len(uploads)),
		Ext:        "mp4",
		Subtitles:  map[string][]extractor.Subtitle{},
	}

	for i, up := range uploads {
		// 1-based index so --playlist-items "1-3,7" matches yt-dlp semantics.
		if ctx.Options != nil && !ctx.Options.WantsPlaylistItem(i + 1) {
			continue
		}

		cid, title, pub, dur, view, author, ferr := fetchViewMetadata(ctx, up.BVID)
		if ferr != nil {
			if ctx.Options != nil && ctx.Options.Verbose {
				fmt.Printf("[bilibili:space] view %s unavailable: %v\n", up.BVID, ferr)
			}
			continue
		}

		entry := &extractor.Info{
			ID:           up.BVID,
			Title:        title,
			Uploader:     author,
			Channel:      author,
			UploadDate:   pub,
			Thumbnail:    httpsThumbnail(up.Pic),
			WebpageURL:   "https://www.bilibili.com/video/" + up.BVID + "/",
			Ext:          "mp4",
			Duration:     dur,
			ViewCount:    view,
			Subtitles:    map[string][]extractor.Subtitle{},
		}

		formats, ferr := fetchFormatsForCid(ctx, up.BVID, cid, keys)
		if ferr != nil {
			if ctx.Options != nil && ctx.Options.Verbose {
				fmt.Printf("[bilibili:space] playurl %s unavailable: %v\n", up.BVID, ferr)
			}
		} else {
			entry.Formats = formats
		}

		pl.Entries = append(pl.Entries, entry)
	}

	if len(pl.Entries) == 0 {
		return nil, fmt.Errorf("bilibili space %s: no entries could be resolved", mid)
	}
	return pl, nil
}

// spaceUpload is one entry from arc/search list.vlist — just the archive
// fields (no cid, since this is not the video page state).
type spaceUpload struct {
	BVID    string
	Title   string
	Pic     string
	Created int64 // unix seconds
	Length  string // "MM:SS" or "HH:MM:SS"
}

// fetchSpaceUploads paginates arc/search and returns every upload it finds.
// Stops when vlist is empty. Each page is 30 items (B站 limit is 100 but 30
// keeps the per-call response size manageable).
func fetchSpaceUploads(ctx *extractor.Context, mid string, keys *wbiKeys) ([]spaceUpload, error) {
	const pageSize = 30
	var out []spaceUpload
	for pn := 1; ; pn++ {
		page, total, err := fetchSpacePage(ctx, mid, pn, pageSize, keys)
		if err != nil {
			return nil, err
		}
		out = append(out, page...)
		// total is the server-reported upload count; once we've collected
		// that many items (or vlist comes back empty), stop paging.
		if len(page) == 0 || (total > 0 && len(out) >= total) {
			break
		}
		// Defensive ceiling so a buggy total can't loop forever.
		if pn > 5000 {
			break
		}
	}
	return out, nil
}

// fetchSpacePage issues one arc/search request and returns its vlist.
func fetchSpacePage(ctx *extractor.Context, mid string, pn, ps int, keys *wbiKeys) ([]spaceUpload, int, error) {
	params := url.Values{}
	params.Set("mid", mid)
	params.Set("pn", strconv.Itoa(pn))
	params.Set("ps", strconv.Itoa(ps))
	params.Set("order", "pubdate") // newest-first, mirrors the page default
	params.Set("tid", "0")
	params.Set("keyword", "")
	params.Set("platform", "web")
	params.Set("web_location", "1550101")
	wts := time.Now().Unix()
	params.Set("w_rid", wbiSign(keys.imgKey, keys.subKey, params, wts))

	body, err := extractor.DownloadJSON(ctx, "https://api.bilibili.com/x/space/wbi/arc/search", nil, params)
	if err != nil {
		return nil, 0, fmt.Errorf("arc/search pn=%d: %w", pn, err)
	}

	code := extractor.IntOrNone(extractor.TraverseObj(body, "code"))
	if code != 0 {
		msg := extractor.StrOrNone(extractor.TraverseObj(body, "message"))
		return nil, 0, fmt.Errorf("arc/search pn=%d: code=%d %s", pn, code, msg)
	}

	vlist, _ := extractor.TraverseObj(body, "data", "list", "vlist").([]any)
	uploads := make([]spaceUpload, 0, len(vlist))
	for _, raw := range vlist {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		uploads = append(uploads, spaceUpload{
			BVID:    extractor.StrOrNone(m["bvid"]),
			Title:   extractor.StrOrNone(m["title"]),
			Pic:     extractor.StrOrNone(m["pic"]),
			Created: int64(extractor.IntOrNone(m["created"])),
			Length:  extractor.StrOrNone(m["length"]),
		})
	}
	total := int(extractor.IntOrNone(extractor.TraverseObj(body, "data", "page", "count")))
	return uploads, total, nil
}

// fetchViewMetadata resolves the cid and stats for a single bvid by hitting
// the public view endpoint. This is the same call the page renderer makes
// server-side, so it's free of WBI / login requirements for public videos.
func fetchViewMetadata(ctx *extractor.Context, bvid string) (cid int64, title, pub string, duration float64, view int64, author string, err error) {
	params := url.Values{}
	params.Set("bvid", bvid)
	body, err := extractor.DownloadJSON(ctx, "https://api.bilibili.com/x/web-interface/view", nil, params)
	if err != nil {
		return 0, "", "", 0, 0, "", err
	}
	data, ok := extractor.TraverseObj(body, "data").(map[string]any)
	if !ok {
		return 0, "", "", 0, 0, "", fmt.Errorf("view %s: missing data", bvid)
	}
	cid = int64(extractor.IntOrNone(extractor.TraverseObj(data, "cid")))
	title = extractor.StrOrNone(data["title"])
	duration = extractor.FloatOrNone(data["duration"])
	view = int64(extractor.IntOrNone(extractor.TraverseObj(data, "stat", "view")))
	author = extractor.StrOrNone(extractor.TraverseObj(data, "owner", "name"))
	if pubSec := int64(extractor.IntOrNone(data["pubdate"])); pubSec > 0 {
		pub = time.Unix(pubSec, 0).UTC().Format("20060102")
	}
	if cid == 0 {
		return 0, "", "", 0, 0, "", fmt.Errorf("view %s: missing cid", bvid)
	}
	return cid, title, pub, duration, view, author, nil
}
