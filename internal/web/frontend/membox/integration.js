/* membox adapter for Miru.
   This is intentionally outside frontend/miru: it adds connection state,
   backend loading, and sync semantics without coupling the Miru source tree to
   membox. */

import { elements } from '../js/dom.js';
import { state } from '../js/state.js';
import { loadDocument } from '../js/document.js';
import { isEditableTarget, sanitizeFilename } from '../js/utils.js';
import { showToast, flashButton, writeClipboard } from '../js/ui/feedback.js';
import { updateMarkdownDownloadControl } from '../js/ui/chrome.js';
import { markAnnotationsSaved } from '../js/annotations/session.js';
import {
  buildAnnotationSidecar,
  parseAnnotationSidecar,
  restoreAnnotationSidecar,
  verifyAnnotationSource,
} from '../js/annotations/sidecar.js';

const style = document.createElement('link');
style.rel = 'stylesheet';
style.href = new URL('./membox.css', import.meta.url).href;
document.head.appendChild(style);

let connected = false;
let connecting = true;
let syncing = false;
let documentID = new URLSearchParams(window.location.search).get('id') || '';
let loadedMarkdown = '';
// Wipe protection: when a sidecar loaded with annotations but none survived
// restoration and the user did not delete any, never overwrite the stored
// notes with an empty set (keep the saved ones, update progress only).
let loadedAnnotationCount = 0;
let annotationsMutated = false;
let annotationsDirty = false;
let saveInFlight = false;
let savePending = false;
let saveAbortController = null;
// Anchors that failed to re-anchor on load. Kept so the next save merges
// them back into the sidecar instead of silently dropping them.
let pendingAnchors = [];
// Newest annotation updated_at (ms) this session has seen. Sent back on
// replace-saves so the server refuses to delete notes that appeared after
// this tab loaded (created or restored elsewhere).
let loadedRevision = 0;

function createConnectionButton() {
  const button = document.createElement('button');
  button.type = 'button';
  button.id = 'membox-connection';
  button.className = 'action membox-connection';
  button.dataset.connected = 'false';
  button.innerHTML = `
    <svg viewBox="0 0 24 24" aria-hidden="true">
      <circle cx="7" cy="12" r="3.25" fill="none" stroke="currentColor" stroke-width="1.5"/>
      <circle cx="17" cy="12" r="3.25" fill="none" stroke="currentColor" stroke-width="1.5"/>
      <path class="connection-bridge" d="M10.25 12h3.5" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/>
      <path class="connection-slash" d="M5 5l14 14" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/>
    </svg>
    <span class="sr-only">membox connection</span>`;
  elements.tocToggle.insertAdjacentElement('afterend', button);
  button.addEventListener('click', toggleConnection);
  return button;
}

const connectionButton = createConnectionButton();

function createDocumentSwitcher() {
  const button = document.createElement('button');
  button.type = 'button';
  button.id = 'membox-document-switcher';
  button.className = 'membox-document-switcher';
  button.hidden = true;
  button.title = 'Switch document · Ctrl+O';
  button.setAttribute('aria-label', 'Switch document');
  button.innerHTML = `
    <svg class="membox-document-switcher-icon" viewBox="0 0 24 24" aria-hidden="true">
      <path d="M7 7V5a2 2 0 0 1 2-2h8l3 3v11a2 2 0 0 1-2 2h-2" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/>
      <rect x="4" y="7" width="12" height="14" rx="2" fill="none" stroke="currentColor" stroke-width="1.5"/>
      <path d="M7 12h6M7 16h6" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/>
    </svg>
    <span class="membox-document-switcher-label">Open document</span>
    <svg class="membox-document-switcher-chevron" viewBox="0 0 16 16" aria-hidden="true">
      <path d="M4 6l4 4 4-4" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/>
    </svg>`;
  document.querySelector('.topbar-center')?.appendChild(button);
  button.addEventListener('click', openDocumentPicker);
  return button;
}

const documentSwitcher = createDocumentSwitcher();

function renderDocumentSwitcher() {
  documentSwitcher.hidden = !connected;
  const liveTitle = document.getElementById('doc-title')?.textContent?.replace(/\s+/g, ' ').trim();
  const title = liveTitle || state.docTitle || 'Open document';
  documentSwitcher.querySelector('.membox-document-switcher-label').textContent = title;
  documentSwitcher.setAttribute('aria-label', documentID ? `Switch document from ${title}` : 'Open document');
}

new MutationObserver(renderDocumentSwitcher).observe(elements.article, {
  childList: true,
  subtree: true,
  characterData: true,
});

