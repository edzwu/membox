/* membox adapter for Miru.
   This is intentionally outside frontend/miru: it adds connection state,
   backend loading, and sync semantics without coupling the Miru source tree to
   membox. */

import { elements } from '../js/dom.js';
import { state } from '../js/state.js';
import { loadDocument } from '../js/document.js';
import { isEditableTarget, sanitizeFilename } from '../js/utils.js';
import { showToast, flashButton } from '../js/ui/feedback.js';
import { updateMarkdownDownloadControl } from '../js/ui/chrome.js';
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
  elements.newPaste.insertAdjacentElement('afterend', button);
  button.addEventListener('click', toggleConnection);
  return button;
}

const connectionButton = createConnectionButton();

function setDownloadMeaning() {
  if (!connected) {
    updateMarkdownDownloadControl();
    return;
  }
  const label = syncing ? 'Syncing to membox…' : 'Sync Markdown and notes to membox';
  elements.downloadAll.setAttribute('aria-label', label);
  elements.downloadAll.title = label;
  elements.downloadAll.classList.toggle('is-syncing', syncing);
}

function renderConnection() {
  connectionButton.disabled = connecting;
  connectionButton.dataset.connected = String(connected);
  connectionButton.classList.toggle('is-connecting', connecting);
  const label = connecting
    ? 'Checking membox connection…'
    : connected
      ? 'Connected to membox — click to disconnect'
      : 'Disconnected from membox — click to connect';
  connectionButton.setAttribute('aria-label', label);
  connectionButton.title = label;
  setDownloadMeaning();
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
    renderConnection();
    showToast('Disconnected from membox — arrow downloads locally');
    return;
  }
  connecting = true;
  renderConnection();
  connected = await backendAvailable();
  connecting = false;
  renderConnection();
  showToast(connected ? 'Connected to membox — arrow now syncs' : 'Could not connect to membox');
}

function replaceDocumentID(id) {
  documentID = id || '';
  const url = new URL(window.location.href);
  if (documentID) url.searchParams.set('id', documentID);
  else url.searchParams.delete('id');
  window.history.replaceState(null, '', url);
}

function unbindDocument() {
  replaceDocumentID('');
  loadedMarkdown = '';
}

async function restoreAnnotations(id, markdown) {
  const response = await fetch(`/api/doc/${encodeURIComponent(id)}/annotations`, { cache: 'no-store' });
  if (response.status === 204 || !response.ok) return;
  const sidecarText = await response.text();
  if (!sidecarText.trim()) return;
  const data = parseAnnotationSidecar(sidecarText);
  await verifyAnnotationSource(data, markdown);
  restoreAnnotationSidecar(data);
}

async function loadFromMembox() {
  if (!documentID) return;
  try {
    const response = await fetch(`/api/doc/${encodeURIComponent(documentID)}`, { cache: 'no-store' });
    if (!response.ok) throw new Error((await response.text()) || `HTTP ${response.status}`);
    const markdown = await response.text();
    const filename = response.headers.get('X-Membox-Filename');
    if (filename) {
      state.droppedFilename = filename;
      document.title = `${filename.replace(/\.[^.]+$/, '')} — membox`;
    }
    loadedMarkdown = markdown;
    loadDocument(markdown);
    await restoreAnnotations(documentID, markdown);
  } catch (err) {
    console.error('membox: failed to load document', err);
    showToast(`Could not load from membox: ${err.message}`);
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
  let annotations = null;

  syncing = true;
  setDownloadMeaning();
  try {
    if (state.annotations.length) {
      const markdownFile = sanitizeFilename(title) + '.md';
      annotations = await buildAnnotationSidecar(markdownFile, body);
    }
    const response = await fetch('/api/sync', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ id, title, body, annotations }),
    });
    if (!response.ok) throw new Error((await response.text()).trim() || `HTTP ${response.status}`);
    const result = await response.json();
    replaceDocumentID(result.id);
    loadedMarkdown = body;
    if (result.path) state.droppedFilename = result.path.split(/[\\/]/).pop();
    flashButton(elements.downloadAll);
    showToast(result.created ? 'Created in membox with notes' : 'Synced Markdown and notes to membox');
  } catch (err) {
    console.error('membox: sync failed', err);
    showToast(`Sync failed: ${err.message}`);
  } finally {
    syncing = false;
    setDownloadMeaning();
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
elements.newPaste.addEventListener('click', unbindDocument);
elements.brand.addEventListener('click', unbindDocument);
document.addEventListener('paste', (event) => {
  if (isEditableTarget(event.target)) return;
  const clipboard = event.clipboardData || window.clipboardData;
  if (clipboard && clipboard.getData('text/plain').trim()) unbindDocument();
}, true);
document.addEventListener('drop', (event) => {
  if (event.dataTransfer && event.dataTransfer.files && event.dataTransfer.files.length) unbindDocument();
}, true);

// Miru updates this title when annotations change; connected mode owns its
// sync wording, so immediately re-apply it after those generic updates.
new MutationObserver(() => {
  if (connected && elements.downloadAll.title !== 'Sync Markdown and notes to membox' && !syncing) {
    setDownloadMeaning();
  }
}).observe(elements.downloadAll, { attributes: true, attributeFilter: ['title'] });

async function start() {
  renderConnection();
  connected = await backendAvailable();
  connecting = false;
  renderConnection();
  await loadFromMembox();
}

void start();
