/* Japanese study (语): kagome readings replace the source inline; grammar +
   Chinese translation stream underneath. Selection and immersive modes share
   the same local inline presentation. */

import { scheduleNoteLayout } from '../../js/annotations/layout.js';
import { applyNote, findAnnot, setNoteOnPassage } from '../../js/annotations/model.js';
import { registerAnnotAction } from '../../js/annotations/toolbar.js';
import { elements } from '../../js/dom.js';
import { state } from '../../js/state.js';
import { showToast } from '../../js/ui/feedback.js';
import { streamTranslation } from './api.js';
import { onRender } from './events.js';
import { noteDocumentURL } from './notes.js';
import { session } from './session.js';
import { getStatusExtras, setStatusExtrasPinned } from './status.js';

const TARGET_SELECTOR = 'h1, h2, h3, h4, h5, h6, p, li, dt, dd, figcaption, th, td';
const STUDY_CLASS = 'membox-jp-study';
const READING_CLASS = 'membox-jp-reading';
const MAX_SEGMENT_BYTES = 32 * 1024;
const textEncoder = new TextEncoder();
const byteLength = (text) => textEncoder.encode(text).length;
const KANA_RE = /[\u3040-\u30ff]/u;

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
  value.id = 'membox-jp-study-toggle';
  value.className = 'membox-translate membox-jp-study-btn';
  value.hidden = true;
  value.innerHTML = '<span class="membox-translate-glyph" aria-hidden="true">语</span><span class="membox-translate-progress" aria-hidden="true"></span>';
  value.addEventListener('click', () => {
    if (active) stopJpStudy();
    else void startJpStudy();
  });
  const extras = getStatusExtras();
  (extras || document.body).appendChild(value);
  return value;
}

function renderButton() {
  if (!button) return;
  const canStudy = session.connected
    && document.body.classList.contains('is-reading')
    && !document.body.classList.contains('is-snippet')
    && documentLooksJapanese();
  button.hidden = !canStudy;
  button.dataset.active = String(active);
  button.dataset.running = String(running);
  button.setAttribute('aria-pressed', String(active));
  button.setAttribute('aria-label', active ? 'Stop Japanese study' : 'Start Japanese study');
  button.title = active
    ? `停止日语精读${total ? ` · ${completed}/${total}` : ''}`
    : '日语精读 · 读音顶替原文 / 语法·翻译在下 · qwen3:14b';
  const progress = button.querySelector('.membox-translate-progress');
  progress.textContent = active && total ? `${completed}/${total}` : '';
  setStatusExtrasPinned('jp-study', active);
}

function sourceText(element) {
  const clone = element.cloneNode(true);
  clone.querySelectorAll([
    'button', '.annot-note-ref', `.${STUDY_CLASS}`, `.${READING_CLASS}`,
    '.membox-translation', '.katex-html', '.code-copy', '.section-copy',
    '.section-download', '.lead-copy',
  ].join(',')).forEach((node) => node.remove());
  // If already replaced with reading, prefer stored original.
  if (element.dataset?.jpStudyOriginalText) return element.dataset.jpStudyOriginalText;
  return (clone.textContent || '').replace(/\s+/g, ' ').trim();
}

function shouldStudy(text) {
  if (text.length < 2 || !KANA_RE.test(text)) return false;
  if (byteLength(text) > MAX_SEGMENT_BYTES) return false;
  return true;
}

function documentLooksJapanese() {
  if (!elements.article) return false;
  const candidates = elements.article.querySelectorAll(TARGET_SELECTOR);
  let checked = 0;
  for (const element of candidates) {
    if (element.closest(`.${STUDY_CLASS}, .membox-translation, pre, code, .mermaid-diagram, .doc-title`)) continue;
    const text = sourceText(element);
    if (!text) continue;
    if (KANA_RE.test(text)) return true;
    if (++checked >= 24) break;
  }
  return false;
}

