package pornhub

import (
	"fmt"
	neturl "net/url"
	"regexp"
	"strings"

	"yt-dlp-go/extractor"
)

// The playlist extractors mirror PornHubUserIE, PornHubPagedVideoListIE,
// PornHubUserVideosUploadIE and PornHubPlaylistIE from upstream. Each one only
// has to enumerate video URLs; the heavy lifting (flashvars -> formats) is
// delegated back to PornHubIE through ExtractURL so a playlist entry is a
// fully-resolved Info.

var (
	// PornHubUserIE._VALID_URL: /users/<id>, /channels/<id>, /model/<id>,
	// /pornstar/<id>. RE2 has no lookahead, so the "but not /videos" guard
	// upstream expresses as `(?:[?#&]|/(?!videos)|$)` is enforced in code by
	// userMatch() below rather than inside the pattern.
	userURLRE = regexp.MustCompile(`(?i)(?P<url>https?://(?:[a-zA-Z0-9.-]+\.)?` + hostRE +
		`/(?:(?:user|channel)s|model|pornstar)/(?P<id>[^/?#&]+))(?:[?#&]|/|$)`)

	// PornHubPagedVideoListIE._VALID_URL: any other PornHub path. The
	// `/playlist/` exclusion (a negative lookahead upstream) is applied in
	// pagedMatch.
	pagedURLRE = regexp.MustCompile(`(?i)https?://(?:[^/]+\.)?` + hostRE +
		`/(?P<id>(?:[^/]+/)*[^/?#&]+)`)

	// PornHubUserVideosUploadIE._VALID_URL.
	uploadURLRE = regexp.MustCompile(`(?i)(?P<url>https?://(?:[^/]+\.)?` + hostRE +
		`/(?:(?:user|channel)s|model|pornstar)/(?P<id>[^/]+)/videos/upload)`)

	// PornHubPlaylistIE._VALID_URL.
	playlistURLRE = regexp.MustCompile(`(?i)(?P<url>https?://(?:[^/]+\.)?` + hostRE +
		`/playlist/(?P<id>[^/?#&]+))`)

	// One <a href="view_video.php?...viewkey=..." title="..."> entry.
	//
	// Upstream writes the captured part as `.*`, which is safe on the real site
	// because each <a> sits on its own line and `.` does not cross newlines. It
	// is tightened here to `[^>"]*` so a listing that packs several links onto
	// one line still yields one match per link instead of swallowing them all.
	entryRE = regexp.MustCompile(`href="/?(view_video\.php\?[^>"]*\bviewkey=[\da-z]+[^"]*)"[^>]*\s+title="([^"]+)"`)

	// The container div guards against the drop-down menus that reuse the same
	// link pattern (ytdl-org/youtube-dl#11594).
	containerRE = regexp.MustCompile(`(?s)(<div[^>]+class=["']container.+)`)

	pageNumRE    = regexp.MustCompile(`\bpage=(\d+)`)
	playlistIDRE = regexp.MustCompile(`var\s+playlistId\s*=\s*"([^"]+)"`)
	itemsCountRE = regexp.MustCompile(`var\s+itemsCount\s*=\s*([0-9]+)\s*\|\|`)
	tokenRE      = regexp.MustCompile(`var\s+token\s*=\s*"([^"]+)"`)
	// Any of these three markers means "there is a next page". Written as a
	// plain alternation because RE2 does not support Python's (?x) verbose
	// mode.
	hasMorePageRE = regexp.MustCompile(`<li[^>]+\bclass=["']page_next|<link[^>]+\brel=["']next|<button[^>]+\bid=["']moreDataBtn`)
)

// maxPlaylistPages caps pagination so a malformed "next page" marker cannot
// spin forever. Upstream iterates until an empty page or a missing marker; this
// is a hard stop with the same practical effect.
const maxPlaylistPages = 1000

// chunkURL builds the XHR endpoint a playlist pages through. A variable so
// tests can point it at a local server (see watchURL).
var chunkURL = func(host string) string {
	return fmt.Sprintf("https://www.%s/playlist/viewChunked", host)
}

// listingURL builds an absolute URL for a listing path (/video, /hd,
// /model/<id>/videos, ...). Like watchURL it is a variable so tests can serve
// the listing from a local server.
var listingURL = func(host, path string) string {
	return "https://www." + host + path
}

// PornHubUserIE resolves a user / channel / model / pornstar page.
type PornHubUserIE struct{}

