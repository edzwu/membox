/* Translation for the rendered Miru article. Full-document immersive blocks
   are disposable; a selected passage is committed as a durable inline
   annotation note after streaming. Neither mode changes the source Markdown.
   Paragraphs are sent independently so long documents share no model context. */

import { scheduleNoteLayout } from '../js/annotations/layout.js';
import { applyNote, findAnnot, setNoteOnPassage } from '../js/annotations/model.js';
import { detachComposeSelection, registerAnnotAction } from '../js/annotations/toolbar.js';
import { elements } from '../js/dom.js';
import { state } from '../js/state.js';
import { showToast } from '../js/ui/feedback.js';
import { streamTranslation } from './api.js';
import { session } from './session.js';

const TARGET_SELECTOR = 'h1, h2, h3, h4, h5, h6, p, li, dt, dd, figcaption, th, td';
const TRANSLATION_CLASS = 'membox-translation';
// Keep in sync with internal/translation/types.go MaxSegmentBytes.
const MAX_SEGMENT_BYTES = 32 * 1024;
const textEncoder = new TextEncoder();
const byteLength = (text) => textEncoder.encode(text).length;

let active = false;
let running = false;
let generation = 0;
let controller = null;
let completed = 0;
let failed = 0;
let total = 0;

// Paint through a small queue instead of dumping each network delta directly.
// Live Ollama deltas still appear immediately, while a disk-cache hit (one
// large delta) is replayed progressively so selection translation keeps the
// same visible streaming phase before it becomes a durable note.
function createStreamPainter(target, { progressive = false } = {}) {
  const output = document.createTextNode('');
  target.appendChild(output);
  const queue = [];
  const waiters = [];
  let timer = null;
  let cancelled = false;

  const settle = () => {
    if (queue.length || timer !== null) return;
    while (waiters.length) waiters.shift()();
  };
  const paint = () => {
    timer = null;
    if (cancelled || !target.isConnected) {
      queue.length = 0;
      settle();
      return;
    }
    // Drain proportionally: short live deltas feel immediate; large cached
    // paragraphs take roughly 1–3 seconds instead of appearing in one paint.
    const count = Math.max(1, Math.ceil(queue.length / 24));
    output.appendData(queue.splice(0, count).join(''));
    if (queue.length) timer = window.setTimeout(paint, 16);
    settle();
  };
  const schedule = () => {
    if (timer === null && queue.length && !cancelled) {
      timer = window.setTimeout(paint, 0);
    }
  };

  return {
    append(value) {
      if (cancelled || !value) return;
      if (!progressive) {
        output.appendData(String(value));
        return;
      }
      queue.push(...Array.from(String(value)));
      schedule();
    },
    flush() {
      if (!queue.length && timer === null) return Promise.resolve();
      return new Promise((resolve) => waiters.push(resolve));
    },
    cancel() {
      cancelled = true;
      queue.length = 0;
      if (timer !== null) window.clearTimeout(timer);
      timer = null;
      settle();
    },
  };
}

function sourceText(element) {
  const clone = element.cloneNode(true);
  clone.querySelectorAll([
    'button', '.annot-note-num', `.${TRANSLATION_CLASS}`, '.membox-jp-study',
    '.katex-html', '.code-copy', '.section-copy', '.section-download', '.lead-copy',
  ].join(',')).forEach((node) => node.remove());
  return (clone.textContent || '').replace(/\s+/g, ' ').trim();
}

function normalizedText(text) {
  return String(text || '').replace(/\s+/g, ' ').trim();
}

function translationNoteBody(translated) {
  const body = String(translated || '').trim();
  return body ? `**翻译：**\n\n${body}\n` : '';
}

function shouldTranslate(text) {
  if (text.length < 2 || !/[\p{L}]/u.test(text)) return false;
  if (byteLength(text) > MAX_SEGMENT_BYTES) return false;
  const latin = (text.match(/[A-Za-z]/g) || []).length;
  const han = (text.match(/[\u3400-\u9fff]/g) || []).length;
  // Chinese prose containing a few English technical terms is already in the
  // target language. Japanese/Korean and Latin prose continue to translation.
  if (han > 0 && han >= latin * 2 && !/[\u3040-\u30ff\uac00-\ud7af]/u.test(text)) return false;
  return true;
}

function isTranslatableBlock(element) {
  if (!element || element.nodeType !== 1) return false;
  if (!element.matches?.(TARGET_SELECTOR)) return false;
  if (element.closest(`.${TRANSLATION_CLASS}, pre, code, .mermaid-diagram, .doc-title`)) return false;
  // Nested list containers are translated via their leaf items / paragraphs.
  if (element.matches('li') && element.querySelector(':scope > p, :scope > ul, :scope > ol')) return false;
  return true;
}

