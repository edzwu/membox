import { detectMiruTheme, MIRU_TOKENS, type MiruTheme } from './theme';

export type FloatingNote = {
  id: string;
  excerpt: string;
  note: string;
  /** Current position in document coordinates (scrolls with the page). */
  top: number;
  left: number;
  /** Where the note belongs (selection origin) — restored on double-click. */
  originTop?: number;
  originLeft?: number;
  createdAt: string;
  /** Hidden via × — still stored; restore from popup. */
  hidden?: boolean;
  /** User edited the note locally; do not clobber with membox copy. */
  editedLocally?: boolean;
  /** 📌 follow mode: card stays at a viewport-relative spot while reading. */
  followViewport?: boolean;
  /** Viewport coordinates used while followViewport is on. */
  fixedTop?: number;
  fixedLeft?: number;
};

export type FloatPageState = {
  collapsed: boolean;
  notes: FloatingNote[];
};

export type FloatStatus = {
  collapsed: boolean;
  /** Visible pins on the page (not hidden). */
  count: number;
  /** Soft-hidden pins that can be restored. */
  hiddenCount: number;
  total: number;
  /** Notes saved in membox for this page URL (from bridge API). */
  savedCount?: number;
};

const STORAGE_PREFIX = 'membox.floats:';
const HOST_ID = 'membox-float-notes-host';
const NOTES_ENABLED_KEY = 'membox.notes-enabled';

/** Note-taking is opt-in: disabled until the user explicitly enables it. */
export async function getNotesEnabled(): Promise<boolean> {
  const stored = await browser.storage.local.get(NOTES_ENABLED_KEY);
  return stored[NOTES_ENABLED_KEY] === true;
}

export async function setNotesEnabled(enabled: boolean): Promise<void> {
  await browser.storage.local.set({ [NOTES_ENABLED_KEY]: enabled });
}
const CARD_WIDTH = 240;
const CARD_EST_HEIGHT = 120;
const DRAG_THRESHOLD = 4;
const FLASH_MS = 1500;

function pageKey(url = location.href): string {
  try {
    const u = new URL(url);
    u.hash = '';
    return STORAGE_PREFIX + u.toString();
  } catch {
    return STORAGE_PREFIX + url;
  }
}

export async function loadFloatState(url?: string): Promise<FloatPageState> {
  const key = pageKey(url);
  const stored = await browser.storage.local.get(key);
  const value = stored[key] as FloatPageState | undefined;
  if (!value || !Array.isArray(value.notes)) {
    return { collapsed: true, notes: [] };
  }
  return {
    collapsed: value.collapsed !== false,
    notes: value.notes.filter((n) => n && n.id && n.excerpt),
  };
}

export async function saveFloatState(state: FloatPageState, url?: string): Promise<void> {
  const key = pageKey(url);
  if (!state.notes.length) {
    await browser.storage.local.remove(key);
    return;
  }
  await browser.storage.local.set({ [key]: state });
}

/**
 * Persistent floating notes on the page.
 * collapsed = poker-deck stack (viewport-fixed);
 * expanded = draggable cards in document coordinates (they scroll with the
 * page, keep their x while scrolling, and can be rearranged by the user).
 */
export class FloatNotesLayer {
  private host: HTMLElement | null = null;
  private shadow: ShadowRoot | null = null;
  private state: FloatPageState = { collapsed: true, notes: [] };
  private theme: MiruTheme = 'light';
  private zTop = 20;
  private editingId: string | null = null;
  private dragCleanup: (() => void) | null = null;
  /** URL used as the card key — the original article URL even inside Miru. */
  private pageUrl: string;

  constructor(pageUrl?: string) {
    this.pageUrl = pageUrl || location.href;
  }

  /** Remove the overlay entirely (used when note-taking is turned off). */
  destroy() {
    this.teardown();
  }

  async init() {
    this.state = await loadFloatState(this.pageUrl);
    this.theme = detectMiruTheme();
    this.render();
    window.addEventListener('resize', () => this.render());
    // Drop stale local pins that are not real membox selection notes for this URL.
    void this.syncWithMemboxInBackground();
  }

  private async syncWithMemboxInBackground() {
    try {
      if (!browser.runtime?.id) return;
      const res = (await browser.runtime.sendMessage({
        type: 'membox.clips-for-url',
        url: this.pageUrl,
      })) as {
        ok: boolean;
        clips?: Array<{ id: string; excerpt?: string; note?: string; title?: string }>;
      };
      if (!res?.ok || !Array.isArray(res.clips)) return;
      await this.reconcileWithMembox(res.clips);
    } catch {
      // offline / unauth — keep local pins as-is
    }
  }

