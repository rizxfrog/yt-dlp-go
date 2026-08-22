// Package hongguo implements native extraction for Hongguo short dramas.
// It accepts public share/detail/player URLs and resolves fqnovel's signed video
// API without Python, plugins, or an external source tree.
package hongguo

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"yt-dlp-go/extractor"
)

type HongguoIE struct{}

func init()                    { extractor.Register(HongguoIE{}) }
func (HongguoIE) Name() string { return "hongguo" }

var (
	hongguoInputRE = regexp.MustCompile(`(?i)^(?:hongguo:\d{10,20}|https?://(?:www\.)?(?:novelquickapp\.com/s/[A-Za-z0-9]+/?|hongguoduanju\.com/\S*))$`)
	decimalIDRE    = regexp.MustCompile(`\d{10,20}`)
	seriesIDRE     = regexp.MustCompile(`"series_id"\s*:\s*"?(\d{10,20})"?`)
	deviceOnce     sync.Once
	processDevice  signerDevice
)

const videoModelBase = "https://api5-normal-sinfonlineb.fqnovel.com/novel/player/multi_video_model/v1/"
const videoModelQuery = "iid=%s&device_id=%s&ac=wifi&channel=update_64&aid=8662&app_name=novelread&version_code=71332&version_name=7.1.3.32&device_platform=android&os=android&ssmix=a&device_type=25053RT47C&device_brand=Redmi&language=zh&os_api=36&os_version=16&manifest_version_code=71332&resolution=1280*2772&dpi=520&update_version_code=71332&host_abi=arm64-v8a&dragon_device_type=phone&pv_player=71332&compliance_status=0&need_personal_recommend=1&player_so_load=1&is_android_pad_screen=0"

var typeCodeCandidates = []string{"1004", "1001", "0", "1", "2", "19"}

func (HongguoIE) Match(raw string) bool { return hongguoInputRE.MatchString(strings.TrimSpace(raw)) }

func (HongguoIE) Extract(ctx *extractor.Context, raw string) (*extractor.Info, error) {
	raw = strings.TrimSpace(raw)
	vid, err := inputVideoID(ctx, raw)
	if err != nil {
		return nil, err
	}
	name, vids, err := fetchSeries(ctx, vid)
	if err != nil {
		return nil, err
	}
	if len(vids) <= 1 {
		return resolveVideo(ctx, vid, name, 0)
	}

	entries := make([]*extractor.Info, len(vids))
	for i, epID := range vids {
		// Extraction precedes core playlist slicing. Avoid signed API calls for
		// entries the user did not request while retaining placeholders so the
		// core's 1-based playlist indexing remains correct.
		if ctx.Options != nil && ctx.Options.PlaylistItems != "" && !ctx.Options.WantsPlaylistItem(i+1) {
			entries[i] = &extractor.Info{ID: epID, Title: fmt.Sprintf("%s 第%d集", name, i+1)}
			continue
		}
		ep, resolveErr := resolveVideo(ctx, epID, name, i+1)
		if resolveErr != nil {
			return nil, fmt.Errorf("hongguo: resolve episode %d (%s): %w", i+1, epID, resolveErr)
		}
		entries[i] = ep
		if ctx.Options != nil && ctx.Options.NoPlaylist {
			entries = entries[:1]
			break
		}
	}
	return &extractor.Info{
		ID: vid, Title: firstNonEmpty(name, vid), WebpageURL: raw, Ext: "mp4",
		Subtitles: map[string][]extractor.Subtitle{}, Entries: entries,
	}, nil
}

func inputVideoID(ctx *extractor.Context, raw string) (string, error) {
	if strings.HasPrefix(raw, "hongguo:") {
		return strings.TrimPrefix(raw, "hongguo:"), nil
	}
	if strings.Contains(raw, "novelquickapp.com/s/") {
		return expandShare(ctx, raw)
	}
	u, err := url.Parse(raw)
	if err == nil {
		for _, key := range []string{"series_id", "video_id"} {
			if id := u.Query().Get(key); decimalIDRE.MatchString(id) {
				return decimalIDRE.FindString(id), nil
			}
		}
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		// An explicit player episode is the most precise ID.
		if len(parts) >= 3 && parts[0] == "player" && decimalIDRE.MatchString(parts[2]) {
			return decimalIDRE.FindString(parts[2]), nil
		}
	}
	if id := decimalIDRE.FindString(raw); id != "" {
		return id, nil
	}
	return "", fmt.Errorf("hongguo: no video_id/series_id in %q", raw)
}