let titleBeforeEdit = '';
elements.article.addEventListener('focusin', (event) => {
  if (event.target.id !== 'doc-title') return;
  titleBeforeEdit = event.target.textContent.replace(/\s+/g, ' ').trim() || state.docTitle || 'Untitled';
});
elements.article.addEventListener('focusout', (event) => {
  if (event.target.id !== 'doc-title') return;
  const nextTitle = (state.docTitle || '').trim() || 'Untitled';
  if (nextTitle === titleBeforeEdit || !documentID) return;
  if (!connected) {
    state.docTitle = titleBeforeEdit;
    event.target.textContent = titleBeforeEdit;
    showToast('Connect to membox before renaming this document');
    return;
  }
  void renameCurrentDocument(event.target, titleBeforeEdit, nextTitle);
});

async function renameCurrentDocument(titleElement, previousTitle, nextTitle) {
  const renamedDocumentID = documentID;
  const currentFilename = state.droppedFilename || '';
  const extensionMatch = currentFilename.match(/\.(?:md|markdown)$/i);
  const extension = extensionMatch ? extensionMatch[0] : '.md';
  const filename = `${sanitizeFilename(nextTitle)}${extension}`;
  titleElement.contentEditable = 'false';
  titleElement.dataset.saving = 'true';
  try {
    const response = await fetch(`/api/doc/${encodeURIComponent(renamedDocumentID)}/rename`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ filename }),
    });
    if (!response.ok) throw new Error((await response.text()).trim() || `HTTP ${response.status}`);
    const result = await response.json();
    if (documentID !== renamedDocumentID) return;
    const savedFilename = result.filename || filename;
    const savedTitle = savedFilename.replace(/\.(?:md|markdown)$/i, '') || nextTitle;
    state.droppedFilename = savedFilename;
    state.docTitle = savedTitle;
    titleElement.textContent = savedTitle;
    document.title = `${savedTitle} — membox`;
    renderDocumentSwitcher();
    showToast(`Renamed document to ${savedFilename}`);
  } catch (err) {
    if (documentID !== renamedDocumentID) return;
    state.docTitle = previousTitle;
    titleElement.textContent = previousTitle;
    renderDocumentSwitcher();
    console.error('membox: document rename failed', err);
    showToast(`Could not rename document: ${err.message}`);
  } finally {
    if (titleElement.isConnected) {
      titleElement.contentEditable = 'true';
      delete titleElement.dataset.saving;
    }
  }
}

// Bottom-left cluster: save/copy status plus the “add related document”
// button. A hollow circle means there are changes to save; a filled circle
// means the document is durable and can be clicked to copy its UUID.
function createStatusCluster() {
  const cluster = document.createElement('div');
  cluster.className = 'membox-status-cluster';
  cluster.hidden = true;
  document.body.appendChild(cluster);
  return cluster;
}

const statusCluster = createStatusCluster();

function createStatusBadge() {
  const badge = document.createElement('button');
  badge.type = 'button';
  badge.id = 'membox-doc-status';
  badge.className = 'membox-doc-status';
  badge.hidden = true;
  badge.innerHTML = '<span class="membox-status-dot" aria-hidden="true"></span><span class="membox-status-label" aria-hidden="true"></span>';
  statusCluster.appendChild(badge);
  badge.addEventListener('click', () => {
    if (syncing || saveInFlight) return;
    const saved = Boolean(documentID) && !annotationsDirty;
    if (!saved) {
      void syncToMembox();
      return;
    }
    writeClipboard(documentID, () => showToast('Copied document ID'));
  });
  return badge;
}

const statusBadge = createStatusBadge();

function createAddRelatedButton() {
  const button = document.createElement('button');
  button.type = 'button';
  button.id = 'membox-add-related';
  button.className = 'membox-add-related';
  button.hidden = true;
  button.textContent = '+';
  button.title = 'Add related document';
  button.setAttribute('aria-label', 'Add related document');
  statusCluster.appendChild(button);
  button.addEventListener('click', openRelatedModal);
  return button;
}

const addRelatedButton = createAddRelatedButton();

function renderDocStatus() {
  if (!connected) {
    statusCluster.hidden = true;
    statusBadge.hidden = true;
    addRelatedButton.hidden = true;
    hideRelatedPanel();
    return;
  }
  statusCluster.hidden = false;
  statusBadge.hidden = false;
  const saved = Boolean(documentID) && !annotationsDirty;
  const saving = syncing || saveInFlight;
  const label = statusBadge.querySelector('.membox-status-label');
  statusBadge.dataset.saved = String(saved);
  statusBadge.dataset.saving = String(saving);
  statusBadge.dataset.hasDocument = String(Boolean(documentID));
  statusBadge.disabled = saving;
  if (saved) {
    const suffix = documentID.slice(-5);
    label.textContent = suffix;
    statusBadge.setAttribute('aria-label', `Copy document ID ${documentID}`);
    statusBadge.title = suffix;
  } else {
    label.textContent = '';
    statusBadge.setAttribute('aria-label', saving ? 'Saving changes' : 'Save changes');
    statusBadge.title = saving ? 'Saving changes…' : 'Unsaved changes · click to save';
  }
  if (documentID) {
    addRelatedButton.hidden = false;
    void loadRelated();
  } else {
    addRelatedButton.hidden = true;
    hideRelatedPanel();
  }
}

