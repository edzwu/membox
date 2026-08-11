/* Notes chrome: the top-bar browse button, the notes picker modal (a local,
   filterable index over the notes already restored into Miru's annotation
   model — no second backend representation), and long-note rail previews. */

import { elements } from '../js/dom.js';
import { state } from '../js/state.js';
import { focusNote } from '../js/annotations/focus.js';
import { layoutMarginNotes } from '../js/annotations/layout.js';
import { session } from './session.js';
import { registerModal, closeOtherModals } from './modals.js';
import { getDocumentNavigation } from './document.js';

const NOTE_PREVIEW_LINES = 6;

let browseNotesButton = null;
let notesModal = null;
let notePreviewTimer = null;

function noteCount() {
  return state.annotations.filter((entry) => typeof entry.note === 'string' && entry.note.trim()).length;
}

function noteDocumentURL(ref) {
  const url = new URL(window.location.href);
  url.searchParams.set('id', ref);
  // `note` is a one-way return target for the source page. Carrying it into
  // the full-note document would make that page try to focus itself.
  url.searchParams.delete('note');
  url.hash = '';
  return url.href;
}

function captureReadingAnchor() {
  const viewportTop = document.querySelector('.topbar')?.getBoundingClientRect().bottom || 0;
  const candidates = [
    ...elements.article.querySelectorAll('.fold-heading, p, li, pre, blockquote, table, img'),
    ...elements.annotationLayer.querySelectorAll('.annot-note'),
  ];
  let best = null;
  for (const element of candidates) {
    const rect = element.getBoundingClientRect();
    if (rect.bottom <= viewportTop || rect.top >= window.innerHeight) continue;
    const score = Math.abs(rect.top - viewportTop);
    if (!best || score < best.score) best = { element, top: rect.top, score };
  }
  return best;
}

function restoreReadingAnchor(anchor) {
  if (!anchor || !anchor.element.isConnected) return;
  const delta = anchor.element.getBoundingClientRect().top - anchor.top;
  if (Math.abs(delta) > 0.5) window.scrollBy({ top: delta, behavior: 'auto' });
}

function focusRequestedSourceNote() {
  if (!session.pendingSourceNoteRef) return false;
  const entry = state.annotations.find((item) => item.ref === session.pendingSourceNoteRef);
  if (!entry) return false;
  session.pendingSourceNoteRef = '';
  session.sourceNoteFocused = true;
  focusNote(entry.id, { scrollTo: 'anchor', duration: 3000 });
  return true;
}

// Comments become regular Markdown documents when saved. Keep long rail cards
// compact immediately while preserving the full text in the model/editor, and
// add the source-document link after the backend assigns its durable UUID. Lock a visible
// content anchor across the synchronous relayout so previewing cannot move the
// reader to a different paragraph or note.
function renderLongNotePreviews() {
  const readingAnchor = captureReadingAnchor();
  elements.annotationLayer.classList.add('membox-notes-relayout');
  const entries = new Map(state.annotations.map((entry) => [String(entry.id), entry]));
  elements.annotationLayer.querySelectorAll('.annot-note[data-annot-id]').forEach((card) => {
    const entry = entries.get(String(card.dataset.annotId));
    const body = card.querySelector('.annot-note-body');
    const text = body && body.querySelector('.annot-note-text');
    let link = body && body.querySelector('.membox-open-note');
    card.classList.remove('membox-note-preview');
    delete card.dataset.exportRemoveClass;
    if (!entry || !text) {
      if (link) link.remove();
      return;
    }
    const lineHeight = parseFloat(getComputedStyle(text).lineHeight) || 16.8;
    const isLong = text.scrollHeight > lineHeight * NOTE_PREVIEW_LINES + 8;
    if (!isLong) {
      if (link) link.remove();
      return;
    }
    // Clamp immediately, even before the async save assigns a durable ref.
    // The synchronous annotation-change handler runs in the same task that
    // replaces the editor, so the browser never paints an intermediate full
    // card and the page cannot visibly jump when the save response arrives.
    card.classList.add('membox-note-preview');
    card.dataset.exportRemoveClass = 'membox-note-preview';
    if (!entry.ref) {
      if (link) link.remove();
      return;
    }
    if (!link) {
      link = document.createElement('a');
      link.className = 'membox-open-note';
      link.target = '_blank';
      link.rel = 'noopener noreferrer';
      link.dataset.exportRemove = 'true';
      link.textContent = 'Open full note ↗';
      link.addEventListener('click', (event) => event.stopPropagation());
      body.appendChild(link);
    }
    link.href = noteDocumentURL(entry.ref);
    link.setAttribute('aria-label', 'Open full note in a new tab');
  });
  layoutMarginNotes();
  restoreReadingAnchor(readingAnchor);
  elements.annotationLayer.classList.remove('membox-notes-relayout');
  focusRequestedSourceNote();
}

