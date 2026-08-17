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

function createButton() {
  const value = document.createElement('button');
  value.type = 'button';
  value.id = 'membox-translation-toggle';
  value.className = 'membox-translate';
  value.hidden = true;
  value.innerHTML = '<span class="membox-translate-glyph" aria-hidden="true">译</span><span class="membox-translate-progress" aria-hidden="true"></span>';
  value.addEventListener('click', () => {
    if (active) stopTranslation();
    else void startTranslation();
  });
  // Low-frequency tray inside the bottom-left pill (collapsed until hover).
  const extras = getStatusExtras();
  (extras || document.body).appendChild(value);
  return value;
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
  button.setAttribute('aria-label', active ? 'Stop immersive translation' : 'Start immersive translation');
  button.title = active
    ? `Stop immersive translation${total ? ` · ${completed}/${total}` : ''}`
    : 'Immersive translation · qwen3:14b';
  const progress = button.querySelector('.membox-translate-progress');
  progress.textContent = active && total ? `${completed}/${total}` : '';
  setStatusExtrasPinned('translate', active);
}

function sourceText(element) {
  const clone = element.cloneNode(true);
  clone.querySelectorAll([
    'button', '.annot-note-ref', `.${TRANSLATION_CLASS}`,
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

function collectParagraphs() {
  const candidates = [...elements.article.querySelectorAll(TARGET_SELECTOR)];
  const rows = [];
  for (const element of candidates) {
    if (element.closest(`.${TRANSLATION_CLASS}, pre, code, .mermaid-diagram, .doc-title`)) continue;
    if (element.matches('li') && element.querySelector(':scope > p, :scope > ul, :scope > ol')) continue;
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
  const wrapper = document.createElement('div');
  wrapper.className = TRANSLATION_CLASS;
  wrapper.dataset.translationId = row.id;
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
    if (!translated || translated === row.text) node.wrapper.remove();
    scheduleNoteLayout();
  } catch (error) {
    node.wrapper.remove();
    throw error;
  }
}

async function startTranslation() {
  const rows = collectParagraphs();
  if (!rows.length) {
    showToast('No paragraphs need translation');
    return;
  }
  stopTranslation({ render: false });
  active = true;
  running = true;
  completed = 0;
  failed = 0;
  total = rows.length;
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
    renderButton();
    showToast(failed
      ? `Translated ${completed}/${total} paragraphs · ${failed} failed`
      : `Translated ${completed} paragraphs with qwen3:14b`);
  } catch (error) {
    if (error && error.name === 'AbortError') return;
    running = false;
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
  window.addEventListener('miru-document-change', () => {
    stopTranslation();
  });
  renderButton();
}
