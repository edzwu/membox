# membox clipper (P0)

WXT browser extension that clips the current page to Markdown, ingests it into
membox (`POST /api/ingest`), and opens the new document in Miru.

## Two different “servers”

| 进程 | 做什么 | 要不要 `npm run dev` |
| --- | --- | --- |
| **membox**（TUI / `mm serve`） | `127.0.0.1:8787` Miru + `/api/ingest` | **不需要** npm |
| **WXT 扩展开发** | 把扩展源码打成 Chrome 可加载的包 | **只有改扩展代码时**才要 |

日常剪网页：只开 TUI（或 `mm serve`）+ 已安装的扩展即可。  
`npm run dev` 不是第二个 membox localhost，只是扩展热更新构建。

## Prerequisites

```bash
# 写入目录（main path，默认 paths[0]；也可在 TUI Ctrl+O 切换）
mm path add ~/repo/append-review/notes

# 方式 A（推荐）：直接开 TUI —— 启动时会自动拉起 bridge
mm
# 状态栏应出现 bridge http://127.0.0.1:8787
# token 在 ~/.membox/bridge.json

# 方式 B：单独挂 server（不关 TUI 时也可）
mm serve --port 8787
```

## Develop the extension (only when changing clipper code)

```bash
cd extensions/clipper
npm install
npm run dev          # Chrome
npm run dev:firefox  # Firefox
```

Load the path printed by WXT (usually `.output/chrome-mv3-dev`) as an unpacked
extension. In the popup:

1. Server URL: `http://127.0.0.1:8787`（与 TUI bridge 一致）
2. Bridge token: `cat ~/.membox/bridge.json` 里的 `token`
3. **Save settings** → status should show **connected**
4. Open any http(s) page → **Save to membox** (full page)
5. Or **select text** on a page → floating card (excerpt + note + Save)

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

After every rebuild click **Reload** on the extension card.

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

`mm serve` also writes `~/.membox/bridge.json` (`base_url`, `token`, `port`).