  /**
   * Local pins are only a cache of membox selection notes for this page.
   * Drop orphans (old FTS false-positives, deleted docs, wrong-page leftovers).
   * Locally edited notes keep the user's text.
   */
  async reconcileWithMembox(
    clips: Array<{ id: string; excerpt?: string; note?: string; title?: string }>,
  ): Promise<FloatStatus> {
    const valid = new Set(clips.map((c) => c.id));
    this.state.notes = this.state.notes.filter((n) => valid.has(n.id));
    const byId = new Map(clips.map((c) => [c.id, c]));
    this.state.notes = this.state.notes.map((n) => {
      const c = byId.get(n.id);
      if (!c) return n;
      return {
        ...n,
        excerpt: (c.excerpt && c.excerpt.trim()) || n.excerpt,
        note: n.editedLocally ? n.note : (c.note ?? '').trim(),
      };
    });
    await saveFloatState(this.state, this.pageUrl);
    this.render();
    return { ...this.getStatus(), savedCount: clips.length };
  }

  getState(): FloatPageState {
    return {
      collapsed: this.state.collapsed,
      notes: this.state.notes.slice(),
    };
  }

  getStatus(savedCount?: number): FloatStatus {
    const visible = this.visibleNotes();
    const hiddenCount = this.state.notes.length - visible.length;
    return {
      collapsed: this.state.collapsed,
      count: visible.length,
      hiddenCount,
      total: this.state.notes.length,
      savedCount,
    };
  }

  private visibleNotes(): FloatingNote[] {
    return this.state.notes.filter((n) => !n.hidden);
  }

  async addNote(note: FloatingNote): Promise<FloatingNote> {
    // Place the new card where it does not cover the article body.
    const pos = findClearPosition(
      { left: note.left, top: note.top, width: 0, height: 0 } as DOMRect,
      this.placedRects(note.id),
    );
    const next: FloatingNote = {
      ...note,
      top: pos.top,
      left: pos.left,
      originTop: pos.top,
      originLeft: pos.left,
      hidden: false,
      editedLocally: false,
    };
    this.state.notes = [next, ...this.state.notes.filter((n) => n.id !== note.id)];
    await saveFloatState(this.state, this.pageUrl);
    this.render();
    return next;
  }

  async setCollapsed(collapsed: boolean) {
    this.state.collapsed = collapsed;
    await saveFloatState(this.state, this.pageUrl);
    this.render();
  }

  async toggleCollapsed(): Promise<boolean> {
    await this.setCollapsed(!this.state.collapsed);
    return this.state.collapsed;
  }

  /** Soft-hide one pin (×). Membox note and local record stay. */
  async hideNote(id: string) {
    let changed = false;
    this.state.notes = this.state.notes.map((n) => {
      if (n.id !== id || n.hidden) return n;
      changed = true;
      return { ...n, hidden: true };
    });
    if (!changed) return;
    await saveFloatState(this.state, this.pageUrl);
    this.render();
  }

  /** Bring back every soft-hidden pin on this page. */
  async restoreHidden(): Promise<number> {
    let restored = 0;
    this.state.notes = this.state.notes.map((n) => {
      if (!n.hidden) return n;
      restored += 1;
      return { ...n, hidden: false };
    });
    if (restored === 0) return 0;
    this.state.collapsed = false;
    await saveFloatState(this.state, this.pageUrl);
    this.render();
    return restored;
  }

  /**
   * Merge membox notes for this URL into local pins (unhide existing, add
   * missing). New pins cascade along the content column's right margin.
   */
  async hydrateFromMembox(
    clips: Array<{ id: string; excerpt?: string; note?: string; title?: string }>,
  ): Promise<number> {
    if (!clips.length) return 0;
    const byId = new Map(this.state.notes.map((n) => [n.id, n]));
    let added = 0;
    const placed = this.placedRects('');
    clips.forEach((clip) => {
      const existing = byId.get(clip.id);
      if (existing) {
        existing.hidden = false;
        if (clip.excerpt) existing.excerpt = clip.excerpt;
        if (clip.note !== undefined && !existing.editedLocally) {
          existing.note = clip.note.trim();
        }
        return;
      }
      const excerpt =
        (clip.excerpt && clip.excerpt.trim()) ||
        (clip.title && clip.title.trim()) ||
        'Saved note';
      const pos = findClearPosition(
        {
          left: window.innerWidth / 2,
          top: window.scrollY + 80 + added * 40,
          width: 0,
          height: 0,
        } as DOMRect,
        placed,
      );
      placed.push({ top: pos.top, left: pos.left, width: CARD_WIDTH, height: CARD_EST_HEIGHT });
      this.state.notes.push({
        id: clip.id,
        excerpt,
        note: (clip.note || '').trim(),
        top: pos.top,
        left: pos.left,
        originTop: pos.top,
        originLeft: pos.left,
        createdAt: new Date().toISOString(),
        hidden: false,
      });
      added += 1;
    });
    this.state.notes.sort((a, b) => (a.createdAt < b.createdAt ? 1 : -1));
    this.state.collapsed = false;
    await saveFloatState(this.state, this.pageUrl);
    this.render();
    return added;
  }

