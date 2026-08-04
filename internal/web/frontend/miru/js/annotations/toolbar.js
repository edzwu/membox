/* Miru — the floating selection toolbar: create highlights/underlines/notes
   from a fresh text selection, or edit/delete an existing annotated passage.
   Owns the toolbar DOM element and the transient "what am I acting on"
   pointers (currentRange / currentAnnotEl), none of which are shared with
   other modules. */

import { elements } from '../dom.js';
import { ANNOTATION_TEXT_EXCLUDE } from '../constants.js';
import { showToast } from '../ui/feedback.js';
import { scheduleNoteLayout } from './layout.js';
import { initNoteFocus, focusNote } from './focus.js';
import { annotationTextFromRange } from './sidecar.js';
import {
  findAnnot,
  applyMark,
  applyNote,
  setNoteOnPassage,
  startEditNoteCard,
  deleteAnnotation,
  unwrapAnnotEl,
  removeAnnot,
} from './model.js';

const ANNOT_ICONS = {
  highlight: '<svg viewBox="0 0 24 24" width="16" height="16" aria-hidden="true"><path fill="currentColor" d="M15.5 3l5.5 5.5-8.5 8.5H7v-5.5L15.5 3zM5 19h14v2H5v-2z"/></svg>',
  underline: '<svg viewBox="0 0 24 24" width="16" height="16" aria-hidden="true"><path fill="currentColor" d="M6 3v7a6 6 0 0 0 12 0V3h-2v7a4 4 0 0 1-8 0V3H6zM4 20h16v2H4v-2z"/></svg>',
  strikethrough: '<svg viewBox="0 0 24 24" width="16" height="16" aria-hidden="true"><path fill="currentColor" d="M6.85 10h10.3v2H6.85v-2zM12 4c-2.8 0-5 1.34-5 3.5h2.2c0-.9 1.2-1.7 2.8-1.7s2.8.8 2.8 1.7c0 .5-.2.9-.6 1.2l1.7 1.3c.8-.7 1.3-1.6 1.3-2.5 0-2.16-2.2-3.5-5.2-3.5zM7 16.5c0 2.16 2.2 3.5 5 3.5s5-1.34 5-3.5h-2.2c0 .9-1.2 1.7-2.8 1.7s-2.8-.8-2.8-1.7c0-.5.2-.9.6-1.2L8.3 14c-.8.7-1.3 1.6-1.3 2.5z"/></svg>',
  note: '<svg viewBox="0 0 24 24" width="16" height="16" aria-hidden="true"><path fill="currentColor" d="M4 4h16v12H8l-4 4V4z"/></svg>',
  trash: '<svg viewBox="0 0 24 24" width="16" height="16" aria-hidden="true"><path fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" d="M3 6h18M8 6V4h8v2m1 0v14a2 2 0 0 1-2 2H9a2 2 0 0 1-2-2V6h10z"/></svg>',
};

let annotToolbar = null;
let currentRange = null;
let currentAnnotEl = null;
let noteResizeObserver = null;

export function hideAnnotToolbar() {
  if (annotToolbar) annotToolbar.hidden = true;
  currentRange = null;
  currentAnnotEl = null;
}

function positionAnnotToolbar(target) {
  annotToolbar.hidden = false;
  const rect = target.getBoundingClientRect();
  const top = rect.top + window.scrollY - annotToolbar.offsetHeight - 8;
  const left = rect.left + window.scrollX + rect.width / 2 - annotToolbar.offsetWidth / 2;
  // Keep the (possibly widened) toolbar inside the viewport on both sides.
  const maxLeft = window.scrollX + document.documentElement.clientWidth - annotToolbar.offsetWidth - 8;
  annotToolbar.style.top = Math.max(8, top) + 'px';
  annotToolbar.style.left = Math.max(8, Math.min(left, Math.max(8, maxLeft))) + 'px';
}

const MARK_FLAGS = {
  highlight: ['hl', 'annot-hl'],
  underline: ['ul', 'annot-ul'],
  strikethrough: ['sl', 'annot-sl'],
};

