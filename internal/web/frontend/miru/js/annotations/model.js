/* Miru — annotation data model: create/toggle/delete highlights and notes.
   Inline anchors live in `.article`; note cards are placed under their
   passage (translation-style), linked by data-annot-id and model state. */

import { elements } from '../dom.js';
import { state } from '../state.js';
import { configureTechnicalMarkdown } from '../markdown/cjk-emphasis.js';
import { findNoteCard, refreshNoteNumbers, scheduleNoteLayout } from './layout.js';
import { focusNote } from './focus.js';
import { createAnnotationClientId, notifyAnnotationsChanged } from './session.js';

export function findAnnot(id) {
  return state.annotations.find((a) => String(a.id) === String(id));
}

export function removeAnnot(id) {
  state.annotations = state.annotations.filter((a) => String(a.id) !== String(id));
  notifyAnnotationsChanged();
  scheduleNoteLayout();
}

// Wrap a range in an element, tolerating ranges that cross element
// boundaries (surroundContents throws there; extracting a range that wholly
// contains existing annotations keeps those spans intact as nested anchors).
export function wrapRange(range, el) {
  try {
    range.surroundContents(el);
  } catch (err) {
    const frag = range.extractContents();
    el.appendChild(frag);
    range.insertNode(el);
  }
}

function elementContentsRange(element) {
  const range = document.createRange();
  range.selectNodeContents(element);
  return range;
}

function rangeContains(outer, inner) {
  return outer.compareBoundaryPoints(Range.START_TO_START, inner) <= 0 &&
    outer.compareBoundaryPoints(Range.END_TO_END, inner) >= 0;
}

// Nested annotations are intentional: a paragraph translation may contain a
// sentence highlight, note, summary, or Q&A. They are safe when one complete
// range contains the other. A crossing/partial overlap would split an existing
// span into duplicate data-annot-id fragments, so reject only that shape.
function assertNestableAnnotationRange(range) {
  const walker = document.createTreeWalker(elements.article, NodeFilter.SHOW_ELEMENT);
  while (walker.nextNode()) {
    const element = walker.currentNode;
    if (!element.classList?.contains('annot') || !element.dataset.annotId) continue;
    let intersects = false;
    try { intersects = range.intersectsNode(element); } catch { intersects = false; }
    if (!intersects) continue;
    const existing = elementContentsRange(element);
    if (rangeContains(existing, range) || rangeContains(range, existing)) continue;
    throw new Error('Selection partially overlaps an existing annotation');
  }
}

export function unwrapAnnotEl(annotEl) {
  const parent = annotEl.parentNode;
  if (!parent) return;
  const sup = annotEl.querySelector(':scope > .annot-note-num');
  if (sup) sup.remove();
  while (annotEl.firstChild) parent.insertBefore(annotEl.firstChild, annotEl);
  parent.removeChild(annotEl);
  parent.normalize();
}

function attachNoteBadge(span, id) {
  if (span.querySelector(':scope > .annot-note-num')) return;
  const num = document.createElement('sup');
  num.className = 'annot-note-num';
  num.textContent = id;
  span.appendChild(num);
}

function buildNoteLabel(id) {
  const label = document.createElement('span');
  label.className = 'annot-note-label';
  label.textContent = id;
  return label;
}

const ANCHOR_KIND_CLASSES = ['qa', 'summary', 'translation', 'jp-study']
  .map((kind) => `annot-kind-${kind}`);

function setAnchorKindClass(anchor, kind) {
  if (!anchor) return;
  anchor.classList.remove(...ANCHOR_KIND_CLASSES);
  if (kind) anchor.classList.add(`annot-kind-${kind}`);
}

