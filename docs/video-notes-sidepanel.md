# YouTube 截帧笔记 Side Panel — 开发 Prompt

> 状态：未开始
>
> 用途：把本文直接交给 coding agent 作为实施规范。接口、安全约束和验收条件都应按本文实现；标记为"后续"的内容不在本期范围。

## 0. 一句话目标

在 YouTube 观看页，用户打开扩展的 Chrome Side Panel，可以**一键截取当前视频帧**并**记下与当前时间戳绑定的笔记**；所有笔记汇总为一篇 membox 文档 `yt-<video_id>-notes.md`，帧图存入 membox note-assets，与已有的 transcript / summary 文档通过 `video_id` 关联。

## 1. 为什么放在 clipper 扩展（背景结论，勿重新论证）

- 截帧必须访问页面里的 `<video>` 元素（`canvas.drawImage(video)`），只有浏览器扩展的 content script 能做到。
- `extensions/clipper`（WXT 0.20，Chrome MV3）已有：YouTube video_id 识别（`src/lib/youtube-captions.ts`）、bridge token 认证客户端（`src/lib/membox-client.ts`）、页内浮层 UI 模式（`src/lib/selection-card.ts`）。
- membox 后端已有：`POST /api/ingest`（Markdown 落盘）、`POST /api/note-assets`（content-addressed 图片存储，返回可嵌入 Markdown 的 URL）、`internal/videosummary/summary.go`（`video_id` 是 transcript/summary 的稳定身份）。

## 2. 总体结构

```text
sidepanel (新 entrypoint)           content script (扩展现有)
┌──────────────────────┐  message  ┌───────────────────────────┐
│ · 当前播放时间 ticker   │ ◄──────► │ · getTime: video.currentTime│
│ · 本视频笔记列表        │          │ · captureFrame:             │
│ · 笔记输入 + 截帧按钮   │          │   canvas.drawImage(video)   │
│ · 点击时间戳跳转播放    │ ──────►  │   → JPEG blob               │
└────────┬─────────────┘  seek     │ · seek(t): currentTime = t  │
         │ fetch (X-Membox-Token)  └───────────────────────────┘
         ▼
   http://127.0.0.1:8787 (membox bridge)
   POST /api/note-assets   ← 存帧图，得 url（本期需放开扩展 token 认证）
   POST /api/ingest        ← 笔记 Markdown upsert
```

## 3. 实施任务

### Task 1 — 后端：放开 note-assets 的扩展认证

文件：`internal/web/backend/note_assets_http.go`

- 现状：`handleNoteImageUpload` 强制 `X-Membox-Miru: 1`（同源 Miru 检查，第 24 行），扩展直接调用会 403。
- 修改：认证逻辑改为 **二选一**——
  1. `X-Membox-Miru: 1`（现有 Miru 路径，保持不变）；或
  2. 有效的 `X-Membox-Token`（复用 `bridge_http.go` 的 `requireBridgeToken`；`s.token == ""` 时放行，与 bridge 行为一致）。
- CORS：`withCORS`（`bridge_http.go`）已对 `chrome-extension://` / `moz-extension://` Origin 放行且允许 `Content-Type, X-Membox-Token` 头，确认 `/api/note-assets` 路由在该 middleware 覆盖下；不在则补上。
- 图片体：扩展以 raw body（`image/jpeg`）POST，走现有 `noteasset.Store` 分支，不要走 `source_path` JSON 分支（那是本地 Miru 粘贴路径）。
- 测试：在 `note_assets_http_test.go` 增加用例——无 Miru 头但带正确 token → 201；带错误 token → 401。

### Task 2 — content script：截帧与时间控制

文件：`extensions/clipper/src/entrypoints/content.ts`（新增一个 `src/lib/video-frame.ts` 承载逻辑，content.ts 只做消息接线）

新增三个 runtime message handler（供 sidepanel 调用）：

| message type | 行为 | 返回 |
| --- | --- | --- |
| `membox:get-time` | 读主 `<video>` 的 `currentTime` / `duration` / `paused` | `{ seconds, duration, paused }` |
| `membox:capture-frame` | `canvas.drawImage(video)`，输出 JPEG | `{ seconds, dataUrl, width, height }` |
| `membox:seek` | `video.currentTime = payload.seconds` | `{ ok: true }` |

约束：

- 视频选择器优先 `video.html5-main-video`（YouTube watch），回退到页面中面积最大的 `<video>`。
- 截帧 canvas 尺寸 = `video.videoWidth × videoHeight`；`toDataURL('image/jpeg', 0.85)`。JPEG 超过 2 MB 时降质量重试（0.7 → 0.55）。
- 视频未加载（`readyState < 2`）时返回明确错误，sidepanel 显示"视频尚未就绪"。
- YouTube 的 `<video>` 不带 crossorigin 问题（同源媒体流），`drawImage` 不会污染 canvas；若 `toDataURL` 抛 SecurityError，把错误透传给 sidepanel 而不是静默失败。

### Task 3 — sidepanel entrypoint（新 UI）

新目录：`extensions/clipper/src/entrypoints/sidepanel/`（`index.html` / `main.ts` / `style.css`）