  async clearPage() {
    this.state = { collapsed: true, notes: [] };
    await saveFloatState(this.state, this.pageUrl);
    this.teardown();
  }

  containsNode(node: Node | null): boolean {
    return Boolean(node && this.host && (node === this.host || this.host.contains(node)));
  }

  private placedRects(excludeId: string): Array<{ top: number; left: number; width: number; height: number }> {
    return this.visibleNotes()
      .filter((n) => n.id !== excludeId)
      .map((n) => ({ top: n.top, left: n.left, width: CARD_WIDTH, height: CARD_EST_HEIGHT }));
  }

  private teardown() {
    this.dragCleanup?.();
    this.dragCleanup = null;
    this.host?.remove();
    this.host = null;
    this.shadow = null;
  }

  private ensureHost() {
    if (this.host?.isConnected && this.shadow) return;
    document.getElementById(HOST_ID)?.remove();
    const host = document.createElement('div');
    host.id = HOST_ID;
    host.setAttribute('data-membox-ui', 'float-notes');
    Object.assign(host.style, {
      all: 'initial',
      position: 'absolute',
      left: '0',
      top: '0',
      width: '100%',
      height: `${pageHeight()}px`,
      zIndex: '2147483645',
      pointerEvents: 'none',
    });
    const shadow = host.attachShadow({ mode: 'closed' });
    document.documentElement.appendChild(host);
    this.host = host;
    this.shadow = shadow;
  }

  private render() {
    const notes = this.visibleNotes();
    if (!notes.length) {
      this.teardown();
      return;
    }
    this.theme = detectMiruTheme();
    this.ensureHost();
    if (!this.host || !this.shadow) return;
    this.dragCleanup?.();
    this.dragCleanup = null;

    // Overlay spans the whole document so pins (document-absolute) scroll
    // with the page instead of staying viewport-fixed.
    this.host.style.height = `${pageHeight()}px`;

    const t = MIRU_TOKENS[this.theme];
    const collapsed = this.state.collapsed;

    this.shadow.innerHTML = `
      <style>${layerCss()}</style>
      <div class="layer" data-theme="${this.theme}" style="
        --paper:${t.paper};
        --paper-raised:${t.paperRaised};
        --ink:${t.ink};
        --ink-soft:${t.inkSoft};
        --muted:${t.muted};
        --accent:${t.accent};
        --accent-soft:${t.accentSoft};
        --line:${t.line};
        --shadow:${t.shadow};
      ">
        ${collapsed ? this.renderStack(notes) : this.renderExpanded(notes)}
      </div>
    `;

    if (collapsed) {
      const stack = this.shadow.querySelector('.stack') as HTMLElement | null;
      stack?.addEventListener('click', () => void this.setCollapsed(false));
      return;
    }

    this.shadow.querySelectorAll('[data-collapse]').forEach((el) => {
      el.addEventListener('click', (e) => {
        e.stopPropagation();
        void this.setCollapsed(true);
      });
    });
    this.shadow.querySelectorAll('[data-remove]').forEach((el) => {
      el.addEventListener('click', (e) => {
        e.stopPropagation();
        const id = (el as HTMLElement).dataset.remove;
        if (id) void this.hideNote(id);
      });
    });
    this.shadow.querySelectorAll('[data-edit]').forEach((el) => {
      el.addEventListener('click', (e) => {
        e.stopPropagation();
        const id = (el as HTMLElement).dataset.edit;
        if (!id) return;
        this.editingId = id;
        this.render();
        const ta = this.shadow?.querySelector(`[data-note-input="${id}"]`) as HTMLTextAreaElement | null;
        if (ta) {
          ta.focus();
          ta.setSelectionRange(ta.value.length, ta.value.length);
          autosize(ta);
        }
      });
    });
    this.shadow.querySelectorAll('[data-follow]').forEach((el) => {
      el.addEventListener('click', (e) => {
        e.stopPropagation();
        const id = (el as HTMLElement).dataset.follow;
        if (id) void this.toggleFollow(id);
      });
    });
    this.shadow.querySelectorAll('[data-note-input]').forEach((el) => {
      const ta = el as HTMLTextAreaElement;
      const id = ta.dataset.noteInput || '';
      ta.addEventListener('input', () => autosize(ta));
      ta.addEventListener('keydown', (e) => {
        if ((e.metaKey || e.ctrlKey) && e.key === 'Enter') {
          e.preventDefault();
          void this.commitEdit(id, ta.value);
        }
        if (e.key === 'Escape') {
          e.preventDefault();
          this.editingId = null;
          this.render();
        }
      });
      ta.addEventListener('blur', () => void this.commitEdit(id, ta.value));
    });

    // Per-card interactions: drag to move, click to bring forward,
    // double-click to jump back to the excerpt in the page.
    this.shadow.querySelectorAll('[data-pin-id]').forEach((el) => {
      const pin = el as HTMLElement;
      const id = pin.dataset.pinId || '';
      this.attachPinInteractions(pin, id);
    });
  }

