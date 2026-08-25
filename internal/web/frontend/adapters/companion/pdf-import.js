/* PDF drag-and-drop belongs to the membox adapter, not Miru's host-neutral
   Markdown importer. A dropped PDF is uploaded into the managed PDF catalog;
   it does not replace the Markdown currently being read. */

import { elements } from '../../js/dom.js';
import { showToast } from '../../js/ui/feedback.js';
import { importPDF } from './api.js';
import { session } from './session.js';

const MAX_PDF_BYTES = 500 * 1024 * 1024;
let importing = false;

function isPDF(file) {
  return /\.pdf$/i.test(file?.name || '') || file?.type === 'application/pdf';
}

function shortID(value) {
  value = String(value || '');
  return value.length > 4 ? value.slice(-4) : value;
}

export function initPDFImport() {
  // Capture before Miru's normal drop listener, which intentionally accepts
  // only Markdown, annotation sidecars, and Miru ZIP bundles.
  document.addEventListener('drop', (event) => {
    const files = Array.from(event.dataTransfer?.files || []);
    const pdfs = files.filter(isPDF);
    // Disconnected Miru is a pure frontend. Do not capture its drop event or
    // surface backend-specific UI when the PDF capability is unavailable.
    if (!pdfs.length || !session.connected) return;

    event.preventDefault();
    event.stopImmediatePropagation();
    elements.body.classList.remove('is-dragover');

    if (files.length !== 1 || pdfs.length !== 1) {
      showToast('Drop one PDF at a time');
      return;
    }
    if (importing) {
      showToast('A PDF import is already running');
      return;
    }

    const file = pdfs[0];
    if (!file.size || file.size > MAX_PDF_BYTES) {
      showToast('PDF must be between 1 byte and 500 MiB');
      return;
    }
    importing = true;
    showToast(`Importing ${file.name}…`);
    void importPDF(file)
      .then((result) => {
        const name = result.title || result.filename || file.name;
        showToast(`Saved PDF: ${name} · ${shortID(result.document_id)}`);
      })
      .catch((err) => {
        console.error('membox: PDF import failed', err);
        showToast(`Could not import PDF: ${err.message}`);
      })
      .finally(() => {
        importing = false;
      });
  }, true);
}
