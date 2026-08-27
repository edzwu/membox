/* Related documents: a tile grid under the TOC showing the one-hop
   neighborhood of the current document, plus the picker modal that links an
   existing Markdown document, creates a new one, or (Ctrl+O) opens another
   document. */

import { elements } from '../../js/dom.js';
import { state } from '../../js/state.js';
import { showToast } from '../../js/ui/feedback.js';
import { syncTocBoxHeight } from '../../js/render/toc.js';
import { session } from './session.js';
import { registerModal, closeOtherModals } from './modals.js';
import { fetchRelated, postRelated, searchCandidates } from './api.js';
import { documentDisplayLabel } from './labels.js';
import { focusPickerNote, notePickerItems, renderNoteSourceBacklink } from './notes.js';
import { syncToMembox } from './sync.js';

let relatedPanel = null;
let relatedGrid = null;
let relatedModal = null;

let relatedSaving = false;
let relatedModalMode = 'existing';
let pickerPurpose = 'related';
let selectedRelatedCandidate = null;
let relatedSearchTimer = 0;
let relatedSearchController = null;
let relatedSearchGeneration = 0;
let relatedLoadGeneration = 0;

function notifyDocumentPickerState(open) {
  window.dispatchEvent(new CustomEvent('membox-document-picker-state', {
    detail: { open: Boolean(open) },
  }));
}

function createRelatedPanel() {
  const panel = document.createElement('div');
  panel.className = 'membox-related';
  panel.hidden = true;
  panel.innerHTML = '<div class="membox-related-title">Related</div><div class="membox-related-grid"></div>';
  // Live inside the TOC pane so pinned mode shows it beneath the outline
  // instead of a lone floating list.
  const pane = elements.tocPane || elements.toc.querySelector('.toc-pane') || elements.toc;
  pane.appendChild(panel);
  return panel;
}

function scheduleTocResize() {
  requestAnimationFrame(() => syncTocBoxHeight());
}

export function hideRelatedPanel() {
  relatedPanel.hidden = true;
  relatedGrid.textContent = '';
  elements.toc?.classList.remove('has-related');
  scheduleTocResize();
}

export async function loadRelated() {
  const generation = ++relatedLoadGeneration;
  if (!session.connected || !session.documentID) {
    hideRelatedPanel();
    return;
  }
  // Never let a previous document's footer participate in this document's
  // initial TOC measurement while the new related graph is loading.
  hideRelatedPanel();
  try {
    const data = await fetchRelated(session.documentID);
    if (generation !== relatedLoadGeneration) return;
    const items = Array.isArray(data.related) ? data.related : [];
    renderRelatedGrid(items);
    // Full note pages (opened from a long inline link) need an obvious return
    // path to the source passage — related tiles under the TOC are too easy to miss.
    renderNoteSourceBacklink(items);
  } catch (err) {
    if (generation !== relatedLoadGeneration) return;
    console.warn('membox: could not load related documents', err);
    hideRelatedPanel();
  }
}

function showRelatedTooltip(tile, tooltip) {
  const rect = tile.getBoundingClientRect();
  document.body.appendChild(tooltip);
  tooltip.classList.add('is-visible');
  // Measure after moving to body, then keep the tooltip inside the viewport.
  const tipRect = tooltip.getBoundingClientRect();
  const margin = 8;
  const left = Math.max(margin, Math.min(
    window.innerWidth - tipRect.width - margin,
    rect.left + (rect.width - tipRect.width) / 2,
  ));
  let top = rect.top - tipRect.height - margin;
  if (top < margin) top = rect.bottom + margin;
  tooltip.style.left = `${Math.round(left)}px`;
  tooltip.style.top = `${Math.round(top)}px`;
}

function hideRelatedTooltip(tile, tooltip) {
  tooltip.classList.remove('is-visible');
  tooltip.style.left = '';
  tooltip.style.top = '';
  if (tooltip.parentNode === document.body) tile.appendChild(tooltip);
}