  private async commitEdit(id: string, value: string) {
    if (this.editingId !== id) return;
    this.editingId = null;
    const text = value.trim();
    let changed = false;
    this.state.notes = this.state.notes.map((n) => {
      if (n.id !== id) return n;
      if (n.note === text) return n;
      changed = true;
      return { ...n, note: text, editedLocally: true };
    });
    if (changed) await saveFloatState(this.state, this.pageUrl);
    this.render();
  }

  private attachPinInteractions(pin: HTMLElement, id: string) {
    let startX = 0;
    let startY = 0;
    let startTop = 0;
    let startLeft = 0;
    let moved = false;
    let dragging = false;

    const onPointerDown = (e: PointerEvent) => {
      const target = e.target as HTMLElement;
      if (target.closest('button, textarea, a')) return;
      if (e.button !== 0) return;
      const note = this.state.notes.find((n) => n.id === id);
      if (!note) return;
      const isFixed = !!note.followViewport;
      startX = e.clientX;
      startY = e.clientY;
      // Drag in the card's own coordinate space (viewport when following).
      startTop = isFixed ? this.currentFixedTop(note) : note.top;
      startLeft = isFixed ? this.currentFixedLeft(note) : note.left;
      moved = false;
      dragging = true;
      // Bring to front immediately.
      this.zTop += 1;
      pin.style.zIndex = String(this.zTop);
      try {
        pin.setPointerCapture(e.pointerId);
      } catch {
        /* ignore */
      }

      const onMove = (ev: PointerEvent) => {
        if (!dragging) return;
        const dx = ev.clientX - startX;
        const dy = ev.clientY - startY;
        if (!moved && Math.hypot(dx, dy) < DRAG_THRESHOLD) return;
        moved = true;
        pin.classList.add('is-dragging');
        const nextTop = Math.max(0, startTop + dy);
        const nextLeft = Math.max(
          0,
          Math.min(startLeft + dx, document.documentElement.clientWidth - CARD_WIDTH),
        );
        pin.style.top = `${nextTop}px`;
        pin.style.left = `${nextLeft}px`;
      };
      const onUp = (ev: PointerEvent) => {
        if (!dragging) return;
        dragging = false;
        pin.classList.remove('is-dragging');
        try {
          pin.releasePointerCapture(ev.pointerId);
        } catch {
          /* ignore */
        }
        pin.removeEventListener('pointermove', onMove);
        pin.removeEventListener('pointerup', onUp);
        pin.removeEventListener('pointercancel', onUp);
        if (!moved) return; // plain click — z-order already bumped
        const current = this.state.notes.find((n) => n.id === id);
        if (!current) return;
        const fixedMode = !!current.followViewport;
        const nextTop = Math.max(0, startTop + (ev.clientY - startY));
        const nextLeft = Math.max(
          0,
          Math.min(startLeft + (ev.clientX - startX), document.documentElement.clientWidth - CARD_WIDTH),
        );
        // Persist in the card's coordinate space; user-placed positions are
        // restored exactly on the next visit to this page.
        this.state.notes = this.state.notes.map((n) =>
          n.id === id
            ? fixedMode
              ? { ...n, fixedTop: nextTop, fixedLeft: nextLeft }
              : { ...n, top: nextTop, left: nextLeft }
            : n,
        );
        void saveFloatState(this.state, this.pageUrl);
      };
      pin.addEventListener('pointermove', onMove);
      pin.addEventListener('pointerup', onUp);
      pin.addEventListener('pointercancel', onUp);
    };

    const onDblClick = (e: MouseEvent) => {
      const target = e.target as HTMLElement;
      if (target.closest('button, textarea, a')) return;
      e.preventDefault();
      void this.returnToOrigin(id);
    };

    pin.addEventListener('pointerdown', onPointerDown);
    pin.addEventListener('dblclick', onDblClick);
  }

  private currentFixedTop(note: FloatingNote): number {
    return note.fixedTop ?? Math.max(0, note.top - window.scrollY);
  }

