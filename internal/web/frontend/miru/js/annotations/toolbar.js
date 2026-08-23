/* Miru — the floating selection toolbar: create highlights/notes from a
   fresh text selection, or edit/delete an existing annotated passage.
   Owns the toolbar DOM element and the transient "what am I acting on"
   pointers (currentRange / currentAnnotEl), none of which are shared with
   other modules. */

import { elements } from '../dom.js';
import { ANNOTATION_TEXT_EXCLUDE } from '../constants.js';
import { showToast } from '../ui/feedback.js';
import { scheduleNoteLayout } from './layout.js';
import { initNoteFocus, focusNote, toggleNoteInline } from './focus.js';
import { annotationTextFromRange } from './sidecar.js';
import { notifyAnnotationsChanged } from './session.js';
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
  note: '<svg viewBox="0 0 24 24" width="16" height="16" aria-hidden="true"><path fill="currentColor" d="M4 4h16v12H8l-4 4V4z"/></svg>',
  ask: '<svg viewBox="0 0 24 24" width="16" height="16" aria-hidden="true"><path fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" d="M12 3l1.2 3.6L17 8l-3.8 1.4L12 13l-1.2-3.6L7 8l3.8-1.4L12 3z"/><path fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" d="M18 14l.7 2 2 .7-2 .7-.7 2-.7-2-2-.7 2-.7.7-2z"/></svg>',
  trash: '<svg viewBox="0 0 24 24" width="16" height="16" aria-hidden="true"><path fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" d="M3 6h18M8 6V4h8v2m1 0v14a2 2 0 0 1-2 2H9a2 2 0 0 1-2-2V6h10z"/></svg>',
};

// Host adapters (membox) may register extra toolbar actions without coupling
// Miru's annotation model to product-specific features like dictionary lookup.
// Each action: { id, icon, title, when({mode,text,entry}), run(ctx) }.
const extraAnnotActions = [];

export function registerAnnotAction(action) {
  if (!action || !action.id || typeof action.run !== 'function') {
    throw new Error('registerAnnotAction requires { id, run }');
  }
  const index = extraAnnotActions.findIndex((item) => item.id === action.id);
  if (index >= 0) extraAnnotActions[index] = action;
  else extraAnnotActions.push(action);
}

const annotToolbarHideListeners = [];

export function onAnnotToolbarHide(listener) {
  if (typeof listener === 'function') annotToolbarHideListeners.push(listener);
}

let annotToolbar = null;
let currentRange = null;
let currentAnnotEl = null;
let noteResizeObserver = null;
let currentSelectionText = '';