// Notes use the same safe Markdown path as the main reader. Keeping a small
// fallback makes the annotation model resilient if a host loads it before the
// vendor renderer is ready.
const NOTE_URL_RE = /(?:https?:\/\/|www\.)[A-Za-z0-9._~:\/?#@!$&()*+,;=%-]+/gi;
let noteMarkdown = null;

// Generated cards keep their restore excerpt in the durable Markdown file but
// show only the generated content inline.
function generatedCardMarkdown(note, markerName) {
  const text = String(note || '');
  const escaped = markerName.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  const marker = text.match(new RegExp(`\\*\\*${escaped}：?\\*\\*\\s*\\n+([\\s\\S]*)`));
  if (marker && marker[1].trim()) return marker[1].trim();

  // No marker: drop leading blockquotes and keep remaining prose.
  const lines = text.split('\n');
  const out = [];
  let inQuote = false;
  for (const line of lines) {
    const trim = line.trim();
    if (trim.startsWith('>')) {
      inQuote = true;
      continue;
    }
    if (inQuote) {
      if (trim === '') continue;
      inQuote = false;
    }
    out.push(line);
  }
  return out.join('\n').trim() || text.trim();
}

function renderNoteText(el, note, opts = {}) {
  el.textContent = '';
  const source = opts.kind === 'summary'
    ? generatedCardMarkdown(note, '总结')
    : opts.kind === 'translation'
      ? generatedCardMarkdown(note, '翻译')
      : note;

  if (typeof window.markdownit === 'function' && typeof window.DOMPurify === 'function') {
    if (!noteMarkdown) {
      noteMarkdown = configureTechnicalMarkdown(
        window.markdownit({ html: false, linkify: true, typographer: true, breaks: true }),
      );
    }
    const clean = window.DOMPurify.sanitize(noteMarkdown.render(source), {
      ADD_ATTR: ['target', 'rel'],
    });
    const fragment = document.createElement('div');
    fragment.innerHTML = clean;
    prepareNoteLinks(fragment);
    highlightNoteCode(fragment);
    while (fragment.firstChild) el.append(fragment.firstChild);
    return;
  }

  renderPlainNoteText(el, source);
}

function prepareNoteLinks(root) {
  root.querySelectorAll('a').forEach((link) => {
    const href = link.getAttribute('href') || '';
    if (/^www\./i.test(href)) link.href = 'https://' + href;
    link.target = '_blank';
    link.rel = 'noopener noreferrer';
    link.classList.add('annot-note-link');
    link.addEventListener('click', (e) => e.stopPropagation());
  });
}

function highlightNoteCode(root) {
  if (!window.hljs || typeof window.hljs.highlightElement !== 'function') return;
  root.querySelectorAll('pre code').forEach((block) => {
    try {
      window.hljs.highlightElement(block);
    } catch (err) {
      console.warn('Could not highlight note code:', err);
    }
  });
}

// Safe fallback for hosts where markdown-it/DOMPurify is not ready yet.
function renderPlainNoteText(el, note) {
  let lastIndex = 0;
  for (const match of note.matchAll(NOTE_URL_RE)) {
    let url = match[0];
    const trailingMatch = url.match(/[.,;:!?)\]}>]+$/);
    const trailing = trailingMatch ? trailingMatch[0] : '';
    if (trailing) url = url.slice(0, url.length - trailing.length);
    if (match.index > lastIndex) el.append(note.slice(lastIndex, match.index));
    const link = document.createElement('a');
    link.href = /^https?:\/\//i.test(url) ? url : 'https://' + url;
    link.textContent = url;
    link.target = '_blank';
    link.rel = 'noopener noreferrer';
    link.className = 'annot-note-link';
    link.addEventListener('click', (e) => e.stopPropagation());
    el.append(link);
    lastIndex = match.index + url.length;
  }
  if (lastIndex < note.length) el.append(note.slice(lastIndex));
}

function buildNoteBtn(kind) {
  const b = document.createElement('button');
  b.type = 'button';
  b.className = 'annot-note-' + kind;
  b.dataset.noteAction = kind;
  b.title = kind === 'edit' ? 'Edit note' : 'Delete note';
  b.setAttribute('aria-label', b.title);
  b.textContent = kind === 'edit' ? '\u270e' : '\u2715';
  return b;
}