function collectParagraphs() {
  const candidates = [...elements.article.querySelectorAll(TARGET_SELECTOR)];
  const rows = [];
  for (const element of candidates) {
    if (element.closest(`.${STUDY_CLASS}, .membox-translation, pre, code, .mermaid-diagram, .doc-title`)) continue;
    if (element.matches('li') && element.querySelector(':scope > p, :scope > ul, :scope > ol')) continue;
    // Skip blocks already replaced by a prior selection study unless immersive restarts.
    const text = sourceText(element);
    if (!shouldStudy(text)) continue;
    rows.push({ id: `jp-study-${rows.length + 1}`, element, text });
  }
  const viewportTop = window.scrollY + 8;
  const start = rows.findIndex(({ element }) => {
    const rect = element.getBoundingClientRect();
    return rect.bottom + window.scrollY >= viewportTop;
  });
  return start > 0 ? rows.slice(start).concat(rows.slice(0, start)) : rows;
}

function escapeHTML(value) {
  return String(value)
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;');
}

function escapeRegExp(value) {
  return String(value).replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}

/* ── Section parsing ─────────────────────────────────────────────── */

function splitStudySections(text) {
  const value = String(text || '');
  const parts = value.split(/(?=【[^】]+】)/);
  const sections = {};
  const order = [];
  for (const part of parts) {
    const m = part.match(/^【([^】]+)】\s*([\s\S]*)$/);
    if (!m) continue;
    sections[m[1]] = m[2].replace(/^\n/, '').replace(/\n$/, '');
    order.push(m[1]);
  }
  return { sections, order, raw: value };
}

function extractReading(raw) {
  const { sections } = splitStudySections(raw);
  if (sections['读音'] != null) return sections['读音'].trim();
  // Partial stream: 【读音】\n... before next section arrives.
  const m = String(raw || '').match(/【读音】\s*([\s\S]*?)(?=\n【|$)/);
  return m ? m[1].trim() : '';
}

function extractBelow(raw) {
  // Grammar + translation only (reading is inlined into the article).
  const { sections, order } = splitStudySections(raw);
  return order
    .filter((name) => name !== '读音')
    .map((name) => `【${name}】\n${sections[name]}`)
    .join('\n\n')
    .trim();
}

function readingIsComplete(raw) {
  return /【语法】|【翻译】/.test(raw) || /\n\n$/.test(raw);
}

/* ── Grammar mark injection into reading line ────────────────────── */

function injectGrammarMarksIntoReading(reading, grammar) {
  if (!reading || !grammar) return reading;
  let out = reading;
  const forms = [];
  const re = /(?:^|\n)\s*(\d+)\.\s*「([^」]+)」/g;
  let m;
  while ((m = re.exec(grammar)) !== null) forms.push({ n: m[1], form: m[2] });
  if (!forms.length) return out;
  forms.sort((a, b) => b.form.length - a.form.length);
  const used = new Set();
  for (const { n, form } of forms) {
    if (!form || used.has(n)) continue;
    const parts = [...form].map((ch) => escapeRegExp(ch) + '(?:（[^）]*）)?');
    const loose = new RegExp(parts.join(''));
    const hit = loose.exec(out);
    if (!hit) {
      const idx = out.indexOf(form);
      if (idx < 0) continue;
      out = `${out.slice(0, idx)}{{${n}|${form}}}${out.slice(idx + form.length)}`;
      used.add(n);
      continue;
    }
    const surface = hit[0];
    out = `${out.slice(0, hit.index)}{{${n}|${surface}}}${out.slice(hit.index + surface.length)}`;
    used.add(n);
  }
  return out;
}

/* ── Reading → ruby HTML ─────────────────────────────────────────── */

// 僕（ぼく）が　生まれ（うまれ）{{1|た}} → <ruby>…</ruby> + marks + gaps
function readingToHTML(reading) {
  let s = escapeHTML(String(reading || ''));
  // Grammar marks first (already escaped surfaces).
  s = s.replace(/\{\{(\d+)\|([^}]+)\}\}/g, (_, n, surface) => (
    `<mark class="membox-jp-g" data-g="${n}"><span class="membox-jp-g-n">${n}</span>${surface}</mark>`
  ));
  // Kanji(+kana base)（hira） → ruby. Base may include nested marks? rare; keep simple.
  // Match runs ending with ） that look like 漢字（ひらがな）
  s = s.replace(
    /([\u3400-\u9fff々〆ヵヶ]+(?:[\u3040-\u309f]*)?)（([ぁ-んー]+)）/g,
    '<ruby>$1<rt>$2</rt></ruby>',
  );
  // Fullwidth spaces → visible phrase gaps
  s = s.replace(/　/g, '<span class="membox-jp-gap" aria-hidden="true"> </span>');
  return s;
}

