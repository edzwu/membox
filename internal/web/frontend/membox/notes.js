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

// Short notes render full inline body (translation-style). Longer ones collapse
// to a titled hyperlink that opens the durable note document.
const INLINE_NOTE_MAX_RUNES = 140;
const INLINE_NOTE_MAX_LINES = 3;
const INLINE_NOTE_TITLE_RUNES = 42;

let browseNotesButton = null;
let notesModal = null;
let notePreviewTimer = null;

function noteCount() {
  return state.annotations.filter((entry) => typeof entry.note === 'string' && entry.note.trim()).length;
}

export function noteDocumentURL(ref) {
  const url = new URL(window.location.href);
  url.searchParams.set('id', ref);
  // Carry the source document so the full-note page can render 「← 原文」.
  // `note` stays a source-page focus target only — do not put it on the note.
  if (session.documentID) {
    url.searchParams.set('from', session.documentID);
  } else {
    url.searchParams.delete('from');
  }
  url.searchParams.delete('note');
  url.hash = '';
  return url.href;
}

function sourceBacklinkURL(sourceID, noteRef) {
  const url = new URL(window.location.href);
  url.searchParams.set('id', sourceID);
  if (noteRef) url.searchParams.set('note', noteRef);
  else url.searchParams.delete('note');
  url.searchParams.delete('from');
  url.hash = '';
  return url.href;
}

/** Prominent return chrome when viewing a full selection-note document.
 *  Pass related items when available. Omitting them (or passing null) only
 *  paints from `?from=` and never tears down an existing backlink. */
export function renderNoteSourceBacklink(relatedItems = null) {
  const noteRef = session.documentID || '';
  if (!noteRef) {
    document.querySelectorAll('.membox-note-backlink').forEach((node) => node.remove());
    return;
  }

  // Prefer explicit ?from= (set by long-note / chip openers), else the related
  // graph edge that marks this document as an annotation note on a target.
  let sourceID = String(session.noteSourceFromRef || '').trim();
  let sourceTitle = '';
  const items = Array.isArray(relatedItems) ? relatedItems : null;
  if (items) {
    const annotated = items.find((item) => item && String(item.annotation_ref || '').trim());
    if (annotated) {
      if (!sourceID) sourceID = String(annotated.id || '').trim();
      sourceTitle = String(annotated.title || '').trim();
    }
  }
  if (!sourceID) {
    // Only remove when related finished and confirmed there is no source edge.
    if (items) {
      document.querySelectorAll('.membox-note-backlink').forEach((node) => node.remove());
    }
    return;
  }

  const href = sourceBacklinkURL(sourceID, noteRef);
  const label = sourceTitle || sourceID;

  let link = document.querySelector('.membox-note-backlink');
  if (!link) {
    link = document.createElement('a');
    link.className = 'membox-note-backlink';
    link.innerHTML =
      '<span class="membox-note-backlink-arrow" aria-hidden="true">←</span>' +
      '<span class="membox-note-backlink-kicker">原文</span>' +
      '<span class="membox-note-backlink-title"></span>';
  }
  link.href = href;
  link.title = `返回原文 ${label}`;
  link.setAttribute('aria-label', `返回原文 ${label}`);
  const titleEl = link.querySelector('.membox-note-backlink-title');
  if (titleEl) titleEl.textContent = label;

  // Prefer inside the article (survives surface layout quirks; scrolls with doc).
  // Fall back to reading-surface so it still shows before the title paints.
  if (elements.article?.isConnected) {
    const first = elements.article.firstChild;
    if (link.parentElement !== elements.article || first !== link) {
      elements.article.insertBefore(link, first);
    }
    return;
  }
  const surface = elements.readingSurface;
  if (surface?.isConnected) {
    surface.insertBefore(link, surface.firstChild);
  }
}

