/* membox adapter for Miru — composition root.
   This is intentionally outside frontend/miru: it adds connection state,
   backend loading, and sync semantics without coupling the Miru source tree to
   membox.

   Like miru/app.js, this file only wires things together: "when the user does
   X, call Y". The behavior lives in feature modules:

     session.js        shared adapter session state (mutable singleton)
     api.js            backend HTTP client — pure I/O, no DOM, no state
     events.js         render-event pub/sub (breaks chrome↔feature cycles)
     modals.js         modal exclusivity registry (breaks notes↔related cycles)
     chrome.js         fans one render event out to all adapter chrome
     connection.js     connection button + connect/disconnect lifecycle
     status.js         save/copy badge, add-related button, download wording
     document.js       document binding, loading, rename, switcher
     reading-state.js  sidecar persist/restore, revision, progress, read status
     sync.js           explicit sync + silent auto-create of pasted content
     related.js        related-documents panel + link/create/open picker
     notes.js          browse-notes button + picker, long-note rail previews
     presence.js       tab heartbeat for the companion/TUI census */

import { elements } from '../js/dom.js';
import { isEditableTarget } from '../js/utils.js';
import { session } from './session.js';
import { emitRender } from './events.js';
import { initChrome } from './chrome.js';
import { initConnection } from './connection.js';
import { initDocumentUI, loadFromMembox, unbindDocument } from './document.js';
import { initStatus } from './status.js';
import { initNotes } from './notes.js';
import { initRelated, handleOpenShortcut } from './related.js';
import { initPresence } from './presence.js';
import { initPDFImport } from './pdf-import.js';
import { initReadingState, isRestoring, scheduleReadingStateSave } from './reading-state.js';
import { autoCreateForAnnotations, syncToMembox } from './sync.js';
import { checkStatus } from './api.js';

const style = document.createElement('link');
style.rel = 'stylesheet';
style.href = new URL('./membox.css', import.meta.url).href;
document.head.appendChild(style);

// Init order matters: chrome subscribes before any state change renders;
// document navigation exists before notes/status attach their buttons.
initChrome();
initConnection();
initDocumentUI({ onOpenPicker: handleOpenShortcut });
initStatus();
initNotes();
initRelated();
initReadingState();
initPresence();
initPDFImport();

// User edits make the in-memory annotation set authoritative for deletions on
// the next save. During a restore the model is incomplete; the restore flow
// detects the version bump and persists once it finishes, so nothing is
// scheduled from here mid-restore.
window.addEventListener('miru-annotations-changed', () => {
  if (isRestoring()) return;
  session.annotationsMutated = true;
  session.annotationsDirty = true;
  emitRender();
  if (!session.connected) return;
  if (!session.documentID) {
    void autoCreateForAnnotations();
    return;
  }
  scheduleReadingStateSave();
});

// Capture before Miru's normal bubbling download listener. Disconnected mode
// does nothing here, so the original local Markdown/bundle download remains.
elements.downloadAll.addEventListener('click', (event) => {
  if (!session.connected) return;
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
  if (!session.connected) return;
  event.preventDefault();
  handleOpenShortcut();
});

async function start() {
  emitRender();
  session.connected = await checkStatus();
  session.connecting = false;
  emitRender();
  if (session.connected) await loadFromMembox();
}

void start();
