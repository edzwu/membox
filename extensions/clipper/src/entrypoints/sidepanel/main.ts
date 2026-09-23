import './style.css';
import { filterTabs, type RankedTab } from '../../lib/tab-organizer/filter';
import {
  getCurrentIntent,
  inferCurrentIntent,
  saveCurrentIntent,
  type CurrentIntent,
} from '../../lib/tab-organizer/intent';
import {
  focusTab,
  getCurrentWindowId,
  listOpenTabs,
  type TabInfo,
} from '../../lib/tab-organizer/tabs';

const appEl = document.querySelector<HTMLDivElement>('#app');
if (!appEl) throw new Error('#app not found');
const app = appEl;

type State = {
  tabs: TabInfo[];
  ranked: RankedTab[];
  query: string;
  selectedIndex: number;
  currentWindowId?: number;
  errorMessage: string;
  intent: CurrentIntent | null;
  intentEditing: boolean;
};

const state: State = {
  tabs: [],
  ranked: [],
  query: '',
  selectedIndex: 0,
  currentWindowId: undefined,
  errorMessage: '',
  intent: null,
  intentEditing: false,
};

let listEl: HTMLDivElement | null = null;
let inputEl: HTMLInputElement | null = null;
let countEl: HTMLSpanElement | null = null;
let footerUrlEl: HTMLDivElement | null = null;
let intentEl: HTMLDivElement | null = null;
let refreshTimer: ReturnType<typeof setTimeout> | null = null;

function escapeHtml(value: string): string {
  return value
    .replaceAll('&', '&amp;')
    .replaceAll('<', '&lt;')
    .replaceAll('>', '&gt;')
    .replaceAll('"', '&quot;')
    .replaceAll("'", '&#39;');
}

function highlight(text: string, indices: number[]): string {
  if (!indices.length) return escapeHtml(text);
  const set = new Set(indices);
  let out = '';
  for (let i = 0; i < text.length; i += 1) {
    const ch = escapeHtml(text[i]!);
    out += set.has(i) ? `<span class="mark">${ch}</span>` : ch;
  }
  return out;
}

function selected(): RankedTab | undefined {
  return state.ranked[state.selectedIndex];
}

function clampSelection(): void {
  if (state.ranked.length === 0) {
    state.selectedIndex = 0;
    return;
  }
  state.selectedIndex = Math.max(0, Math.min(state.selectedIndex, state.ranked.length - 1));
}

function recompute(preserveTabId?: number): void {
  state.ranked = filterTabs(state.tabs, state.query, {
    currentWindowId: state.currentWindowId,
  });

  if (preserveTabId != null) {
    const idx = state.ranked.findIndex((r) => r.tab.id === preserveTabId);
    state.selectedIndex = idx >= 0 ? idx : 0;
  } else {
    clampSelection();
  }
}

function renderShell(): void {
  app.innerHTML = `
    <div id="intent" class="intent"></div>
    <div id="intent-resize" class="intent-resize"></div>
    <div id="list" class="list" tabindex="-1"></div>
    <footer class="footer">
      <div id="footer-url" class="footer-url is-empty">no selection</div>
      <div class="query-bar">
        <span class="query-prompt">&gt;</span>
        <input
          id="query"
          class="query-input"
          type="text"
          spellcheck="false"
          autocomplete="off"
          placeholder="filter tabs · host:github is:pinned win:current"
        />
        <span id="count" class="query-count">0/0</span>
      </div>
    </footer>
  `;

  listEl = document.querySelector('#list');
  inputEl = document.querySelector('#query');
  countEl = document.querySelector('#count');
  footerUrlEl = document.querySelector('#footer-url');
  intentEl = document.querySelector('#intent');

  setupIntentResize();

  inputEl?.addEventListener('input', () => {
    state.query = inputEl?.value ?? '';
    recompute();
    paint();
  });

  inputEl?.addEventListener('keydown', onQueryKeyDown);
  listEl?.addEventListener('keydown', onQueryKeyDown);

  // Keep typing focused on the finder (but not while editing the intent).
  document.addEventListener('keydown', (event) => {
    if (event.metaKey || event.ctrlKey || event.altKey) return;
    if (state.intentEditing) return;
    if (event.key.length === 1 && document.activeElement !== inputEl) {
      inputEl?.focus();
    }
  });
}