function renderRelatedGrid(items) {
  relatedGrid.textContent = '';
  if (!items.length) {
    relatedPanel.hidden = true;
    elements.toc?.classList.remove('has-related');
    scheduleTocResize();
    return;
  }
  relatedPanel.hidden = false;
  elements.toc?.classList.add('has-related');
  for (const item of items) {
    const filename = String(item.path || '').split(/[\\/]/).pop();
    const label = documentDisplayLabel({
      filename,
      catalogTitle: item.title || '',
    }) || String(item.id);
    const tile = document.createElement('a');
    tile.className = 'membox-related-tile' + (item.annotation_ref ? ' is-source-backlink' : '');
    const targetURL = new URL('/', window.location.origin);
    targetURL.searchParams.set('id', item.id);
    if (item.annotation_ref) targetURL.searchParams.set('note', item.annotation_ref);
    tile.href = targetURL.href;
    tile.dataset.direction = item.direction === 'in' ? 'in' : 'out';
    if (item.annotation_ref) {
      tile.setAttribute('aria-label', `原文 ${label} (${item.id})`);
      tile.dataset.role = 'source-backlink';
    } else {
      tile.setAttribute('aria-label', `${label} (${item.id})`);
    }
    const tooltip = document.createElement('span');
    tooltip.className = 'membox-related-tooltip';
    const tooltipTitle = document.createElement('span');
    tooltipTitle.className = 'membox-related-tooltip-title';
    tooltipTitle.textContent = item.annotation_ref ? `← 原文 · ${label}` : label;
    const tooltipID = document.createElement('code');
    tooltipID.className = 'membox-related-tooltip-id';
    tooltipID.textContent = `membox · ${String(item.id).slice(-5)}`;
    tooltip.append(tooltipTitle, tooltipID);
    tile.appendChild(tooltip);
    tile.addEventListener('mouseenter', () => showRelatedTooltip(tile, tooltip));
    tile.addEventListener('mouseleave', () => hideRelatedTooltip(tile, tooltip));
    tile.addEventListener('focus', () => showRelatedTooltip(tile, tooltip));
    tile.addEventListener('blur', () => hideRelatedTooltip(tile, tooltip));
    relatedGrid.appendChild(tile);
  }
  scheduleTocResize();
}

