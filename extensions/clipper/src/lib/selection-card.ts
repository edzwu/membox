import { detectMiruTheme, MIRU_TOKENS, type MiruTheme } from './theme';

export type SelectionCardSave = {
  excerptText: string;
  excerptHTML: string;
  note: string;
  rect: DOMRect;
};

type Handlers = {
  onSave: (payload: SelectionCardSave) => void | Promise<void>;
  onDismiss?: () => void;
};

const HOST_ID = 'membox-selection-card-host';

/**
 * Minimal floating card: excerpt + note + Save.
 * Mounted in a closed shadow root so host page CSS cannot restyle it.
 */
export class SelectionCard {
  private host: HTMLElement | null = null;
  private shadow: ShadowRoot | null = null;
  private excerptEl: HTMLElement | null = null;
  private noteEl: HTMLTextAreaElement | null = null;
  private saveBtn: HTMLButtonElement | null = null;
  private statusEl: HTMLElement | null = null;
  private handlers: Handlers;
  private theme: MiruTheme = 'light';
  private lastRect: DOMRect | null = null;
  private excerptText = '';
  private excerptHTML = '';
  private saving = false;

  constructor(handlers: Handlers) {
    this.handlers = handlers;
  }

  isOpen(): boolean {
    return Boolean(this.host?.isConnected);
  }

  show(args: { excerptText: string; excerptHTML: string; rect: DOMRect }) {
    this.theme = detectMiruTheme();
    this.excerptText = args.excerptText.trim();
    this.excerptHTML = args.excerptHTML;
    this.lastRect = args.rect;
    this.saving = false;

    if (!this.host) {
      this.mount();
    }
    this.applyTheme();
    if (this.excerptEl) {
      this.excerptEl.textContent = this.excerptText;
    }
    if (this.noteEl) {
      this.noteEl.value = '';
      this.noteEl.disabled = false;
      autosizeNote(this.noteEl);
    }
    if (this.saveBtn) {
      this.saveBtn.disabled = false;
      this.saveBtn.textContent = 'Save';
    }
    if (this.statusEl) {
      this.statusEl.hidden = true;
      this.statusEl.classList.remove('is-error');
      this.statusEl.textContent = '';
      this.statusEl.title = '';
    }
    this.position(args.rect);
    this.noteEl?.focus();
  }

  hide() {
    this.host?.remove();
    this.host = null;
    this.shadow = null;
    this.excerptEl = null;
    this.noteEl = null;
    this.saveBtn = null;
    this.statusEl = null;
    this.handlers.onDismiss?.();
  }

  setError(message: string) {
    this.saving = false;
    if (this.saveBtn) {
      this.saveBtn.disabled = false;
      this.saveBtn.textContent = 'Save';
    }
    if (this.noteEl) this.noteEl.disabled = false;
    if (this.statusEl) {
      this.statusEl.hidden = false;
      this.statusEl.classList.add('is-error');
      this.statusEl.textContent = message;
      this.statusEl.title = message;
    }
  }

  setSaved(shortId: string) {
    this.saving = false;
    if (this.saveBtn) {
      this.saveBtn.disabled = true;
      this.saveBtn.textContent = 'Saved';
    }
    if (this.statusEl) {
      this.statusEl.hidden = false;
      this.statusEl.classList.remove('is-error');
      this.statusEl.textContent = shortId;
      this.statusEl.title = shortId;
    }
    window.setTimeout(() => this.hide(), 700);
  }

  containsNode(node: Node | null): boolean {
    return Boolean(node && this.host && (node === this.host || this.host.contains(node)));
  }