func expandShare(ctx *extractor.Context, raw string) (string, error) {
	client := *ctx.Client
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	req, err := http.NewRequest(http.MethodGet, raw, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Linux; Android 13) AppleWebKit/537.36 Chrome/120 Mobile")
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("hongguo: expand short link: %w", err)
	}
	defer resp.Body.Close()
	location := resp.Header.Get("Location")
	if location == "" {
		location = raw
	}
	u, err := url.Parse(location)
	if err != nil {
		return "", err
	}
	zlink := u.Query().Get("zlink")
	if zlink == "" {
		return "", fmt.Errorf("hongguo: short-link redirect has no zlink")
	}
	zu, err := url.Parse(zlink)
	if err != nil {
		return "", err
	}
	scheme := zu.Query().Get("schemeParams")
	if scheme == "" {
		return "", fmt.Errorf("hongguo: zlink has no schemeParams")
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(scheme), &payload); err != nil {
		return "", fmt.Errorf("hongguo: decode schemeParams: %w", err)
	}
	for _, key := range []string{"video_id", "vid"} {
		if id := extractor.StrOrNone(payload[key]); decimalIDRE.MatchString(id) {
			return decimalIDRE.FindString(id), nil
		}
	}
	if report := extractor.StrOrNone(payload["report_params"]); report != "" {
		if decoded, decErr := url.QueryUnescape(report); decErr == nil {
			report = decoded
		}
		var rp map[string]any
		if json.Unmarshal([]byte(report), &rp) == nil {
			if id := extractor.StrOrNone(rp["content_id"]); decimalIDRE.MatchString(id) {
				return decimalIDRE.FindString(id), nil
			}
		}
	}
	return "", fmt.Errorf("hongguo: no video_id in share link")
}

func fetchSeries(ctx *extractor.Context, videoID string) (string, []string, error) {
	var html string
	var last error
	for i := 0; i < 6; i++ {
		html, last = extractor.DownloadWebpage(ctx, "https://hongguoduanju.com/detail?series_id="+videoID,
			map[string]string{"User-Agent": "Mozilla/5.0 (Linux; Android 13) AppleWebKit/537.36 Chrome/120 Mobile"}, nil)
		if last == nil && len(html) > 1000 {
			break
		}
		time.Sleep(time.Duration(i+1) * time.Second)
	}
	if last != nil {
		return "", nil, fmt.Errorf("hongguo: detail page: %w", last)
	}
	return extractSeries(html, videoID)
}

func extractSeries(html, target string) (string, []string, error) {
	matches := seriesIDRE.FindAllStringSubmatchIndex(html, -1)
	start, end := -1, len(html)
	for i, m := range matches {
		if html[m[2]:m[3]] == target {
			start = m[0]
			if i+1 < len(matches) {
				end = matches[i+1][0]
			}
			break
		}
	}
	if start < 0 {
		return "", nil, nil
	}
	segment := html[start:end]
	name := ""
	if m := regexp.MustCompile(`"series_name"\s*:\s*"([^"\\]{1,100})"`).FindStringSubmatch(segment); m != nil {
		name = m[1]
	}
	m := regexp.MustCompile(`"vid_list"\s*:\s*\[([^\]]*)\]`).FindStringSubmatch(segment)
	if m == nil {
		return name, nil, nil
	}
	ids := decimalIDRE.FindAllString(m[1], -1)
	seen := map[string]bool{}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return name, out, nil
}

type videoPayload struct {
	BizParam struct {
		DetailPageVersion      int  `json:"detail_page_version"`
		DeviceLevel            int  `json:"device_level"`
		DisableDiggStat        bool `json:"disable_digg_stat"`
		NeedAllVideoDefinition bool `json:"need_all_video_definition"`
		NeedMP4Align           bool `json:"need_mp4_align"`
		UseOSPlayer            bool `json:"use_os_player"`
		UseServerDNS           bool `json:"use_server_dns"`
		VideoPlatform          int  `json:"video_platform"`
	} `json:"biz_param"`
	MixedVideoIDMap map[string][]string `json:"mixed_video_id_map"`
}

func makeVideoPayload(typ, vid string) []byte {
	var p videoPayload
	p.BizParam.DeviceLevel = 3
	p.BizParam.NeedAllVideoDefinition = true
	p.BizParam.VideoPlatform = 1024
	p.MixedVideoIDMap = map[string][]string{typ: {vid}}
	b, _ := json.Marshal(p)
	return b
}