  private currentFixedLeft(note: FloatingNote): number {
    return note.fixedLeft ?? note.left;
  }

  /**
   * 📌 toggle: follow mode keeps the card at a viewport-relative spot, so it
   * stays in view while the article scrolls past.
   */
  private async toggleFollow(id: string) {
    this.state.notes = this.state.notes.map((n) => {
      if (n.id !== id) return n;
      if (n.followViewport) {
        // Back to document space at the current on-screen spot.
        return {
          ...n,
          followViewport: false,
          top: Math.max(0, this.currentFixedTop(n) + window.scrollY),
          left: this.currentFixedLeft(n),
        };
      }
      return {
        ...n,
        followViewport: true,
        fixedTop: Math.max(0, n.top - window.scrollY),
        fixedLeft: n.left,
      };
    });
    await saveFloatState(this.state, this.pageUrl);
    this.render();
  }

  /** Double-click: leave follow mode, move back to the origin, flash excerpt. */
  private async returnToOrigin(id: string) {
    const note = this.state.notes.find((n) => n.id === id);
    if (!note) return;
    note.followViewport = false;
    if (note.originTop !== undefined && note.originLeft !== undefined) {
      note.top = note.originTop;
      note.left = note.originLeft;
    }
    await saveFloatState(this.state, this.pageUrl);
    this.render();
    flashExcerpt(note.excerpt);
  }

  private renderStack(notes: FloatingNote[]): string {
    const visible = notes.slice(0, 5);
    const extra = Math.max(0, notes.length - visible.length);
    const cards = visible
      .map((note, i) => {
        const rot = (i - (visible.length - 1) / 2) * 4;
        const tx = i * 3;
        const ty = i * -4;
        return `
          <div class="mini" style="--z:${20 - i}; --rot:${rot}deg; --tx:${tx}px; --ty:${ty}px">
            <div class="mini-excerpt">${escapeHtml(truncate(note.excerpt, 72))}</div>
            ${note.note ? `<div class="mini-note">${escapeHtml(truncate(note.note, 48))}</div>` : ''}
          </div>`;
      })
      .join('');
    return `
      <button type="button" class="stack" title="Expand floating notes" style="pointer-events:auto">
        <div class="deck">${cards}</div>
        <div class="badge">${notes.length}${extra ? '+' : ''}</div>
        <div class="stack-hint">expand</div>
      </button>`;
  }

  private renderExpanded(notes: FloatingNote[]): string {
    const cards = notes
      .map((note) => {
        const following = !!note.followViewport;
        const top = following
          ? this.currentFixedTop(note)
          : Math.max(0, note.top);
        const left = Math.max(
          0,
          Math.min(
            following ? this.currentFixedLeft(note) : note.left,
            document.documentElement.clientWidth - CARD_WIDTH,
          ),
        );
        const editing = this.editingId === note.id;
        const body = editing
          ? `<textarea class="pin-edit" data-note-input="${escapeHtml(note.id)}" placeholder="Write a note…">${escapeHtml(note.note)}</textarea>`
          : note.note
            ? `<div class="pin-note">${escapeHtml(note.note)}</div>`
            : `<div class="pin-note pin-note-empty">No note — click ✎ to add</div>`;
        return `
          <article class="pin${editing ? ' is-editing' : ''}${following ? ' is-fixed' : ''}" data-pin-id="${escapeHtml(note.id)}" style="pointer-events:auto; top:${top}px; left:${left}px">
            <div class="pin-excerpt">${escapeHtml(truncate(note.excerpt, 160))}</div>
            ${body}
            <div class="pin-foot">
              <span class="pin-id">…${escapeHtml(shortUuid(note.id))}</span>
              <span class="pin-actions">
                <button type="button" class="pin-btn${following ? ' is-active' : ''}" data-follow="${escapeHtml(note.id)}" title="${following ? 'Unpin from view' : 'Pin to view (follows while reading)'}">📌</button>
                <button type="button" class="pin-btn" data-edit="${escapeHtml(note.id)}" title="Edit note">✎</button>
                <button type="button" class="pin-btn" data-remove="${escapeHtml(note.id)}" title="Hide pin (restore from popup)">×</button>
              </span>
            </div>
          </article>`;
      })
      .join('');
    return `
      <div class="expanded-toolbar" style="pointer-events:auto">
        <button type="button" class="tool-btn" data-collapse>Collapse</button>
        <span class="tool-count">${notes.length} note${notes.length === 1 ? '' : 's'}</span>
      </div>
      ${cards}`;
  }
}

/* ------------------------------------------------------------------ */
/* Positioning: prefer the margin beside the content column so cards   */
/* never cover the article body.                                       */
/* ------------------------------------------------------------------ */