function scheduleLongNotePreviews() {
  if (notePreviewTimer !== null) return;
  notePreviewTimer = window.setTimeout(() => {
    notePreviewTimer = null;
    renderLongNotePreviews();
  }, 0);
}

export function renderBrowseNotesButton() {
  const count = noteCount();
  const countBadge = browseNotesButton.querySelector('.membox-notes-count');
  countBadge.textContent = String(count);
  countBadge.hidden = count === 0;
  browseNotesButton.setAttribute('aria-label', `Browse ${count} note${count === 1 ? '' : 's'} in this document`);
  browseNotesButton.title = count === 1 ? 'Browse 1 note' : `Browse ${count} notes`;
}

// Visibility follows the document chrome rules owned by status.js.
export function setBrowseNotesVisible(visible) {
  if (browseNotesButton) browseNotesButton.hidden = !visible;
}

// ---------------------------------------------------------------------------
// Notes picker modal. Selecting a result focuses its passage.
// ---------------------------------------------------------------------------
function createBrowseNotesButton() {
  const button = document.createElement('button');
  button.type = 'button';
  button.id = 'membox-browse-notes';
  button.className = 'membox-browse-notes';
  button.hidden = true;
  button.title = 'Browse notes';
  button.setAttribute('aria-label', 'Browse notes in this document');
  button.innerHTML = `
    <svg viewBox="0 0 24 24" aria-hidden="true">
      <path d="M5 4.5h14a1.5 1.5 0 0 1 1.5 1.5v9a1.5 1.5 0 0 1-1.5 1.5h-8l-5.5 4v-4H5A1.5 1.5 0 0 1 3.5 15V6A1.5 1.5 0 0 1 5 4.5Z" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linejoin="round"/>
      <path d="M8 9h8M8 12.5h5" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/>
    </svg>
    <span class="membox-notes-count" hidden>0</span>`;
  getDocumentNavigation().appendChild(button);
  button.addEventListener('click', openNotesModal);
  return button;
}

function createNotesModal() {
  const backdrop = document.createElement('div');
  backdrop.className = 'membox-modal-backdrop';
  backdrop.hidden = true;
  backdrop.innerHTML = `
    <div class="membox-modal" role="dialog" aria-modal="true" aria-labelledby="membox-notes-dialog-title">
      <div class="membox-modal-title" id="membox-notes-dialog-title">Notes in this document</div>
      <div class="membox-related-picker">
        <input class="membox-related-search" type="search" role="combobox" aria-autocomplete="list" aria-expanded="false" aria-controls="membox-note-options" placeholder="Filter notes or quoted text…" autocomplete="off" spellcheck="false">
        <div class="membox-related-options" id="membox-note-options" role="listbox" aria-label="Notes in this document"></div>
        <div class="membox-modal-hint">Select a note to jump to its passage.</div>
      </div>
      <div class="membox-modal-actions">
        <button type="button" class="membox-modal-btn membox-modal-cancel">Close</button>
      </div>
    </div>`;
  document.body.appendChild(backdrop);
  const modal = backdrop.querySelector('.membox-modal');
  const searchInput = backdrop.querySelector('.membox-related-search');
  const options = backdrop.querySelector('.membox-related-options');
  backdrop.addEventListener('mousedown', (event) => {
    if (event.target === backdrop) closeNotesModal();
  });
  backdrop.querySelector('.membox-modal-cancel').addEventListener('click', () => closeNotesModal());
  searchInput.addEventListener('input', renderNoteOptions);
  searchInput.addEventListener('keydown', onNoteSearchKeydown);
  modal.addEventListener('keydown', (event) => {
    if (event.key === 'Escape') {
      event.stopPropagation();
      closeNotesModal();
    }
  });
  return { backdrop, searchInput, options };
}

