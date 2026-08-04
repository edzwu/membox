/* Miru — annotation data model: create/toggle/delete highlights, underlines
   and margin notes, and keep their DOM (inline <span class="annot"> +
   .annot-note card) in sync with the in-memory `state.annotations` list. */

import { elements } from '../dom.js';
import { state } from '../state.js';
import { refreshNoteNumbers, scheduleNoteLayout } from './layout.js';
import { focusNote } from './focus.js';
import { updateMarkdownDownloadControl } from '../ui/chrome.js';

export function findAnnot(id) {
  return state.annotations.find((a) => String(a.id) === String(id));
}

export function removeAnnot(id) {
  state.annotations = state.annotations.filter((a) => String(a.id) !== String(id));
  updateMarkdownDownloadControl();
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

function closestBlock(el) {
  const blocks = ['P', 'LI', 'BLOCKQUOTE', 'H1', 'H2', 'H3', 'H4', 'H5', 'H6', 'PRE', 'TD', 'TH'];
  let node = el.parentElement;
  while (node && node !== elements.article) {
    if (blocks.includes(node.tagName)) return node;
    node = node.parentElement;
  }
  return null;
}

export function insertCardNaturally(card, refSpan) {
  const cell = refSpan.closest('td, th');
  if (cell) {
    // A sibling of <td>/<th> would become an invalid child of <tr> and can
    // scramble the table. Keep the card in-flow at the end of its own cell.
    card.classList.add('annot-note-in-cell');
    cell.appendChild(card);
    return;
  }
  const block = closestBlock(refSpan);
  if (block && block.tagName === 'LI') {
    // Keep list structure valid: <ul>/<ol> may only own <li> children.
    block.appendChild(card);
  } else if (block && block.classList.contains('fold-heading')) {
    const section = block.closest('.fold-section');
    const body = section && Array.from(section.children)
      .find((child) => child.classList.contains('fold-body'));
    if (body) body.insertBefore(card, body.firstChild);
    else block.insertAdjacentElement('afterend', card);
  } else if (block && block.parentNode) {
    block.parentNode.insertBefore(card, block.nextSibling);
  } else {
    refSpan.appendChild(card);
  }
}

function insertNoteCard(id, noteText, refSpan) {
  const card = document.createElement('aside');
  card.className = 'annot-note';
  card.dataset.annotId = id;
  card.appendChild(buildNoteLabel(id));
  const text = document.createElement('span');
  text.className = 'annot-note-text';
  text.textContent = ' ' + noteText;
  card.appendChild(text);
  card.appendChild(buildNoteBtn('edit'));
  card.appendChild(buildNoteBtn('del'));
  insertCardNaturally(card, refSpan);
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
    insertNoteCard(id, flags.note, span);
  }
  state.annotations.push({ id, hl: !!flags.hl, ul: !!flags.ul, sl: !!flags.sl, note: flags.note || null, ref: flags.ref || null });
  refreshNoteNumbers();
  updateMarkdownDownloadControl();
  scheduleNoteLayout();
  if (flags.note) {
    requestAnimationFrame(() => focusNote(id, { scrollTo: 'card', duration: 1600 }));
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

export function applyNote(range, noteText) {
  applyAnnotationRange(range, { hl: false, ul: false, note: noteText });
}

// Add a note to (or update the note on) an existing annotated passage.
export function setNoteOnPassage(entry, annotEl, text) {
  if (entry.note) {
    entry.note = text;
    const card = elements.article.querySelector(`.annot-note[data-annot-id="${entry.id}"]`);
    const textEl = card && card.querySelector('.annot-note-text');
    if (textEl) textEl.textContent = ' ' + text;
  } else {
    entry.note = text;
    annotEl.classList.add('annot-note-ref');
    attachNoteBadge(annotEl, entry.id);
    insertNoteCard(entry.id, text, annotEl);
    requestAnimationFrame(() => focusNote(entry.id, { scrollTo: 'card', duration: 1600 }));
  }
  refreshNoteNumbers();
  scheduleNoteLayout();
  // Note content changed: notify persistence listeners (miru-annotations-changed).
  updateMarkdownDownloadControl();
}

export function deleteAnnotation(id) {
  const annotEl = elements.article.querySelector(`span.annot[data-annot-id="${id}"]`);
  const card = elements.article.querySelector(`.annot-note[data-annot-id="${id}"]`);
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
  input.title = 'Enter saves \u00b7 Shift+Enter inserts a new line';
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
    const t = document.createElement('span');
    t.className = 'annot-note-text';
    t.textContent = ' ' + entry.note;
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
      if (changed) updateMarkdownDownloadControl();
    } else {
      deleteAnnotation(entry.id);
    }
  };
  input.addEventListener('keydown', (e) => {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault();
      save();
    } else if (e.key === 'Escape') restore();
  });
  input.addEventListener('blur', save);
}
