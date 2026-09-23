import './style.css';
import {
  filterStashedTabs,
  visitedAt,
  type RankedStashedTab,
  type StashSortMode,
} from '../../lib/tab-organizer/stash-filter';
import {
  clearStash,
  importanceOf,
  loadStash,
  onStashChanged,
  removeStashedTab,
  type StashedTab,
} from '../../lib/tab-organizer/stash';

const appEl = document.querySelector<HTMLDivElement>('#app');
if (!appEl) throw new Error('#app not found');
const app = appEl;

type State = {
  tabs: StashedTab[];
  ranked: RankedStashedTab[];
  query: string;
  sort: StashSortMode;
  selectedIndex: number;
};

const SORT_KEY = 'membox.tabs.stashSort';

function loadSortMode(): StashSortMode {
  return localStorage.getItem(SORT_KEY) === 'importance' ? 'importance' : 'visited';
}

const state: State = {
  tabs: [],
  ranked: [],
  query: '',
  sort: loadSortMode(),
  selectedIndex: 0,
};

let listEl: HTMLDivElement | null = null;
let inputEl: HTMLInputElement | null = null;
let countEl: HTMLSpanElement | null = null;

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

function formatRelativeTime(timestamp: number): string {
  const delta = Date.now() - timestamp;
  if (delta < 0) return 'just now';
  const minutes = Math.floor(delta / 60_000);
  if (minutes < 1) return 'just now';
  if (minutes < 60) return `${minutes}m ago`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours}h ago`;
  const days = Math.floor(hours / 24);
  if (days < 30) return `${days}d ago`;
  const months = Math.floor(days / 30);
  if (months < 12) return `${months}mo ago`;
  return `${Math.floor(months / 12)}y ago`;
}

function recompute(preserveUrl?: string): void {
  state.ranked = filterStashedTabs(state.tabs, state.query, state.sort);
  if (preserveUrl != null) {
    const idx = state.ranked.findIndex((r) => r.tab.url === preserveUrl);
    state.selectedIndex = idx >= 0 ? idx : 0;
  } else {
    state.selectedIndex = Math.max(0, Math.min(state.selectedIndex, state.ranked.length - 1));
  }
}

function renderShell(): void {
  app.innerHTML = `
    <div class="page">
      <header class="header">
        <div class="brand">saved tabs</div>
        <div class="sort-toggle" role="tablist" aria-label="Sort order">
          <button
            id="sortVisited"
            type="button"
            class="sort-btn"
            title="Sort by last visit time"
          >visit time</button>
          <button
            id="sortImportance"
            type="button"
            class="sort-btn"
            title="Sort by importance (stash count × visits + pinned)"
          >importance</button>
        </div>
        <div class="header-spacer"></div>
        <button id="clearAll" type="button" class="clear-btn" title="Delete every stashed tab">clear all</button>
      </header>
      <div class="query-bar">
        <span class="query-prompt">&gt;</span>
        <input
          id="query"
          class="query-input"
          type="text"
          spellcheck="false"
          autocomplete="off"
          placeholder="filter saved tabs · host:github is:pinned"
        />
        <span id="count" class="query-count">0/0</span>
      </div>
      <div id="list" class="list" tabindex="-1"></div>
    </div>
  `;

  listEl = document.querySelector('#list');
  inputEl = document.querySelector('#query');
  countEl = document.querySelector('#count');

  document.querySelector('#sortVisited')?.addEventListener('click', () => setSort('visited'));
  document.querySelector('#sortImportance')?.addEventListener('click', () => setSort('importance'));
  document.querySelector('#clearAll')?.addEventListener('click', () => {
    if (state.tabs.length === 0) return;
    if (window.confirm(`Delete all ${state.tabs.length} stashed tabs?`)) {
      void clearStash();
    }
  });

  inputEl?.addEventListener('input', () => {
    state.query = inputEl?.value ?? '';
    recompute();
    paint();
  });
  inputEl?.addEventListener('keydown', onKeyDown);
  listEl?.addEventListener('keydown', onKeyDown);

  paintSortToggle();
}

function setSort(sort: StashSortMode): void {
  if (state.sort === sort) return;
  state.sort = sort;
  localStorage.setItem(SORT_KEY, sort);
  recompute(selected()?.tab.url);
  paintSortToggle();
  paint();
}

function paintSortToggle(): void {
  document.querySelector('#sortVisited')?.classList.toggle('is-on', state.sort === 'visited');
  document.querySelector('#sortImportance')?.classList.toggle('is-on', state.sort === 'importance');
}

function selected(): RankedStashedTab | undefined {
  return state.ranked[state.selectedIndex];
}

function paintRow(item: RankedStashedTab, index: number): string {
  const { tab, titleIndices, hostIndices } = item;
  const favicon = tab.favIconUrl
    ? `<img class="favicon" src="${escapeHtml(tab.favIconUrl)}" alt="" />`
    : `<span class="favicon placeholder" aria-hidden="true">${escapeHtml(
        (tab.hostname[0] ?? '?').toUpperCase(),
      )}</span>`;

  const badges: string[] = [];
  if (tab.saveCount > 1) badges.push(`<span title="stashed ${tab.saveCount} times">×${tab.saveCount}</span>`);
  if (tab.visitCount > 0) badges.push(`<span title="${tab.visitCount} browser visits">${tab.visitCount} visits</span>`);
  if (tab.pinned) badges.push('<span>pin</span>');
  if (state.sort === 'importance') {
    badges.push(`<span class="score" title="importance score">${importanceOf(tab)}</span>`);
  }

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
          <span class="time">${formatRelativeTime(visitedAt(tab))}</span>
          <span class="sep">·</span>
          <span class="badges">${badges.join(' ')}</span>
        </div>
      </div>
      <button
        class="remove-tab"
        type="button"
        data-url="${escapeHtml(tab.url)}"
        title="Remove from stash"
        aria-label="Remove from stash"
      >
        <svg viewBox="0 0 16 16" aria-hidden="true">
          <path d="M4 4l8 8M12 4l-8 8"></path>
        </svg>
      </button>
    </div>
  `;
}