func stableDevice() signerDevice {
	deviceOnce.Do(func() {
		processDevice = signerDevice{DeviceID: validDeviceEnv("DUANJU_DEVICE_ID"), InstallID: validDeviceEnv("DUANJU_INSTALL_ID")}
		if processDevice.DeviceID == "" {
			processDevice.DeviceID = randomDecimalID()
		}
		if processDevice.InstallID == "" {
			processDevice.InstallID = randomDecimalID()
		}
	})
	return processDevice
}
func validDeviceEnv(name string) string {
	v := strings.TrimSpace(os.Getenv(name))
	if len(v) == 19 && regexp.MustCompile(`^\d{19}$`).MatchString(v) {
		return v
	}
	return ""
}
func randomDecimalID() string {
	b := make([]byte, 19)
	_, _ = rand.Read(b)
	b[0] = '1' + b[0]%9
	for i := 1; i < len(b); i++ {
		b[i] = '0' + b[i]%10
	}
	return string(b)
}

func resolveVideo(ctx *extractor.Context, vid, seriesName string, episode int) (*extractor.Info, error) {
	dev := stableDevice()
	params := parseOrderedQuery(fmt.Sprintf(videoModelQuery, dev.InstallID, dev.DeviceID))
	var model map[string]any
	var fallback string
	var lastErr error
	for _, typ := range typeCodeCandidates {
		body := makeVideoPayload(typ, vid)
		signedURL, headers, err := signRequest(videoModelBase, params, body, dev, time.Now(), newSignRandom())
		if err != nil {
			lastErr = err
			continue
		}
		root, err := requestJSON(ctx, http.MethodPost, signedURL, headers, body)
		if err != nil {
			lastErr = err
			continue
		}
		fallback, model, err = extractFallback(root, vid)
		if err == nil {
			break
		}
		lastErr = fmt.Errorf("type %s: %w (response=%s)", typ, err, compactJSON(root))
	}
	if fallback == "" {
		return nil, fmt.Errorf("signed video API failed: %w", lastErr)
	}
	fallbackRoot, err := requestJSON(ctx, http.MethodGet, fallback, map[string]string{"User-Agent": hongguoUserAgent}, nil)
	if err != nil {
		return nil, fmt.Errorf("fallback API: %w", err)
	}
	videoData, _ := extractor.TraverseObj(fallbackRoot, "video_info", "data").(map[string]any)
	if len(videoData) == 0 {
		return nil, fmt.Errorf("fallback API returned no video data")
	}
	variants, _ := videoData["video_list"].(map[string]any)
	best := selectBestVariant(variants)
	if best == nil {
		return nil, fmt.Errorf("fallback API returned no playable formats")
	}
	rawURL := mediaValue(best["main_url"])
	if rawURL == "" {
		rawURL = firstNonEmpty(mediaValue(best["play_addr"]), mediaValue(best["backup_url_1"]), mediaValue(best["url"]))
	}
	if rawURL == "" {
		return nil, fmt.Errorf("selected format has no URL")
	}
	if seedText := extractor.StrOrNone(videoData["key_seed"]); seedText != "" {
		if seed, decErr := decodeBase64(seedText); decErr == nil {
			if decoded, decErr := decryptSpadeURL(rawURL, seed); decErr == nil && strings.HasPrefix(decoded, "http") {
				rawURL = decoded
			}
		}
	}
	key := ""
	if spade := extractor.StrOrNone(best["spade_a"]); spade != "" {
		key, err = deriveContentKey(spade)
		if err != nil {
			return nil, err
		}
	}
	title := vid
	if seriesName != "" {
		title = seriesName
		if episode > 0 {
			title = fmt.Sprintf("%s 第%d集", seriesName, episode)
		}
	}
	duration := extractor.FloatOrNone(model["duration"])
	if duration == 0 {
		duration = extractor.FloatOrNone(videoData["duration"])
	}
	return &extractor.Info{
		ID: vid, Title: title, Thumbnail: firstNonEmpty(mediaValue(best["cover"]), mediaValue(best["poster"]), mediaValue(model["origin_cover"]), mediaValue(model["cover_url"])),
		Duration: duration, WebpageURL: "hongguo:" + vid, Ext: "mp4", Subtitles: map[string][]extractor.Subtitle{}, Raw: model,
		Formats: []extractor.Format{{FormatID: "cenc", URL: normalizeMediaURL(rawURL), Protocol: "http", Ext: "mp4",
			VCodec: "unknown", ACodec: "unknown",
			Width: extractor.IntOrNone(firstValue(best["vwidth"], best["width"])), Height: extractor.IntOrNone(firstValue(best["vheight"], best["height"])),
			TBR: extractor.FloatOrNone(best["bitrate"]) / 1000, Filesize: int64(extractor.FloatOrNone(firstValue(best["size"], best["data_size"], best["file_size"]))),
			Headers: map[string]string{"User-Agent": "com.phoenix.read/71332", "Referer": "https://novel.snssdk.com/"}, DecryptionKey: key}},
	}, nil
}