function clearTranslationFor(element) {
  if (!element) return;
  if (element.matches('li, td, th, dd, dt')) {
    element.querySelectorAll(`:scope > .${TRANSLATION_CLASS}`).forEach((node) => node.remove());
    return;
  }
  let next = element.nextElementSibling;
  while (next && next.classList.contains(TRANSLATION_CLASS)) {
    const node = next;
    next = next.nextElementSibling;
    node.remove();
  }
}

/** Stable-enough fingerprint so full-doc runs can skip unchanged blocks. */
function sourceFingerprint(text) {
  const value = String(text || '');
  let hash = 2166136261;
  for (let i = 0; i < value.length; i++) {
    hash ^= value.charCodeAt(i);
    hash = Math.imul(hash, 16777619);
  }
  return `${value.length.toString(36)}:${(hash >>> 0).toString(36)}`;
}

function translationNodesFor(element) {
  if (!element) return [];
  if (element.matches('li, td, th, dd, dt')) {
    return [...element.querySelectorAll(`:scope > .${TRANSLATION_CLASS}`)];
  }
  const nodes = [];
  let next = element.nextElementSibling;
  while (next && next.classList.contains(TRANSLATION_CLASS)) {
    nodes.push(next);
    next = next.nextElementSibling;
  }
  return nodes;
}

/** Finished translation that still matches this source text (idempotent hit). */
function hasUsableTranslation(element, text) {
  const want = sourceFingerprint(text);
  return translationNodesFor(element).some((node) => {
    if (node.querySelector('.membox-translation-spinner')) return false;
    const body = node.querySelector('.membox-translation-text')?.textContent?.trim() || '';
    if (!body) return false;
    const got = node.dataset.sourceFp || '';
    // Legacy nodes (no fingerprint): treat non-empty body as reusable so a
    // later full-doc pass does not duplicate selection translations.
    return !got || got === want;
  });
}

/** Blocks intersecting the current article selection, or null if none.
 *  Accepts a range from detachComposeSelection() — the compose dialog masks
 *  the page selection out of window.getSelection(), so dock actions must
 *  hand the passage over instead of reading the live selection. */
function selectionRows(rangeArg) {
  const selection = window.getSelection();
  let range = rangeArg || null;
  if (!range) {
    if (!selection || selection.isCollapsed || !selection.rangeCount || !elements.article) return null;
    try {
      range = selection.getRangeAt(0);
    } catch {
      return null;
    }
  }
  if (!elements.article.contains(range.commonAncestorContainer)) return null;
  if (!rangeArg && !String(selection.toString() || '').replace(/\s+/g, ' ').trim()) return null;

  const candidates = [...elements.article.querySelectorAll(TARGET_SELECTOR)];
  const rows = [];
  for (const element of candidates) {
    if (!isTranslatableBlock(element)) continue;
    let hits = false;
    try {
      hits = range.intersectsNode(element);
    } catch {
      hits = false;
    }
    if (!hits) continue;
    const text = sourceText(element);
    if (!shouldTranslate(text)) continue;
    rows.push({ id: `selection-${rows.length + 1}`, element, text });
  }
  return rows.length ? rows : null;
}

function collectParagraphs() {
  const candidates = [...elements.article.querySelectorAll(TARGET_SELECTOR)];
  const rows = [];
  for (const element of candidates) {
    if (!isTranslatableBlock(element)) continue;
    const text = sourceText(element);
    if (!shouldTranslate(text)) continue;
    rows.push({ id: `paragraph-${rows.length + 1}`, element, text });
  }

  // Start at the block nearest the current reading position, continue toward
  // the end, then finish earlier material. This mirrors lazy immersive readers
  // while still eventually translating the whole document.
  const viewportTop = window.scrollY + 8;
  const start = rows.findIndex(({ element }) => {
    const rect = element.getBoundingClientRect();
    return rect.bottom + window.scrollY >= viewportTop;
  });
  return start > 0 ? rows.slice(start).concat(rows.slice(0, start)) : rows;
}

function translationNode(row) {
  // Never stack a second body under an already-translated block.
  clearTranslationFor(row.element);
  const wrapper = document.createElement('div');
  wrapper.className = TRANSLATION_CLASS;
  wrapper.dataset.translationId = row.id;
  wrapper.dataset.sourceFp = sourceFingerprint(row.text);
  wrapper.lang = 'zh-CN';
  wrapper.setAttribute('translate', 'no');

  const text = document.createElement('span');
  text.className = 'membox-translation-text';
  wrapper.appendChild(text);
  const spinner = document.createElement('span');
  spinner.className = 'membox-translation-spinner';
  spinner.setAttribute('aria-hidden', 'true');
  wrapper.appendChild(spinner);

  if (row.element.matches('li, td, th, dd, dt')) row.element.appendChild(wrapper);
  else row.element.insertAdjacentElement('afterend', wrapper);
  return { wrapper, text, spinner };
}

