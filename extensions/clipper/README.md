# membox clipper (P0)

WXT browser extension that clips the current page to Markdown, ingests it into
membox (`POST /api/ingest`), opens the new document in Miru, and provides a
side panel for finding and switching between open tabs.

## Clipboard: Copy / Paste & save

**Copy page** extracts the active tab exactly like Save does, but composes a
self-contained Markdown document (H1 title + body + source line) onto the
clipboard — nothing is sent to membox.

**Paste & save** goes the other way: clipboard text is ingested as a new
note. Title resolution: the text's own first H1 wins; when it has none, the
bridge's naming model suggests one from the lead paragraph and outline
(`POST /api/suggest-title`, bridge-token auth); if that fails the server's
default title applies.

## Tab stash (OneTab-style history)

The popup's **Stash all tabs** button snapshots every open http(s) tab into a
persistent, accumulating store (`browser.storage.local`; tabs stay open).
Re-stashing a URL bumps its save count instead of duplicating the row, and
each URL is enriched with `browser.history` visit stats.

**Saved tabs** opens a full page listing the stash with:

- a sort toggle: **visit time** (real last-visit from browser history) vs
  **importance** (`saveCount × 5 + min(visits, 50) + pinned bonus`);
- fuzzy filter/search with match highlighting and the side panel's query
  syntax (`host:`, `title:`, `url:`, `is:pinned`);
- keyboard navigation (`↑`/`↓`, `Enter` to open, `Esc` to clear the filter);
- hover-revealed per-row remove, plus a guarded clear-all.

The store caps at 5000 URLs, evicting the least-recently-saved rows.

## Tab organizer side panel

The popup's **Organize tabs** button opens a browser side panel with:

- all tabs across browser windows, refreshed on tab events;
- a close button on the right of each row, revealed on hover or keyboard focus;
- fuzzy matching over title, URL, and host with match highlighting;
- filters such as `host:`, `title:`, `url:`, `is:pinned|active|discarded`, and
  `win:current`;
- keyboard navigation: `↑`/`↓` or `Ctrl+P`/`Ctrl+N`, `Enter` to focus,
  `Esc` to clear, and `Ctrl+R` to reload;
- a persisted Current Intent card that can be edited by double-clicking it.

Automatic tab deduplication is intentionally not enabled yet. The organizer
reads tab metadata only and does not read page content.

YouTube watch/shorts/embed pages take a different path:

1. Content script extracts captions (echo-style: player / InnerTube /
   textTracks / timedtext — browser session, no yt-dlp).
2. Extension builds caption **Markdown** (`[HH:MM:SS] text` lines).
3. Companion saves `yt-<video_id>-transcript.md` into membox.
4. mmd → `ebp automation summary-local --transcript-file <that path>`:
   LLM input = system prompt + that Markdown; model default
   **deepseek/deepseek-flash**. Summary published as `<course>-lecN.md`.
5. If a managed summary for that `video_id` already exists, Miru opens it
   immediately (idempotent; no caption fetch / no LLM).

## Two different “servers”

| 进程 | 做什么 | 要不要 `npm run dev` |
| --- | --- | --- |
| **membox**（TUI / `mm web start`） | `127.0.0.1:8787` Miru + `/api/ingest` | **不需要** npm |
| **WXT 扩展开发** | 把扩展源码打成 Chrome 可加载的包 | **只有改扩展代码时**才要 |

日常剪网页：只开 TUI（或 `mm web start`）+ 已安装的扩展即可。  
`npm run dev` 不是第二个 membox localhost，只是扩展热更新构建。

## Prerequisites

```bash
# 写入目录（main path，默认 paths[0]；也可在 TUI Ctrl+O 切换）
mm path add ~/repo/append-review/notes

# 方式 A（推荐）：直接开 TUI —— 启动时会自动拉起 bridge
mm
# 状态栏应出现 bridge http://127.0.0.1:8787
# token 在 ~/.membox/bridge.json

# 方式 B：单独挂 companion（前台，Ctrl+C 停；后台用 mm web start）
mm web start --fg --port 8787
```

## Develop the extension (only when changing clipper code)

```bash
cd extensions/clipper
npm install
npm run dev          # Chrome
npm run dev:firefox  # Firefox
```

The clipping pipeline is deliberately split by responsibility:

- `article-extractor.ts` — isolated live/raw/iframe candidates + Defuddle
- `markdown.ts` — deterministic HTML serialization (GFM, code, Mermaid)
- `clip.ts` — payload/frontmatter orchestration and selection notes
- `image-assets.ts` / `image-localizer.ts` — lazy-image discovery, background download, content-addressed membox upload, and Markdown URL rewriting

Binary image localization is deliberately limited to WeChat Official Account
articles (`mp.weixin.qq.com/s…`). Their text and remote image URLs are ingested
first so Miru opens immediately; image downloads then continue in the extension
background, patch only the matching Markdown destinations, and swap images in
the open reader as they finish. `mmbiz.qpic.cn` is fetched without a Referer
(avoiding its localhost anti-hotlink placeholder) and stored under
`$MEMBOX_HOME/assets/note-images`. An individual image failure keeps its remote
URL and does not abort the page clip. All other sites keep the original clipper
behavior: Markdown references their remote image URLs directly.

Regression checks:

```bash
npm test
npm run typecheck
npm run build
```

Load the path printed by WXT (usually `.output/chrome-mv3-dev`) as an unpacked
extension. In the popup:

1. Server URL: `http://127.0.0.1:8787`（与 TUI bridge 一致）
2. Bridge token: `cat ~/.membox/bridge.json` 里的 `token`
3. **Save settings** → status should show **connected**
4. Open any http(s) page → **Save / summarize** (full page Markdown clip)
5. Open a YouTube video → same button summarizes via echo-bp (or opens the existing summary)
6. Or **select text** on a page → floating card (excerpt + note + Save)

### Selection card

- Appears near the selection after mouseup
- Only three parts: excerpt, note field, Save
- Theme follows `prefers-color-scheme` / Miru tokens
- Saves a new membox note with `clip_mode: selection` and `source_url`
- If a prior full-page clip of the same URL exists, membox graph-links the excerpt to it
- Esc / click outside / scroll dismisses the card
- ⌘/Ctrl+Enter saves

## Build / reload the installed extension

```bash
cd extensions/clipper
npm install
npm run build
```

Then in Chrome → Extensions → **Load unpacked** → select:

```text
extensions/clipper/dist/chrome-mv3
```

**Do not** load `dist/chrome-mv3-dev` unless `npm run dev` is running — that
build points at `localhost:3000` and the popup stays stuck on `checking…`
with no styles.

After every rebuild click **Reload** on the extension card. Open the popup and
choose **Organize tabs** to open the side panel; the normal toolbar click still
opens the clipping popup.

Pairing in the popup:

```bash
mm web status   # prints URL + Token
```

1. Server URL = printed URL (usually `http://127.0.0.1:8787`)
2. Bridge token = printed Token (not the placeholder text)
3. **Save settings** → status becomes `connected · auth`
4. **Save page**

```bash
npm run zip
```

## Pairing file

`mm web start` / `mm web start --fg` also write `~/.membox/bridge.json` (`base_url`, `token`, `port`).