func (PornHubUserIE) Name() string { return "pornhub:user" }

// Match is userURLRE minus the /videos sub-pages: those belong to
// PornHubPagedVideoListIE, and upstream separates them with a lookahead RE2
// cannot express.
func (PornHubUserIE) Match(u string) bool {
	if !userURLRE.MatchString(u) {
		return false
	}
	// /model/<id>/videos belongs to PornHubPagedVideoListIE; /model/<id>/
	// videos/upload to PornHubUserVideosUploadIE (checked first by the registry
	// because it registers later only if unreachable — see MatchURL order).
	if uploadURLRE.MatchString(u) {
		return false
	}
	return !isUserVideosSubPage(u)
}

func (PornHubUserIE) Extract(ctx *extractor.Context, pageURL string) (*extractor.Info, error) {
	userID := namedGroup(userURLRE, pageURL, "id")
	base := namedGroup(userURLRE, pageURL, "url")
	if userID == "" {
		return nil, fmt.Errorf("pornhub: could not parse user id from %q", pageURL)
	}
	path := sitePath(base) + "/videos"
	if p := firstGroup(pageNumRE, pageURL); p != "" {
		path = withPage(path, p)
	}
	return pagedList(ctx, hostOf(pageURL), path, userID)
}

// PornHubPagedVideoListIE handles every paginated listing: /video, /hd,
// /categories/<cat>, /video/search?search=..., /model/<id>/videos, ...
type PornHubPagedVideoListIE struct{}

func (PornHubPagedVideoListIE) Name() string { return "pornhub:paged" }

// Match excludes /playlist/ (upstream's negative lookahead) plus the paths
// the more specific extractors already own.
func (PornHubPagedVideoListIE) Match(u string) bool {
	if u == "" || !pagedURLRE.MatchString(u) {
		return false
	}
	path := urlPath(u)
	if strings.HasPrefix(path, "/playlist/") {
		return false
	}
	// Defer to the more specific extractors. The user check has to apply the
	// same /videos carve-out PornHubUserIE uses, otherwise /model/<id>/videos
	// would be claimed by neither extractor.
	if videoURLRE.MatchString(u) || uploadURLRE.MatchString(u) {
		return false
	}
	if userURLRE.MatchString(u) && !isUserVideosSubPage(u) {
		return false
	}
	// This extractor is the catch-all for PornHub paths, so it also has to step
	// aside for anything that *looks* like a watch page even when the URL is
	// malformed enough that videoURLRE rejects it. A shell that fails to swallow
	// its own escapes turns `?viewkey=` into `\?viewkey\=`, and without this
	// guard that URL lands here and is fetched as a listing, producing a
	// confusing 404 with a `page=1` query bolted on.
	if isWatchPath(path) {
		return false
	}
	return true
}

// isWatchPath reports whether a URL path points at a watch page
// (view_video.php or video/show). It is checked as a path prefix rather than a
// full-URL regex so a mangled query string still routes to PornHubIE, which
// reports a precise "cannot parse video id" error instead of a 404.
func isWatchPath(path string) bool {
	base := path
	if i := strings.IndexAny(base, "?#"); i >= 0 {
		base = base[:i]
	}
	// Trim any trailing junk a mis-escaped URL leaves on the path (a stray
	// backslash from `\?`, or a slash), then compare the last one or two
	// segments against the watch-page script names.
	base = strings.TrimRight(base, "/\\")
	return strings.HasSuffix(base, "view_video.php") || strings.HasSuffix(base, "video/show")
}

// isUserVideosSubPage reports whether u is a user page's /videos listing (as
// opposed to the user page itself), which belongs to PornHubPagedVideoListIE.
func isUserVideosSubPage(u string) bool {
	loc := userURLRE.FindStringSubmatchIndex(u)
	if loc == nil || !strings.HasSuffix(u[:loc[1]], "/") {
		return false
	}
	seg := u[loc[1]:]
	if i := strings.IndexAny(seg, "/?#&"); i >= 0 {
		seg = seg[:i]
	}
	return strings.EqualFold(seg, "videos")
}

// urlPath returns the lowercased path of a URL, or "" when it will not parse.
func urlPath(u string) string {
	p, err := neturl.Parse(u)
	if err != nil {
		return ""
	}
	return strings.ToLower(p.Path)
}

