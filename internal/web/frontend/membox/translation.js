/* Immersive bilingual translation for the rendered Miru article.
   Paragraphs are sent one at a time so an arbitrarily long document never
   shares one model context. Each Pi text_delta is appended immediately below
   its source block; translated DOM is disposable and never changes Markdown. */

import { scheduleNoteLayout } from '../js/annotations/layout.js';
import { elements } from '../js/dom.js';
import { state } from '../js/state.js';
import { showToast } from '../js/ui/feedback.js';
import { streamTranslation } from './api.js';
import { onRender } from './events.js';
import { session } from './session.js';
import { getStatusExtras, setStatusExtrasPinned } from './status.js';

const TARGET_SELECTOR = 'h1, h2, h3, h4, h5, h6, p, li, dt, dd, figcaption, th, td';
const TRANSLATION_CLASS = 'membox-translation';
// Keep in sync with internal/translation/types.go MaxSegmentBytes.
const MAX_SEGMENT_BYTES = 32 * 1024;
const textEncoder = new TextEncoder();
const byteLength = (text) => textEncoder.encode(text).length;

let button = null;
let active = false;
let running = false;
let generation = 0;
let controller = null;
let completed = 0;
let failed = 0;
let total = 0;
/** @type {Array<{id: string, element: Element, text: string}> | null} */
let pendingSelectionRows = null;

function createButton() {
  const value = document.createElement('button');
  value.type = 'button';
  value.id = 'membox-translation-toggle';
  value.className = 'membox-translate';
  value.hidden = true;
  value.innerHTML = '<span class="membox-translate-glyph" aria-hidden="true">译</span><span class="membox-translate-progress" aria-hidden="true"></span>';
  // Focus/click on the dock button collapses the page selection — snapshot
  // intersecting blocks on pointerdown while the range still exists.
  value.addEventListener('pointerdown', () => {
    pendingSelectionRows = selectionRows();
  });
  value.addEventListener('click', () => {
    if (active) stopTranslation();
    else void startTranslation();
  });
  // Low-frequency tray inside the bottom-left pill (collapsed until hover).
  const extras = getStatusExtras();
  (extras || document.body).appendChild(value);
  return value;
}

function hasArticleSelection() {
  return Boolean(selectionRows());
}

function renderButton() {
  if (!button) return;
  const canTranslate = session.connected
    && document.body.classList.contains('is-reading')
    && !document.body.classList.contains('is-snippet');
  button.hidden = !canTranslate;
  button.dataset.active = String(active);
  button.dataset.running = String(running);
  button.setAttribute('aria-pressed', String(active));
  const selectionMode = !active && hasArticleSelection();
  button.dataset.selection = String(selectionMode);
  if (active) {
    button.setAttribute('aria-label', 'Stop immersive translation');
    button.title = `Stop immersive translation${total ? ` · ${completed}/${total}` : ''}`;
  } else if (selectionMode) {
    button.setAttribute('aria-label', 'Translate selected passage');
    button.title = 'Translate selection · qwen3:14b';
  } else {
    button.setAttribute('aria-label', 'Start immersive translation');
    button.title = 'Immersive translation (select text to translate one passage) · qwen3:14b';
  }
  const progress = button.querySelector('.membox-translate-progress');
  progress.textContent = active && total ? `${completed}/${total}` : '';
  setStatusExtrasPinned('translate', active);
}

function sourceText(element) {
  const clone = element.cloneNode(true);
  clone.querySelectorAll([
    'button', '.annot-note-ref', `.${TRANSLATION_CLASS}`, '.membox-jp-study',
    '.katex-html', '.code-copy', '.section-copy', '.section-download', '.lead-copy',
  ].join(',')).forEach((node) => node.remove());
  return (clone.textContent || '').replace(/\s+/g, ' ').trim();
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

/** Blocks intersecting the current article selection, or null if none. */
function selectionRows() {
  const selection = window.getSelection();
  if (!selection || selection.isCollapsed || !selection.rangeCount || !elements.article) return null;
  let range;
  try {
    range = selection.getRangeAt(0);
  } catch {
    return null;
  }
  if (!elements.article.contains(range.commonAncestorContainer)) return null;
  if (!String(selection.toString() || '').replace(/\s+/g, ' ').trim()) return null;

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
  let done = false;
  try {
    await streamTranslation({
      id: row.id,
      title: state.docTitle || document.title || '',
      target_language: 'Simplified Chinese (zh-CN)',
      text: row.text,
    }, (event) => {
      if (!active || generation !== currentGeneration || event.id !== row.id) return;
      if (event.type === 'delta') node.text.append(document.createTextNode(event.text || ''));
      if (event.type === 'done') done = true;
    }, { signal: controller.signal });
    if (!done) throw new Error('Translation stream ended before completion');
    const translated = node.text.textContent.trim();
    node.spinner.remove();
    if (!translated || translated === row.text) {
      node.wrapper.remove();
    } else {
      node.wrapper.dataset.sourceFp = sourceFingerprint(row.text);
    }
    scheduleNoteLayout();
  } catch (error) {
    node.wrapper.remove();
    throw error;
  }
}

async function startTranslation() {
  // Selection wins: translate only the passage blocks under the caret range.
  // Empty selection keeps the existing full-document immersive behavior.
  // Prefer the pointerdown snapshot — click focus often clears the live range.
  const selectedRows = pendingSelectionRows || selectionRows();
  pendingSelectionRows = null;
  const selectionOnly = Boolean(selectedRows?.length);
  const candidates = selectionOnly ? selectedRows : collectParagraphs();
  if (!candidates.length) {
    showToast(selectionOnly ? 'Selection has nothing to translate' : 'No paragraphs need translation');
    return;
  }

  // Idempotency: reuse finished translations whose source fingerprint still
  // matches. Full-doc must NOT wipe earlier selection translations.
  // Selection re-click forces a refresh of only the chosen blocks.
  let skipped = 0;
  let rows = candidates;
  if (!selectionOnly) {
    rows = [];
    for (const row of candidates) {
      if (hasUsableTranslation(row.element, row.text)) skipped++;
      else rows.push(row);
    }
  }
  if (!rows.length) {
    showToast(skipped
      ? `Already translated (${skipped} kept)`
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
  renderButton();

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
      renderButton();
    }
    running = false;
    // Selection jobs are one-shot; full-doc stays toggleable until stop.
    if (selectionOnly) active = false;
    renderButton();
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
    renderButton();
    showToast(error instanceof Error ? error.message : 'Translation failed');
  }
}

function stopTranslation({ render = true } = {}) {
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
  if (render) renderButton();
}

export function initTranslation() {
  button = createButton();
  onRender(() => {
    if (!session.connected && active) stopTranslation();
    else renderButton();
  });
  // Refresh tooltip so 「译」 advertises selection mode while text is highlighted.
  document.addEventListener('selectionchange', () => {
    if (!active) renderButton();
  });
  window.addEventListener('miru-document-change', () => {
    stopTranslation();
  });
  window.addEventListener('membox-stop-immersive', (event) => {
    if (event.detail?.source === 'translate') return;
    if (active) stopTranslation();
  });
  renderButton();
}
