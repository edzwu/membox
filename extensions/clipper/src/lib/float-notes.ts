import { detectMiruTheme, MIRU_TOKENS, type MiruTheme } from './theme';

export type FloatingNote = {
  id: string;
  excerpt: string;
  note: string;
  /** Document coordinates (pageY / pageX). */
  top: number;
  left: number;
  createdAt: string;
  /** Hidden via × — still stored; restore from popup. */
  hidden?: boolean;
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
 * collapsed = poker-deck stack (bottom-right);
 * expanded = each card near its original selection.
 */
export class FloatNotesLayer {
  private host: HTMLElement | null = null;
  private shadow: ShadowRoot | null = null;
  private state: FloatPageState = { collapsed: true, notes: [] };
  private theme: MiruTheme = 'light';

  async init() {
    this.state = await loadFloatState();
    this.theme = detectMiruTheme();
    this.render();
    window.addEventListener('resize', () => this.render());
    // Drop stale local pins that are not real membox selection notes for this URL.
    void this.syncWithMemboxInBackground();
  }

  private async syncWithMemboxInBackground() {
    try {
      if (!browser.runtime?.id) return;
      const url = location.href;
      const res = (await browser.runtime.sendMessage({
        type: 'membox.clips-for-url',
        url,
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
   */
  async reconcileWithMembox(
    clips: Array<{ id: string; excerpt?: string; note?: string; title?: string }>,
  ): Promise<FloatStatus> {
    const valid = new Set(clips.map((c) => c.id));
    const before = this.state.notes.length;
    this.state.notes = this.state.notes.filter((n) => valid.has(n.id));
    // Refresh excerpt/note text from membox when available.
    const byId = new Map(clips.map((c) => [c.id, c]));
    this.state.notes = this.state.notes.map((n) => {
      const c = byId.get(n.id);
      if (!c) return n;
      return {
        ...n,
        excerpt: (c.excerpt && c.excerpt.trim()) || n.excerpt,
        note: c.note !== undefined ? (c.note || '').trim() : n.note,
      };
    });
    if (this.state.notes.length !== before) {
      await saveFloatState(this.state);
    } else {
      await saveFloatState(this.state);
    }
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

  async addNote(note: FloatingNote) {
    // Newest on top of the deck; re-saving unhides if it was soft-removed.
    const next = { ...note, hidden: false };
    this.state.notes = [next, ...this.state.notes.filter((n) => n.id !== note.id)];
    await saveFloatState(this.state);
    this.render();
  }

  async setCollapsed(collapsed: boolean) {
    this.state.collapsed = collapsed;
    await saveFloatState(this.state);
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
    await saveFloatState(this.state);
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
    // Show them expanded so the user can find them.
    this.state.collapsed = false;
    await saveFloatState(this.state);
    this.render();
    return restored;
  }

  /**
   * Merge membox notes for this URL into local pins (unhide existing, add missing).
   * Positions for newly hydrated pins cascade near the viewport top.
   */
  async hydrateFromMembox(
    clips: Array<{ id: string; excerpt?: string; note?: string; title?: string }>,
  ): Promise<number> {
    if (!clips.length) return 0;
    const byId = new Map(this.state.notes.map((n) => [n.id, n]));
    let added = 0;
    const baseTop = window.scrollY + 96;
    const baseLeft = Math.min(48, Math.max(12, window.innerWidth - 280));
    clips.forEach((clip, index) => {
      const existing = byId.get(clip.id);
      if (existing) {
        existing.hidden = false;
        if (clip.excerpt) existing.excerpt = clip.excerpt;
        if (clip.note !== undefined) existing.note = clip.note;
        return;
      }
      const excerpt =
        (clip.excerpt && clip.excerpt.trim()) ||
        (clip.title && clip.title.trim()) ||
        'Saved note';
      this.state.notes.push({
        id: clip.id,
        excerpt,
        note: (clip.note || '').trim(),
        top: baseTop + index * 28,
        left: baseLeft + index * 12,
        createdAt: new Date().toISOString(),
        hidden: false,
      });
      added += 1;
    });
    // Newest first for deck order.
    this.state.notes.sort((a, b) => (a.createdAt < b.createdAt ? 1 : -1));
    this.state.collapsed = false;
    await saveFloatState(this.state);
    this.render();
    return added;
  }

  async clearPage() {
    this.state = { collapsed: true, notes: [] };
    await saveFloatState(this.state);
    this.teardown();
  }

  containsNode(node: Node | null): boolean {
    return Boolean(node && this.host && (node === this.host || this.host.contains(node)));
  }

  private teardown() {
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
      height: `${Math.max(document.documentElement.scrollHeight, document.body?.scrollHeight || 0)}px`,
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
      // Keep hidden notes in storage; only tear down UI when nothing to show.
      this.teardown();
      return;
    }
    this.theme = detectMiruTheme();
    this.ensureHost();
    if (!this.host || !this.shadow) return;

    // Keep overlay as tall as the document for absolute card positions.
    this.host.style.height = `${Math.max(
      document.documentElement.scrollHeight,
      document.body?.scrollHeight || 0,
      window.innerHeight,
    )}px`;

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
    } else {
      this.shadow.querySelectorAll('[data-collapse]').forEach((el) => {
        el.addEventListener('click', (e) => {
          e.stopPropagation();
          void this.setCollapsed(true);
        });
      });
      // Soft-hide pin on this page only — restore from popup; membox untouched.
      this.shadow.querySelectorAll('[data-remove]').forEach((el) => {
        el.addEventListener('click', (e) => {
          e.stopPropagation();
          const id = (el as HTMLElement).dataset.remove;
          if (id) void this.hideNote(id);
        });
      });
    }
  }

  private renderStack(notes: FloatingNote[]): string {
    const visible = notes.slice(0, 5);
    const extra = Math.max(0, notes.length - visible.length);
    const cards = visible
      .map((note, i) => {
        // Poker-ish fan: slight rotate + offset, newest on top (i=0).
        const depth = visible.length - 1 - i;
        const rot = (i - (visible.length - 1) / 2) * 4;
        const tx = i * 3;
        const ty = i * -4;
        return `
          <div class="mini" style="--z:${20 - i}; --rot:${rot}deg; --tx:${tx}px; --ty:${ty}px; --depth:${depth}">
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
    return `
      <div class="expanded-toolbar" style="pointer-events:auto">
        <button type="button" class="tool-btn" data-collapse>Collapse</button>
        <span class="tool-count">${notes.length} note${notes.length === 1 ? '' : 's'}</span>
      </div>
      ${notes
        .map((note, i) => {
          const top = Math.max(12, note.top);
          const left = Math.max(12, Math.min(note.left, window.innerWidth - 280));
          return `
            <article class="pin" style="pointer-events:auto; top:${top}px; left:${left}px; --i:${i}">
              <div class="pin-excerpt">${escapeHtml(truncate(note.excerpt, 160))}</div>
              ${note.note ? `<div class="pin-note">${escapeHtml(note.note)}</div>` : ''}
              <div class="pin-foot">
                <span class="pin-id">${escapeHtml(note.id.slice(0, 8))}…</span>
                <button type="button" class="pin-x" data-remove="${escapeHtml(note.id)}" title="Hide pin — restore anytime from extension popup">×</button>
              </div>
            </article>`;
        })
        .join('')}`;
  }
}

function truncate(text: string, n: number): string {
  const t = text.replace(/\s+/g, ' ').trim();
  return t.length > n ? t.slice(0, n - 1) + '…' : t;
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

    /* —— Collapsed poker stack —— */
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
    .stack:hover .mini {
      box-shadow: 0 12px 28px var(--shadow);
    }
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

    /* —— Expanded pins —— */
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
    .tool-count {
      font-size: 11px;
      color: var(--muted);
      margin-right: 2px;
    }
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
      width: 240px;
      background: var(--paper-raised);
      color: var(--ink);
      border: 1px solid var(--line);
      border-radius: 10px;
      box-shadow: 0 10px 28px var(--shadow);
      padding: 10px 10px 8px;
      z-index: calc(10 + var(--i));
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
    .pin-x {
      appearance: none;
      border: 0;
      background: transparent;
      color: var(--muted);
      font-size: 14px;
      line-height: 1;
      padding: 2px 4px;
      border-radius: 4px;
      cursor: pointer;
    }
    .pin-x:hover { color: var(--accent); background: var(--accent-soft); }
  `;
}