  private mount() {
    document.getElementById(HOST_ID)?.remove();
    const host = document.createElement('div');
    host.id = HOST_ID;
    host.setAttribute('data-membox-ui', 'selection-card');
    Object.assign(host.style, {
      all: 'initial',
      position: 'fixed',
      zIndex: '2147483646',
      top: '0',
      left: '0',
    });
    const shadow = host.attachShadow({ mode: 'closed' });
    shadow.innerHTML = `
      <style>${cardCss()}</style>
      <div class="card" part="card">
        <div class="excerpt" id="excerpt"></div>
        <div class="composer">
          <textarea class="note" id="note" rows="1" placeholder="Write a note…"></textarea>
          <div class="composer-bar">
            <div class="status" id="status" hidden></div>
            <button class="save" id="save" type="button">Save</button>
          </div>
        </div>
      </div>
    `;
    document.documentElement.appendChild(host);
    this.host = host;
    this.shadow = shadow;
    this.excerptEl = shadow.getElementById('excerpt');
    this.noteEl = shadow.getElementById('note') as HTMLTextAreaElement | null;
    this.saveBtn = shadow.getElementById('save') as HTMLButtonElement | null;
    this.statusEl = shadow.getElementById('status');

    this.saveBtn?.addEventListener('click', () => void this.submit());
    this.noteEl?.addEventListener('input', () => {
      if (this.noteEl) autosizeNote(this.noteEl);
      // Re-clamp to viewport after growth.
      if (this.lastRect) this.position(this.lastRect);
    });
    this.noteEl?.addEventListener('keydown', (event) => {
      if ((event.metaKey || event.ctrlKey) && event.key === 'Enter') {
        event.preventDefault();
        void this.submit();
      }
      if (event.key === 'Escape') {
        event.preventDefault();
        this.hide();
      }
    });
    if (this.noteEl) autosizeNote(this.noteEl);
  }

  private async submit() {
    if (this.saving || !this.lastRect) return;
    this.saving = true;
    if (this.saveBtn) {
      this.saveBtn.disabled = true;
      this.saveBtn.textContent = '…';
    }
    if (this.noteEl) this.noteEl.disabled = true;
    if (this.statusEl) {
      this.statusEl.hidden = true;
      this.statusEl.classList.remove('is-error');
      this.statusEl.textContent = '';
      this.statusEl.title = '';
    }
    try {
      await this.handlers.onSave({
        excerptText: this.excerptText,
        excerptHTML: this.excerptHTML,
        note: (this.noteEl?.value || '').trim(),
        rect: this.lastRect,
      });
    } catch (err) {
      this.setError(formatSaveError(err));
    }
  }

  private applyTheme() {
    const t = MIRU_TOKENS[this.theme];
    const card = this.shadow?.querySelector('.card') as HTMLElement | null;
    if (!card) return;
    card.dataset.theme = this.theme;
    card.style.setProperty('--paper', t.paper);
    card.style.setProperty('--paper-raised', t.paperRaised);
    card.style.setProperty('--ink', t.ink);
    card.style.setProperty('--ink-soft', t.inkSoft);
    card.style.setProperty('--muted', t.muted);
    card.style.setProperty('--accent', t.accent);
    card.style.setProperty('--accent-soft', t.accentSoft);
    card.style.setProperty('--line', t.line);
    card.style.setProperty('--input-bg', t.inputBg);
    card.style.setProperty('--shadow', t.shadow);
  }

  private position(rect: DOMRect) {
    if (!this.host) return;
    const card = this.shadow?.querySelector('.card') as HTMLElement | null;
    if (!card) return;
    const gap = 10;
    const vw = window.innerWidth;
    const vh = window.innerHeight;
    // Measure after content is set.
    const width = Math.min(268, vw - 24);
    card.style.width = `${width}px`;
    const height = card.getBoundingClientRect().height || 120;

    let top = rect.bottom + gap;
    if (top + height > vh - 12) {
      top = rect.top - height - gap;
    }
    top = Math.max(12, Math.min(top, vh - height - 12));

    let left = rect.left + rect.width / 2 - width / 2;
    left = Math.max(12, Math.min(left, vw - width - 12));

    this.host.style.top = `${Math.round(top)}px`;
    this.host.style.left = `${Math.round(left)}px`;
  }
}

function formatSaveError(err: unknown): string {
  const raw = err instanceof Error ? err.message : String(err);
  if (/extension context invalidated/i.test(raw)) {
    return 'Extension reloaded — refresh this page';
  }
  if (/receiving end does not exist|could not establish connection/i.test(raw)) {
    return 'Extension asleep — refresh this page';
  }
  if (/failed to fetch|networkerror|load failed/i.test(raw)) {
    return 'membox offline — open TUI / mm web start';
  }
  if (/unauthorized/i.test(raw)) {
    return 'Token mismatch — check popup settings';
  }
  // Keep the card compact; full text is on title tooltip.
  return raw.length > 64 ? raw.slice(0, 61) + '…' : raw;
}