type Rect = { top: number; left: number; width: number; height: number };

function pageHeight(): number {
  return Math.max(
    document.documentElement.scrollHeight,
    document.body?.scrollHeight || 0,
    window.innerHeight,
  );
}

/** Content column under a viewport point (walk up to a wide block). */
function contentColumnAt(x: number, y: number): { left: number; right: number } | null {
  let el = document.elementFromPoint(x, y) as HTMLElement | null;
  while (el && el !== document.body) {
    const r = el.getBoundingClientRect();
    if (r.width > CARD_WIDTH + 80 && r.width < window.innerWidth - 24) {
      return { left: r.left, right: r.right };
    }
    el = el.parentElement;
  }
  return null;
}

/** How many sample points inside a candidate rect sit on top of text content. */
function obstructionScore(left: number, topViewport: number, w: number, h: number): number {
  const probes: Array<[number, number]> = [
    [left + 8, topViewport + 8],
    [left + w - 8, topViewport + 8],
    [left + 8, topViewport + h - 8],
    [left + w - 8, topViewport + h - 8],
    [left + w / 2, topViewport + h / 2],
  ];
  let score = 0;
  for (const [x, y] of probes) {
    if (x < 0 || y < 0 || x > window.innerWidth || y > window.innerHeight) {
      score += 2;
      continue;
    }
    const el = document.elementFromPoint(x, y) as HTMLElement | null;
    if (!el) continue;
    if (
      el.closest('article, main, p, li, blockquote, h1, h2, h3, h4, h5, h6, td, th, .post, .entry-content')
    ) {
      score += 1;
    }
  }
  return score;
}

function rectsOverlap(a: Rect, b: Rect): boolean {
  return a.left < b.left + b.width && b.left < a.left + a.width && a.top < b.top + b.height && b.top < a.top + a.height;
}

/**
 * Pick a position that (1) avoids covering article text and (2) avoids
 * existing cards when possible. Coordinates are document-space.
 * `anchor` is given in document coordinates.
 */
function findClearPosition(
  anchor: { left: number; top: number; width: number; height: number },
  placed: Rect[],
): { top: number; left: number } {
  const vw = window.innerWidth;
  const scrollY = window.scrollY;
  const clampLeft = (x: number) => Math.max(8, Math.min(x, vw - CARD_WIDTH - 8));

  const anchorViewportTop = anchor.top - scrollY;
  const col = contentColumnAt(anchor.left, Math.max(20, anchorViewportTop));

  const candidates: Array<{ top: number; left: number }> = [];
  if (col) {
    candidates.push({ top: anchor.top, left: clampLeft(col.right + 16) }); // right margin
    candidates.push({ top: anchor.top, left: clampLeft(col.left - CARD_WIDTH - 16) }); // left margin
  }
  candidates.push({ top: anchor.top, left: clampLeft(anchor.left + anchor.width + 16) });
  candidates.push({ top: anchor.top, left: clampLeft(anchor.left - CARD_WIDTH - 16) });
  candidates.push({ top: anchor.top + anchor.height + 12, left: clampLeft(anchor.left) });
  candidates.push({ top: anchor.top, left: clampLeft(vw - CARD_WIDTH - 16) });

  let best = { top: anchor.top, left: clampLeft(anchor.left) };
  let bestScore = Infinity;
  let bestOverlap = Infinity;
  for (const c of candidates) {
    const rect: Rect = { top: c.top, left: c.left, width: CARD_WIDTH, height: CARD_EST_HEIGHT };
    const overlap = placed.filter((p) => rectsOverlap(rect, p)).length;
    const score = obstructionScore(c.left, c.top - scrollY, CARD_WIDTH, CARD_EST_HEIGHT) + overlap * 3;
    if (score < bestScore) {
      bestScore = score;
      bestOverlap = overlap;
      best = c;
      if (score === 0) break;
    }
  }
  // If every candidate collides with existing cards, cascade downward a bit
  // (partial overlap is acceptable by design).
  if (bestOverlap > 0) {
    best = { top: best.top + bestOverlap * 18, left: Math.min(best.left + bestOverlap * 10, vw - CARD_WIDTH - 8) };
  }
  return { top: Math.max(0, best.top), left: best.left };
}

/* ------------------------------------------------------------------ */
/* Flash the original excerpt text in the page for ~1.5 s.             */
/* ------------------------------------------------------------------ */

const FLASH_ATTR = 'data-membox-flash';