function belowToHTML(below) {
  let html = escapeHTML(below || '');
  html = html.replace(/(^|\n)(\d+)\.(\s*)/g, (_, br, n, sp) => (
    `${br}<span class="membox-jp-g-item" data-g="${n}"><span class="membox-jp-g-n">${n}</span>.</span>${sp}`
  ));
  html = html.replace(/【([^】]+)】/g, '<strong class="membox-jp-study-label">【$1】</strong>');
  return html;
}

function bindGrammarHover(root) {
  if (!root) return;
  // Include sibling reading host if present.
  const scope = root.parentElement || root;
  const setActive = (n, on) => {
    scope.querySelectorAll(`[data-g="${n}"]`).forEach((el) => {
      el.classList.toggle('is-active', on);
    });
  };
  scope.querySelectorAll('[data-g]').forEach((el) => {
    const n = el.getAttribute('data-g');
    if (!n) return;
    el.addEventListener('mouseenter', () => setActive(n, true));
    el.addEventListener('mouseleave', () => setActive(n, false));
  });
}

/* ── DOM: replace source with reading; panel below for 语法/翻译 ─── */

function buildStudyWrapper(id, tag = 'div') {
  const wrapper = document.createElement(tag);
  wrapper.className = STUDY_CLASS;
  wrapper.dataset.jpStudyId = id;
  wrapper.lang = 'zh-CN';
  wrapper.setAttribute('translate', 'no');
  const text = document.createElement(tag === 'span' ? 'span' : 'div');
  text.className = 'membox-jp-study-text';
  if (tag === 'span') text.style.display = 'block';
  wrapper.appendChild(text);
  const spinner = document.createElement('span');
  spinner.className = 'membox-translation-spinner';
  spinner.setAttribute('aria-hidden', 'true');
  wrapper.appendChild(spinner);
  const actions = document.createElement(tag === 'span' ? 'span' : 'div');
  actions.className = 'membox-jp-study-actions';
  if (tag === 'span') actions.style.display = 'none';
  actions.hidden = true;
  wrapper.appendChild(actions);
  return wrapper;
}

function panelParts(wrapper) {
  return {
    wrapper,
    text: wrapper.querySelector('.membox-jp-study-text'),
    spinner: wrapper.querySelector('.membox-translation-spinner'),
    actions: wrapper.querySelector('.membox-jp-study-actions'),
  };
}

// Immersive: panel after the whole block (paragraph / li).
function ensureBelowPanel(anchor, id) {
  let wrapper = null;
  if (anchor.matches('li, td, th, dd, dt')) {
    wrapper = anchor.querySelector(`:scope > .${STUDY_CLASS}[data-jp-study-id="${id}"]`);
  } else if (anchor.nextElementSibling?.classList?.contains(STUDY_CLASS)
    && anchor.nextElementSibling.dataset.jpStudyId === id) {
    wrapper = anchor.nextElementSibling;
  }
  if (!wrapper) {
    wrapper = buildStudyWrapper(id);
    if (anchor.matches('li, td, th, dd, dt')) anchor.appendChild(wrapper);
    else anchor.insertAdjacentElement('afterend', wrapper);
  }
  return panelParts(wrapper);
}

// Selection: panel sits on the next line right after the replaced reading host
// (or after an end-marker placed at the selection tail).
function ensurePanelAfter(refNode, id) {
  if (!refNode?.isConnected) return null;
  let wrapper = null;
  const next = refNode.nextElementSibling;
  if (next?.classList?.contains(STUDY_CLASS) && next.dataset.jpStudyId === id) {
    wrapper = next;
  } else if (refNode.parentElement) {
    // Prefer moving an existing panel for this id next to the reading host.
    wrapper = refNode.parentElement.querySelector(`.${STUDY_CLASS}[data-jp-study-id="${id}"]`);
  }
  if (!wrapper) {
    // <span display:block> stays valid phrasing content inside <p>; a <div>
    // would split the paragraph and leak styles across later lines.
    wrapper = buildStudyWrapper(id, 'span');
  }
  wrapper.classList.add('membox-jp-study-inline-follow');
  if (refNode.nextSibling !== wrapper) {
    refNode.insertAdjacentElement('afterend', wrapper);
  }
  return panelParts(wrapper);
}