function paintRow(item: RankedTab, index: number): string {
  const { tab, titleIndices, hostIndices } = item;
  const favicon = tab.favIconUrl
    ? `<img class="favicon" src="${escapeHtml(tab.favIconUrl)}" alt="" />`
    : `<span class="favicon placeholder" aria-hidden="true"></span>`;

  const flags: string[] = [];
  if (tab.active) flags.push('<span class="active-flag">*</span>');
  if (tab.pinned) flags.push('<span class="on">pin</span>');
  if (tab.discarded) flags.push('<span>zzz</span>');

  return `
    <div
      class="row ${index === state.selectedIndex ? 'is-selected' : ''}"
      data-index="${index}"
      role="option"
      aria-selected="${index === state.selectedIndex}"
    >
      ${favicon}
      <div class="main">
        <div class="title">${highlight(tab.title, titleIndices)}</div>
        <div class="sub">
          <span class="host">${highlight(tab.hostname, hostIndices)}</span>
          <span class="sep">·</span>
          <span class="path">${escapeHtml(tab.path || '/')}</span>
        </div>
      </div>
      <div class="row-trailing">
        <div class="flags">${flags.join('')}</div>
        <button
          class="close-tab"
          type="button"
          data-tab-id="${tab.id}"
          title="Close tab"
          aria-label="Close tab"
        >
          <svg viewBox="0 0 16 16" aria-hidden="true">
            <path d="M4 4l8 8M12 4l-8 8"></path>
          </svg>
        </button>
      </div>
    </div>
  `;
}

/* ── current intent card ─────────────────────────────────────────── */

const INTENT_HEIGHT_KEY = 'membox.tabs.intentHeight';
const INTENT_MIN_HEIGHT = 40;

function setupIntentResize(): void {
  const handle = document.querySelector<HTMLDivElement>('#intent-resize');
  if (!intentEl || !handle) return;

  // Restore the user-adjusted height from a previous session.
  const saved = Number(localStorage.getItem(INTENT_HEIGHT_KEY));
  if (Number.isFinite(saved) && saved >= INTENT_MIN_HEIGHT) {
    intentEl.style.height = `${saved}px`;
  }

  handle.addEventListener('mousedown', (event) => {
    event.preventDefault();
    const startY = event.clientY;
    const startHeight = intentEl!.offsetHeight;
    const maxHeight = Math.floor(window.innerHeight * 0.6);

    const onMove = (ev: MouseEvent): void => {
      const next = Math.max(
        INTENT_MIN_HEIGHT,
        Math.min(maxHeight, startHeight + ev.clientY - startY),
      );
      intentEl!.style.height = `${next}px`;
    };
    const onUp = (): void => {
      document.removeEventListener('mousemove', onMove);
      document.removeEventListener('mouseup', onUp);
      localStorage.setItem(INTENT_HEIGHT_KEY, String(intentEl!.offsetHeight));
    };
    document.addEventListener('mousemove', onMove);
    document.addEventListener('mouseup', onUp);
  });
}

function paintIntent(): void {
  if (!intentEl || state.intentEditing) return;

  const intent = state.intent;
  if (!intent) {
    intentEl.innerHTML = `<div class="intent-empty">inferring intent…</div>`;
    return;
  }

  intentEl.innerHTML = `
    <div class="intent-text" title="double-click to edit">${escapeHtml(intent.text)}</div>
  `;
  intentEl
    .querySelector('.intent-text')
    ?.addEventListener('dblclick', startIntentEdit);
}

