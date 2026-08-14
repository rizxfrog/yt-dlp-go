# yt-dlp-go 实施计划（实现 yt-dlp 大部分功能）

> 目标：在 `D:\code\yt-dlp-go` 用 **Go（仅标准库优先，必要处引入少量成熟依赖）**
> 实现一个功能上覆盖 yt-dlp **大部分核心能力** 的下载引擎：网络、格式选择、输出模板、
> 多种协议下载（HTTP / HLS / DASH）、后处理、字幕、播放列表、并发与续传，并配套
> 一个可运行的提取器子集（YouTube / Bilibili / TikTok / Acast / 直链兜底）。
>
> 本文件即进度看板：每个任务完成后更新「状态」与「备注」，并 `git commit`。

---

## 0. 环境与约束（已确认）

- 沙箱本无 Go 工具链、无网络；现已 **离线下载并解压 Go 1.26.6** 到 `D:/_gotoolchain/go`，
  通过 `PATH=/d/_gotoolchain/go/bin:$PATH` 使用（每次 Bash 调用都需带上该 export）。
- 网络已可用（`go get` 可拉取依赖），因此计划中的可选依赖（如签名引擎、TLS 伪装）可落地。
- **纯逻辑模块（格式选择 / 输出模板 / MPD 解析 / 选项解析 / 续传逻辑）可离线单元测试**，
  这是「测试→修复→再测试」主循环的核心。
- 依赖**真实站点**的提取器（YouTube/Bilibili/TikTok）无法在离线环境完整验证，
  仅能验证结构、解析函数与降级路径；在线行为以单测 + 代码审查保障，运行时可能随站点改版失效
  （与真实 yt-dlp 一致）。

---

## 1. 总体架构（与 yt-dlp 对齐）

```
main.go
  └─ options.Parse          # CLI 解析（覆盖 yt-dlp 常用开关）
       └─ core.YoutubeDL    # 编排：匹配 → 提取 → 选流 → 下载 → 后处理
            ├─ extractor.Registry   # 各站点 Extractor 经 init() 注册
            │    ├─ youtube / bilibili / tiktok / acast / generic
            │    └─ 共享 helpers：TraverseObj / CleanHTML / ParseISO8601 / JS 签名求值
            ├─ format.Select        # -f 选择器引擎
            ├─ output.Render        # -o 输出模板引擎
            ├─ downloader           # HTTP / HLS(native) / DASH(native)
            └─ postprocessor        # ffmpeg 编排（合并/提取音频/元数据/字幕/嵌入）
```

---

## 2. 任务清单（状态看板）

