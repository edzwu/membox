/* membox integration — fetch a document from the local membox server and
   render it with miru once the reader is ready.
   The document id is passed as a URL query parameter, e.g. ?id=<uuid>. */

import { state } from './js/state.js';
import { loadDocument } from './js/document.js';
import { initSave } from './membox-save.js';

async function boot() {
  const id = new URLSearchParams(window.location.search).get('id');
  if (!id) return;
  try {
    const response = await fetch(`/api/doc/${encodeURIComponent(id)}`);
    if (!response.ok) throw new Error(`HTTP ${response.status}`);
    const text = await response.text();

    // Miru shows state.droppedFilename as the document title (and uses it for
    // download/export). The server sends the on-disk filename so the reader
    // displays it instead of "Untitled".
    const filename = response.headers.get('X-Membox-Filename');
    if (filename) {
      state.droppedFilename = filename;
      document.title = `${filename.replace(/\.[^.]+$/, '')} — membox`;
    }

    loadDocument(text.trim());
  } catch (err) {
    console.error('membox: failed to load document', err);
  }
}

function start() {
  initSave();
  boot();
}

if (window.__miruReady) {
  start();
} else {
  window.addEventListener('miru-ready', start, { once: true });
}