function startIntentEdit(): void {
  if (!intentEl) return;
  state.intentEditing = true;
  // Let the card grow freely while editing; the fixed height returns after.
  intentEl.classList.add('editing');
  intentEl.innerHTML = `
    <div class="intent-prompt">what are you trying to do?</div>
    <textarea
      id="intent-input"
      class="intent-input"
      rows="2"
      spellcheck="false"
      placeholder="e.g. Preparing for HFT backend interviews"
    >${escapeHtml(state.intent?.text ?? '')}</textarea>
    <div class="intent-actions">
      <button class="intent-cancel" type="button">cancel <kbd>esc</kbd></button>
      <button class="intent-save" type="button">save <kbd>⌘↵</kbd></button>
    </div>
  `;

  const textarea = intentEl.querySelector<HTMLTextAreaElement>('#intent-input');
  textarea?.focus();
  textarea?.setSelectionRange(textarea.value.length, textarea.value.length);

  intentEl.querySelector('.intent-cancel')?.addEventListener('click', cancelIntentEdit);
  intentEl.querySelector('.intent-save')?.addEventListener('click', () => void saveIntentEdit());
  textarea?.addEventListener('keydown', (event) => {
    if (event.key === 'Escape') {
      event.preventDefault();
      cancelIntentEdit();
      return;
    }
    if (event.key === 'Enter' && (event.metaKey || event.ctrlKey)) {
      event.preventDefault();
      void saveIntentEdit();
    }
    event.stopPropagation();
  });
}

function cancelIntentEdit(): void {
  state.intentEditing = false;
  intentEl?.classList.remove('editing');
  paintIntent();
  inputEl?.focus();
}

async function saveIntentEdit(): Promise<void> {
  const textarea = intentEl?.querySelector<HTMLTextAreaElement>('#intent-input');
  const text = textarea?.value.trim() ?? '';
  state.intentEditing = false;
  intentEl?.classList.remove('editing');

  // Empty save = reset: fall back to a fresh inferred intent.
  state.intent = text
    ? { text, source: 'confirmed', updatedAt: Date.now() }
    : await inferCurrentIntent(state.tabs);

  await saveCurrentIntent(state.intent);
  paintIntent();
  inputEl?.focus();
}

function paint(): void {
  if (!listEl || !countEl || !footerUrlEl) return;

  paintIntent();

  const total = state.tabs.length;
  const matched = state.ranked.length;
  countEl.textContent = `${matched}/${total}`;
  countEl.classList.toggle('has-filter', state.query.trim().length > 0);

  if (state.errorMessage) {
    listEl.innerHTML = `<div class="empty footer-error">${escapeHtml(state.errorMessage)}</div>`;
  } else if (matched === 0) {
    const msg = total === 0
      ? 'no open tabs'
      : `no matches for '${escapeHtml(state.query.trim())}'`;
    listEl.innerHTML = `<div class="empty">${msg}</div>`;
  } else {
    listEl.innerHTML = state.ranked.map((item, index) => paintRow(item, index)).join('');
    listEl.querySelectorAll<HTMLElement>('.row').forEach((row) => {
      row.addEventListener('mouseenter', () => {
        const index = Number(row.dataset.index);
        if (Number.isFinite(index) && index !== state.selectedIndex) {
          state.selectedIndex = index;
          paintSelectionOnly();
        }
      });
      row.addEventListener('click', () => {
        const index = Number(row.dataset.index);
        if (!Number.isFinite(index)) return;
        state.selectedIndex = index;
        paintSelectionOnly();
        void activateSelected();
      });
      const closeButton = row.querySelector<HTMLButtonElement>('.close-tab');
      closeButton?.addEventListener('click', (event) => {
        event.stopPropagation();
        const tabId = Number(closeButton.dataset.tabId);
        if (Number.isFinite(tabId)) void closeTab(tabId);
      });
    });
  }

  const current = selected()?.tab;
  if (current) {
    footerUrlEl.textContent = current.url || '(no url)';
    footerUrlEl.classList.remove('is-empty');
  } else {
    footerUrlEl.textContent = 'no selection';
    footerUrlEl.classList.add('is-empty');
  }

  scrollSelectedIntoView();
}

function paintSelectionOnly(): void {
  if (!listEl || !footerUrlEl) return;
  listEl.querySelectorAll<HTMLElement>('.row').forEach((row) => {
    const index = Number(row.dataset.index);
    const on = index === state.selectedIndex;
    row.classList.toggle('is-selected', on);
    row.setAttribute('aria-selected', String(on));
  });

  const current = selected()?.tab;
  if (current) {
    footerUrlEl.textContent = current.url || '(no url)';
    footerUrlEl.classList.remove('is-empty');
  } else {
    footerUrlEl.textContent = 'no selection';
    footerUrlEl.classList.add('is-empty');
  }
  scrollSelectedIntoView();
}