// Modal for linking an existing document or creating a new one.
function createRelatedModal() {
  const backdrop = document.createElement('div');
  backdrop.className = 'membox-modal-backdrop';
  backdrop.hidden = true;
  backdrop.innerHTML = `
    <div class="membox-modal" role="dialog" aria-modal="true" aria-labelledby="membox-related-dialog-title">
      <div class="membox-modal-title" id="membox-related-dialog-title">Add related document</div>
      <div class="membox-modal-tabs" role="tablist" aria-label="Add related document">
        <button type="button" class="membox-modal-tab is-active" role="tab" aria-selected="true" data-mode="existing">Choose existing</button>
        <button type="button" class="membox-modal-tab" role="tab" aria-selected="false" data-mode="new">Create new</button>
        <button type="button" class="membox-modal-tab" role="tab" aria-selected="false" data-mode="notes" hidden>Notes <span class="membox-modal-tab-count" hidden>0</span></button>
      </div>
      <div class="membox-related-picker" data-panel="existing" role="tabpanel">
        <input class="membox-related-search" type="search" role="combobox" aria-autocomplete="list" aria-expanded="false" aria-controls="membox-related-options" placeholder="Search by title or UUID…" autocomplete="off" spellcheck="false">
        <div class="membox-related-options" id="membox-related-options" role="listbox" aria-label="Matching documents"></div>
        <div class="membox-modal-hint">Recently opened files appear first, followed by recently modified files.</div>
      </div>
      <div class="membox-related-create" data-panel="new" role="tabpanel" hidden>
        <input class="membox-modal-input" type="text" placeholder="Title" maxlength="200" spellcheck="false">
        <textarea class="membox-modal-body" placeholder="Paste related content (Markdown)…" spellcheck="false"></textarea>
        <div class="membox-modal-hint">Saves a new Markdown document linked to the current one · ⌘Enter to save</div>
      </div>
      <div class="membox-related-picker" data-panel="notes" role="tabpanel" hidden>
        <input class="membox-related-search membox-note-search" type="search" role="combobox" aria-autocomplete="list" aria-expanded="false" aria-controls="membox-picker-note-options" placeholder="Filter notes or quoted text…" autocomplete="off" spellcheck="false">
        <div class="membox-related-options membox-note-options" id="membox-picker-note-options" role="listbox" aria-label="Notes in this document"></div>
        <div class="membox-modal-hint">Select a note to jump to its passage.</div>
      </div>
      <div class="membox-modal-actions">
        <button type="button" class="membox-modal-btn membox-modal-cancel">Cancel</button>
        <button type="button" class="membox-modal-btn membox-modal-save" disabled>Link document</button>
      </div>
    </div>`;
  document.body.appendChild(backdrop);
  const modal = backdrop.querySelector('.membox-modal');
  const dialogTitle = backdrop.querySelector('.membox-modal-title');
  const tablist = backdrop.querySelector('.membox-modal-tabs');
  const searchInput = backdrop.querySelector('.membox-related-search');
  const options = backdrop.querySelector('.membox-related-options');
  const hint = backdrop.querySelector('.membox-related-picker .membox-modal-hint');
  const titleInput = backdrop.querySelector('.membox-modal-input');
  const bodyInput = backdrop.querySelector('.membox-modal-body');
  const noteSearchInput = backdrop.querySelector('.membox-note-search');
  const noteOptions = backdrop.querySelector('.membox-note-options');
  const primaryButton = backdrop.querySelector('.membox-modal-save');
  const cancelButton = backdrop.querySelector('.membox-modal-cancel');
  const tabs = Array.from(backdrop.querySelectorAll('.membox-modal-tab'));
  const panels = Array.from(backdrop.querySelectorAll('[data-panel]'));

  backdrop.addEventListener('mousedown', (event) => {
    if (event.target === backdrop) closeRelatedModal();
  });
  cancelButton.addEventListener('click', closeRelatedModal);
  primaryButton.addEventListener('click', submitPickerSelection);
  tabs.forEach((tab) => tab.addEventListener('click', () => setRelatedModalMode(tab.dataset.mode)));
  searchInput.addEventListener('input', scheduleRelatedSearch);
  searchInput.addEventListener('keydown', onRelatedSearchKeydown);
  noteSearchInput.addEventListener('input', renderPickerNoteOptions);
  noteSearchInput.addEventListener('keydown', onPickerNoteSearchKeydown);
  modal.addEventListener('keydown', (event) => {
    if (event.key === 'Escape') {
      event.stopPropagation();
      closeRelatedModal();
    } else if (relatedModalMode === 'new' && event.key === 'Enter' && (event.metaKey || event.ctrlKey)) {
      event.preventDefault();
      void saveRelated();
    }
  });
  return {
    backdrop, dialogTitle, tablist, searchInput, options, hint, titleInput, bodyInput,
    noteSearchInput, noteOptions, primaryButton, cancelButton, tabs, panels,
  };
}

function configurePickerTabs() {
  const open = pickerPurpose === 'open';
  const existingTab = relatedModal.tabs.find((tab) => tab.dataset.mode === 'existing');
  const newTab = relatedModal.tabs.find((tab) => tab.dataset.mode === 'new');
  const notesTab = relatedModal.tabs.find((tab) => tab.dataset.mode === 'notes');
  existingTab.textContent = open ? 'Documents' : 'Choose existing';
  newTab.textContent = 'Create new';
  newTab.hidden = open;
  notesTab.hidden = !open;
  relatedModal.tablist.setAttribute('aria-label', open ? 'Library sections' : 'Add related document');
  relatedModal.cancelButton.textContent = open ? 'Close' : 'Cancel';
}

