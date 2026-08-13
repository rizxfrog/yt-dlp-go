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
| 4 | 下载器健壮性 | 🔲 待做 | httptest 单测 | 断点续传 / 限速 / 退避重试 / 进度 |
| 5 | 后处理器集合 | 🔲 待做 | 参数构建单测 | 提取音频/重封装/元数据/字幕嵌入/转换 |
| 6 | 字幕支持 | 🔲 待做 | 解析单测 | 结构 + 下载 + 转换 |
| 7 | 选项与 CLI 扩展 | 🔲 待做 | 解析单测 | archive / 日期过滤 / 播放列表项 / 不覆盖 |
| 8 | 播放列表 + 并发 | 🔲 待做 | 合成数据单测 | 列表提取 + 多 URL 并发 + 错误隔离 |
| 9 | 更多提取器 | 🔲 待做 | 结构/解析单测 | Bilibili / TikTok / YouTube 增强 |
| 10 | TLS 伪装（utls） | 🔲 待做（可选） | 编译 + 真实请求抽查 | 替换 header-only 伪装 |
| 11 | 测试/修复/集成/文档 | 🔲 待做 | `go test ./...` + README/PLAN 更新 | 收尾提交 |

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