async function translateParagraph(row, currentGeneration) {
  const node = translationNode(row);
  const painter = createStreamPainter(node.text, { progressive: Boolean(row.progressiveReveal) });
  const signal = controller?.signal;
  let done = false;
  try {
    await streamTranslation({
      id: row.id,
      title: state.docTitle || document.title || '',
      target_language: 'Simplified Chinese (zh-CN)',
      text: row.text,
    }, (event) => {
      if (!active || generation !== currentGeneration || event.id !== row.id) return;
      if (event.type === 'delta') painter.append(event.text || '');
      if (event.type === 'done') done = true;
    }, { signal });
    if (!done) throw new Error('Translation stream ended before completion');
    // Cache replay can finish its HTTP response before the browser gets a
    // paint. Wait for the visible queue so conversion to a note happens only
    // after the user has seen the translation stream complete.
    await painter.flush();
    if (!active || generation !== currentGeneration || signal?.aborted) {
      const aborted = new Error('Translation aborted');
      aborted.name = 'AbortError';
      throw aborted;
    }
    // Yield one task so the fully revealed transient line is paintable before
    // the durable note card replaces it.
    await new Promise((resolve) => window.setTimeout(resolve, 0));
    const translated = node.text.textContent.trim();
    node.spinner.remove();
    if (!translated || translated === row.text) {
      node.wrapper.remove();
      scheduleNoteLayout();
      return '';
    }
    node.wrapper.dataset.sourceFp = sourceFingerprint(row.text);
    scheduleNoteLayout();
    return translated;
  } catch (error) {
    painter.cancel();
    node.wrapper.remove();
    throw error;
  }
}

async function translateRows(rows, { selectionOnly, skipped = 0 } = {}) {
  if (!rows.length) {
    showToast(selectionOnly
      ? 'Selection has nothing to translate'
      : 'No paragraphs need translation');
    return;
  }

  // Drop any sibling immersive overlay (jp-study) before starting.
  window.dispatchEvent(new CustomEvent('membox-stop-immersive', { detail: { source: 'translate' } }));
  // Abort in-flight work but keep finished DOM (unless selection refresh).
  generation++;
  if (controller) controller.abort();
  controller = null;
  if (selectionOnly) {
    for (const row of rows) clearTranslationFor(row.element);
  }

  active = true;
  running = true;
  completed = 0;
  failed = 0;
  total = rows.length + skipped;
  const currentGeneration = ++generation;
  controller = new AbortController();
  showToast(selectionOnly ? '翻译中…' : `沉浸翻译中 · ${rows.length} 段…`);

  try {
    for (const row of rows) {
      if (!active || generation !== currentGeneration) return;
      try {
        await translateParagraph(row, currentGeneration);
        completed++;
      } catch (error) {
        if (error && error.name === 'AbortError') throw error;
        // A single bad paragraph (oversized, model hiccup) must not abort the
        // remaining document. Skip it and keep translating.
        failed++;
      }
    }
    running = false;
    // Selection jobs are one-shot; full-doc stays toggleable until stop.
    if (selectionOnly) active = false;
    const kept = skipped ? ` · ${skipped} kept` : '';
    if (selectionOnly) {
      showToast(failed
        ? `Selection: ${completed}/${rows.length} translated · ${failed} failed`
        : (rows.length === 1 ? 'Selection translated' : `Selection: ${completed} blocks translated`));
    } else {
      showToast(failed
        ? `Translated ${completed}/${rows.length}${kept} · ${failed} failed`
        : `Translated ${completed} paragraphs${kept}`);
    }
  } catch (error) {
    if (error && error.name === 'AbortError') return;
    running = false;
    if (selectionOnly) active = false;
    showToast(error instanceof Error ? error.message : 'Translation failed');
  }
}

/** Translate the passage blocks under a selection (legacy transient helper). */
async function runSelectionRows(rows) {
  return translateRows(rows, { selectionOnly: true, skipped: 0 });
}

function selectionHost(rangeOrElement) {
  let node = rangeOrElement;
  if (node && typeof node.endContainer !== 'undefined') node = node.endContainer;
  if (node?.nodeType === Node.TEXT_NODE) node = node.parentElement;
  if (!node || !elements.article?.contains(node)) return null;
  return node.closest?.(TARGET_SELECTOR) || node.closest?.('.fold-body, article') || null;
}

/** Selection translation is a durable reading artifact. It streams through
 *  the disposable translation DOM, then commits through the same annotation
 *  note path used by Q&A and summaries. The standalone SQLite cache remains
 *  only a computation cache. */