// sitePath returns the raw (case-preserving) path plus any query string of a
// URL, or "" when it will not parse. Listing paths are site-relative and are
// later turned absolute by listingURL.
func sitePath(u string) string {
	p, err := neturl.Parse(u)
	if err != nil {
		return ""
	}
	out := p.Path
	if p.RawQuery != "" {
		out += "?" + p.RawQuery
	}
	return out
}

func (PornHubPagedVideoListIE) Extract(ctx *extractor.Context, pageURL string) (*extractor.Info, error) {
	id := namedGroup(pagedURLRE, pageURL, "id")
	if id == "" {
		return nil, fmt.Errorf("pornhub: could not parse list id from %q", pageURL)
	}
	path := sitePath(pageURL)
	if path == "" {
		return nil, fmt.Errorf("pornhub: could not parse list path from %q", pageURL)
	}
	return pagedList(ctx, hostOf(pageURL), path, id)
}

// PornHubUserVideosUploadIE handles /model/<id>/videos/upload.
type PornHubUserVideosUploadIE struct{}

func (PornHubUserVideosUploadIE) Name() string { return "pornhub:upload" }

func (PornHubUserVideosUploadIE) Match(u string) bool { return uploadURLRE.MatchString(u) }

func (PornHubUserVideosUploadIE) Extract(ctx *extractor.Context, pageURL string) (*extractor.Info, error) {
	id := namedGroup(uploadURLRE, pageURL, "id")
	base := namedGroup(uploadURLRE, pageURL, "url")
	if id == "" || base == "" {
		return nil, fmt.Errorf("pornhub: could not parse upload id from %q", pageURL)
	}
	return pagedList(ctx, hostOf(pageURL), sitePath(base), id)
}

// PornHubPlaylistIE handles /playlist/<id>, which pages through a chunked XHR
// endpoint instead of ?page=N.
type PornHubPlaylistIE struct{}

func (PornHubPlaylistIE) Name() string { return "pornhub:playlist" }

func (PornHubPlaylistIE) Match(u string) bool { return playlistURLRE.MatchString(u) }

func (PornHubPlaylistIE) Extract(ctx *extractor.Context, pageURL string) (*extractor.Info, error) {
	host := hostOf(pageURL)
	itemID := namedGroup(playlistURLRE, pageURL, "id")
	base := namedGroup(playlistURLRE, pageURL, "url")
	if itemID == "" || base == "" {
		return nil, fmt.Errorf("pornhub: could not parse playlist id from %q", pageURL)
	}

	headers := siteHeaders(host)
	headers["Cookie"] = ageCookieHeader()
	path := sitePath(base)
	if path == "" {
		return nil, fmt.Errorf("pornhub: could not parse playlist path from %q", pageURL)
	}
	webpage, err := extractor.DownloadWebpage(ctx, listingURL(host, path), headers, nil)
	if err != nil {
		return nil, err
	}

	playlistID := firstGroup(playlistIDRE, webpage)
	token := firstGroup(tokenRE, webpage)
	count := strToInt(firstGroup(itemsCountRE, webpage))

	pl := &extractor.Info{
		ID:         itemID,
		Title:      itemID,
		WebpageURL: pageURL,
	}
	pl.Entries = entriesFromPage(ctx, webpage, host)

	// Upstream's page-count formula: the first page carries 36 items and each
	// chunked page adds 40.
	pageCount := 1
	if count > 36 && token != "" && playlistID != "" {
		pageCount = (count-36+39)/40 + 1
	}
	for page := 2; page <= pageCount && page <= maxPlaylistPages; page++ {
		chunk, err := extractor.DownloadWebpage(ctx,
			chunkURL(host), headers, neturl.Values{
				"id":    {playlistID},
				"page":  {fmt.Sprint(page)},
				"token": {token},
			})
		if err != nil {
			break
		}
		got := entriesFromPage(ctx, chunk, host)
		if len(got) == 0 {
			break
		}
		pl.Entries = append(pl.Entries, got...)
	}
	if len(pl.Entries) == 0 {
		return nil, fmt.Errorf("pornhub: playlist %s has no resolvable entries", itemID)
	}
	return pl, nil
}

// ---- shared playlist helpers ----