// Place a zero-width marker at the end of the live selection so we always have
// an insertion point on that line, even before the reading host exists.
function placeSelectionEndMarker(range) {
  if (!range) return null;
  try {
    const marker = document.createElement('span');
    marker.className = 'membox-jp-sel-end';
    marker.setAttribute('aria-hidden', 'true');
    const end = range.cloneRange();
    end.collapse(false);
    end.insertNode(marker);
    // Avoid leaving the marker selected.
    range.setEndBefore(marker);
    return marker;
  } catch {
    return null;
  }
}

function replaceElementWithReading(element, readingHTML, originalText) {
  if (!element?.isConnected) return null;
  if (!element.dataset.jpStudyOriginalHTML) {
    element.dataset.jpStudyOriginalHTML = element.innerHTML;
  }
  if (!element.dataset.jpStudyOriginalText) {
    element.dataset.jpStudyOriginalText = originalText;
  }
  // Keep non-content chrome (section buttons) if any, replace body text.
  const keep = [...element.querySelectorAll(
    '.section-copy, .section-download, .lead-copy, .annot-note-num, button',
  )];
  element.innerHTML = '';
  const host = document.createElement('span');
  host.className = READING_CLASS;
  host.innerHTML = readingHTML;
  element.appendChild(host);
  keep.forEach((node) => element.appendChild(node));
  element.classList.add('membox-jp-source-replaced');
  return host;
}

function replaceRangeWithReading(range, readingHTML, originalText) {
  const host = document.createElement('span');
  host.className = READING_CLASS;
  host.dataset.jpStudyOriginalText = originalText;
  host.innerHTML = readingHTML;
  range.deleteContents();
  range.insertNode(host);
  // Collapse after inserted node
  range.setStartAfter(host);
  range.collapse(true);
  return host;
}

function restoreAllReadings() {
  elements.article.querySelectorAll('.membox-jp-source-replaced').forEach((el) => {
    if (el.dataset.jpStudyOriginalHTML != null) {
      el.innerHTML = el.dataset.jpStudyOriginalHTML;
      delete el.dataset.jpStudyOriginalHTML;
      delete el.dataset.jpStudyOriginalText;
      el.classList.remove('membox-jp-source-replaced');
    }
  });
  // Selection hosts that replaced only a range (parent not fully replaced).
  elements.article.querySelectorAll(`.${READING_CLASS}[data-jp-study-original-text]`).forEach((host) => {
    if (host.closest('.membox-jp-source-replaced')) return;
    const original = host.dataset.jpStudyOriginalText || host.textContent || '';
    host.replaceWith(document.createTextNode(original));
  });
}

function restoreOne(anchor, readingHost) {
  if (anchor?.classList?.contains('membox-jp-source-replaced') && anchor.dataset.jpStudyOriginalHTML != null) {
    anchor.innerHTML = anchor.dataset.jpStudyOriginalHTML;
    delete anchor.dataset.jpStudyOriginalHTML;
    delete anchor.dataset.jpStudyOriginalText;
    anchor.classList.remove('membox-jp-source-replaced');
    return;
  }
  if (readingHost?.isConnected && readingHost.dataset.jpStudyOriginalText != null) {
    readingHost.replaceWith(document.createTextNode(readingHost.dataset.jpStudyOriginalText));
  }
}

/* ── Persist study as note markdown; show UUID chip (no floating card) ─ */

function studyNoteBody(source, study) {
  const q = String(source || '').trim();
  const a = String(study || '').trim();
  if (!a) return '';
  return `> ${q}

**语：**

${a}
`;
}

function shortNoteID(ref) {
  const value = String(ref || '').trim();
  if (!value) return '';
  // Prefer last 8 of UUID (matches membox short-id habit).
  const bare = value.replace(/-/g, '');
  return bare.length >= 8 ? bare.slice(-8) : value.slice(0, 8);
}