// ---------------------------------------------------------------------------
// Related documents: a tile grid under the TOC showing the one-hop
// neighborhood of the current document, plus a “+” flow that links an
// existing Markdown document or creates a new one.
// ---------------------------------------------------------------------------
function createRelatedPanel() {
  const panel = document.createElement('div');
  panel.className = 'membox-related';
  panel.hidden = true;
  panel.innerHTML = '<div class="membox-related-title">Related</div><div class="membox-related-grid"></div>';
  elements.toc.appendChild(panel);
  return panel;
}

const relatedPanel = createRelatedPanel();
const relatedGrid = relatedPanel.querySelector('.membox-related-grid');

function hideRelatedPanel() {
  relatedPanel.hidden = true;
  relatedGrid.textContent = '';
}

async function loadRelated() {
  if (!connected || !documentID) {
    hideRelatedPanel();
    return;
  }
  try {
    const response = await fetch(`/api/doc/${encodeURIComponent(documentID)}/related`, { cache: 'no-store' });
    if (!response.ok) throw new Error((await response.text()) || `HTTP ${response.status}`);
    const data = await response.json();
    renderRelatedGrid(Array.isArray(data.related) ? data.related : []);
  } catch (err) {
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
    return;
  }
  relatedPanel.hidden = false;
  for (const item of items) {
    const filename = String(item.path || '').split(/[\\/]/).pop();
    const label = String(item.title || filename || item.id).trim() || filename;
    const tile = document.createElement('a');
    tile.className = 'membox-related-tile';
    tile.href = `/?id=${encodeURIComponent(item.id)}`;
    tile.dataset.direction = item.direction === 'in' ? 'in' : 'out';
    tile.setAttribute('aria-label', `${label} (${item.id})`);
    const tooltip = document.createElement('span');
    tooltip.className = 'membox-related-tooltip';
    const tooltipTitle = document.createElement('span');
    tooltipTitle.className = 'membox-related-tooltip-title';
    tooltipTitle.textContent = label;
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
      </div>
      <div class="membox-related-picker" data-panel="existing" role="tabpanel">
        <input class="membox-related-search" type="search" role="combobox" aria-autocomplete="list" aria-expanded="false" aria-controls="membox-related-options" placeholder="Search by title or UUID\u2026" autocomplete="off" spellcheck="false">
        <div class="membox-related-options" id="membox-related-options" role="listbox" aria-label="Matching documents"></div>
        <div class="membox-modal-hint">Recently opened files appear first, followed by recently modified files.</div>
      </div>
      <div class="membox-related-create" data-panel="new" role="tabpanel" hidden>
        <input class="membox-modal-input" type="text" placeholder="Title" maxlength="200" spellcheck="false">
        <textarea class="membox-modal-body" placeholder="Paste related content (Markdown)\u2026" spellcheck="false"></textarea>
        <div class="membox-modal-hint">Saves a new Markdown document linked to the current one \u00b7 \u2318Enter to save</div>
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
  const primaryButton = backdrop.querySelector('.membox-modal-save');
  const tabs = Array.from(backdrop.querySelectorAll('.membox-modal-tab'));
  const panels = Array.from(backdrop.querySelectorAll('[data-panel]'));

  backdrop.addEventListener('mousedown', (event) => {
    if (event.target === backdrop) closeRelatedModal();
  });
  backdrop.querySelector('.membox-modal-cancel').addEventListener('click', closeRelatedModal);
  primaryButton.addEventListener('click', submitPickerSelection);
  tabs.forEach((tab) => tab.addEventListener('click', () => setRelatedModalMode(tab.dataset.mode)));
  searchInput.addEventListener('input', scheduleRelatedSearch);
  searchInput.addEventListener('keydown', onRelatedSearchKeydown);
  modal.addEventListener('keydown', (event) => {
    if (event.key === 'Escape') {
      event.stopPropagation();
      closeRelatedModal();
    } else if (relatedModalMode === 'new' && event.key === 'Enter' && (event.metaKey || event.ctrlKey)) {
      event.preventDefault();
      void saveRelated();
    }
  });
  return { backdrop, dialogTitle, tablist, searchInput, options, hint, titleInput, bodyInput, primaryButton, tabs, panels };
}

const relatedModal = createRelatedModal();
let relatedSaving = false;
let relatedModalMode = 'existing';
let pickerPurpose = 'related';
let selectedRelatedCandidate = null;
let relatedSearchTimer = 0;
let relatedSearchController = null;
let relatedSearchGeneration = 0;

function setRelatedModalMode(mode, focus = true) {
  relatedModalMode = pickerPurpose === 'open' ? 'existing' : mode === 'new' ? 'new' : 'existing';
  relatedModal.tabs.forEach((tab) => {
    const active = tab.dataset.mode === relatedModalMode;
    tab.classList.toggle('is-active', active);
    tab.setAttribute('aria-selected', String(active));
  });
  relatedModal.panels.forEach((panel) => { panel.hidden = panel.dataset.panel !== relatedModalMode; });
  relatedModal.primaryButton.textContent = pickerPurpose === 'open'
    ? 'Open document'
    : relatedModalMode === 'existing' ? 'Link document' : 'Create';
  updateRelatedPrimaryButton();
  if (focus) {
    (relatedModalMode === 'existing' ? relatedModal.searchInput : relatedModal.titleInput).focus();
  }
}

function updateRelatedPrimaryButton() {
  const ready = relatedModalMode === 'existing' ? Boolean(selectedRelatedCandidate) : true;
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
    title.textContent = item.title || item.path || item.id;
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
    renderRelatedOptions([], 'Loading files\u2026');
    relatedSearchTimer = window.setTimeout(() => void searchRelatedCandidates(''), 0);
    return;
  }
  renderRelatedOptions([], 'Searching\u2026');
  relatedSearchTimer = window.setTimeout(() => void searchRelatedCandidates(query), 180);
}