function detectNoteKind(noteText) {
  const text = String(noteText || '');
  if (/\*\*Q:\*\*|\*\*Q\*\*:/.test(text) || /^---[\s\S]*?\nkind:\s*["']?qa["']?/m.test(text)) {
    return 'qa';
  }
  // Local-model summaries carry an explicit **总结：** marker (or kind front
  // matter); keeping the kind lets .is-summary hide the restore quote.
  if (/\*\*总结：?\*\*|^---[\s\S]*?\nkind:\s*["']?summary["']?/m.test(text)) {
    return 'summary';
  }
  if (/\*\*翻译：?\*\*|^---[\s\S]*?\nkind:\s*["']?translation["']?/m.test(text)) {
    return 'translation';
  }
  // Japanese study (语) notes: furigana / chunking / grammar / zh translation.
  if (/\*\*语：?\*\*|^---[\s\S]*?\nkind:\s*["']?jp-study["']?/m.test(text)) {
    return 'jp-study';
  }
  return '';
}

function insertNoteCard(id, noteText, kind) {
  const card = document.createElement('aside');
  const kindClass = kind === 'qa' ? ' is-qa'
    : kind === 'summary' ? ' is-summary'
    : kind === 'translation' ? ' is-translation'
    : kind === 'jp-study' ? ' is-jp-study'
    : '';
  card.className = 'annot-note' + kindClass;
  card.dataset.annotId = id;
  if (kind) card.dataset.noteKind = kind;
  const body = document.createElement('div');
  body.className = 'annot-note-body';
  const text = document.createElement('div');
  text.className = 'annot-note-text';
  renderNoteText(text, noteText, { kind });
  body.append(buildNoteLabel(id), text);
  card.appendChild(body);
  card.appendChild(buildNoteBtn('edit'));
  card.appendChild(buildNoteBtn('del'));
  elements.annotationLayer.appendChild(card);
  return card;
}

// `flags` carries highlight + note (underline/strikethrough removed from the
// product surface; legacy fields stay false on write).
export function applyAnnotationRange(range, flags) {
  // Async generators keep a live Range while they stream. Browser layout or
  // selection changes can invalidate/collapse it; never create an empty model
  // entry that later makes the entire sidecar impossible to sync.
  if (!range || range.collapsed || !range.commonAncestorContainer?.isConnected || !range.toString().trim()) {
    throw new Error('Selection lost — select the passage again');
  }
  assertNestableAnnotationRange(range);
  const id = ++state.noteCounter;
  const span = document.createElement('span');
  const kind = flags.note ? (flags.kind || detectNoteKind(flags.note) || '') : '';
  const classes = ['annot'];
  if (flags.hl) classes.push('annot-hl');
  if (flags.note) classes.push('annot-note-ref');
  if (kind) classes.push(`annot-kind-${kind}`);
  span.className = classes.join(' ');
  span.dataset.annotId = id;
  wrapRange(range, span);
  if (flags.note) {
    attachNoteBadge(span, id);
    insertNoteCard(id, flags.note, kind);
  }
  state.annotations.push({
    id,
    clientId: flags.clientId || createAnnotationClientId(),
    hl: !!flags.hl,
    ul: false,
    sl: false,
    note: flags.note || null,
    // Anchored-artifact subtype persisted on annotation_notes.kind.
    kind: kind || null,
    ref: flags.ref || null,
  });
  if (flags.notify === false) {
    // Restores are a batch. Renumbering and laying out after every entry turns
    // document open into O(annotation²); restoreAnnotationSidecar refreshes the
    // presentation once after all ranges have been applied.
  } else {
    refreshNoteNumbers();
    notifyAnnotationsChanged();
    scheduleNoteLayout();
  }
  // Pulse only a note the user just created. The selection is already visible,
  // so automatically scrolling to its rail/stack card can drag a long page all
  // the way to the bottom. Restored notes use notify:false and do not pulse.
  if (flags.note && flags.notify !== false) {
    requestAnimationFrame(() => focusNote(id, { duration: 1600 }));
  }
  return span;
}

export function applyMark(type, range) {
  if (type !== 'highlight') return;
  applyAnnotationRange(range, {
    hl: true,
    note: null,
  });
}

export function applyNote(range, noteText, opts = {}) {
  applyAnnotationRange(range, {
    hl: false,
    ul: false,
    note: noteText,
    kind: opts.kind || detectNoteKind(noteText) || null,
  });
}

// Add a note to (or update the note on) an existing annotated passage.
export function setNoteOnPassage(entry, annotEl, text, opts = {}) {
  const kind = opts.kind || detectNoteKind(text) || entry.kind || '';
  setAnchorKindClass(annotEl, kind);
  if (entry.note) {
    entry.note = text;
    entry.kind = kind || null;
    const card = findNoteCard(entry.id);
    if (card) {
      card.classList.toggle('is-qa', kind === 'qa');
      card.classList.toggle('is-summary', kind === 'summary');
      card.classList.toggle('is-translation', kind === 'translation');
      card.classList.toggle('is-jp-study', kind === 'jp-study');
      if (kind) card.dataset.noteKind = kind;
      else delete card.dataset.noteKind;
    }
    const textEl = card && card.querySelector('.annot-note-text');
    if (textEl) {
      textEl.hidden = false;
      renderNoteText(textEl, text, { kind });
    }
  } else {
    entry.note = text;
    entry.kind = kind || null;
    annotEl.classList.add('annot-note-ref');
    attachNoteBadge(annotEl, entry.id);
    insertNoteCard(entry.id, text, kind);
    // The annotated passage is already in view. Highlight the new pair without
    // moving the document to a rail/stack card that may sit near the page end.
    requestAnimationFrame(() => focusNote(entry.id, { duration: 1600 }));
  }
  refreshNoteNumbers();
  scheduleNoteLayout();
  notifyAnnotationsChanged();
}

export function deleteAnnotation(id) {
  const annotEl = elements.article.querySelector(`span.annot[data-annot-id="${id}"]`);
  const card = findNoteCard(id);
  if (card) card.remove();
  if (annotEl) unwrapAnnotEl(annotEl);
  refreshNoteNumbers();
  removeAnnot(id);
}

export function startEditNoteCard(card, entry) {
  const textEl = card.querySelector('.annot-note-text');
  if (!textEl) return;
  // Expand link-only long notes so the editor has a place to sit.
  card.classList.remove('membox-note-link-only', 'membox-note-preview');
  textEl.hidden = false;
  const pendingLink = card.querySelector('.membox-open-note');
  if (pendingLink) pendingLink.hidden = true;
  const input = document.createElement('textarea');
  input.rows = 2;
  input.title = 'Enter for a new line \u00b7 \u2318Enter to save';
  input.className = 'annot-note-input annot-note-edit-input';
  input.value = entry.note;
  textEl.replaceWith(input);
  input.focus();
  input.select();
  const autosize = () => {
    input.style.height = 'auto';
    input.style.height = Math.min(input.scrollHeight, 240) + 'px';
  };
  autosize();
  input.addEventListener('input', () => {
    autosize();
    scheduleNoteLayout();
  });
  scheduleNoteLayout();

  const restore = () => {
    const t = document.createElement('div');
    t.className = 'annot-note-text';
    renderNoteText(t, entry.note, { kind: entry.kind || detectNoteKind(entry.note) });
    input.replaceWith(t);
    scheduleNoteLayout();
  };
  const save = () => {
    const v = input.value.trim();
    if (v) {
      const changed = v !== entry.note;
      entry.note = v;
      restore();
      scheduleNoteLayout();
      if (changed) notifyAnnotationsChanged();
    } else {
      deleteAnnotation(entry.id);
    }
  };
  input.addEventListener('keydown', (e) => {
    if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) {
      e.preventDefault();
      save();
    } else if (e.key === 'Escape') restore();
  });
  input.addEventListener('blur', save);
}