function paint(): void {
  if (!listEl || !countEl) return;

  const total = state.tabs.length;
  countEl.textContent = `${state.ranked.length}/${total}`;
  countEl.classList.toggle('has-filter', state.query.trim().length > 0);

  if (state.ranked.length === 0) {
    const msg = total === 0
      ? 'nothing stashed yet — press "Stash all tabs" in the popup'
      : `no matches for '${escapeHtml(state.query.trim())}'`;
    listEl.innerHTML = `<div class="empty">${msg}</div>`;
    return;
  }

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
      openSelected();
    });
    const removeButton = row.querySelector<HTMLButtonElement>('.remove-tab');
    removeButton?.addEventListener('click', (event) => {
      event.stopPropagation();
      const url = removeButton.dataset.url;
      if (url) void removeStashedTab(url); // storage change re-renders
    });
  });

  scrollSelectedIntoView();
}

function paintSelectionOnly(): void {
  listEl?.querySelectorAll<HTMLElement>('.row').forEach((row) => {
    const index = Number(row.dataset.index);
    const on = index === state.selectedIndex;
    row.classList.toggle('is-selected', on);
    row.setAttribute('aria-selected', String(on));
  });
  scrollSelectedIntoView();
}

function scrollSelectedIntoView(): void {
  listEl
    ?.querySelector<HTMLElement>(`.row[data-index="${state.selectedIndex}"]`)
    ?.scrollIntoView({ block: 'nearest' });
}

function moveSelection(delta: number): void {
  if (state.ranked.length === 0) return;
  const next = state.selectedIndex + delta;
  state.selectedIndex = (next + state.ranked.length) % state.ranked.length;
  paintSelectionOnly();
}

function openSelected(): void {
  const item = selected();
  if (!item) return;
  void browser.tabs.create({ url: item.tab.url });
}

function onKeyDown(event: KeyboardEvent): void {
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
    openSelected();
    return;
  }
  if (event.key === 'Escape' && state.query) {
    event.preventDefault();
    state.query = '';
    if (inputEl) inputEl.value = '';
    recompute();
    paint();
  }
}

renderShell();
onStashChanged((tabs) => {
  state.tabs = tabs;
  recompute(selected()?.tab.url);
  paint();
});
void (async () => {
  state.tabs = await loadStash();
  recompute();
  paint();
})();
document.querySelector<HTMLInputElement>('#query')?.focus();