async function runDurableSelectionTranslation({ text, range = null, annotEl = null, entry = null }) {
  const source = normalizedText(annotEl ? sourceText(annotEl) : text);
  const host = selectionHost(annotEl || range);
  if (!source || !shouldTranslate(source) || !host) {
    throw new Error('选区没有可翻译的内容');
  }
  window.dispatchEvent(new CustomEvent('membox-stop-immersive', { detail: { source: 'translate' } }));
  generation++;
  controller?.abort();
  controller = new AbortController();
  active = true;
  running = true;
  completed = 0;
  failed = 0;
  total = 1;
  const currentGeneration = ++generation;
  const row = { id: 'selection-1', element: host, text: source, progressiveReveal: true };
  showToast(entry ? '重新翻译中…' : '翻译中…');

  try {
    const translated = await translateParagraph(row, currentGeneration);
    if (!translated) throw new Error('模型返回空翻译');
    if (!annotEl && (!range || !range.commonAncestorContainer?.isConnected)) {
      throw new Error('选区已失效，请重新选择');
    }

    // Replace the stream preview with a durable annotation card. The note
    // persistence adapter writes an independent *-note.md automatically.
    clearTranslationFor(host);
    const noteText = translationNoteBody(translated);
    if (annotEl && entry) {
      setNoteOnPassage(entry, annotEl, noteText, { kind: 'translation' });
    } else {
      applyNote(range, noteText, { kind: 'translation' });
    }
    completed = 1;
    active = false;
    running = false;
    controller = null;
    scheduleNoteLayout();
    showToast(entry ? '翻译已更新' : '翻译已写入原文');
    return { translated };
  } catch (error) {
    clearTranslationFor(host);
    active = false;
    running = false;
    failed = 1;
    controller = null;
    scheduleNoteLayout();
    throw error;
  }
}

/** Full-document immersive translation (keyboard-only entry). */
async function startFullTranslation() {
  if (!session.connected) {
    showToast('Connect to membox before translating');
    return;
  }
  const candidates = collectParagraphs();
  // Idempotency: reuse finished translations whose source fingerprint still
  // matches. Full-doc must NOT wipe earlier selection translations.
  let skipped = 0;
  const rows = [];
  for (const row of candidates) {
    if (hasUsableTranslation(row.element, row.text)) skipped++;
    else rows.push(row);
  }
  return translateRows(rows, { selectionOnly: false, skipped });
}

function stopTranslation() {
  generation++;
  if (controller) controller.abort();
  controller = null;
  active = false;
  running = false;
  completed = 0;
  failed = 0;
  total = 0;
  elements.article.querySelectorAll(`.${TRANSLATION_CLASS}`).forEach((node) => node.remove());
  scheduleNoteLayout();
}

export function initTranslation() {
  // Selection translate lives inside the compose dialog and persists as an
  // anchored translation note. Full-document immersive translation stays
  // disposable and available via Cmd/Ctrl+Alt+T.
  registerAnnotAction({
    id: 'translate',
    icon: '译',
    title: '翻译选中内容并保存 · Cmd+Alt+T 全文沉浸',
    when: ({ mode, text, entry }) =>
      (mode === 'create' && Boolean(text)) || (mode === 'edit' && entry?.kind === 'translation'),
    run: (ctx) => {
      if (active) {
        stopTranslation();
        return;
      }
      if (!session.connected) {
        showToast('需要连接 membox 才能翻译');
        return;
      }

      if (ctx.mode === 'edit' && ctx.annotEl) {
        const annotEl = ctx.annotEl;
        const entry = findAnnot(annotEl.dataset.annotId);
        if (!entry || entry.kind !== 'translation') return;
        const text = sourceText(annotEl);
        ctx.hide?.();
        void runDurableSelectionTranslation({ text, annotEl, entry })
          .catch((error) => showToast(error?.message || '翻译失败'));
        return;
      }

      const detached = detachComposeSelection();
      if (!detached) return;
      void runDurableSelectionTranslation(detached)
        .catch((error) => showToast(error?.message || '翻译失败'));
    },
  });

  document.addEventListener('keydown', (e) => {
    if (e.repeat || e.isComposing || e.keyCode === 229) return;
    if (e.metaKey && e.altKey && e.key.toLowerCase() === 't') {
      e.preventDefault();
      if (active) stopTranslation();
      else void startFullTranslation();
    }
  });

  window.addEventListener('miru-document-change', () => {
    stopTranslation();
  });
  window.addEventListener('membox-stop-immersive', (event) => {
    if (event.detail?.source === 'translate') return;
    if (active) stopTranslation();
  });
}