function pinStudyNote(readingHost, source, study) {
  const noteText = studyNoteBody(source, study);
  if (!noteText || !readingHost?.isConnected) return null;

  const existing = readingHost.closest?.('span.annot');
  if (existing?.dataset?.annotId) {
    const entry = findAnnot(existing.dataset.annotId);
    if (entry) {
      setNoteOnPassage(entry, existing, noteText, { kind: 'jp-study' });
      decorateJpStudyAnchor(existing, entry);
      return entry;
    }
  }

  const range = document.createRange();
  try {
    range.selectNode(readingHost);
  } catch {
    return null;
  }
  applyNote(range, noteText, { kind: 'jp-study' });
  // applyNote wraps the host; find the new annot and decorate.
  const annot = readingHost.closest?.('span.annot') || readingHost.parentElement?.closest?.('span.annot');
  if (annot?.dataset?.annotId) {
    const entry = findAnnot(annot.dataset.annotId);
    if (entry) decorateJpStudyAnchor(annot, entry);
    return entry || null;
  }
  return null;
}

// Compact UUID chip beside the passage — opens the note Markdown document.
// Floating rail cards for jp-study are hidden via CSS.
function decorateJpStudyAnchor(annotEl, entry) {
  if (!annotEl) return;
  annotEl.classList.add('membox-jp-annot');
  // Remove default numeric badge; replace with durable/short id chip.
  annotEl.querySelectorAll('.annot-note-num:not(.membox-jp-note-id)').forEach((n) => n.remove());

  let chip = annotEl.querySelector('a.membox-jp-note-id');
  if (!chip) {
    chip = document.createElement('a');
    chip.className = 'membox-jp-note-id';
    chip.target = '_blank';
    chip.rel = 'noopener noreferrer';
    chip.addEventListener('click', (event) => event.stopPropagation());
    annotEl.appendChild(chip);
  }

  const ref = entry?.ref || '';
  if (ref) {
    chip.href = noteDocumentURL(ref);
    chip.textContent = shortNoteID(ref);
    chip.title = `打开语笔记 ${ref}`;
    chip.setAttribute('aria-label', `打开语笔记 ${ref}`);
    chip.classList.remove('is-pending');
  } else {
    chip.removeAttribute('href');
    chip.textContent = '…';
    chip.title = '笔记保存中…';
    chip.classList.add('is-pending');
  }

  // Hide the floating card immediately if the model already inserted one.
  hideJpStudyCards();
}

function hideJpStudyCards() {
  const byID = new Map(state.annotations.map((e) => [String(e.id), e]));
  elements.annotationLayer?.querySelectorAll('.annot-note[data-annot-id]').forEach((card) => {
    const entry = byID.get(String(card.dataset.annotId));
    if (entry?.kind === 'jp-study' || /\*\*语：?\*\*/.test(entry?.note || '')) {
      card.classList.add('is-jp-study', 'annot-note-host-hidden');
      card.hidden = true;
    }
  });
}

function refreshJpStudyChips() {
  hideJpStudyCards();
  for (const entry of state.annotations) {
    if (entry.kind !== 'jp-study' && !/\*\*语：?\*\*/.test(entry.note || '')) continue;
    const annot = elements.article?.querySelector(`span.annot[data-annot-id="${entry.id}"]`);
    if (annot) decorateJpStudyAnchor(annot, entry);
  }
}

function attachActions(node, row, finished, readingHost) {
  if (!node.actions) return;
  node.actions.hidden = false;
  if (node.actions.style.display === 'none' || node.wrapper?.tagName === 'SPAN') {
    node.actions.style.display = 'flex';
  }
  const dismissBtn = document.createElement('button');
  dismissBtn.type = 'button';
  dismissBtn.className = 'membox-jp-study-dismiss';
  dismissBtn.textContent = '还原原文';
  dismissBtn.title = '还原 inline 读音；笔记 UUID 链接仍保留在原文旁';
  dismissBtn.addEventListener('click', () => {
    // Keep the note anchor/UUID; only strip ruby markup back to plain source.
    restoreReadingKeepAnchor(row.element, readingHost);
    node.wrapper.remove();
    scheduleNoteLayout();
    showToast('已还原读音。原文旁 UUID 可打开语笔记');
  });
  node.actions.replaceChildren(dismissBtn);
}

// Restore plain text inside the reading host / annot without deleting the note.
function restoreReadingKeepAnchor(anchor, readingHost) {
  const host = readingHost?.isConnected
    ? readingHost
    : anchor?.querySelector?.(`.${READING_CLASS}`);
  const original = host?.dataset?.jpStudyOriginalText
    || anchor?.dataset?.jpStudyOriginalText
    || '';
  if (host?.isConnected && original) {
    host.textContent = original;
    host.classList.add(READING_CLASS);
    // Keep dataset so a later 语 can still know the source.
    return;
  }
  restoreOne(anchor, readingHost);
}