func compactJSON(v any) string {
	b, _ := json.Marshal(v)
	if len(b) > 300 {
		b = b[:300]
	}
	return string(b)
}

func requestJSON(ctx *extractor.Context, method, raw string, headers map[string]string, body []byte) (map[string]any, error) {
	var last error
	for attempt := 0; attempt < 3; attempt++ {
		req, err := http.NewRequest(method, raw, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		for k, v := range ctx.Headers {
			req.Header.Set(k, v)
		}
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		resp, err := ctx.Client.Do(req)
		if err == nil {
			data, readErr := io.ReadAll(resp.Body)
			resp.Body.Close()
			if readErr == nil && resp.StatusCode >= 200 && resp.StatusCode < 300 {
				var root map[string]any
				if json.Unmarshal(data, &root) == nil {
					return root, nil
				}
				last = fmt.Errorf("invalid JSON response")
			} else {
				last = fmt.Errorf("HTTP status %d", resp.StatusCode)
			}
		} else {
			last = err
		}
		time.Sleep(time.Duration(attempt+1) * 200 * time.Millisecond)
	}
	return nil, last
}

func extractFallback(root map[string]any, vid string) (string, map[string]any, error) {
	data, _ := root["data"].(map[string]any)
	if data == nil {
		return "", nil, fmt.Errorf("response has no data")
	}
	entry, _ := data[vid].(map[string]any)
	if entry == nil {
		return "", nil, fmt.Errorf("video %s absent from response", vid)
	}
	var model map[string]any
	switch vm := entry["video_model"].(type) {
	case map[string]any:
		model = vm
	case string:
		_ = json.Unmarshal([]byte(vm), &model)
	}
	if model == nil {
		return "", nil, fmt.Errorf("video_model is missing")
	}
	fallback := parseFallback(model["fallback_api"])
	if fallback == "" {
		return "", nil, fmt.Errorf("fallback_api is missing")
	}
	return fallback, model, nil
}
func parseFallback(v any) string {
	switch x := v.(type) {
	case string:
		if strings.HasPrefix(strings.TrimSpace(x), "{") {
			var m map[string]any
			if json.Unmarshal([]byte(x), &m) == nil {
				return mediaValue(m["fallback_api"])
			}
		}
		return x
	case []any:
		if len(x) > 0 {
			return mediaValue(x[0])
		}
	case map[string]any:
		return mediaValue(x["fallback_api"])
	}
	return ""
}
func selectBestVariant(v map[string]any) map[string]any {
	type candidate struct {
		h       int
		bitrate float64
		item    map[string]any
	}
	var all []candidate
	for _, raw := range v {
		if item, ok := raw.(map[string]any); ok {
			all = append(all, candidate{extractor.IntOrNone(firstValue(item["vheight"], item["height"])), extractor.FloatOrNone(item["bitrate"]), item})
		}
	}
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].h != all[j].h {
			return all[i].h > all[j].h
		}
		return all[i].bitrate > all[j].bitrate
	})
	if len(all) == 0 {
		return nil
	}
	return all[0].item
}
func mediaValue(v any) string {
	switch x := v.(type) {
	case string:
		return strings.TrimSpace(x)
	case []any:
		for _, e := range x {
			if s := mediaValue(e); s != "" {
				return s
			}
		}
	case map[string]any:
		for _, k := range []string{"url", "uri", "src", "download_url", "url_list", "urls"} {
			if s := mediaValue(x[k]); s != "" {
				return s
			}
		}
	}
	return ""
}
func firstValue(v ...any) any {
	for _, x := range v {
		if x != nil && extractor.StrOrNone(x) != "" {
			return x
		}
	}
	return nil
}
func firstNonEmpty(v ...string) string {
	for _, x := range v {
		if x != "" {
			return x
		}
	}
	return ""
}
func normalizeMediaURL(v string) string {
	if strings.HasPrefix(v, "//") {
		return "https:" + v
	}
	return v
}
func atoi(s string) int { n, _ := strconv.Atoi(s); return n }
