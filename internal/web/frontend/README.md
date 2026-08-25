# Frontend boundaries

The web UI is split into two independently usable trees:

- `miru/` is the host-neutral, static reader. It must run from a plain static
  file server and must not import membox modules or call `/api/*`.
- `adapters/companion/` is an optional host adapter. The Go companion injects
  only `adapters/companion/integration.js`; that adapter owns backend
  discovery, persistence, document switching, model tools, and all
  product-specific chrome.

## Capability lifecycle

Miru's local Note and Highlight interactions are available without a backend.
They are intentionally session-only: Miru performs no local persistence and a
refresh may discard them without a navigation warning. The Markdown download
remains the verbatim source and deliberately excludes annotations. A host may
persist annotations separately or make the reader fully read-only through
`setAnnotInteractionsEnabled(false)`.

Backend capabilities are gated independently. The Companion adapter calls
`setAnnotHostCapabilitiesEnabled(true)` only after connecting and disables it
again on disconnect. Optional features register handlers/actions with Miru
rather than being imported by Miru. For example, the adapter registers Ask
through `registerAnnotAskHandler`; a disconnected or static Miru deployment
shows no Ask control or host actions.

Backend-only keyboard shortcuts must check the connected capability before
calling `preventDefault()`. This lets the browser or another host retain the
shortcut when membox is absent.

## Miru submodule workflow

`frontend/miru/` is a Git submodule pinned to a tested commit of the standalone
Miru repository. `frontend/adapters/companion/` remains owned by Membox and is
not part of that repository. The boundary test rejects adapter imports and
`/api/*` calls from the core.

Initialize it after cloning Membox:

```bash
git submodule update --init --recursive
```

To change Miru while working inside Membox, first ensure the submodule is on
its development branch (a fresh `submodule update` may leave it detached):

```bash
cd internal/web/frontend/miru
git switch main
git pull --ff-only
# edit and test
git add .
git commit -m "..."
git push origin main
```

Then return to the Membox root and commit the new pinned Miru revision:

```bash
cd ../../../..
git add internal/web/frontend/miru
git commit -m "chore(web): update Miru"
```

The separate `~/repo/miru` checkout receives those changes with a normal
`git pull --ff-only`. Do not copy files between the two checkouts.

## Math preprocessing

The portable core uses a single-pass, escape-aware scanner for explicit `$` and
`$$` delimiters. It supersedes the older placeholder tokenizer in standalone
Miru while retaining its escaped-dollar fix. Core tests cover code fences,
link destinations, unclosed delimiters, and placeholder-leak regressions; do
not overwrite this module during a sync without running those tests.
