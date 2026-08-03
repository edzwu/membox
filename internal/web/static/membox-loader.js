/* membox integration — fetch a document from the local membox server and
   render it with miru once the reader is ready.
   The document id is passed as a URL query parameter, e.g. ?id=<uuid>. */

import { loadDocument } from './js/document.js';
import { initSave } from './membox-save.js';

async function boot() {
  const id = new URLSearchParams(window.location.search).get('id');
  if (!id) return;
  try {
    const response = await fetch(`/api/doc/${encodeURIComponent(id)}`);
    if (!response.ok) throw new Error(`HTTP ${response.status}`);
    const text = await response.text();
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
