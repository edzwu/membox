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
// Wipe protection: when a sidecar loaded with annotations but none survived
// restoration and the user did not delete any, never overwrite the stored
// notes with an empty set (keep the saved ones, update progress only).
let loadedAnnotationCount = 0;
let annotationsMutated = false;
// Anchors that failed to re-anchor on load. Kept so the next save merges
// them back into the sidecar instead of silently dropping them.
let pendingAnchors = [];

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

async function persistReadingState(keepalive) {
  if (!documentID || !state.currentMarkdown) return;
  try {
    const sidecar = await buildAnnotationSidecar(sidecarFilename(), state.currentMarkdown, currentProgress());
    // Only an actual annotation mutation makes the submitted set authoritative
    // for deletions. Scroll/progress saves must never delete note documents.
    sidecar.replaceAnnotations = annotationsMutated;
    if (pendingAnchors.length) {
      // Merge back annotations that could not be re-anchored this load so a
      // save never destroys notes it merely failed to display.
      const have = new Set(sidecar.annotations.map((a) => a.exact));
      const kept = pendingAnchors.filter((a) => !have.has(a.exact));
      if (kept.length) {
        sidecar.annotations = [...sidecar.annotations, ...kept].sort((a, b) => a.start - b.start);
      }
    }
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
    });
    if (!response.ok) throw new Error((await response.text()) || `HTTP ${response.status}`);
  } catch (err) {
    console.error('membox: failed to save reading state', err);
  }
}

function scheduleReadingStateSave() {
  if (!documentID || restoring) return;
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
  loadedMarkdown = '';
  pendingAnchors = [];
  loadedAnnotationCount = 0;
  annotationsMutated = false;
}

async function restoreReadingState(id, markdown) {
  pendingAnchors = [];
  try {
    const response = await fetch(`/api/doc/${encodeURIComponent(id)}/annotations`, { cache: 'no-store' });
    if (response.status === 204 || !response.ok) return;
    const sidecarText = await response.text();
    if (!sidecarText.trim()) return;
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
    if (Array.isArray(data.unrestored) && data.unrestored.length) {
      pendingAnchors = data.unrestored;
    }
    restoreProgress(data.progress);
  } catch (err) {
    restoring = false;
    console.warn('membox: could not restore reading state', err);
  }
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
  try {
    const title = (state.docTitle || '').trim() || 'Untitled';
    let annotations = null;
    try {
      annotations = await buildAnnotationSidecar(sanitizeFilename(title) + '.md', body, currentProgress());
    } catch (err) {
      console.warn('membox: could not pack annotation sidecar', err);
    }
    const response = await fetch('/api/sync', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ id: '', title, body, annotations }),
    });
    if (!response.ok) throw new Error((await response.text()).trim() || `HTTP ${response.status}`);
    const result = await response.json();
    replaceDocumentID(result.id);
    loadedMarkdown = body;
    if (result.path) state.droppedFilename = result.path.split(/[\\/]/).pop();
    showToast(`Notes auto-saved to membox: ${String(result.id).slice(0, 8)}`);
  } catch (err) {
    console.error('membox: auto-save failed', err);
    showToast(`Auto-save failed: ${err.message}`);
  } finally {
    autoCreating = false;
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

  syncing = true;
  setDownloadMeaning();
  try {
    // A sidecar-shaped DTO rides along so the backend can reconcile separate
    // Markdown note documents and refresh reading progress in one request.
    let annotations = null;
    try {
      const markdownFile = sanitizeFilename(title) + '.md';
      annotations = await buildAnnotationSidecar(markdownFile, body, currentProgress());
    } catch (err) {
      console.warn('membox: could not pack annotation sidecar', err);
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