/* ── Stream + apply ──────────────────────────────────────────────── */

async function streamStudy(row, { signal, onDelta, alive } = {}) {
  let done = false;
  let raw = '';
  await streamTranslation({
    id: row.id,
    title: state.docTitle || document.title || '',
    target_language: 'Simplified Chinese (zh-CN)',
    mode: 'jp-study',
    text: row.text,
  }, (event) => {
    if (alive && !alive()) return;
    if (event.id !== row.id) return;
    if (event.type === 'delta') {
      const chunk = event.text || '';
      raw += chunk;
      if (onDelta) onDelta(chunk, raw);
    }
    if (event.type === 'done') done = true;
  }, { signal });
  if (!done) throw new Error('Japanese study stream ended before completion');
  return raw.trim();
}

function placeSelectionPanel(ctx, id) {
  // Prefer: right after the reading host (same line flow → panel breaks to next line).
  // Fallback: end marker at selection tail → whole anchor block.
  const ref = ctx.readingHost || ctx.endMarker || rowElement(ctx);
  if (!ref) return ctx.node;
  const node = ensurePanelAfter(ref, id);
  if (node) ctx.node = node;
  return ctx.node;
}

function rowElement(ctx) {
  return ctx.rowElement || null;
}

function applyStreamingUI(row, raw, ctx) {
  // ctx: { readingApplied, readingHost, node, mode, range?, endMarker?, rowElement }
  ctx.rowElement = row.element;
  const reading = extractReading(raw);
  if (reading && !ctx.readingApplied && reading.length > 0) {
    const html = readingToHTML(reading);
    if (ctx.mode === 'range' && ctx.range) {
      try {
        ctx.readingHost = replaceRangeWithReading(ctx.range, html, row.text);
        ctx.readingApplied = true;
      } catch {
        ctx.readingHost = replaceElementWithReading(row.element, html, row.text);
        ctx.readingApplied = true;
      }
    } else {
      ctx.readingHost = replaceElementWithReading(row.element, html, row.text);
      ctx.readingApplied = true;
    }
    // Selection: snap the grammar panel to the line under the replaced host.
    if (ctx.mode === 'range') placeSelectionPanel(ctx, row.id);
  }

  if (ctx.mode === 'range' && !ctx.node) placeSelectionPanel(ctx, row.id);

  const below = extractBelow(raw);
  if (below && ctx.node) {
    ctx.node.text.textContent = below;
    if (ctx.node.spinner) ctx.node.spinner.hidden = false;
  }
  scheduleNoteLayout();
}

function finalizeUI(row, finished, ctx) {
  const { sections } = splitStudySections(finished);
  let reading = (sections['读音'] || extractReading(finished) || '').trim();
  const grammar = sections['语法'] || '';
  if (reading && grammar) {
    reading = injectGrammarMarksIntoReading(reading, grammar);
  }
  if (reading) {
    const html = readingToHTML(reading);
    if (ctx.readingHost?.isConnected) {
      ctx.readingHost.innerHTML = html;
    } else if (ctx.mode === 'range' && ctx.range) {
      try {
        ctx.readingHost = replaceRangeWithReading(ctx.range, html, row.text);
      } catch {
        ctx.readingHost = replaceElementWithReading(row.element, html, row.text);
      }
    } else {
      ctx.readingHost = replaceElementWithReading(row.element, html, row.text);
    }
  }
  // Selection: keep the panel glued under the reading host (not the block end).
  if (ctx.mode === 'range') placeSelectionPanel(ctx, row.id);

  const below = extractBelow(finished);
  if (ctx.node) {
    if (ctx.node.spinner) ctx.node.spinner.remove();
    if (!below) {
      // Reading-only is still useful; keep empty panel with actions.
      ctx.node.text.textContent = '';
    } else {
      ctx.node.text.innerHTML = belowToHTML(below);
    }
    bindGrammarHover(ctx.node.wrapper);
    attachActions(ctx.node, row, finished, ctx.readingHost);
  }
  // Persist Markdown note + UUID chip beside the passage (no floating card).
  if (ctx.readingHost?.isConnected) {
    pinStudyNote(ctx.readingHost, row.text, finished);
  }
  scheduleNoteLayout();
  refreshJpStudyChips();
}