- `wxt.config.ts` manifest 增加：`permissions` 加 `"sidePanel"`；`side_panel: { default_path: "sidepanel.html" }`（WXT 会自动生成）。
- `background.ts`：在 YouTube watch 页（`*://*.youtube.com/watch*`）点击扩展图标时 `chrome.sidePanel.open({ tabId })`；其他页面保持现有 popup 行为。可用 `chrome.sidePanel.setPanelBehavior({ openPanelOnActionClick: false })` + `tabs.onUpdated` 按 URL 切换 action 行为，注意不破坏现有 popup 流程。
- UI 三个区域（自上而下）：
  1. **视频信息条**：视频标题 + `video_id`；显示对应 membox 笔记文档是否已存在（复用 `fetchClipsBySource` 按 `source_url` 查）。
  2. **笔记列表**：本视频已有笔记按时间戳升序渲染；每条显示帧图缩略图（若有）、`HH:MM:SS`、笔记文本；点击时间戳发 `membox:seek` 跳播。
  3. **输入区**：当前播放时间每秒刷新（ticker，用 `membox:get-time` 轮询，1s 间隔，panel 不可见时停止）；文本框 + 「截帧」开关 + 「保存」按钮；快捷键 Cmd/Ctrl+Enter 保存（与 selection-card 一致）。
- 样式：复用 `src/lib/theme.ts` 的 Miru token / `prefers-color-scheme` 方案，不引入新 UI 框架。

### Task 4 — 保存管线（sidepanel → membox）

新文件：`extensions/clipper/src/lib/video-notes.ts`

保存一条笔记的流程：

1. 若用户开了「截帧」：先 `membox:capture-frame` 拿 `dataUrl`，转 `Blob`，`POST {base}/api/note-assets`（raw body，`Content-Type: image/jpeg`，带 `X-Membox-Token`），得到 `url`（形如 `/api/note-assets/<hash>.jpg`）。
2. 组装笔记文档并 `POST /api/ingest`：

   - **身份**：每个视频一篇文档，文件名约定由后端沿用 ingest 的 URL 去重逻辑；`source_url` = `https://www.youtube.com/watch?v=<video_id>`，`clip_mode: "video-notes"`。已存在时走 ingest 的 conflict/overwrite 流程做**整篇更新**（先读旧文档、追加新条目、再 overwrite 提交；读旧文档用 `GET /api/bridge/clips?source_url=...&mode=all`，若拿不到 body 则在 sidepanel 本地缓存全量条目作为 fallback——见「未决问题」）。
   - **frontmatter** 额外带 `video_id: "<id>"`，便于与 transcript/summary 关联。

   条目格式（追加到文档正文）：

   ```markdown
   ## [00:12:34](https://www.youtube.com/watch?v=<id>&t=754s)

   ![frame](/api/note-assets/<hash>.jpg)

   笔记正文……
   ```

   - 时间戳链接的 `t=` 参数是**秒数 floor**；标题里的 `HH:MM:SS` 与 transcript 文档的 `[HH:MM:SS]` 格式保持一致（复用 `youtube-captions.ts` 里的 `formatTimestamp` 逻辑，抽到公共函数）。
   - 无截帧的笔记省略 `![frame]` 行。

3. 成功后刷新笔记列表；失败把错误显示在输入区（沿用 `membox-client.ts` 的 `extractErrorMessage` 风格，401 时提示重新填 token）。

### Task 5 — 测试与验收

- `npm test`：为 `video-notes.ts` 的 Markdown 组装、时间戳格式化、`dataUrl → Blob` 转换写单测（mock fetch 与消息层）。
- `npm run typecheck && npm run build` 通过。
- Go 侧：`go test ./internal/web/backend/ -run NoteImage` 通过。
- 手动验收（写进 PR 描述）：
  1. `mm`（TUI 起 bridge）→ 打开任意 YouTube 视频 → 点扩展图标 → side panel 打开且时间 ticker 走动；
  2. 开「截帧」保存一条笔记 → membox 出现 `video_id` 对应的笔记文档，帧图 URL 可访问，Miru 中正常渲染；
  3. 再保存一条 → 同一文档追加，不产生第二篇；
  4. 点击列表中的时间戳 → 视频跳转到对应位置；
  5. 关闭 TUI 后保存 → sidepanel 显示连接错误而非静默失败。

## 4. 明确不做（本期）

- 不做连续截帧 / 录屏 / GIF。
- 不做非 YouTube 站点的视频适配（generic `<video>` 选择器已留余地，但不测）。
- 不改 transcript / summary 的生成流程；不引入新的后端文档类型（复用 `/api/ingest`）。
- 不做 Miru 端的笔记时间轴视图（后续单独提案）。
- 不动 `draft` repo；那里只是笔记 app 的头脑风暴，与本功能无关。

## 5. 未决问题（实现前确认）

1. **整篇更新 vs 每帧一篇**：本方案选「每个视频一篇、追加更新」。风险是 ingest 的 overwrite 需要拿到旧 body；若 bridge 暂时没有"按 source_url 读回全文"的接口，需要确认是 (a) 后端加一个只读 endpoint，还是 (b) sidepanel 用 `chrome.storage` 本地缓存全量条目。实现 Task 4 前先验证 `GET /api/bridge/clips` 的返回里是否含 body。
2. 笔记文档的**文件名**约定（如 `yt-<id>-notes.md`）目前 ingest 是否支持客户端指定？若不支持，接受后端自动命名，只保证 frontmatter `video_id` + `clip_mode: video-notes` 稳定可查。