function scrollSelectedIntoView(): void {
  const row = listEl?.querySelector<HTMLElement>(`.row[data-index="${state.selectedIndex}"]`);
  row?.scrollIntoView({ block: 'nearest' });
}

function moveSelection(delta: number): void {
  if (state.ranked.length === 0) return;
  const next = state.selectedIndex + delta;
  state.selectedIndex = (next + state.ranked.length) % state.ranked.length;
  paintSelectionOnly();
}

async function activateSelected(): Promise<void> {
  const item = selected();
  if (!item) return;
  try {
    await focusTab(item.tab.id);
  } catch (error) {
    state.errorMessage = error instanceof Error ? error.message : String(error);
    paint();
  }
}

async function closeTab(tabId: number): Promise<void> {
  const selectedTabId = selected()?.tab.id;
  try {
    await browser.tabs.remove(tabId);
    state.errorMessage = '';
    state.tabs = state.tabs.filter((tab) => tab.id !== tabId);
    recompute(selectedTabId === tabId ? undefined : selectedTabId);
    paint();
  } catch (error) {
    state.errorMessage = error instanceof Error ? error.message : String(error);
    paint();
  }
}

function onQueryKeyDown(event: KeyboardEvent): void {
  if (event.key === 'ArrowDown' || (event.key === 'n' && event.ctrlKey)) {
    event.preventDefault();
    moveSelection(1);
    return;
  }
  if (event.key === 'ArrowUp' || (event.key === 'p' && event.ctrlKey)) {
    event.preventDefault();
    moveSelection(-1);
    return;
  }
  if (event.key === 'Enter') {
    event.preventDefault();
    void activateSelected();
    return;
  }
  if (event.key === 'Escape') {
    event.preventDefault();
    if (state.query) {
      state.query = '';
      if (inputEl) inputEl.value = '';
      recompute();
      paint();
    }
    return;
  }
  if (event.key === 'r' && event.ctrlKey) {
    event.preventDefault();
    void reload();
  }
}

async function reload(): Promise<void> {
  const preserveTabId = selected()?.tab.id;
  state.errorMessage = '';
  paint();

  try {
    const [tabs, currentWindowId] = await Promise.all([
      listOpenTabs(),
      getCurrentWindowId(),
    ]);
    state.tabs = tabs;
    state.currentWindowId = currentWindowId;
    recompute(preserveTabId);

    // Keep the intent fresh as the observed context changes — but never
    // overwrite a user-confirmed intent, and never mid-edit.
    if (!state.intentEditing && state.intent?.source !== 'confirmed') {
      const inferred = await inferCurrentIntent(state.tabs);
      if (inferred.text !== state.intent?.text) {
        state.intent = inferred;
        await saveCurrentIntent(inferred);
      }
    }
  } catch (error) {
    state.errorMessage = error instanceof Error ? error.message : String(error);
  } finally {
    paint();
    inputEl?.focus();
  }
}

function scheduleReload(): void {
  if (refreshTimer) clearTimeout(refreshTimer);
  refreshTimer = setTimeout(() => {
    void reload();
  }, 80);
}

function subscribeTabEvents(): void {
  browser.tabs.onCreated.addListener(scheduleReload);
  browser.tabs.onUpdated.addListener(scheduleReload);
  browser.tabs.onRemoved.addListener(scheduleReload);
  browser.tabs.onActivated.addListener(scheduleReload);
  browser.tabs.onMoved.addListener(scheduleReload);
  browser.tabs.onAttached.addListener(scheduleReload);
  browser.tabs.onDetached.addListener(scheduleReload);
}

renderShell();
subscribeTabEvents();
void (async () => {
  try {
    state.intent = await getCurrentIntent();
  } catch (error) {
    console.warn('failed to load current intent', error);
  }
  await reload();
})();
document.querySelector<HTMLInputElement>('#query')?.focus();