async function studyParagraph(row, currentGeneration) {
  const node = ensureBelowPanel(row.element, row.id);
  const ctx = { readingApplied: false, readingHost: null, node, mode: 'element' };
  try {
    const finished = await streamStudy(row, {
      signal: controller.signal,
      alive: () => active && generation === currentGeneration,
      onDelta: (_chunk, raw) => applyStreamingUI(row, raw, ctx),
    });
    if (!active || generation !== currentGeneration) {
      node.wrapper.remove();
      restoreOne(row.element, ctx.readingHost);
      return;
    }
    finalizeUI(row, finished, ctx);
  } catch (error) {
    node.wrapper.remove();
    restoreOne(row.element, ctx.readingHost);
    throw error;
  }
}

async function startJpStudy() {
  const rows = collectParagraphs();
  if (!rows.length) {
    showToast('未找到需要精读的日语段落');
    return;
  }
  window.dispatchEvent(new CustomEvent('membox-stop-immersive', { detail: { source: 'jp-study' } }));
  stopJpStudy({ render: false });
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
        await studyParagraph(row, currentGeneration);
        completed++;
      } catch (error) {
        if (error && error.name === 'AbortError') throw error;
        failed++;
      }
      renderButton();
    }
    running = false;
    renderButton();
    showToast(failed
      ? `日语精读 ${completed}/${total} · ${failed} 失败`
      : `日语精读完成 ${completed} 段 · 原文已替换为读音`);
  } catch (error) {
    if (error && error.name === 'AbortError') return;
    running = false;
    renderButton();
    showToast(error instanceof Error ? error.message : '日语精读失败');
  }
}

function stopJpStudy({ render = true } = {}) {
  generation++;
  if (controller) controller.abort();
  controller = null;
  active = false;
  running = false;
  completed = 0;
  failed = 0;
  total = 0;
  elements.article.querySelectorAll(`.${STUDY_CLASS}`).forEach((node) => node.remove());
  restoreAllReadings();
  scheduleNoteLayout();
  if (render) renderButton();
}

/* ── Selection toolbar 语 ────────────────────────────────────────── */

const STUDY_ICON =
  '<span class="membox-jp-study-toolbar-glyph" aria-hidden="true">语</span>';
const SPIN_ICON =
  '<svg viewBox="0 0 24 24" width="16" height="16" aria-hidden="true" class="membox-summarize-spin">' +
  '<circle cx="12" cy="12" r="8" fill="none" stroke="currentColor" stroke-width="1.8" stroke-dasharray="36 20" stroke-linecap="round"/>' +
  '</svg>';

function blockForRange(range) {
  if (!range) return null;
  let node = range.commonAncestorContainer;
  if (node.nodeType === Node.TEXT_NODE) node = node.parentElement;
  if (!(node instanceof Element)) return null;
  return node.closest(TARGET_SELECTOR)
    || node.closest('p, li, td, th, dd, dt, blockquote, section, article')
    || node;
}

async function studySelection(selection, anchor, range) {
  const row = {
    id: `jp-sel-${Date.now().toString(36)}`,
    element: anchor,
    text: selection,
  };
  // Drop previous selection panels that follow readings inside this block.
  anchor.querySelectorAll(`.${STUDY_CLASS}`).forEach((n) => n.remove());
  if (anchor.nextElementSibling?.classList?.contains(STUDY_CLASS)) {
    anchor.nextElementSibling.remove();
  }
  anchor.querySelectorAll('.membox-jp-sel-end').forEach((n) => n.remove());

  let liveRange = null;
  try { liveRange = range ? range.cloneRange() : null; } catch { liveRange = null; }

  // Marker at the selection tail keeps the panel on that line (not block end).
  const endMarker = liveRange ? placeSelectionEndMarker(liveRange) : null;

  const ctx = {
    readingApplied: false,
    readingHost: null,
    node: null,
    mode: liveRange ? 'range' : 'element',
    range: liveRange,
    endMarker,
    rowElement: anchor,
  };
  // Panel starts after the end marker so it already sits under the selected line
  // while reading is still streaming in.
  if (endMarker) placeSelectionPanel(ctx, row.id);
  else ctx.node = ensureBelowPanel(anchor, row.id);

  try {
    const finished = await streamStudy(row, {
      onDelta: (_chunk, raw) => applyStreamingUI(row, raw, ctx),
    });
    // After reading host exists, keep panel immediately after it.
    if (ctx.readingHost) placeSelectionPanel(ctx, row.id);
    finalizeUI(row, finished, ctx);
  } finally {
    // Marker is only a placement anchor; drop it once layout is done.
    if (endMarker?.isConnected) endMarker.remove();
  }
}