async function searchRelatedCandidates(query) {
  if (relatedSearchController) relatedSearchController.abort();
  relatedSearchController = new AbortController();
  const generation = ++relatedSearchGeneration;
  try {
    const params = new URLSearchParams({
      limit: '100',
      purpose: pickerPurpose === 'open' ? 'open' : 'related',
    });
    if (query) params.set('q', query);
    if (pickerPurpose === 'open' && documentID) params.set('focus', documentID);
    const endpoint = pickerPurpose === 'open'
      ? '/api/documents/candidates'
      : `/api/doc/${encodeURIComponent(documentID)}/related/candidates`;
    const response = await fetch(`${endpoint}?${params}`, {
      cache: 'no-store',
      signal: relatedSearchController.signal,
    });
    if (!response.ok) throw new Error((await response.text()) || `HTTP ${response.status}`);
    const data = await response.json();
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
  if (!connected || (purpose === 'related' && !documentID)) return;
  pickerPurpose = purpose === 'open' ? 'open' : 'related';
  relatedModal.dialogTitle.textContent = pickerPurpose === 'open' ? 'Open document' : 'Add related document';
  relatedModal.tablist.hidden = pickerPurpose === 'open';
  relatedModal.hint.textContent = pickerPurpose === 'open'
    ? 'Recently opened first · remaining files are sorted by recent changes · Ctrl+O'
    : 'Recently opened first · remaining files are sorted by recent changes.';
  relatedModal.searchInput.value = '';
  relatedModal.titleInput.value = '';
  relatedModal.bodyInput.value = '';
  selectedRelatedCandidate = null;
  relatedModal.backdrop.hidden = false;
  setRelatedModalMode('existing');
  renderRelatedOptions([], 'Loading files\u2026');
  void searchRelatedCandidates('');
}

function openRelatedModal() {
  openPicker('related');
}

function openDocumentPicker() {
  openPicker('open');
}

function closeRelatedModal() {
  window.clearTimeout(relatedSearchTimer);
  if (relatedSearchController) relatedSearchController.abort();
  relatedSearchGeneration++;
  relatedModal.backdrop.hidden = true;
}

function submitPickerSelection() {
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
    const currentNeedsSave = Boolean((state.currentMarkdown || '').trim()) && (!documentID || annotationsDirty);
    if (currentNeedsSave) {
      showToast('Saving current document before switching…');
      await syncToMembox();
      if (!documentID || annotationsDirty) {
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
    const response = await fetch(`/api/doc/${encodeURIComponent(documentID)}/related`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ target_id: selectedRelatedCandidate.id }),
    });
    if (!response.ok) throw new Error((await response.text()) || `HTTP ${response.status}`);
    const result = await response.json();
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
    const response = await fetch(`/api/doc/${encodeURIComponent(documentID)}/related`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ title, body }),
    });
    if (!response.ok) throw new Error((await response.text()) || `HTTP ${response.status}`);
    const result = await response.json();
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

function setDownloadMeaning() {
  if (!connected) {
    updateMarkdownDownloadControl();
    return;
  }
  const label = syncing
    ? 'Syncing to membox…'
    : annotationsDirty
      ? 'Sync Markdown and unsaved notes to membox'
      : 'Sync Markdown and notes to membox';
  elements.downloadAll.setAttribute('aria-label', label);
  elements.downloadAll.title = label;
  elements.downloadAll.classList.toggle('is-syncing', syncing);
}

