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
     related.js        related-documents panel + unified Documents/Notes picker
     notes.js          note picker model + long-note rail previews
     note-images.js    clipboard images → private asset store + Markdown refs
     dictionary.js     single-word Free Dictionary lookup → related note
     presence.js       tab heartbeat for the companion/TUI census
     polish.js         magic-wand layout polish: LLM rewrite → rendered
                       preview → classified diff → apply/discard
     retitle.js        AI title suggestion from the outline + lead sample */

import { elements } from '../../js/dom.js';
import { isEditableTarget } from '../../js/utils.js';
import { hasUnsavedAnnotationChanges } from '../../js/annotations/session.js';
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
import { initNoteImages } from './note-images.js';
import { initTranslation } from './translation.js';
import { initJpStudy } from './jp-study.js';
import { initDocSummarize } from './doc-summarize.js';
import { initFileActions } from './file-actions.js';
import { initPolish } from './polish.js';
import { initRetitle } from './retitle.js';
import { initDictionary } from './dictionary.js';
import { initSeriesNav } from './series-nav.js';
import { initWikiLinks } from './wiki-links.js';
import { initAssist } from './assist.js';
import { initSelectionSummarize } from './summarize.js';
import { initReview } from './review.js';
import { initAgentSearch } from './agent/search.js';
import { initReadingState, isRestoring, scheduleReadingStateSave } from './reading-state.js';
import { autoCreateForAnnotations, syncToMembox } from './sync.js';
import { checkStatus } from './api.js';

const style = document.createElement('link');
style.rel = 'stylesheet';
style.href = new URL('./companion.css', import.meta.url).href;
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
initNoteImages();
initTranslation();
initJpStudy();
initDocSummarize();
initFileActions();
initPolish();
initRetitle();
initDictionary();
initSeriesNav();
initWikiLinks();
initAssist();
initSelectionSummarize();
initReview();
initAgentSearch();

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

// Pure Miru annotations are intentionally session-only. Warn on navigation
// only while a connected durable host still has changes waiting to save.
window.addEventListener('beforeunload', (event) => {
  if (!session.connected || !hasUnsavedAnnotationChanges()) return;
  event.preventDefault();
  event.returnValue = '';
});

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
  await connectAndLoad();
}

// The TUI's "open in browser" can race the companion: a stale-binary recycle
// or a fresh spawn leaves a window where the page is already open but the
// server is not answering yet. A single failed probe used to leave the
// reader on the empty state until a manual reload. Keep probing in the
// background instead, and load the bound document as soon as the connection
// lands.
async function connectAndLoad() {
  let announced = false;
  let delay = 1000;
  for (;;) {
    session.connected = await checkStatus();
    session.connecting = false;
    emitRender();
    if (session.connected) {
      if (session.documentID && !session.loadedMarkdown) await loadFromMembox();
      return;
    }
    if (session.documentID && !announced) {
      announced = true;
      showToast('正在等待 membox companion 就绪…');
    }
    await new Promise((resolve) => setTimeout(resolve, delay));
    delay = Math.min(delay * 2, 5000);
  }
}

void start();