// pagedList walks ?page=N until a page yields nothing or the "next page"
// marker disappears. A 404 on the first page falls back to the un-paged URL
// (ytdl-org/youtube-dl#27853: some premium sources have no /videos page).
//
// `path` is a site-relative path (e.g. "/model/zoe_ph/videos"); the absolute
// URL is built by listingURL so tests can serve the listing locally.
func pagedList(ctx *extractor.Context, host, path, itemID string) (*extractor.Info, error) {
	headers := siteHeaders(host)
	headers["Cookie"] = ageCookieHeader()

	pl := &extractor.Info{ID: itemID, Title: itemID, WebpageURL: listingURL(host, path)}

	start := 1
	if p := firstGroup(pageNumRE, path); p != "" {
		if n := strToInt(p); n > 0 {
			start = n
		}
	}

	// withPage works on a path just as well as on a full URL.
	base := strings.TrimSuffix(path, "/")
	webpage, err := downloadListing(ctx, host, withPage(base, fmt.Sprint(start)), headers)
	if err != nil && start == 1 && strings.Contains(base, "/videos") {
		// Fall back to pagination on the main page.
		base = strings.Replace(base, "/videos", "", 1)
		webpage, err = downloadListing(ctx, host, withPage(base, fmt.Sprint(start)), headers)
	}
	if err != nil {
		return nil, err
	}

	for page := start; page <= maxPlaylistPages; page++ {
		if page > start {
			next, err := downloadListing(ctx, host, withPage(base, fmt.Sprint(page)), headers)
			if err != nil {
				break // past the last page
			}
			webpage = next
		}
		got := entriesFromPage(ctx, webpage, host)
		if len(got) == 0 {
			break
		}
		pl.Entries = append(pl.Entries, got...)
		if !hasMorePageRE.MatchString(webpage) {
			break
		}
	}
	if len(pl.Entries) == 0 {
		return nil, fmt.Errorf("pornhub: no videos found on %s", pl.WebpageURL)
	}
	return pl, nil
}

// downloadListing fetches one listing page through listingURL.
func downloadListing(ctx *extractor.Context, host, path string, headers map[string]string) (string, error) {
	return extractor.DownloadWebpage(ctx, listingURL(host, path), headers, nil)
}

// entriesFromPage turns one listing page into resolved child Infos.
//
// Unlike upstream (which yields url_result placeholders and lets yt-dlp fetch
// each entry later), yt-dlp-go's core expects a playlist to be fully material,
// so every entry is extracted here. A failure on one entry is logged and
// skipped so the rest of the list still downloads.
func entriesFromPage(ctx *extractor.Context, webpage, host string) []*extractor.Info {
	container := firstGroup(containerRE, webpage)
	if container == "" {
		container = webpage
	}
	out := []*extractor.Info{}
	seen := map[string]bool{}
	for _, m := range entryRE.FindAllStringSubmatch(container, -1) {
		if len(m) < 3 {
			continue
		}
		href, title := unescapeHTML(m[1]), unescapeHTML(m[2])
		if seen[href] {
			continue
		}
		seen[href] = true
		// Call PornHubIE directly rather than going through ExtractURL: the
		// entry URL is built by watchURL, which does not have to be a PornHub
		// hostname (tests point it at a local server), so registry matching
		// would not find it.
		videoID := viewKeyOf(href)
		if videoID == "" {
			continue
		}
		child, err := PornHubIE{}.extractByID(ctx, videoID, host, "")
		if err != nil {
			if ctx.Options != nil && ctx.Options.Verbose {
				fmt.Printf("[pornhub] skipping %s: %v\n", href, err)
			}
			continue
		}
		if child.Title == "" {
			child.Title = title
		}
		out = append(out, child)
	}
	return out
}

// uploaderWatchURL absolutises a listing's relative href to the canonical
// watch page so PornHubIE matches it.
// viewKeyOf pulls the viewkey out of a listing entry's href, which is either
// `view_video.php?viewkey=<id>` or `video/show?viewkey=<id>`.
func viewKeyOf(href string) string {
	href = strings.TrimPrefix(href, "/")
	u, err := neturl.Parse(href)
	if err != nil {
		return ""
	}
	return u.Query().Get("viewkey")
}

// withPage sets (or replaces) the ?page= query parameter.
func withPage(u, page string) string {
	i := strings.IndexAny(u, "?#")
	var base, rest string
	if i >= 0 {
		base, rest = u[:i], u[i:]
	} else {
		base = u
	}
	if idx := strings.Index(rest, "?"); idx >= 0 {
		q, err := neturl.ParseQuery(strings.TrimPrefix(rest[idx:], "?"))
		if err == nil {
			q.Set("page", page)
			return base + "?" + q.Encode()
		}
	}
	return base + "?page=" + page
}