function flashExcerpt(excerpt: string) {
  const normalized = excerpt.replace(/\s+/g, ' ').trim();
  if (!normalized) return;
  const probe = normalized.slice(0, Math.min(28, normalized.length));

  const walker = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT, {
    acceptNode(node) {
      const parent = node.parentElement;
      if (!parent || parent.closest(`script, style, [${FLASH_ATTR}]`)) {
        return NodeFilter.FILTER_REJECT;
      }
      if (!node.nodeValue) return NodeFilter.FILTER_REJECT;
      return NodeFilter.FILTER_ACCEPT;
    },
  });
  let target: { node: Text; start: number } | null = null;
  while (walker.nextNode()) {
    const node = walker.currentNode as Text;
    const value = (node.nodeValue || '').replace(/\s+/g, ' ');
    const idx = value.indexOf(probe);
    if (idx >= 0) {
      target = { node, start: idx };
      break;
    }
  }
  if (!target) return;

  const range = document.createRange();
  const end = Math.min(target.node.nodeValue!.length, target.start + probe.length);
  range.setStart(target.node, target.start);
  range.setEnd(target.node, end);

  let flash: HTMLElement;
  try {
    flash = document.createElement('span');
    flash.setAttribute(FLASH_ATTR, '1');
    range.surroundContents(flash);
  } catch {
    // Multi-node ranges etc.: highlight the whole text node instead.
    flash = document.createElement('span');
    flash.setAttribute(FLASH_ATTR, '1');
    range.setStartBefore(target.node);
    range.setEndAfter(target.node);
    try {
      range.surroundContents(flash);
    } catch {
      return;
    }
  }
  applyFlashStyle(flash);
  flash.scrollIntoView({ behavior: 'smooth', block: 'center' });

  window.setTimeout(() => {
    const parent = flash.parentNode;
    if (!parent) return;
    while (flash.firstChild) parent.insertBefore(flash.firstChild, flash);
    parent.removeChild(flash);
    parent.normalize();
  }, FLASH_MS);
}

function applyFlashStyle(el: HTMLElement) {
  el.style.background = 'rgba(255, 214, 102, 0.55)';
  el.style.color = 'inherit';
  el.style.borderRadius = '2px';
  el.style.padding = '0 1px';
  el.style.transition = 'background 0.6s ease';
  el.style.boxDecorationBreak = 'clone';
  window.setTimeout(() => {
    el.style.background = 'rgba(255, 214, 102, 0)';
  }, FLASH_MS - 600);
}

/* ------------------------------------------------------------------ */

function autosize(ta: HTMLTextAreaElement) {
  ta.style.height = 'auto';
  ta.style.height = `${Math.min(200, Math.max(36, ta.scrollHeight))}px`;
}

function truncate(text: string, n: number): string {
  const t = text.replace(/\s+/g, ' ').trim();
  return t.length > n ? t.slice(0, n - 1) + '…' : t;
}

/** Last 5 chars — UUIDv7 prefixes (019...) are identical across notes. */
function shortUuid(id: string): string {
  const clean = id.replace(/-/g, '');
  return clean.length > 5 ? clean.slice(-5) : clean;
}

function escapeHtml(value: string): string {
  return value
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;');
}