function japaneseStudyText(value) {
  const text = String(value || '').trim();
  return text.length >= 2 && KANA_RE.test(text) && byteLength(text) <= MAX_SEGMENT_BYTES;
}

// Edit-mode click on an existing annot: study that passage's text.
function passageStudyTarget(ctx) {
  if (ctx.annotEl?.isConnected) {
    const el = ctx.annotEl;
    // Prefer inner reading host if present; else the annot passage itself.
    const host = el.querySelector?.(`.${READING_CLASS}`) || el;
    const clone = host.cloneNode(true);
    clone.querySelectorAll('.annot-note-num, button, .membox-jp-study').forEach((n) => n.remove());
    const text = (clone.textContent || '').replace(/\s+/g, ' ').trim();
    const range = document.createRange();
    try {
      range.selectNodeContents(host);
    } catch {
      return null;
    }
    const anchor = blockForRange(range) || el.closest(TARGET_SELECTOR) || el.parentElement;
    return { selection: text, anchor, range };
  }
  const selection = (ctx.text || '').trim();
  if (!selection || !ctx.range) return null;
  const anchor = blockForRange(ctx.range);
  if (!anchor) return null;
  let range = null;
  try { range = ctx.range.cloneRange(); } catch { range = null; }
  return { selection, anchor, range };
}

function registerSelectionAction() {
  registerAnnotAction({
    id: 'jp-study',
    icon: STUDY_ICON,
    title: '日语精读（读音顶替原文 · 语法/翻译在下）',
    when: ({ mode, text, entry }) => {
      // create: fresh selection; edit: click an existing annot / restored passage
      if (mode !== 'create' && mode !== 'edit') return false;
      const value = mode === 'edit' && entry?.note
        ? (text || '')
        : (text || '');
      return japaneseStudyText(value);
    },
    run: async (ctx) => {
      if (!session.connected) {
        showToast('需要连接 membox 才能精读');
        return;
      }
      const target = passageStudyTarget(ctx);
      if (!target?.selection || !target.anchor?.isConnected) {
        showToast('请先划选日语，或点击已标注的句子');
        return;
      }
      if (!japaneseStudyText(target.selection)) {
        showToast('选区太短或不是日语');
        return;
      }
      const btn = ctx.toolbar.querySelector('button[data-action="ext:jp-study"]');
      const originalIcon = btn ? btn.innerHTML : '';
      if (btn) {
        btn.disabled = true;
        btn.innerHTML = SPIN_ICON;
        btn.title = '精读中…';
      }
      showToast('日语精读中…（读音将替换选区）');
      try {
        await studySelection(target.selection, target.anchor, target.range);
        if (btn) {
          btn.disabled = false;
          btn.innerHTML = originalIcon;
          btn.title = '日语精读（读音顶替原文 · 语法/翻译在下）';
        }
        ctx.finish();
        showToast('精读完成 · 还原后可再划选调用「语」');
      } catch (err) {
        console.error('membox: jp-study selection failed', err);
        if (btn) {
          btn.disabled = false;
          btn.innerHTML = originalIcon;
          btn.title = '日语精读（读音顶替原文 · 语法/翻译在下）';
        }
        showToast(err.message || '日语精读失败');
      }
    },
  });
}

export function initJpStudy() {
  button = createButton();
  registerSelectionAction();
  onRender(() => {
    if (!session.connected && active) stopJpStudy();
    else renderButton();
    refreshJpStudyChips();
  });
  window.addEventListener('miru-document-change', () => {
    stopJpStudy();
  });
  window.addEventListener('miru-annotations-changed', () => {
    // Backend assigns durable note UUID after sync — refresh chips.
    refreshJpStudyChips();
  });
  window.addEventListener('membox-stop-immersive', (event) => {
    if (event.detail?.source === 'jp-study') return;
    if (active) stopJpStudy();
  });
  renderButton();
  refreshJpStudyChips();
}