function setRelatedModalMode(mode, focus = true) {
  relatedModalMode = pickerPurpose === 'open'
    ? mode === 'notes' ? 'notes' : 'existing'
    : mode === 'new' ? 'new' : 'existing';
  relatedModal.tabs.forEach((tab) => {
    const active = tab.dataset.mode === relatedModalMode;
    tab.classList.toggle('is-active', active);
    tab.setAttribute('aria-selected', String(active));
  });
  relatedModal.panels.forEach((panel) => { panel.hidden = panel.dataset.panel !== relatedModalMode; });
  relatedModal.primaryButton.hidden = relatedModalMode === 'notes';
  relatedModal.primaryButton.textContent = pickerPurpose === 'open'
    ? 'Open document'
    : relatedModalMode === 'existing' ? 'Link document' : 'Create';
  if (relatedModalMode === 'notes') renderPickerNoteOptions();
  updateRelatedPrimaryButton();
  if (focus) {
    const target = relatedModalMode === 'notes'
      ? relatedModal.noteSearchInput
      : relatedModalMode === 'existing' ? relatedModal.searchInput : relatedModal.titleInput;
    target.focus();
  }
}

function updateRelatedPrimaryButton() {
  const ready = relatedModalMode === 'existing'
    ? Boolean(selectedRelatedCandidate)
    : relatedModalMode === 'new';
  relatedModal.primaryButton.disabled = relatedSaving || !ready;
}

function renderRelatedOptions(items, message = '') {
  relatedModal.options.textContent = '';
  relatedModal.searchInput.setAttribute('aria-expanded', String(items.length > 0));
  if (!items.length) {
    const status = document.createElement('div');
    status.className = 'membox-related-option-status';
    status.textContent = message;
    relatedModal.options.appendChild(status);
    return;
  }
  for (const item of items) {
    const option = document.createElement('button');
    option.type = 'button';
    option.className = 'membox-related-option';
    option.setAttribute('role', 'option');
    option.setAttribute('aria-selected', String(selectedRelatedCandidate?.id === item.id));
    const title = document.createElement('span');
    title.className = 'membox-related-option-title';
    title.textContent = documentDisplayLabel({
      filename: item.path || '',
      catalogTitle: item.title || '',
    }) || item.id;
    const id = document.createElement('code');
    id.className = 'membox-related-option-id';
    id.textContent = item.id;
    const path = document.createElement('span');
    path.className = 'membox-related-option-path';
    path.textContent = item.path || '';
    option.append(title, id, path);
    option.addEventListener('click', () => selectRelatedCandidate(item, option));
    option.addEventListener('keydown', onRelatedOptionKeydown);
    relatedModal.options.appendChild(option);
  }
}

function renderPickerNoteOptions() {
  const query = relatedModal.noteSearchInput.value.trim().toLocaleLowerCase();
  const allItems = notePickerItems();
  const items = query
    ? allItems.filter((item) => `${item.note}\n${item.excerpt}\n${item.number}`.toLocaleLowerCase().includes(query))
    : allItems;
  const notesTab = relatedModal.tabs.find((tab) => tab.dataset.mode === 'notes');
  const count = notesTab.querySelector('.membox-modal-tab-count');
  count.textContent = String(allItems.length);
  count.hidden = allItems.length === 0;
  relatedModal.noteOptions.textContent = '';
  relatedModal.noteSearchInput.setAttribute('aria-expanded', String(items.length > 0));
  if (!items.length) {
    const status = document.createElement('div');
    status.className = 'membox-related-option-status';
    status.textContent = allItems.length ? 'No matching notes' : 'No notes in this document';
    relatedModal.noteOptions.appendChild(status);
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
    option.addEventListener('click', () => {
      closeRelatedModal();
      focusPickerNote(item.id);
    });
    option.addEventListener('keydown', onPickerNoteOptionKeydown);
    relatedModal.noteOptions.appendChild(option);
  }
}

function onPickerNoteSearchKeydown(event) {
  if (!['ArrowDown', 'Enter'].includes(event.key)) return;
  const first = relatedModal.noteOptions.querySelector('.membox-related-option');
  if (!first) return;
  event.preventDefault();
  if (event.key === 'Enter') first.click();
  else first.focus();
}

function onPickerNoteOptionKeydown(event) {
  if (!['ArrowDown', 'ArrowUp', 'Enter'].includes(event.key)) return;
  event.preventDefault();
  if (event.key === 'Enter') {
    event.currentTarget.click();
    return;
  }
  const options = Array.from(relatedModal.noteOptions.querySelectorAll('.membox-related-option'));
  const index = options.indexOf(event.currentTarget);
  const next = event.key === 'ArrowDown' ? options[index + 1] : options[index - 1];
  (next || relatedModal.noteSearchInput).focus();
}