export function hideAnnotToolbar() {
  if (annotToolbar) {
    annotToolbar.hidden = true;
    annotToolbar.classList.remove('is-dict', 'is-assist', 'is-compose', 'is-mode-note', 'is-mode-ask');
  }
  currentRange = null;
  currentAnnotEl = null;
  currentSelectionText = '';
  for (const listener of annotToolbarHideListeners) {
    try { listener(); } catch (err) { console.warn(err); }
  }
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

const ICON_SEND =
  '<svg viewBox="0 0 24 24" width="16" height="16" aria-hidden="true"><path fill="currentColor" d="M3.4 4.2 21 12 3.4 19.8l2.1-6.3L15 12l-9.5-1.5L3.4 4.2z"/></svg>';

/** @type {'note' | 'ask'} */
let composeMode = 'note';

// Build the toolbar for create mode (compose dialog) or edit mode (icon row).
function buildAnnotToolbar(mode, entry, text = '') {
  const i = ANNOT_ICONS;
  annotToolbar.classList.remove('is-dict', 'is-assist', 'is-compose');

  if (mode === 'create') {
    composeMode = 'note';
    openComposeDialog();
    return;
  }

  // Edit existing annotation: compact icon row.
  let html =
    `<button type="button" data-action="highlight" class="${entry && entry.hl ? 'active' : ''}" title="Highlight" aria-label="Highlight">${i.highlight}</button>` +
    `<button type="button" data-action="note" title="${entry && entry.note ? 'Edit note' : 'Add note'}" aria-label="Note">${i.note}</button>`;
  for (const action of extraAnnotActions) {
    const visible = typeof action.when === 'function'
      ? action.when({ mode, text, entry })
      : false;
    if (!visible) continue;
    const title = action.title || action.id;
    html += `<button type="button" data-action="ext:${action.id}" title="${escapeAttr(title)}" aria-label="${escapeAttr(title)}">${action.icon || title}</button>`;
  }
  html += `<button type="button" data-action="delete" class="annot-del" title="Delete annotation" aria-label="Delete annotation">${i.trash}</button>`;
  annotToolbar.innerHTML = html;
}

function openComposeDialog(presetText = '') {
  annotToolbar.classList.add('is-compose');
  annotToolbar.classList.toggle('is-mode-ask', composeMode === 'ask');
  annotToolbar.classList.toggle('is-mode-note', composeMode === 'note');
  const modeLabel = composeMode === 'ask' ? 'Ask' : 'Note';
  const modeIcon = composeMode === 'ask' ? ANNOT_ICONS.ask : ANNOT_ICONS.note;
  const modeTitle = composeMode === 'ask'
    ? 'Ask mode · click to switch to Note'
    : 'Note mode · click to switch to Ask';
  const placeholder = composeMode === 'ask'
    ? 'Ask about the selection…'
    : 'Add a note…';
  let extras = '';
  for (const action of extraAnnotActions) {
    const visible = typeof action.when === 'function'
      ? action.when({ mode: 'create', text: currentSelectionText, entry: null })
      : false;
    if (!visible) continue;
    const title = action.title || action.id;
    extras += `<button type="button" class="annot-compose-ext" data-action="ext:${escapeAttr(action.id)}" title="${escapeAttr(title)}" aria-label="${escapeAttr(title)}">${action.icon || title}</button>`;
  }

  // Deepseek-style panel: full-width input on top; mode toggle bottom-left,
  // highlight + send bottom-right.
  annotToolbar.innerHTML =
    '<div class="annot-compose" role="dialog" aria-label="Selection actions">' +
      `<textarea class="annot-compose-input" rows="2" placeholder="${escapeAttr(placeholder)}" ` +
        'title="Enter for a new line · ⌘Enter to send" ' +
        `aria-label="${escapeAttr(placeholder)}" spellcheck="true"></textarea>` +
      '<div class="annot-compose-foot">' +
        '<div class="annot-compose-foot-left">' +
          `<button type="button" class="annot-compose-mode" data-action="toggle-mode" title="${escapeAttr(modeTitle)}" aria-label="${escapeAttr(modeTitle)}">` +
            `<span class="annot-compose-mode-icon" aria-hidden="true">${modeIcon}</span>` +
          '</button>' +
          extras +
        '</div>' +
        '<div class="annot-compose-actions">' +
          `<button type="button" class="annot-compose-hl" data-action="highlight" title="Highlight selection" aria-label="Highlight">${ANNOT_ICONS.highlight}</button>` +
          `<button type="button" class="annot-compose-send" data-action="send" title="Send (⌘Enter)" aria-label="Send">${ICON_SEND}</button>` +
        '</div>' +
      '</div>' +
    '</div>';

  const input = annotToolbar.querySelector('.annot-compose-input');
  if (presetText) input.value = presetText;
  autosizeNoteInput(input);
  input.addEventListener('input', () => {
    autosizeNoteInput(input);
    positionAnnotToolbar(currentAnnotEl || currentRange);
  });
  input.addEventListener('keydown', (e) => {
    if (e.key === 'Enter' && (e.metaKey || e.ctrlKey) && !e.isComposing && e.keyCode !== 229) {
      e.preventDefault();
      void commitComposeSend();
    } else if (e.key === 'Escape' && !e.isComposing) {
      hideAnnotToolbar();
    }
  });
  // Do not blur-commit — accidental focus loss should not save empty notes.
  positionAnnotToolbar(currentAnnotEl || currentRange);
  // Focus after layout so the caret is visible (mousedown preventDefault on
  // non-fields must not block this).
  requestAnimationFrame(() => {
    input.focus({ preventScroll: true });
    const len = input.value.length;
    try { input.setSelectionRange(len, len); } catch { /* ignore */ }
  });
}

function toggleComposeMode() {
  const input = annotToolbar.querySelector('.annot-compose-input');
  const keep = input ? input.value : '';
  composeMode = composeMode === 'note' ? 'ask' : 'note';
  openComposeDialog(keep);
}

async function commitComposeSend() {
  const input = annotToolbar.querySelector('.annot-compose-input');
  const text = (input?.value || '').trim();
  const sendBtn = annotToolbar.querySelector('.annot-compose-send');

  if (composeMode === 'note') {
    if (!text) {
      showToast('Write a note first');
      input?.focus();
      return;
    }
    commitNoteInput(text);
    return;
  }

  // Ask mode → deepseek assist, streams inline under the passage.
  if (!text) {
    showToast('Ask a question');
    input?.focus();
    return;
  }
  if (sendBtn) {
    sendBtn.disabled = true;
    sendBtn.classList.add('is-busy');
  }
  try {
    const { runAssistAsk } = await import('../../membox/assist.js');
    await runAssistAsk(extensionContext(), text);
  } catch (err) {
    if (err && err.name === 'AbortError') return;
    console.error('compose ask failed', err);
    showToast(err?.message || 'Ask failed');
    if (sendBtn) {
      sendBtn.disabled = false;
      sendBtn.classList.remove('is-busy');
    }
    input?.focus();
  }
}

function escapeAttr(value) {
  return String(value)
    .replace(/&/g, '&amp;')
    .replace(/"/g, '&quot;')
    .replace(/</g, '&lt;');
}

function extensionContext() {
  return {
    mode: currentAnnotEl ? 'edit' : 'create',
    text: currentSelectionText,
    range: currentRange,
    annotEl: currentAnnotEl,
    toolbar: annotToolbar,
    position: (target) => positionAnnotToolbar(target || currentAnnotEl || currentRange),
    finish: finishAnnotation,
    hide: hideAnnotToolbar,
  };
}

function finishAnnotation() {
  hideAnnotToolbar();
  const sel = window.getSelection();
  if (sel) sel.removeAllRanges();
}

function openNoteInput() {
  annotToolbar.innerHTML =
    '<div class="annot-note-compose">' +
      '<textarea class="annot-note-input" rows="2" placeholder="Add a note\u2026" title="Enter for a new line \u00b7 \u2318Enter to send" aria-label="Add a note"></textarea>' +
      '<button type="button" class="annot-note-send" title="Send note (\u2318Enter)" aria-label="Send note">' +
        '<svg viewBox="0 0 24 24" aria-hidden="true"><path fill="currentColor" d="M3.4 4.2 21 12 3.4 19.8l2.1-6.3L15 12l-9.5-1.5L3.4 4.2z"/></svg>' +
      '</button>' +
    '</div>';
  const input = annotToolbar.querySelector('textarea');
  const send = annotToolbar.querySelector('.annot-note-send');
  const entry = currentAnnotEl ? findAnnot(currentAnnotEl.dataset.annotId) : null;
  if (entry && entry.note) input.value = entry.note;
  const sendNote = () => commitNoteInput(input.value.trim());
  input.focus();
  autosizeNoteInput(input);
  input.addEventListener('input', () => autosizeNoteInput(input));
  input.addEventListener('keydown', (e) => {
    // Enter inserts a newline; Command+Enter sends. Ctrl+Enter is supported
    // as a cross-platform fallback for terminals/browsers without Cmd.
    if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) {
      e.preventDefault();
      sendNote();
    } else if (e.key === 'Escape') {
      hideAnnotToolbar();
    }
  });
  send.addEventListener('click', sendNote);
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
  // Idempotent toggle of highlight on the passage.
  if (action !== 'highlight') return;
  entry.hl = !entry.hl;
  entry.ul = false;
  entry.sl = false;
  annotEl.classList.toggle('annot-hl', entry.hl);
  annotEl.classList.remove('annot-ul', 'annot-sl');
  btn.classList.toggle('active', entry.hl);
  if (!entry.hl && !entry.note) {
    unwrapAnnotEl(annotEl);
    removeAnnot(entry.id);
    hideAnnotToolbar();
  } else {
    notifyAnnotationsChanged();
  }
}

function onAnnotToolbarClick(e) {
  const btn = e.target.closest('button[data-action]');
  if (!btn) return;
  const action = btn.dataset.action;

  if (action === 'toggle-mode') {
    toggleComposeMode();
    return;
  }
  if (action === 'send') {
    void commitComposeSend();
    return;
  }

  if (action.startsWith('ext:')) {
    const ext = extraAnnotActions.find((item) => item.id === action.slice(4));
    if (ext) ext.run(extensionContext());
    return;
  }

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
    if (action === 'highlight') {
      applyMark('highlight', currentRange);
      // Keep compose open so the user can still note/ask on the same selection.
      // Re-clone range after DOM wrap may invalidate it — finish for safety.
      finishAnnotation();
      return;
    }
    if (action === 'note') {
      openNoteInput();
      return;
    }
  }
}

