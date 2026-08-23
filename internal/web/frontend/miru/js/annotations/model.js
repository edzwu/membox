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
// boundaries (surroundContents throws there; we merge instead).
//
// Nested/overlapping annotations must be flattened first: wrapping over an
// existing span.annot corrupts the inner span (extractContents splits it and
// orphans its text — the "selected text disappears" bug). absorbOverlapping
// annotations does that before we reach here; this fallback is only a safety
// net for exotic element-boundary cases.
export function wrapRange(range, el) {
  try {
    range.surroundContents(el);
  } catch (err) {
    const frag = range.extractContents();
    el.appendChild(frag);
    range.insertNode(el);
  }
}

// Collect the existing annotation spans that intersect `range`. Used before a
// wrap so a new annotation never nests inside/around old ones, which breaks
// the inner spans' text (orphaned text nodes = "selection disappeared").
function intersectingAnnotEls(range) {
  const found = [];
  const walker = document.createTreeWalker(elements.article, NodeFilter.SHOW_ELEMENT);
  while (walker.nextNode()) {
    const el = walker.currentNode;
    if (!el.classList || !el.classList.contains('annot') || !el.dataset.annotId) continue;
    if (range.intersectsNode(el)) found.push(el);
  }
  return found;
}

// Re-anchor a Range onto the current article text. Callers pass the original
// selected text; after unwrapping overlaps the DOM is flattened, so text-node
// offsets from before are stale. Prefers the occurrence closest to the old
// start offset to stay deterministic on repeated passages.
function reanchorRange(range, text, hintStart) {
  if (!text) return;
  const walker = document.createTreeWalker(elements.article, NodeFilter.SHOW_TEXT);
  const nodes = [];
  while (walker.nextNode()) nodes.push(walker.currentNode);
  let pos = 0;
  let best = -1;
  let bestDist = Infinity;
  const idxs = [];
  for (const n of nodes) {
    const t = n.textContent;
    let from = 0;
    for (;;) {
      const i = t.indexOf(text, from);
      if (i < 0) break;
      idxs.push({ node: n, offset: i, abs: pos + i });
      from = i + 1;
    }
    pos += t.length;
  }
  for (const hit of idxs) {
    const dist = Math.abs(hit.abs - hintStart);
    if (dist < bestDist) {
      bestDist = dist;
      best = hit;
    }
  }
  if (best >= 0) {
    const hit = idxs[best];
    range.setStart(hit.node, hit.offset);
    range.setEnd(hit.node, hit.offset + text.length);
  }
}

// Flatten every existing annotation span that overlaps `range`, folding their
// flags/note into `flags` so the new annotation supersedes them visually and
// in state. Without this, wrapping a range that covers an old highlight/note
// splits the inner spans and orphans their text (the "selected text
// disappears" bug). Overlap is a deliberate supersede, not data loss: the
// inner note text survives on the new annotation.
function absorbOverlappingAnnotations(range, flags) {
  const els = intersectingAnnotEls(range);
  if (!els.length) return;

  const selText = range.toString();
  const startHint = range.startOffset;
  const absorbed = [];

  els.forEach((el) => {
    const entry = findAnnot(el.dataset.annotId);
    if (entry) {
      absorbed.push(entry);
      if (entry.hl) flags.hl = true;
      if (entry.note && !flags.note) {
        flags.note = entry.note;
        flags.kind = entry.kind || flags.kind || null;
      }
    }
    const card = elements.annotationLayer.querySelector(`.annot-note[data-annot-id="${el.dataset.annotId}"]`);
    if (card) card.remove();
    unwrapAnnotEl(el);
  });

  if (absorbed.length) {
    const ids = new Set(absorbed.map((a) => a.id));
    state.annotations = state.annotations.filter((a) => !ids.has(a.id));
  }

  // The unwraps mutated the DOM the range pointed into; re-anchor by text so
  // the new wrap covers exactly the original selection.
  reanchorRange(range, selText, startHint);
}

export function unwrapAnnotEl(annotEl) {
  const parent = annotEl.parentNode;
  if (!parent) return;
  const sup = annotEl.querySelector('.annot-note-num');
  if (sup) sup.remove();
  while (annotEl.firstChild) parent.insertBefore(annotEl.firstChild, annotEl);
  parent.removeChild(annotEl);
  parent.normalize();
}

function attachNoteBadge(span, id) {
  if (span.querySelector('.annot-note-num')) return;
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
  const id = ++state.noteCounter;
  const span = document.createElement('span');
  const classes = ['annot'];
  if (flags.hl) classes.push('annot-hl');
  if (flags.note) classes.push('annot-note-ref');
  span.className = classes.join(' ');
  span.dataset.annotId = id;
  // Flatten any existing annotation the new range overlaps; wrapping over
  // them would split inner spans and orphan their text.
  absorbOverlappingAnnotations(range, flags);
  wrapRange(range, span);
  if (flags.note) {
    attachNoteBadge(span, id);
    insertNoteCard(id, flags.note, flags.kind || detectNoteKind(flags.note));
  }
  state.annotations.push({
    id,
    clientId: flags.clientId || createAnnotationClientId(),
    hl: !!flags.hl,
    ul: false,
    sl: false,
    note: flags.note || null,
    // kind: '' plain note, 'qa' assist Q&A — persisted on annotation_notes.kind
    kind: flags.kind || detectNoteKind(flags.note) || null,
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