function renderConnection() {
  connectionButton.disabled = connecting;
  connectionButton.dataset.connected = String(connected);
  connectionButton.dataset.sync = annotationsDirty ? 'dirty' : 'clean';
  connectionButton.classList.toggle('is-connecting', connecting);
  const label = connecting
    ? 'Checking membox connection…'
    : connected
      ? annotationsDirty
        ? 'Connected to membox — notes waiting to sync'
        : 'Connected to membox — click to disconnect'
      : annotationsDirty
        ? 'Disconnected — notes remain available in this session'
        : 'Disconnected from membox — click to connect';
  connectionButton.setAttribute('aria-label', label);
  connectionButton.title = label;
  setDownloadMeaning();
  renderDocStatus();
  renderDocumentSwitcher();
}

async function backendAvailable() {
  try {
    const response = await fetch('/api/status', { cache: 'no-store' });
    return response.ok;
  } catch (err) {
    return false;
  }
}

async function toggleConnection() {
  if (connecting) return;
  if (connected) {
    connected = false;
    if (sidecarSaveTimer) {
      clearTimeout(sidecarSaveTimer);
      sidecarSaveTimer = null;
    }
    if (saveAbortController) saveAbortController.abort();
    renderConnection();
    showToast(annotationsDirty
      ? 'Disconnected — notes remain available in this session'
      : 'Disconnected from membox — arrow downloads locally');
    return;
  }
  connecting = true;
  renderConnection();
  connected = await backendAvailable();
  connecting = false;
  renderConnection();
  if (connected && annotationsDirty) {
    showToast('Connected to membox — syncing session notes');
    savePending = false;
    if (documentID) scheduleReadingStateSave();
    else void autoCreateForAnnotations();
  } else {
    showToast(connected ? 'Connected to membox — arrow now syncs' : 'Could not connect to membox');
  }
}

function replaceDocumentID(id) {
  documentID = id || '';
  const url = new URL(window.location.href);
  if (documentID) url.searchParams.set('id', documentID);
  else url.searchParams.delete('id');
  window.history.replaceState(null, '', url);
  renderDocStatus();
}

// ---------------------------------------------------------------------------
// Reading state and annotations share Miru's sidecar-shaped wire DTO, but not
// a persistence model: progress is DB UI state, while every annotation is an
// individual *-note.md document plus a normalized UUID/anchor relation. The
// source Markdown itself is only written by an explicit sync.
// ---------------------------------------------------------------------------
let restoring = false;
let sidecarSaveTimer = null;

function currentProgress() {
  return { y: Math.round(window.scrollY), at: new Date().toISOString() };
}

function sidecarFilename() {
  return state.droppedFilename || sanitizeFilename(state.docTitle || 'document') + '.md';
}

// Merge back annotations that could not be re-anchored this load so no save
// (auto or explicit sync) ever destroys notes it merely failed to display.
// Dedupe by excerpt AND note text: several notes can share one passage, and
// keying by excerpt alone would drop every sibling of a restored note.
function pendingAnchorKey(a) {
  return (a.exact || '') + '\u0000' + (a.note || '');
}

function mergePendingAnchors(sidecar) {
  if (!pendingAnchors.length) return;
  const have = new Set(sidecar.annotations.map((a) => pendingAnchorKey(a)));
  const kept = pendingAnchors.filter((a) => !have.has(pendingAnchorKey(a)));
  if (kept.length) {
    sidecar.annotations = [...sidecar.annotations, ...kept].sort((a, b) => a.start - b.start);
  }
}

function assertAnnotationReplacement(result) {
  if (result && result.replacement_applied === false) {
    throw new Error('Notes changed in another session; reload before replacing them');
  }
}

function applySavedAnnotationRefs(saved, sessionId) {
  if (sessionId !== state.annotationSessionId || !Array.isArray(saved)) return;
  const refsByClientID = new Map(saved
    .filter((item) => item && item.clientId && item.ref)
    .map((item) => [item.clientId, item.ref]));
  for (const entry of state.annotations) {
    const ref = refsByClientID.get(entry.clientId);
    if (ref) entry.ref = ref;
  }
}