function selectRelatedCandidate(item, option) {
  selectedRelatedCandidate = item;
  relatedModal.options.querySelectorAll('.membox-related-option').forEach((candidate) => {
    const selected = candidate === option;
    candidate.classList.toggle('is-selected', selected);
    candidate.setAttribute('aria-selected', String(selected));
  });
  updateRelatedPrimaryButton();
}

function scheduleRelatedSearch() {
  selectedRelatedCandidate = null;
  updateRelatedPrimaryButton();
  window.clearTimeout(relatedSearchTimer);
  if (relatedSearchController) relatedSearchController.abort();
  relatedSearchGeneration++;
  const query = relatedModal.searchInput.value.trim();
  if (!query) {
    renderRelatedOptions([], 'Loading files…');
    relatedSearchTimer = window.setTimeout(() => void searchRelatedCandidates(''), 0);
    return;
  }
  renderRelatedOptions([], 'Searching…');
  relatedSearchTimer = window.setTimeout(() => void searchRelatedCandidates(query), 180);
}

async function searchRelatedCandidates(query) {
  if (relatedSearchController) relatedSearchController.abort();
  relatedSearchController = new AbortController();
  const generation = ++relatedSearchGeneration;
  try {
    const data = await searchCandidates({
      purpose: pickerPurpose,
      documentID: session.documentID,
      query,
      signal: relatedSearchController.signal,
    });
    if (generation !== relatedSearchGeneration || relatedModal.backdrop.hidden) return;
    const candidates = Array.isArray(data.candidates) ? data.candidates : [];
    const emptyMessage = pickerPurpose === 'open'
      ? query ? 'No matching documents' : 'No available documents'
      : query ? 'No matching unlinked documents' : 'No available unlinked documents';
    renderRelatedOptions(candidates, candidates.length ? '' : emptyMessage);
  } catch (err) {
    if (err.name === 'AbortError') return;
    console.warn('membox: document picker search failed', err);
    renderRelatedOptions([], 'Could not search documents');
  }
}

function onRelatedSearchKeydown(event) {
  if (!['ArrowDown', 'Enter'].includes(event.key)) return;
  const first = relatedModal.options.querySelector('.membox-related-option');
  if (!first) return;
  event.preventDefault();
  if (event.key === 'Enter') {
    if (selectedRelatedCandidate) submitPickerSelection();
    else first.click();
  } else {
    first.focus();
  }
}

function onRelatedOptionKeydown(event) {
  if (!['ArrowDown', 'ArrowUp', 'Enter'].includes(event.key)) return;
  event.preventDefault();
  if (event.key === 'Enter') {
    if (event.currentTarget.getAttribute('aria-selected') === 'true') submitPickerSelection();
    else event.currentTarget.click();
    return;
  }
  const options = Array.from(relatedModal.options.querySelectorAll('.membox-related-option'));
  const index = options.indexOf(event.currentTarget);
  const next = event.key === 'ArrowDown' ? options[index + 1] : options[index - 1];
  (next || relatedModal.searchInput).focus();
}

function openPicker(purpose) {
  if (!session.connected || (purpose === 'related' && !session.documentID)) return;
  closeOtherModals();
  pickerPurpose = purpose === 'open' ? 'open' : 'related';
  relatedModal.backdrop.dataset.pickerPurpose = pickerPurpose;
  relatedModal.dialogTitle.textContent = pickerPurpose === 'open' ? 'Library' : 'Add related document';
  configurePickerTabs();
  relatedModal.hint.textContent = pickerPurpose === 'open'
    ? 'Recently opened first · remaining files are sorted by recent changes · Ctrl+O'
    : 'Recently opened first · remaining files are sorted by recent changes.';
  relatedModal.searchInput.value = '';
  relatedModal.noteSearchInput.value = '';
  relatedModal.titleInput.value = '';
  relatedModal.bodyInput.value = '';
  selectedRelatedCandidate = null;
  relatedModal.backdrop.hidden = false;
  notifyDocumentPickerState(pickerPurpose === 'open');
  if (pickerPurpose === 'open') renderPickerNoteOptions();
  setRelatedModalMode('existing');
  renderRelatedOptions([], 'Loading files…');
  void searchRelatedCandidates('');
}