// Build the toolbar for create mode (fresh selection) or edit mode (an
// existing annotated passage, given its entry to reflect toggle states).
function buildAnnotToolbar(mode, entry) {
  const i = ANNOT_ICONS;
  let html =
    `<button type="button" data-action="highlight" class="${entry && entry.hl ? 'active' : ''}" title="Highlight" aria-label="Highlight">${i.highlight}</button>` +
    `<button type="button" data-action="underline" class="${entry && entry.ul ? 'active' : ''}" title="Underline" aria-label="Underline">${i.underline}</button>` +
    `<button type="button" data-action="strikethrough" class="${entry && entry.sl ? 'active' : ''}" title="Strikethrough" aria-label="Strikethrough">${i.strikethrough}</button>` +
    `<button type="button" data-action="note" title="${entry && entry.note ? 'Edit note' : 'Add note'}" aria-label="Note">${i.note}</button>`;
  if (mode === 'edit') {
    html += `<button type="button" data-action="delete" class="annot-del" title="Delete annotation" aria-label="Delete annotation">${i.trash}</button>`;
  }
  annotToolbar.innerHTML = html;
}

function finishAnnotation() {
  hideAnnotToolbar();
  const sel = window.getSelection();
  if (sel) sel.removeAllRanges();
}

function openNoteInput() {
  annotToolbar.innerHTML = '<textarea class="annot-note-input" rows="2" placeholder="Add a note\u2026" title="Enter saves \u00b7 Shift+Enter inserts a new line" aria-label="Add a note"></textarea>';
  const input = annotToolbar.querySelector('textarea');
  const entry = currentAnnotEl ? findAnnot(currentAnnotEl.dataset.annotId) : null;
  if (entry && entry.note) input.value = entry.note;
  input.focus();
  autosizeNoteInput(input);
  input.addEventListener('input', () => autosizeNoteInput(input));
  input.addEventListener('keydown', (e) => {
    // Enter saves; Shift+Enter keeps the default newline behavior.
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault();
      commitNoteInput(input.value.trim());
    } else if (e.key === 'Escape') {
      hideAnnotToolbar();
    }
  });
  input.addEventListener('blur', () => commitNoteInput(input.value.trim()));
  // The textarea is wider than the icon row; re-center the toolbar so the
  // wider box stays inside the viewport.
  positionAnnotToolbar(currentAnnotEl || currentRange);
}

function autosizeNoteInput(input) {
  input.style.height = 'auto';
  input.style.height = Math.min(input.scrollHeight, 180) + 'px';
}

function commitNoteInput(text) {
  if (currentAnnotEl) {
    const entry = findAnnot(currentAnnotEl.dataset.annotId);
    if (entry && text) setNoteOnPassage(entry, currentAnnotEl, text);
    hideAnnotToolbar();
  } else if (currentRange) {
    if (text) applyNote(currentRange, text);
    finishAnnotation();
  }
}

function handleEditAction(action, annotEl, entry, btn) {
  if (action === 'delete') {
    deleteAnnotation(entry.id);
    hideAnnotToolbar();
    return;
  }
  if (action === 'note') {
    openNoteInput();
    return;
  }
  // Idempotent toggle of highlight / underline / strikethrough on the passage.
  const [flag, className] = MARK_FLAGS[action] || [];
  if (!flag) return;
  entry[flag] = !entry[flag];
  annotEl.classList.toggle(className, entry[flag]);
  btn.classList.toggle('active', entry[flag]);
  if (!entry.hl && !entry.ul && !entry.sl && !entry.note) {
    unwrapAnnotEl(annotEl);
    removeAnnot(entry.id);
    hideAnnotToolbar();
  }
}

function onAnnotToolbarClick(e) {
  const btn = e.target.closest('button[data-action]');
  if (!btn) return;
  const action = btn.dataset.action;

  if (currentAnnotEl) {
    const entry = findAnnot(currentAnnotEl.dataset.annotId);
    if (!entry) {
      hideAnnotToolbar();
      return;
    }
    handleEditAction(action, currentAnnotEl, entry, btn);
    return;
  }

  if (currentRange) {
    if (action === 'note') {
      openNoteInput();
    } else {
      applyMark(action, currentRange);
      finishAnnotation();
    }
  }
}