async function persistReadingState(keepalive) {
  if (!connected || !documentID || !state.currentMarkdown) return;
  if (saveInFlight) {
    savePending = true;
    return;
  }
  saveInFlight = true;
  renderDocStatus();
  saveAbortController = new AbortController();
  const savedSessionId = state.annotationSessionId;
  const savedVersion = state.annotationVersion;
  try {
    const sidecar = await buildAnnotationSidecar(sidecarFilename(), state.currentMarkdown, currentProgress());
    if (!connected || savedSessionId !== state.annotationSessionId) return;
    // Only an actual annotation mutation makes the submitted set authoritative
    // for deletions. Scroll/progress saves must never delete note documents.
    sidecar.replaceAnnotations = annotationsMutated;
    sidecar.revision = loadedRevision;
    mergePendingAnchors(sidecar);
    // Wipe protection: annotations loaded, none survived, and the user never
    // deleted any → keep the stored annotations, only refresh progress.
    if (sidecar.annotations.length === 0 && loadedAnnotationCount > 0 && !annotationsMutated) {
      try {
        const resp = await fetch(`/api/doc/${encodeURIComponent(documentID)}/annotations`, { cache: 'no-store' });
        if (resp.ok && resp.status !== 204) {
          const existing = await resp.json();
          if (existing && Array.isArray(existing.annotations) && existing.annotations.length) {
            sidecar.annotations = existing.annotations;
          }
        }
      } catch (err) {
        /* keep whatever we have */
      }
    }
    const response = await fetch(`/api/doc/${encodeURIComponent(documentID)}/annotations`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(sidecar),
      keepalive: !!keepalive,
      signal: saveAbortController.signal,
    });
    if (!response.ok) throw new Error((await response.text()) || `HTTP ${response.status}`);
    const result = await response.json();
    if (savedSessionId !== state.annotationSessionId) return;
    applySavedAnnotationRefs(result && result.annotations, savedSessionId);
    assertAnnotationReplacement(result);
    if (result && Number.isFinite(Number(result.revision))) {
      loadedRevision = Math.max(loadedRevision, Number(result.revision));
    }
    markAnnotationsSaved(savedSessionId, savedVersion);
    annotationsDirty = savedSessionId === state.annotationSessionId && savedVersion < state.annotationVersion;
    if (!annotationsDirty) annotationsMutated = false;
    if (annotationsDirty) savePending = true;
    renderConnection();
  } catch (err) {
    annotationsDirty = annotationsDirty || savedVersion > state.annotationSavedVersion;
    renderConnection();
    if (err.name !== 'AbortError') {
      console.error('membox: failed to save reading state; notes remain in this session', err);
    }
  } finally {
    saveAbortController = null;
    saveInFlight = false;
    renderDocStatus();
    if (savePending && connected) {
      savePending = false;
      void persistReadingState(false);
    }
  }
}

function scheduleReadingStateSave() {
  if (!connected || !documentID || restoring) return;
  if (sidecarSaveTimer) clearTimeout(sidecarSaveTimer);
  sidecarSaveTimer = setTimeout(() => {
    sidecarSaveTimer = null;
    void persistReadingState(false);
  }, 800);
}

function flushReadingStateSave() {
  if (sidecarSaveTimer) {
    clearTimeout(sidecarSaveTimer);
    sidecarSaveTimer = null;
  }
  if (!connected) return;
  void persistReadingState(true);
}

let pendingProgressY = 0;

function applyProgressScroll() {
  if (!pendingProgressY) return;
  const max = Math.max(0, document.documentElement.scrollHeight - window.innerHeight);
  // 'auto' overrides the page-wide smooth-scroll so the restore is instant.
  window.scrollTo({ top: Math.min(pendingProgressY, max), behavior: 'auto' });
}

function restoreProgress(progress) {
  pendingProgressY = progress && progress.y > 0 ? progress.y : 0;
  if (!pendingProgressY) return;
  applyProgressScroll();
  // Note cards lay out asynchronously and images arrive later, so re-apply
  // once on the next frame and again when the page finishes loading.
  requestAnimationFrame(applyProgressScroll);
}

window.addEventListener('scroll', scheduleReadingStateSave, { passive: true });
window.addEventListener('pagehide', flushReadingStateSave);
window.addEventListener('load', applyProgressScroll);
window.addEventListener('miru-annotations-changed', () => {
  if (restoring) return;
  annotationsMutated = true;
  annotationsDirty = true;
  renderConnection();
  if (!connected) return;
  if (!documentID) {
    void autoCreateForAnnotations();
    return;
  }
  scheduleReadingStateSave();
});
document.addEventListener('visibilitychange', () => {
  if (document.visibilityState === 'hidden') flushReadingStateSave();
});

function unbindDocument() {
  replaceDocumentID('');
  if (sidecarSaveTimer) {
    clearTimeout(sidecarSaveTimer);
    sidecarSaveTimer = null;
  }
  if (saveAbortController) saveAbortController.abort();
  loadedMarkdown = '';
  pendingAnchors = [];
  loadedAnnotationCount = 0;
  annotationsMutated = false;
  annotationsDirty = false;
  savePending = false;
  loadedRevision = 0;
}

// Re-read the server revision after operations that bypass the annotation
// POST response (explicit sync, auto-create).
async function refreshRevision(id) {
  try {
    const response = await fetch(`/api/doc/${encodeURIComponent(id)}/annotations`, { cache: 'no-store' });
    if (response.status === 204 || !response.ok) return;
    loadedRevision = Number((await response.json()).revision) || 0;
  } catch (err) {
    /* keep the previous revision */
  }
}