export function openRelatedModal() {
  openPicker('related');
}

export function openDocumentPicker() {
  openPicker('open');
}

// Ctrl+O entry point shared with the composition root's keydown handler.
export function handleOpenShortcut() {
  if (relatedModal && !relatedModal.backdrop.hidden && pickerPurpose === 'open') {
    setRelatedModalMode('existing');
    return;
  }
  openDocumentPicker();
}

function closeRelatedModal() {
  const documentPickerWasOpen = pickerPurpose === 'open' && !relatedModal.backdrop.hidden;
  window.clearTimeout(relatedSearchTimer);
  if (relatedSearchController) relatedSearchController.abort();
  relatedSearchGeneration++;
  relatedModal.backdrop.hidden = true;
  if (documentPickerWasOpen) notifyDocumentPickerState(false);
}

function submitPickerSelection() {
  if (relatedModalMode === 'notes') return;
  if (pickerPurpose === 'open') void openSelectedDocument();
  else if (relatedModalMode === 'existing') void linkExistingRelated();
  else void saveRelated();
}

async function openSelectedDocument() {
  if (relatedSaving || !selectedRelatedCandidate) return;
  const targetID = selectedRelatedCandidate.id;
  relatedSaving = true;
  updateRelatedPrimaryButton();
  try {
    const currentNeedsSave = Boolean((state.currentMarkdown || '').trim()) && (!session.documentID || session.annotationsDirty);
    if (currentNeedsSave) {
      showToast('Saving current document before switching…');
      await syncToMembox();
      if (!session.documentID || session.annotationsDirty) {
        showToast('Could not switch because the current document is not saved');
        return;
      }
    }
    window.location.assign(`/?id=${encodeURIComponent(targetID)}`);
  } finally {
    relatedSaving = false;
    updateRelatedPrimaryButton();
  }
}

async function linkExistingRelated() {
  if (relatedSaving || !selectedRelatedCandidate) return;
  relatedSaving = true;
  updateRelatedPrimaryButton();
  try {
    const result = await postRelated(session.documentID, { target_id: selectedRelatedCandidate.id });
    closeRelatedModal();
    showToast(`Linked related document: ${result.title || String(result.id).slice(-5)}`);
    void loadRelated();
  } catch (err) {
    console.error('membox: linking related document failed', err);
    showToast(`Could not link document: ${err.message}`);
  } finally {
    relatedSaving = false;
    updateRelatedPrimaryButton();
  }
}

async function saveRelated() {
  if (relatedSaving) return;
  const title = relatedModal.titleInput.value.trim();
  const body = relatedModal.bodyInput.value;
  if (!title) {
    showToast('Give the related document a title');
    relatedModal.titleInput.focus();
    return;
  }
  if (!body.trim()) {
    showToast('Paste some content first');
    relatedModal.bodyInput.focus();
    return;
  }
  relatedSaving = true;
  updateRelatedPrimaryButton();
  try {
    const result = await postRelated(session.documentID, { title, body });
    closeRelatedModal();
    showToast(`Created related document: ${String(result.id).slice(0, 8)}`);
    void loadRelated();
  } catch (err) {
    console.error('membox: creating related document failed', err);
    showToast(`Could not create related document: ${err.message}`);
  } finally {
    relatedSaving = false;
    updateRelatedPrimaryButton();
  }
}

export function initRelated() {
  relatedPanel = createRelatedPanel();
  relatedGrid = relatedPanel.querySelector('.membox-related-grid');
  relatedModal = createRelatedModal();
  registerModal({
    isOpen: () => !relatedModal.backdrop.hidden,
    close: closeRelatedModal,
  });
  const refreshNotes = () => {
    if (!relatedModal.backdrop.hidden && pickerPurpose === 'open') renderPickerNoteOptions();
  };
  window.addEventListener('miru-annotations-changed', refreshNotes);
  window.addEventListener('miru-annotations-saved', refreshNotes);
}