function onAnnotMouseUp(e) {
  if (annotToolbar.contains(e.target)) return;
  // Clicks on an annotated passage are handled by onAnnotPassageClick.
  if (e.target.closest && e.target.closest('span.annot')) return;
  setTimeout(() => {
    const sel = window.getSelection();
    if (!sel || sel.isCollapsed || sel.rangeCount === 0) {
      hideAnnotToolbar();
      return;
    }
    const range = sel.getRangeAt(0);
    if (!elements.article.contains(range.commonAncestorContainer)) {
      hideAnnotToolbar();
      return;
    }
    // Generated chrome (editable title, note cards, action buttons) is not
    // part of the stable annotation text stream and cannot be annotated.
    const startEl = range.startContainer.nodeType === Node.ELEMENT_NODE
      ? range.startContainer : range.startContainer.parentElement;
    const endEl = range.endContainer.nodeType === Node.ELEMENT_NODE
      ? range.endContainer : range.endContainer.parentElement;
    if ((startEl && startEl.closest(ANNOTATION_TEXT_EXCLUDE)) ||
        (endEl && endEl.closest(ANNOTATION_TEXT_EXCLUDE)) ||
        !annotationTextFromRange(range)) {
      hideAnnotToolbar();
      return;
    }
    // A range spanning multiple table cells cannot be wrapped without
    // damaging the row structure. Notes within one cell remain supported.
    const startCell = startEl && startEl.closest('td, th');
    const endCell = endEl && endEl.closest('td, th');
    if ((startCell || endCell) && startCell !== endCell) {
      hideAnnotToolbar();
      showToast('Select text within one table cell');
      return;
    }
    // Don't start a new annotation inside an existing one; use passage click.
    const container = range.commonAncestorContainer;
    const containerEl = container.nodeType === 1 ? container : container.parentElement;
    if (containerEl && containerEl.closest('span.annot')) {
      hideAnnotToolbar();
      return;
    }
    currentRange = range.cloneRange();
    currentAnnotEl = null;
    buildAnnotToolbar('create', null);
    positionAnnotToolbar(range);
  }, 0);
}

function onAnnotPassageClick(e) {
  // Note card edit/delete buttons take priority.
  const noteBtn = e.target.closest('[data-note-action]');
  if (noteBtn) {
    const card = noteBtn.closest('.annot-note');
    if (!card) return;
    const entry = findAnnot(card.dataset.annotId);
    if (!entry) return;
    if (noteBtn.dataset.noteAction === 'del') {
      deleteAnnotation(entry.id);
    } else if (noteBtn.dataset.noteAction === 'edit') {
      startEditNoteCard(card, entry);
    }
    return;
  }

  // Click a note card body → jump to its passage (rail stays put).
  const card = e.target.closest('.annot-note');
  if (card && card.dataset.annotId) {
    focusNote(card.dataset.annotId, { scrollTo: 'anchor' });
    return;
  }

  const annotEl = e.target.closest('span.annot');
  if (!annotEl) return;

  // Note anchors also light up their card in the rail.
  if (annotEl.classList.contains('annot-note-ref') && annotEl.dataset.annotId) {
    focusNote(annotEl.dataset.annotId, { scrollTo: 'card' });
  }

  currentAnnotEl = annotEl;
  currentRange = null;
  buildAnnotToolbar('edit', findAnnot(annotEl.dataset.annotId));
  positionAnnotToolbar(annotEl);
}

export function initAnnotations() {
  annotToolbar = document.createElement('div');
  annotToolbar.className = 'annot-toolbar';
  annotToolbar.hidden = true;
  document.body.appendChild(annotToolbar);

  // Prevent the toolbar from stealing the selection/focus.
  annotToolbar.addEventListener('mousedown', (e) => e.preventDefault());
  annotToolbar.addEventListener('click', onAnnotToolbarClick);

  document.addEventListener('mouseup', onAnnotMouseUp);
  document.addEventListener('mousedown', (e) => {
    if (annotToolbar && !annotToolbar.hidden && !annotToolbar.contains(e.target)) {
      hideAnnotToolbar();
    }
  });

  // Click an annotated passage (or a note card button) to edit it.
  elements.article.addEventListener('click', onAnnotPassageClick);

  window.addEventListener('resize', scheduleNoteLayout);
  if (typeof window.ResizeObserver === 'function') {
    noteResizeObserver = new ResizeObserver(scheduleNoteLayout);
    noteResizeObserver.observe(elements.article);
  }
  if (document.fonts && document.fonts.ready) {
    document.fonts.ready.then(scheduleNoteLayout);
  }

  initNoteFocus();
}