| # | 任务 | 状态 | 验证方式 | 备注 |
|---|------|------|----------|------|
| 0 | 工具链 + 基线编译 | ✅ 完成 | `go build ./...` 通过 | 修复 3 处编译错误（hls.go depth、youtube.go err、core.go v 作用域） |
| 1 | 格式选择引擎 `-f` | ✅ 完成 | `go test ./format/` 通过 | best/worst/+ 合并// 回退/[filter]；已接入 core（替换原 selectFormats） |
| 2 | 输出模板引擎 `-o` | ✅ 完成 | `go test ./output/` 通过 | `%(field)s`/默认`|`/大小写`u l`/日期`>strftime`/duration/raw 路径/`%%`；已接入 core.outputBase |
| 3 | DASH 原生下载器 | ✅ 完成 | `go test ./downloader/` 通过 | MPD 解析（SegmentTemplate/Base/List）+ 并发分片下载；已接入 FragmentDownloader（按 .mpd 分发） |
| 4 | 下载器健壮性 | ✅ 完成 | `go test ./downloader/` 通过 | HTTP 续传(Range+`.part`原子重命名)/限速/指数退避重试/进度；httptest 覆盖基础/续传/503重试/404不重试/限速解析 |
| 5 | 后处理器集合 | ✅ 完成 | `go test ./postprocessor/` 通过 | FFmpegExtractAudio/VideoRemux/Metadata(标题/艺术家/日期+缩略图)/EmbedSubtitle/SubtitlesConvertor；参数构建单测（无需 ffmpeg）。core 合并已用，extract-audio/metadata 链入待阶段7 |
| 6 | 字幕支持 | ✅ 完成 | `go test ./extractor/` 通过 | ParseHLSSubtitles(#EXT-X-MEDIA 解析) + DownloadSubtitle(httptest 单测)；info.Subtitles 结构已就绪 |
| 7 | 选项与 CLI 扩展 | ✅ 完成 | `go test ./options/` 通过 | 新增 --download-archive/--no-overwrite/--dateafter|before/--playlist-items/--write-subs/--sub-langs/--convert-subs/-x --audio-format/--remux-video/--trim-filenames/--print/--yes\|no-playlist；全部接入 core（print/archive/no-overwrite/extract-audio/remux/subs/trim） |
| 8 | 播放列表 + 并发 | ✅ 完成 | `go test ./core/` 通过 | `Info.Entries []*Info` 承载播放列表；`Download` 分流到 `downloadPlaylist`（按 `--playlist-items` 过滤、`--no-playlist` 仅取首条、单条失败不中断）；新增 `DownloadURLs` 多 URL goroutine 并发 + 错误隔离；新增 `extractor.ExtractURL` 供列表提取器复用；日志走 `printMu` 互斥避免并发交错；main 改为调用 `DownloadURLs`。单测：Playlist / PlaylistItems(1,3) / Concurrency(错误隔离) 全绿 |
| 9 | 更多提取器 | ✅ 完成 | `go test ./extractor/...` 通过 | 新增 `extractor/bilibili`（解析 `window.__INITIAL_STATE__` 元数据 + 纯 Go 实现 WBI 签名 `wbiSign`/playurl DASH/durl 解析，均已单测）、`extractor/tiktok`（og:video meta + `__NEXT_DATA__` 两种形态解析，均已单测）；YouTube 增强：提取 `captionTracks` 字幕（`extractSubtitles`）+ 正确解析时长，单测覆盖字幕与签名求值；classify 增加 `.slice(` 识别；新提取器已注册进 core |
| 10 | TLS 伪装（utls） | ✅ 完成 | `go build -tags utls` + `go test -tags utls` 通过 | 默认构建保持纯标准库（见下）；`-tags utls` 时用 `github.com/refraction-networking/utls` 替换 transport 的 `DialTLSContext`，按 `--impersonate chrome/firefox/safari/edge` 复刻真实 ClientHello（HelloChrome/Firefox/Safari/Edge_Auto）。实现为 build-tag 双文件：`transport_stdlib.go`(no-op) + `transport_utls.go`(utls dialer)；`configureTLS` 钩子接入 `NewClient`。注意依赖经 goproxy.cn 拉取（proxy.golang.org 不可达） |
| 11 | 测试/修复/集成/文档 | ✅ 完成 | `go test ./...` 全绿 + utls 构建通过 | 更新 README（功能清单/构建说明/已知限制）、main 帮助文本；`gofmt -w` 全量格式化；默认构建（纯标准库）与 `-tags utls` 构建均 `build/vet/test` 通过；全部测试绿 |
| 12 | YouTube 签名求值器升级为 goja | ✅ 完成 | `go test ./extractor/` 全绿（含对象字面量/内联两种形态） | `extractor/jseval.go` 改为以 `github.com/dop251/goja`（纯 Go ES 引擎）直接求值播放器 transform 函数；保留原正则 classify 管线作为降级兜底。新增 `collectDefs`/`extractBlock`/`findTransform`/`referencedNames`；transform 识别改为 `split("")...+join("")` 特征，按源码顺序扫描保证确定性；新增 `jseval_test.go`（对象字面量 helper `var V={...}`、内联、无 transform 报错三种）。go.mod 引入 goja（direct dep） |
| 13 | n/nsig 求值 + sts 注入 | ✅ 完成 | `go test ./extractor/ ./extractor/youtube/` 全绿 | `extractor/jseval.go`：`DeobfuscateSignature` 新增 `sts` 第二参数（新播放器 `function(a,b)` 形态）；新增 `DeobfuscateNSig(playerJS, n)`（goja 求值 n/throttling 变换，复用 `deobfuscateGoja` 通用求值器 + `findNTransform`/`isNTransform` 启发式：含 charAt/substr/substring/slice 且有 return、且非 split("")+join("") 的签名变换）；通用求值器抽出 `deobfuscateGoja(findFn)` + `quoteAll`。`extractor/youtube/youtube.go`：从 `player.sts` 取时间戳并传入签名；`buildFormat` 新增 `rewriteNParam`（解析 URL 的 `n`，经 `DeobfuscateNSig` 重写，失败则原样返回）；`needsJS` 判定新增「URL 含 n=」场景（直链也可能带 n 节流参数）。测试：NSig（goja/无变换报错）、sts 透传、buildFormat 重写 n / 无 JS 不动 |
| 14 | n 函数按调用点精确定位 | ✅ 完成 | `go test ./extractor/` 全绿（含 call-site + module-form） | 把 `findNTransform` 由「扫描全部函数定义、按 body 形状猜测」改为「**按播放器调用点定位**」：`findNByCallSite` 用正则 `reSelfCall`（`x = NAME(x)` 自赋值调用，同标识符两侧 + 单参数）与 `reSelfCallMember`（`x = (0, MOD.NAME)(x)` 模块形态）定位 n 变换被「原地应用」的位置，解析 NAME 到定义并校验 `isNTransform`；旧 body-shape 扫描降级为 `findNByBody` 兜底。新增 `resolveNShaped`/`findMethodInObj` 校验辅助；`deobfuscateGoja` 的 `expand` 增强：成员调用名（如 `M.fo`）自动嵌入父对象 `M`。测试：`TestDeobfuscateNSig_CallSite`（含早于 n 变换出现的诱饵 n 形状函数，验证选中被应用的变换而非诱饵）、`TestDeobfuscateNSig_ModuleCallSite`（模块形态） |
| 15 | Bilibili 端到端可用 + 三处下载 bug 修复 | ✅ 完成 | 真实网络验证通过（bilibili 在本沙箱可达，YouTube 不可达）；`go build/vet/test ./...` 全绿 | **实测 Bilibili 现在可下载**：提取 `window.__INITIAL_STATE__` → 纯 Go WBI 签名 → playurl DASH，成功拉到真实视频（《船新版本新宝岛！》7 格式）并以 `-f best` 默认合并下载产出可播放 `_dltest/BV1mJuB6jEDj.mkv`（h264 480x852 + AAC，54s，7.1MB，ffprobe 校验通过）。修复三个阻塞默认下载的 bug：(1) `format.Select` 的 `best`/`worst` 原按「单一合并格式」选取，对 DASH/分离流（Bilibili、YouTube）永远落空；改为 `bestvideo+bestaudio` 合并（无分离流时回退到合并格式），`format_test.go` 同步更新。(2) `output.SanitizePath` 新增：原 `Sanitize` 把 `/` 也替换成 `_`，导致 `-o` 模板里子目录失效；现仅 sanitize 各路径分段、保留分隔符；并去掉模板 `%(ext)s` 被重复拼接的扩展名。(3) `postprocessor.Merge` 原显式传 `-f container`（如 `-f mkv`），本机 ffmpeg 构建拒绝该写法；改为交由输出扩展名推断 muxer，合并成功。注意：高清(>720p)仍需 SESSDATA cookie（`--cookies`） |

图例：✅ 完成  🔲 待做  🔧 进行中  ⚠️ 受阻

---

## 3. 各阶段设计要点

### 阶段 1 — 格式选择引擎 `format` 包
- 输入：`info.Formats`（含 VCodec/ACodec/Height/Width/Filesize/TBR/Ext/Protocol/FormatID）
- 语法（对齐 yt-dlp `-f`）：
  - 原子选择：`best`(`bestvideo+bestaudio` 合并)、`worst`、`bestvideo`、`bestaudio`
  - 按 id：`137` / `137+140`(合并) / `137-138`(范围)
  - 分组与回退：`a/b/c` 表示依次尝试
  - 过滤器：`[height<=720]`、`[ext=mp4]`、`[protocol!=http]`、`[vcodec!=none]`、`[acodec=none]`
  - 排序：`+sort:height,-tbr`（在组前用 `+` 前缀时为排序而非合并——实现为显式 `sort:` 指令）
- 输出：`[]Format`（含「合并两组」的标记，供 core 分两次下载再 ffmpeg 合并）
- 关键函数：`Select(formats, selector) (groups [][]Format, err error)`

### 阶段 2 — 输出模板引擎 `output` 包
- 语法：`%(title)s`、`%(uploader)s`、`%(duration)S`(秒)、`%(upload_date>%Y-%m-%d)s`、
  `%(id)s`、`%(ext)s`、`%(epoch)s`、默认值 `%(title|id)s`
- 字段来源：Info 的命名字段 + `Raw` map 的任意嵌套路径 `%(raw.field.sub)s`
- 清洗：非法文件名字符替换、长度截断（`--trim-filenames`）
- 函数：`Render(template string, info *Info) (string, error)`

### 阶段 3 — DASH 原生下载器
- 解析 MPD：`<MPD>` → `<Period>` → `<AdaptationSet>` → `<Representation>`
  - `SegmentBase@indexRange` + `Initialization` → init 分片 + 单一媒体分片
  - `SegmentTemplate@duration/media/init` + `timescale` + `startNumber` → 计算 N 个分片 URL
  - `SegmentList` → 显式分片列表
- 下载：按 Period/AdaptationSet 拉取分片 → 先写 init 再拼接媒体分片 → 交 ffmpeg 重封装

### 阶段 4 — 下载器健壮性
- HTTP：`HEAD`/`Range` 探测可续传 → 断点续传；限速令牌桶；失败指数退避重试；
  部分文件临时名 `*.part` → 成功后重命名；进度回调（已下载/总量）。
- 用 `net/http/httptest` 起本地服务器，模拟 206/限速/断开，验证上述逻辑。

### 阶段 5 — 后处理器集合（ffmpeg 编排）
- `FFmpegExtractAudio`：`mp3/aac/m4a/opus/flac/wav`（含比特率、音质）
- `FFmpegVideoRemux` / `FFmpegVideoConvertor`
- `FFmpegMetadata`：写 `title/artist/date/description` + 嵌入缩略图
- `FFmpegEmbedSubtitle`、`FFmpegSubtitlesConvertor`
- `FixupStretched`/`FixupRedirect` 等轻量修复
- 仅构建参数并调用 `ffmpeg`；无 ffmpeg 时标记跳过并保留原始文件。

### 阶段 6 — 字幕
- `Info.Subtitles map[lang][]Subtitle`（url/ext/name）
- 下载与格式转换（vtt/srt/ass）；HLS `#EXT-X-MEDIA` 与 YouTube `captionTracks` 解析。
- 可选嵌入（走阶段 5 的嵌入后处理器）。

### 阶段 7 — 选项与 CLI 扩展
- `--download-archive FILE`：记录已下 ID，跳过重复
- `--dateafter/--datebefore YYYYMMDD`
- `--playlist-items 1-5,8` / `--yes-playlist` / `--no-playlist`
- `--no-overwrites` / `--continue` / `--trim-filenames N` / `--no-colors`
- `--print %(field)s`、`--simulate` 精细化
- 每项为 `options` 增加字段 + flag，并写解析单测。

### 阶段 8 — 播放列表与并发
- 通用 `Playlist` 提取：从列表/频道页抽取子视频 URL（各提取器可返回多条目）
- `Info` 增加 `Entries []*Info` 或 `PlaylistURLs []string`
- core 层对多 URL 使用 goroutine + errgroup 并发，单条失败不中断整体。

### 阶段 9 — 更多提取器
- **Bilibili**：`bilibili.com/video/BVxxx`、`.tv` 短链；解析 `window.__INITIAL_STATE__` /
  `playurl` API；支持多清晰度（需登录 cookie 才有高清）。
- **TikTok**：`vm.tiktok.com`/`tiktok.com`；解析 `@__NEXT_DATA__` 或 `/api/v1/...`。
- **YouTube 增强**：可选引入 `github.com/dop251/goja` 直接执行播放器签名函数，
  替代正则求值器（`DeobfuscateSignature` 作为降级）；增强 natsort、n参数。

### 阶段 10 — TLS 伪装（可选）
- 引入 `github.com/refraction-networking/utls`，为 `--impersonate chrome` 提供真实
  ClientHello（替换 network 包当前的 header-only 伪装）。保留标准库回退。

---

## 4. 非目标（本次不做）

- 不 1:1 移植全部 ~2000 个站点提取器（维护成本与 Go 重编译分发矛盾，见可行性报告）。
- 不实现 GUI / WebUI / 订阅管理。
- 不实现 `yt-dlp` 全部 CLI 开关（仅覆盖高频常用项，架构可扩展）。

---

## 5. 工作流约定

1. 每完成一个阶段：`go build ./...` → `go vet ./...` → `go test ./...`。
2. 每阶段提交一次：`git add -A && git commit -m "feat: ..."`。
3. PLAN.md 状态表同步更新（✅/🔧/⚠️）。
4. 遇真实站点依赖无法离线验证，明确标注并在 README「已知限制」记录。