function layerCss(): string {
  return `
    :host { all: initial; }
    .layer {
      position: absolute;
      inset: 0;
      font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, "Helvetica Neue", Arial, sans-serif;
      pointer-events: none;
    }

    /* —— Collapsed poker stack (viewport-fixed) —— */
    .stack {
      position: fixed;
      right: 20px;
      bottom: 20px;
      width: 168px;
      height: 118px;
      border: 0;
      background: transparent;
      padding: 0;
      cursor: pointer;
      z-index: 50;
    }
    .deck { position: absolute; inset: 0; }
    .mini {
      position: absolute;
      left: 8px; right: 16px; top: 12px; bottom: 18px;
      background: var(--paper-raised);
      color: var(--ink);
      border: 1px solid color-mix(in srgb, var(--line) 85%, var(--ink) 15%);
      border-radius: 8px;
      box-shadow: 0 8px 20px var(--shadow);
      padding: 8px 9px;
      text-align: left;
      transform: translate(var(--tx), var(--ty)) rotate(var(--rot));
      z-index: var(--z);
      overflow: hidden;
      transition: transform 0.2s ease, box-shadow 0.2s ease;
    }
    .stack:hover .mini { box-shadow: 0 12px 28px var(--shadow); }
    .stack:hover .mini:nth-child(1) { transform: translate(-6px, -8px) rotate(-8deg); }
    .stack:hover .mini:nth-child(2) { transform: translate(2px, -10px) rotate(1deg); }
    .stack:hover .mini:nth-child(3) { transform: translate(10px, -6px) rotate(7deg); }
    .stack:hover .mini:nth-child(4) { transform: translate(14px, 0px) rotate(11deg); }
    .stack:hover .mini:nth-child(5) { transform: translate(16px, 6px) rotate(14deg); }
    .mini-excerpt {
      font-family: Charter, Georgia, "Songti SC", "Noto Serif CJK SC", serif;
      font-size: 11px;
      line-height: 1.4;
      color: var(--ink-soft);
      display: -webkit-box;
      -webkit-line-clamp: 3;
      -webkit-box-orient: vertical;
      overflow: hidden;
    }
    .mini-note {
      margin-top: 4px;
      font-size: 10.5px;
      line-height: 1.35;
      color: var(--accent);
      display: -webkit-box;
      -webkit-line-clamp: 2;
      -webkit-box-orient: vertical;
      overflow: hidden;
    }
    .badge {
      position: absolute;
      right: 6px;
      top: 4px;
      min-width: 22px;
      height: 22px;
      padding: 0 6px;
      border-radius: 999px;
      background: var(--accent);
      color: var(--paper);
      font: 700 11px/22px -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
      text-align: center;
      z-index: 40;
      box-shadow: 0 2px 8px var(--shadow);
    }
    .layer[data-theme="dark"] .badge { color: #141413; }
    .stack-hint {
      position: absolute;
      left: 0; right: 0; bottom: 0;
      text-align: center;
      font-size: 10px;
      font-weight: 600;
      letter-spacing: 0.06em;
      text-transform: uppercase;
      color: var(--muted);
    }

    /* —— Expanded pins (document-space; scroll with the page) —— */
    .expanded-toolbar {
      position: fixed;
      right: 20px;
      bottom: 20px;
      display: flex;
      align-items: center;
      gap: 8px;
      background: var(--paper-raised);
      border: 1px solid var(--line);
      border-radius: 999px;
      padding: 4px 4px 4px 10px;
      box-shadow: 0 8px 24px var(--shadow);
      z-index: 60;
    }
    .tool-count { font-size: 11px; color: var(--muted); margin-right: 2px; }
    .tool-btn {
      appearance: none;
      border: 0;
      border-radius: 999px;
      background: color-mix(in srgb, var(--accent) 14%, transparent);
      color: var(--accent);
      font: 600 11px/1 -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
      padding: 6px 10px;
      cursor: pointer;
    }
    .pin {
      position: absolute;
      width: ${CARD_WIDTH}px;
      background: var(--paper-raised);
      color: var(--ink);
      border: 1px solid var(--line);
      border-radius: 10px;
      box-shadow: 0 10px 28px var(--shadow);
      padding: 10px 10px 8px;
      z-index: 10;
      cursor: grab;
      user-select: none;
      touch-action: none;
    }
    /* 📌 follow mode: viewport-relative, stays in view while reading. */
    .pin.is-fixed {
      position: fixed;
      z-index: 45;
      border-color: color-mix(in srgb, var(--accent) 45%, var(--line));
    }
    .pin.is-dragging {
      cursor: grabbing;
      box-shadow: 0 16px 40px var(--shadow);
    }
    .pin-excerpt {
      font-family: Charter, Georgia, "Songti SC", "Noto Serif CJK SC", serif;
      font-size: 12px;
      line-height: 1.45;
      color: var(--ink-soft);
      border-left: 2px solid color-mix(in srgb, var(--accent) 55%, var(--line));
      padding-left: 8px;
      max-height: 4.5em;
      overflow: hidden;
    }
    .pin-note {
      margin-top: 6px;
      font-size: 12px;
      line-height: 1.4;
      color: var(--ink);
      white-space: pre-wrap;
      word-break: break-word;
    }
    .pin-note-empty { color: var(--muted); font-style: italic; }
    .pin-edit {
      margin-top: 6px;
      width: 100%;
      min-height: 36px;
      max-height: 200px;
      border: 1px solid color-mix(in srgb, var(--accent) 40%, var(--line));
      border-radius: 7px;
      background: var(--paper);
      color: var(--ink);
      padding: 7px 9px;
      font: 12px/1.4 -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
      resize: none;
      outline: none;
      box-sizing: border-box;
    }
    .pin-foot {
      display: flex;
      align-items: center;
      justify-content: space-between;
      margin-top: 6px;
    }
    .pin-id {
      font-size: 10px;
      color: var(--muted);
      font-variant-numeric: tabular-nums;
    }
    .pin-actions { display: inline-flex; gap: 2px; }
    .pin-btn {
      appearance: none;
      border: 0;
      background: transparent;
      color: var(--muted);
      font-size: 13px;
      line-height: 1;
      padding: 3px 5px;
      border-radius: 4px;
      cursor: pointer;
    }
    .pin-btn:hover { color: var(--accent); background: var(--accent-soft); }
    .pin-btn.is-active {
      color: var(--accent);
      background: var(--accent-soft);
    }
  `;
}