/** Grow textarea with content, starting from a single compact row. */
function autosizeNote(el: HTMLTextAreaElement) {
  const max = 120;
  el.style.height = '0px';
  const style = getComputedStyle(el);
  const line = parseFloat(style.lineHeight) || 18;
  const pad =
    (parseFloat(style.paddingTop) || 0) + (parseFloat(style.paddingBottom) || 0);
  // ~1.2 line boxes when empty.
  const min = Math.ceil(line * 1.2 + pad);
  const height = Math.min(max, Math.max(min, el.scrollHeight));
  el.style.height = `${height}px`;
  el.style.overflowY = height >= max ? 'auto' : 'hidden';
}

function cardCss(): string {
  return `
    :host { all: initial; }
    .card {
      font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, "Helvetica Neue", Arial, sans-serif;
      background: var(--paper-raised);
      color: var(--ink);
      border: 1px solid color-mix(in srgb, var(--line) 88%, var(--ink) 12%);
      border-radius: 10px;
      box-shadow:
        0 1px 0 color-mix(in srgb, var(--paper-raised) 80%, transparent) inset,
        0 10px 28px var(--shadow);
      padding: 10px;
      display: flex;
      flex-direction: column;
      gap: 8px;
      box-sizing: border-box;
    }
    .excerpt {
      font-family: Charter, Georgia, "Songti SC", "Noto Serif CJK SC", serif;
      font-size: 12.5px;
      line-height: 1.5;
      color: var(--ink-soft);
      border-left: 2px solid color-mix(in srgb, var(--accent) 55%, var(--line));
      padding: 1px 0 1px 10px;
      max-height: 5.6em;
      overflow: auto;
      white-space: pre-wrap;
      word-break: break-word;
    }
    /* Composer: note + Save share one field (Linear/Notion-style). */
    .composer {
      position: relative;
      border: 1px solid var(--line);
      background: var(--input-bg);
      border-radius: 8px;
      transition: border-color 0.12s ease, box-shadow 0.12s ease;
    }
    .composer:focus-within {
      border-color: color-mix(in srgb, var(--accent) 40%, var(--line));
      box-shadow: 0 0 0 2px color-mix(in srgb, var(--accent) 12%, transparent);
    }
    .note {
      display: block;
      width: 100%;
      min-height: calc(1.2em + 10px + 26px);
      height: calc(1.2em + 10px + 26px);
      max-height: 132px;
      border: 0;
      background: transparent;
      color: var(--ink);
      border-radius: 8px;
      /* Bottom padding reserves the Save-button row; text keeps the full width. */
      padding: 6px 9px 28px 9px;
      font: 12.5px/1.45 -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
      resize: none;
      overflow-y: hidden;
      outline: none;
      box-sizing: border-box;
      field-sizing: content;
    }
    .note::placeholder { color: var(--muted); opacity: 0.85; }
    .composer-bar {
      position: absolute;
      right: 5px;
      bottom: 5px;
      display: flex;
      align-items: center;
      gap: 6px;
      max-width: calc(100% - 12px);
      pointer-events: none;
    }
    .composer-bar .save { pointer-events: auto; }
    .status {
      font-size: 10px;
      line-height: 1.25;
      color: var(--muted);
      max-width: 11em;
      overflow: hidden;
      text-overflow: ellipsis;
      white-space: nowrap;
      pointer-events: none;
    }
    .status.is-error {
      color: color-mix(in srgb, #b3402f 80%, var(--ink));
      white-space: normal;
      max-width: 14em;
      text-align: right;
    }
    .card[data-theme="dark"] .status.is-error {
      color: color-mix(in srgb, #e07a6a 85%, var(--ink));
    }
    .save {
      appearance: none;
      border: 0;
      border-radius: 5px;
      background: color-mix(in srgb, var(--accent) 14%, transparent);
      color: var(--accent);
      font: 600 11px/1 -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
      letter-spacing: 0.01em;
      padding: 4px 8px;
      cursor: pointer;
    }
    .save:hover { background: color-mix(in srgb, var(--accent) 22%, transparent); }
    .save:disabled { opacity: 0.5; cursor: default; }
  `;
}