async function restoreReadingState(id, markdown) {
  pendingAnchors = [];
  try {
    const response = await fetch(`/api/doc/${encodeURIComponent(id)}/annotations`, { cache: 'no-store' });
    if (response.status === 204 || !response.ok) return;
    const sidecarText = await response.text();
    if (!sidecarText.trim()) return;
    try {
      loadedRevision = Number(JSON.parse(sidecarText).revision) || 0;
    } catch (err) {
      loadedRevision = 0;
    }
    const data = parseAnnotationSidecar(sidecarText);
    try {
      await verifyAnnotationSource(data, markdown);
    } catch (verifyErr) {
      // The page Markdown changed since this on-read DTO was assembled (e.g.
      // a concurrent edit). Re-anchor by text anyway; whatever still fails is kept as a
      // pending anchor so the next save does not discard it.
      console.warn('membox: sidecar source mismatch — re-anchoring by text', verifyErr);
    }
    restoring = true;
    try {
      restoreAnnotationSidecar(data);
    } finally {
      restoring = false;
    }
    loadedAnnotationCount = Array.isArray(data.annotations) ? data.annotations.length : 0;
    annotationsMutated = false;
    annotationsDirty = false;
    markAnnotationsSaved(state.annotationSessionId, state.annotationVersion);
    renderConnection();
    if (Array.isArray(data.unrestored) && data.unrestored.length) {
      pendingAnchors = data.unrestored;
    }
    restoreProgress(data.progress);
  } catch (err) {
    restoring = false;
    console.warn('membox: could not restore reading state', err);
  }
}

function decodeFilenameHeader(value) {
  if (!value) return '';
  try {
    return decodeURIComponent(value);
  } catch (err) {
    // Backward compatibility with older servers and literal `%` filenames.
    return value;
  }
}

async function loadFromMembox() {
  if (!documentID) return;
  try {
    const response = await fetch(`/api/doc/${encodeURIComponent(documentID)}`, { cache: 'no-store' });
    if (!response.ok) throw new Error((await response.text()) || `HTTP ${response.status}`);
    const markdown = await response.text();
    const filename = decodeFilenameHeader(response.headers.get('X-Membox-Filename'));
    if (filename) {
      state.droppedFilename = filename;
      document.title = `${filename.replace(/\.[^.]+$/, '')} — membox`;
    }
    loadedMarkdown = markdown;
    loadDocument(markdown);
    // Notes before progress: note cards change the layout, so the saved
    // scroll position only means something once they are in place.
    await restoreReadingState(documentID, markdown);
  } catch (err) {
    console.error('membox: failed to load document', err);
    showToast(`Could not load from membox: ${err.message}`);
  }
}

// Notes need an identity to persist. When the user takes a note on pasted
// content that has never been synced, silently create the membox note (only
// while connected) so the notes and reading position can flow through the
// normal auto-save path afterwards.
let autoCreating = false;

async function autoCreateForAnnotations() {
  if (autoCreating || documentID || !connected) return;
  const body = state.currentMarkdown || '';
  if (!body.trim() || state.annotations.length === 0) return;
  autoCreating = true;
  const savedSessionId = state.annotationSessionId;
  const savedVersion = state.annotationVersion;
  try {
    const title = (state.docTitle || '').trim() || 'Untitled';
    let annotations;
    try {
      annotations = await buildAnnotationSidecar(sanitizeFilename(title) + '.md', body, currentProgress());
    } catch (err) {
      throw new Error(`Could not prepare session notes: ${err.message}`);
    }
    if (!connected || savedSessionId !== state.annotationSessionId) return;
    const response = await fetch('/api/sync', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ id: '', title, body, annotations }),
    });
    if (!response.ok) throw new Error((await response.text()).trim() || `HTTP ${response.status}`);
    const result = await response.json();
    if (savedSessionId !== state.annotationSessionId) return;
    replaceDocumentID(result.id);
    loadedMarkdown = body;
    if (result.path) state.droppedFilename = result.path.split(/[\\/]/).pop();
    applySavedAnnotationRefs(result.annotations, savedSessionId);
    assertAnnotationReplacement(result);
    loadedRevision = Number(result.revision) || loadedRevision;
    if (!loadedRevision) await refreshRevision(result.id);
    markAnnotationsSaved(savedSessionId, savedVersion);
    annotationsDirty = savedSessionId === state.annotationSessionId && savedVersion < state.annotationVersion;
    if (!annotationsDirty) annotationsMutated = false;
    renderConnection();
    showToast(`Notes auto-saved to membox: ${String(result.id).slice(0, 8)}`);
  } catch (err) {
    annotationsDirty = true;
    renderConnection();
    console.error('membox: auto-save failed; notes remain in this session', err);
    showToast(`Auto-save failed — notes remain in this session: ${err.message}`);
  } finally {
    autoCreating = false;
    if (annotationsDirty && connected) {
      if (documentID) scheduleReadingStateSave();
      else if (savedSessionId !== state.annotationSessionId) queueMicrotask(() => void autoCreateForAnnotations());
    }
  }
}