function onAnnotMouseUp(e) {
  if (annotToolbar.contains(e.target)) return;
  // Clicks on an annotated passage are handled by onAnnotPassageClick.
  if (e.target.closest && e.target.closest('span.annot')) return;
  setTimeout(() => {
    // Assist/dict panels own the toolbar until Escape or explicit close.
    // Focusing the prompt input collapses the page selection; that must NOT
    // tear down an in-flight deepseek request or the panel itself.
    if (annotToolbar && !annotToolbar.hidden &&
        (annotToolbar.classList.contains('is-assist')
          || annotToolbar.classList.contains('is-dict')
          || annotToolbar.classList.contains('is-compose'))) {
      return;
    }
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
    currentSelectionText = annotationTextFromRange(range) || '';
    buildAnnotToolbar('create', null, currentSelectionText);
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

  // Reference number on the passage toggles the nearby inline note body.
  // No scroll — the note sits right under the block.
  const badge = e.target.closest('.annot-note-num');
  if (badge) {
    const host = badge.closest('span.annot.annot-note-ref');
    const id = host?.dataset?.annotId;
    if (id) {
      e.preventDefault();
      e.stopPropagation();
      toggleNoteInline(id);
      return;
    }
  }

  const annotEl = e.target.closest('span.annot');
  if (!annotEl) return;

  // Clicking the highlighted passage (not the number) still opens the edit bar
  // and briefly pulses the inline note without forcing a scroll jump.
  if (annotEl.classList.contains('annot-note-ref') && annotEl.dataset.annotId) {
    const id = annotEl.dataset.annotId;
    const card = document.querySelector(`.annot-note[data-annot-id="${id}"]`);
    if (card?.classList.contains('is-collapsed') || card?.hidden) {
      toggleNoteInline(id); // expand
    } else {
      focusNote(id, { duration: 1200 });
    }
  }

  currentAnnotEl = annotEl;
  currentRange = null;
  const entry = findAnnot(annotEl.dataset.annotId);
  currentSelectionText = (annotEl.textContent || '').trim();
  buildAnnotToolbar('edit', entry, currentSelectionText);
  positionAnnotToolbar(annotEl);
}

export function initAnnotations() {
  annotToolbar = document.createElement('div');
  annotToolbar.className = 'annot-toolbar';
  annotToolbar.hidden = true;
  document.body.appendChild(annotToolbar);

  // Keep page selection while clicking icon buttons, but allow real fields
  // (textarea/input) to take focus so the caret is visible and typing works.
  annotToolbar.addEventListener('mousedown', (e) => {
    const field = e.target.closest('textarea, input, [contenteditable="true"]');
    if (field) return;
    e.preventDefault();
  });
  annotToolbar.addEventListener('click', onAnnotToolbarClick);

  document.addEventListener('mouseup', onAnnotMouseUp);
  document.addEventListener('mousedown', (e) => {
    if (annotToolbar && !annotToolbar.hidden && !annotToolbar.contains(e.target)) {
      hideAnnotToolbar();
    }
  });

  // Passage anchors and note cards live in separate sibling layers but share
  // one interaction handler through their data-annot-id relationship.
  elements.article.addEventListener('click', onAnnotPassageClick);
  elements.annotationLayer.addEventListener('click', onAnnotPassageClick);

  window.addEventListener('resize', scheduleNoteLayout);
  if (typeof window.ResizeObserver === 'function') {
    noteResizeObserver = new ResizeObserver(scheduleNoteLayout);
    noteResizeObserver.observe(elements.article);
    noteResizeObserver.observe(elements.annotationLayer);
  }
  if (document.fonts && document.fonts.ready) {
    document.fonts.ready.then(scheduleNoteLayout);
  }

  initNoteFocus();
}