function captureReadingAnchor() {
  const viewportTop = document.querySelector('.topbar')?.getBoundingClientRect().bottom || 0;
  const candidates = [
    ...elements.article.querySelectorAll('.fold-heading, p, li, pre, blockquote, table, img, .annot-note'),
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

function notePlainForInline(entry) {
  let raw = String(entry?.note || '').trim();
  if (!raw) return '';
  // Match Miru card rendering: summary/jp notes hide the restore excerpt.
  if (entry.kind === 'summary' || /\*\*总结：?\*\*/.test(raw)) {
    const m = raw.match(/\*\*总结：?\*\*\s*\n+([\s\S]*)/);
    if (m) raw = m[1].trim();
  }
  if (entry.kind === 'jp-study' || /\*\*语：?\*\*/.test(raw)) {
    const m = raw.match(/\*\*语：?\*\*\s*\n+([\s\S]*)/);
    if (m) raw = m[1].trim();
  }
  // Strip common Markdown noise for length / title decisions.
  return raw
    .replace(/^>.*$/gm, '')
    .replace(/^#{1,6}\s+/gm, '')
    .replace(/\*\*([^*]+)\*\*/g, '$1')
    .replace(/\*([^*]+)\*/g, '$1')
    .replace(/`([^`]+)`/g, '$1')
    .replace(/\[([^\]]+)\]\([^)]+\)/g, '$1')
    .replace(/\s+/g, ' ')
    .trim();
}

function isLongInlineNote(plain) {
  if (!plain) return false;
  const runes = Array.from(plain);
  if (runes.length > INLINE_NOTE_MAX_RUNES) return true;
  // Approximate multi-sentence dumps even when under the rune budget.
  const sentences = plain.split(/[。！？.!?]+/).filter((part) => part.trim());
  return sentences.length > INLINE_NOTE_MAX_LINES;
}

function inlineNoteTitle(entry, plain) {
  const kind = entry?.kind || '';
  let title = plain.split(/[。！？\n]/)[0].trim() || plain;
  if (!title) {
    if (kind === 'summary') title = '总结';
    else if (kind === 'qa') title = 'Q&A';
    else if (kind === 'jp-study') title = '语';
    else title = '笔记';
  }
  const runes = Array.from(title);
  if (runes.length > INLINE_NOTE_TITLE_RUNES) {
    title = runes.slice(0, INLINE_NOTE_TITLE_RUNES - 1).join('') + '…';
  }
  return title;
}

function shortNoteRef(ref) {
  const value = String(ref || '').trim().replace(/-/g, '');
  if (!value) return '';
  return value.length >= 4 ? value.slice(-4) : value;
}

// Inline presentation: short notes keep their full body under the passage;
// long notes collapse to a titled hyperlink (full text stays in the model and
// edit textarea). Layout moves cards next to their anchors.
function renderLongNotePreviews() {
  const readingAnchor = captureReadingAnchor();
  elements.annotationLayer.classList.add('membox-notes-relayout');
  const entries = new Map(state.annotations.map((entry) => [String(entry.id), entry]));
  const cards = [
    ...elements.article.querySelectorAll('.annot-note[data-annot-id]'),
    ...elements.annotationLayer.querySelectorAll('.annot-note[data-annot-id]'),
  ];
  const seen = new Set();
  for (const card of cards) {
    const id = String(card.dataset.annotId || '');
    if (!id || seen.has(id)) continue;
    seen.add(id);
    const entry = entries.get(id);
    const body = card.querySelector('.annot-note-body');
    const text = body && body.querySelector('.annot-note-text');
    let link = body && body.querySelector('.membox-open-note');
    card.classList.remove('membox-note-preview', 'membox-note-link-only');
    delete card.dataset.exportRemoveClass;
    if (!entry || !text) {
      if (link) link.remove();
      continue;
    }
    // jp-study: UUID chip on the passage only — never show body/link card.
    if (entry.kind === 'jp-study' || card.classList.contains('is-jp-study')) {
      if (link) link.remove();
      continue;
    }

    // DeepSeek / assist Q&A always stays full inline under the passage — the
    // answer is the point of the note, not a titled link to another page.
    if (entry.kind === 'qa' || card.classList.contains('is-qa')) {
      text.hidden = false;
      if (link) link.remove();
      card.classList.remove('membox-note-preview', 'membox-note-link-only');
      continue;
    }

    const plain = notePlainForInline(entry);
    const long = isLongInlineNote(plain);
    if (!long) {
      text.hidden = false;
      if (link) link.remove();
      continue;
    }

    // Long note → titled hyperlink inline; hide the prose body.
    card.classList.add('membox-note-preview', 'membox-note-link-only');
    card.dataset.exportRemoveClass = 'membox-note-preview';
    text.hidden = true;
    const title = inlineNoteTitle(entry, plain);
    if (!link) {
      link = document.createElement('a');
      link.className = 'membox-open-note';
      link.target = '_blank';
      link.rel = 'noopener noreferrer';
      // Keep the titled link in PNG/site exports — it is the note content.
      link.addEventListener('click', (event) => event.stopPropagation());
      body.appendChild(link);
    }
    link.textContent = title;
    link.title = plain.slice(0, 200) + (plain.length > 200 ? '…' : '');
    if (entry.ref) {
      link.href = noteDocumentURL(entry.ref);
      const short = shortNoteRef(entry.ref);
      link.setAttribute('aria-label', short ? `打开笔记 ${short}：${title}` : `打开笔记：${title}`);
      link.classList.remove('is-pending');
    } else {
      link.removeAttribute('href');
      link.classList.add('is-pending');
      link.setAttribute('aria-label', `笔记保存中：${title}`);
    }
  }
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

// Restore applies notes with notify:false, so no miru-annotations-changed
// fires after a page load and long cards would stay expanded until the first
// edit or resize. document.js calls this once the restore has landed so the
// height clamp runs on the initial paint too.
export function scheduleNotePreviewPass() {
  scheduleLongNotePreviews();
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