function notePickerItems() {
  const entries = new Map(state.annotations
    .filter((entry) => typeof entry.note === 'string' && entry.note.trim())
    .map((entry) => [String(entry.id), entry]));
  const items = [];
  const anchors = elements.article.querySelectorAll('span.annot-note-ref[data-annot-id]');
  anchors.forEach((anchor, index) => {
    const id = String(anchor.dataset.annotId);
    const entry = entries.get(id);
    if (!entry) return;
    const clone = anchor.cloneNode(true);
    clone.querySelectorAll('.annot-note-num').forEach((badge) => badge.remove());
    const excerpt = clone.textContent.replace(/\s+/g, ' ').trim();
    const note = entry.note.replace(/\s+/g, ' ').trim();
    items.push({ id, number: index + 1, note, excerpt });
  });
  return items;
}

function renderNoteOptions() {
  const query = notesModal.searchInput.value.trim().toLocaleLowerCase();
  const allItems = notePickerItems();
  const items = query
    ? allItems.filter((item) => `${item.note}\n${item.excerpt}\n${item.number}`.toLocaleLowerCase().includes(query))
    : allItems;
  notesModal.options.textContent = '';
  notesModal.searchInput.setAttribute('aria-expanded', String(items.length > 0));
  if (!items.length) {
    const status = document.createElement('div');
    status.className = 'membox-related-option-status';
    status.textContent = allItems.length ? 'No matching notes' : 'No notes in this document';
    notesModal.options.appendChild(status);
    return;
  }
  for (const item of items) {
    const option = document.createElement('button');
    option.type = 'button';
    option.className = 'membox-related-option';
    option.setAttribute('role', 'option');
    option.setAttribute('aria-label', `Note ${item.number}: ${item.note}`);
    const title = document.createElement('span');
    title.className = 'membox-related-option-title';
    title.textContent = item.note;
    const number = document.createElement('code');
    number.className = 'membox-related-option-id';
    number.textContent = `#${item.number}`;
    const excerpt = document.createElement('span');
    excerpt.className = 'membox-related-option-path';
    excerpt.textContent = item.excerpt ? `“${item.excerpt}”` : 'Quoted passage unavailable';
    option.append(title, number, excerpt);
    option.addEventListener('click', () => jumpToNote(item.id));
    option.addEventListener('keydown', onNoteOptionKeydown);
    notesModal.options.appendChild(option);
  }
}

function openNotesModal() {
  if (!session.connected || !session.documentID) return;
  closeOtherModals();
  notesModal.searchInput.value = '';
  renderNoteOptions();
  notesModal.backdrop.hidden = false;
  notesModal.searchInput.focus();
}

function closeNotesModal(restoreFocus = true) {
  notesModal.backdrop.hidden = true;
  if (restoreFocus && !browseNotesButton.hidden) browseNotesButton.focus();
}

function jumpToNote(id) {
  closeNotesModal(false);
  focusNote(id, { scrollTo: 'anchor', duration: 3000 });
}

function onNoteSearchKeydown(event) {
  if (!['ArrowDown', 'Enter'].includes(event.key)) return;
  const first = notesModal.options.querySelector('.membox-related-option');
  if (!first) return;
  event.preventDefault();
  if (event.key === 'Enter') first.click();
  else first.focus();
}

function onNoteOptionKeydown(event) {
  if (!['ArrowDown', 'ArrowUp', 'Enter'].includes(event.key)) return;
  event.preventDefault();
  if (event.key === 'Enter') {
    event.currentTarget.click();
    return;
  }
  const options = Array.from(notesModal.options.querySelectorAll('.membox-related-option'));
  const index = options.indexOf(event.currentTarget);
  const next = event.key === 'ArrowDown' ? options[index + 1] : options[index - 1];
  (next || notesModal.searchInput).focus();
}

export function initNotes() {
  browseNotesButton = createBrowseNotesButton();
  notesModal = createNotesModal();
  registerModal({
    isOpen: () => !notesModal.backdrop.hidden,
    close: () => closeNotesModal(false),
  });

  window.addEventListener('miru-annotations-changed', () => {
    renderBrowseNotesButton();
    renderLongNotePreviews();
    if (!notesModal.backdrop.hidden) renderNoteOptions();
  });
  window.addEventListener('miru-annotations-saved', () => {
    renderBrowseNotesButton();
    renderLongNotePreviews();
    if (!notesModal.backdrop.hidden) renderNoteOptions();
  });
  window.addEventListener('resize', scheduleLongNotePreviews);
}