async function syncToMembox() {
  if (syncing) return;
  const body = state.currentMarkdown || '';
  if (!body.trim()) {
    showToast('Nothing to sync');
    return;
  }

  // A paste/drop over a loaded document is a new note, not an implicit
  // overwrite. Explicitly loaded content keeps its UUID while unchanged.
  const id = documentID && body === loadedMarkdown ? documentID : '';
  const title = (state.docTitle || '').trim() || 'Untitled';
  const savedSessionId = state.annotationSessionId;
  const savedVersion = state.annotationVersion;

  syncing = true;
  setDownloadMeaning();
  renderDocStatus();
  try {
    // A sidecar-shaped DTO rides along so the backend can reconcile separate
    // Markdown note documents and refresh reading progress in one request.
    let annotations;
    try {
      const markdownFile = sanitizeFilename(title) + '.md';
      annotations = await buildAnnotationSidecar(markdownFile, body, currentProgress());
      // Explicit sync replaces stored notes, so it must carry every note we
      // know about — including ones that failed to re-anchor this load.
      mergePendingAnchors(annotations);
      // Now that the set is complete, the client authorizes deletions itself,
      // scoped to the state it actually saw (revision).
      annotations.replaceAnnotations = true;
      annotations.revision = loadedRevision;
    } catch (err) {
      throw new Error(`Could not prepare session notes: ${err.message}`);
    }
    if (!connected || savedSessionId !== state.annotationSessionId) return;
    const response = await fetch('/api/sync', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ id, title, body, annotations }),
    });
    if (!response.ok) throw new Error((await response.text()).trim() || `HTTP ${response.status}`);
    const result = await response.json();
    if (savedSessionId !== state.annotationSessionId) return;
    replaceDocumentID(result.id);
    loadedMarkdown = body;
    if (result.path) state.droppedFilename = result.path.split(/[\\/]/).pop();
    applySavedAnnotationRefs(result.annotations, savedSessionId);
    assertAnnotationReplacement(result);
    loadedRevision = Number(result.revision) || loadedRevision;
    if (!loadedRevision) await refreshRevision(result.id);
    markAnnotationsSaved(savedSessionId, savedVersion);
    annotationsDirty = savedSessionId === state.annotationSessionId && savedVersion < state.annotationVersion;
    if (!annotationsDirty) annotationsMutated = false;
    renderConnection();
    if (annotationsDirty) scheduleReadingStateSave();
    flashButton(elements.downloadAll);
    showToast(result.created ? 'Created in membox with notes' : 'Synced Markdown and notes to membox');
  } catch (err) {
    annotationsDirty = annotationsDirty || savedVersion > state.annotationSavedVersion;
    renderConnection();
    console.error('membox: sync failed; notes remain in this session', err);
    showToast(`Sync failed — notes remain in this session: ${err.message}`);
  } finally {
    syncing = false;
    setDownloadMeaning();
    renderDocStatus();
  }
}

// Capture before Miru's normal bubbling download listener. Disconnected mode
// does nothing here, so the original local Markdown/bundle download remains.
elements.downloadAll.addEventListener('click', (event) => {
  if (!connected) return;
  event.preventDefault();
  event.stopImmediatePropagation();
  void syncToMembox();
}, true);

// Keep the adapter identity honest when the user starts a fresh paste/session.
elements.brand.addEventListener('click', unbindDocument);
document.addEventListener('paste', (event) => {
  if (isEditableTarget(event.target)) return;
  const clipboard = event.clipboardData || window.clipboardData;
  if (clipboard && clipboard.getData('text/plain').trim()) unbindDocument();
}, true);
document.addEventListener('drop', (event) => {
  if (event.dataTransfer && event.dataTransfer.files && event.dataTransfer.files.length) unbindDocument();
}, true);

document.addEventListener('keydown', (event) => {
  if (event.repeat || event.altKey || event.shiftKey) return;
  if (!event.ctrlKey || event.metaKey || event.key.toLowerCase() !== 'o') return;
  if (!connected) return;
  event.preventDefault();
  if (!relatedModal.backdrop.hidden && pickerPurpose === 'open') {
    relatedModal.searchInput.focus();
    return;
  }
  openDocumentPicker();
});

// Miru updates this title when annotations change; connected mode owns its
// sync wording, so immediately re-apply it after those generic updates.
new MutationObserver(() => {
  if (connected && !elements.downloadAll.title.startsWith('Sync Markdown') && !syncing) {
    setDownloadMeaning();
  }
}).observe(elements.downloadAll, { attributes: true, attributeFilter: ['title'] });

async function start() {
  renderConnection();
  connected = await backendAvailable();
  connecting = false;
  renderConnection();
  if (connected) await loadFromMembox();
}

void start();
