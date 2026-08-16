/* Miru — annotation data model: create/toggle/delete highlights, underlines
   and margin notes. Inline anchors live in `.article`; note cards live in the
   sibling `.annotation-layer`, linked only by data-annot-id and model state. */

import { elements } from '../dom.js';
import { state } from '../state.js';
import { configureTechnicalMarkdown } from '../markdown/cjk-emphasis.js';
import { refreshNoteNumbers, scheduleNoteLayout } from './layout.js';
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
export function wrapRange(range, el) {
  try {
    range.surroundContents(el);
  } catch (err) {
    const frag = range.extractContents();
    el.appendChild(frag);
    range.insertNode(el);
  }
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

function renderNoteText(el, note) {
  el.textContent = '';

  if (typeof window.markdownit === 'function' && typeof window.DOMPurify === 'function') {
    if (!noteMarkdown) {
      noteMarkdown = configureTechnicalMarkdown(
        window.markdownit({ html: false, linkify: true, typographer: true, breaks: true }),
      );
    }
    const clean = window.DOMPurify.sanitize(noteMarkdown.render(note), {
      ADD_ATTR: ['target', 'rel'],
    });
    const fragment = document.createElement('div');
    fragment.innerHTML = clean;
    prepareNoteLinks(fragment);
    highlightNoteCode(fragment);
    while (fragment.firstChild) el.append(fragment.firstChild);
    return;
  }

  renderPlainNoteText(el, note);
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
  return '';
}

function insertNoteCard(id, noteText, kind) {
  const card = document.createElement('aside');
  card.className = 'annot-note' + (kind === 'qa' ? ' is-qa' : '');
  card.dataset.annotId = id;
  if (kind) card.dataset.noteKind = kind;
  const body = document.createElement('div');
  body.className = 'annot-note-body';
  const text = document.createElement('div');
  text.className = 'annot-note-text';
  renderNoteText(text, noteText);
  body.append(buildNoteLabel(id), text);
  card.appendChild(body);
  card.appendChild(buildNoteBtn('edit'));
  card.appendChild(buildNoteBtn('del'));
  elements.annotationLayer.appendChild(card);
  return card;
}

// `flags` carries the four orthogonal annotation aspects (highlight /
// underline / strikethrough / note); named to avoid colliding with the
// shared app `state`.
export function applyAnnotationRange(range, flags) {
  const id = ++state.noteCounter;
  const span = document.createElement('span');
  const classes = ['annot'];
  if (flags.hl) classes.push('annot-hl');
  if (flags.ul) classes.push('annot-ul');
  if (flags.sl) classes.push('annot-sl');
  if (flags.note) classes.push('annot-note-ref');
  span.className = classes.join(' ');
  span.dataset.annotId = id;
  wrapRange(range, span);
  if (flags.note) {
    attachNoteBadge(span, id);
    insertNoteCard(id, flags.note, flags.kind || detectNoteKind(flags.note));
  }
  state.annotations.push({
    id,
    clientId: flags.clientId || createAnnotationClientId(),
    hl: !!flags.hl,
    ul: !!flags.ul,
    sl: !!flags.sl,
    note: flags.note || null,
    // kind: '' plain note, 'qa' assist Q&A — persisted on annotation_notes.kind
    kind: flags.kind || detectNoteKind(flags.note) || null,
    ref: flags.ref || null,
  });
  refreshNoteNumbers();
  if (flags.notify === false) {
    // Restores update the in-memory/visual model but are not user mutations.
    // The caller performs one presentation refresh after the batch.
  } else {
    notifyAnnotationsChanged();
  }
  scheduleNoteLayout();
  // Pulse only a note the user just created. The selection is already visible,
  // so automatically scrolling to its rail/stack card can drag a long page all
  // the way to the bottom. Restored notes use notify:false and do not pulse.
  if (flags.note && flags.notify !== false) {
    requestAnimationFrame(() => focusNote(id, { duration: 1600 }));
  }
  return span;
}

export function applyMark(type, range) {
  applyAnnotationRange(range, {
    hl: type === 'highlight',
    ul: type === 'underline',
    sl: type === 'strikethrough',
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
    const card = elements.annotationLayer.querySelector(`.annot-note[data-annot-id="${entry.id}"]`);
    if (card) {
      card.classList.toggle('is-qa', kind === 'qa');
      if (kind) card.dataset.noteKind = kind;
      else delete card.dataset.noteKind;
    }
    const textEl = card && card.querySelector('.annot-note-text');
    if (textEl) renderNoteText(textEl, text);
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
  const card = elements.annotationLayer.querySelector(`.annot-note[data-annot-id="${id}"]`);
  if (card) card.remove();
  if (annotEl) unwrapAnnotEl(annotEl);
  refreshNoteNumbers();
  removeAnnot(id);
}

export function startEditNoteCard(card, entry) {
  const textEl = card.querySelector('.annot-note-text');
  if (!textEl) return;
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
    renderNoteText(t, entry.note);
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
