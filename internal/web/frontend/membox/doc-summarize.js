/* Full-document summarize: map-reduce through the local mmd model into a
   ≤1000-rune Markdown note linked to the source. Lives in the low-frequency
   extras tray next to translate/search; the button shows stage progress. */

import { showToast } from '../../js/ui/feedback.js';
import { session } from './session.js';
import { onRender } from './events.js';
import { getStatusExtras, setStatusExtrasPinned } from './status.js';
import { replaceDocumentID, loadFromMembox } from './document.js';
import { streamDocSummarize } from './api.js';

let button = null;
let labelEl = null;
let running = false;
let controller = null;

function createButton() {
  const value = document.createElement('button');
  value.type = 'button';
  value.id = 'membox-doc-summarize';
  value.className = 'membox-doc-summarize';
  value.hidden = true;
  value.innerHTML =
    '<span class="membox-doc-summarize-glyph" aria-hidden="true">摘</span>' +
    '<span class="membox-doc-summarize-progress" aria-hidden="true"></span>';
  value.addEventListener('click', (event) => {
    if (running) {
      controller?.abort();
      return;
    }
    void startSummarize(Boolean(event?.altKey));
  });
  // Leftmost of the low-frequency tray: [摘] [译] [search]
  const extras = getStatusExtras();
  if (extras) extras.insertBefore(value, extras.firstChild);
  else document.body.appendChild(value);
  labelEl = value.querySelector('.membox-doc-summarize-progress');
  return value;
}

function stageText(event) {
  switch (event.stage) {
    case 'split': return `0/${event.total || '?'}`;
    case 'segment': return `${event.index}/${event.total}`;
    case 'merge': return event.total ? `合${event.index}/${event.total}` : `合${event.depth || ''}`;
    case 'final': return '润色';
    case 'save': return '保存';
    default: return '';
  }
}

function renderButton(progressText = '') {
  if (!button) return;
  const canSummarize = session.connected
    && Boolean(session.documentID)
    && document.body.classList.contains('is-reading')
    && !document.body.classList.contains('is-snippet');
  button.hidden = !canSummarize && !running;
  button.dataset.running = String(running);
  button.setAttribute('aria-pressed', String(running));
  button.title = running
    ? `Summarizing… ${progressText} · click to stop`
    : 'Summarize document · local model · Alt-click to regenerate';
  button.setAttribute('aria-label', running ? `Summarizing ${progressText}, click to stop` : 'Summarize document');
  if (labelEl) labelEl.textContent = progressText;
  setStatusExtrasPinned('doc-summarize', running);
}

async function startSummarize(force = false) {
  if (!session.connected) {
    showToast('Connect to membox before summarizing');
    return;
  }
  const documentID = session.documentID;
  if (!documentID) {
    showToast('Open a membox document to summarize');
    return;
  }
  running = true;
  controller = new AbortController();
  renderButton('…');
  try {
    let done = null;
    await streamDocSummarize(documentID, { force }, (event) => {
      if (event.type === 'progress') {
        renderButton(stageText(event));
      } else if (event.type === 'done') {
        done = event;
      }
    }, controller.signal);
    running = false;
    renderButton('');
    if (done?.id) {
      showToast(done.existing ? 'Summary already exists — opening' : `Summary saved · ${done.chars || ''}字`);
      replaceDocumentID(done.id);
      await loadFromMembox();
    } else {
      showToast('Summary finished');
    }
  } catch (err) {
    running = false;
    renderButton('');
    if (err?.name === 'AbortError') {
      showToast('Summarize stopped');
      return;
    }
    console.error('membox: document summarize failed', err);
    showToast(err?.message || 'Summarize failed');
  } finally {
    controller = null;
  }
}

export function initDocSummarize() {
  if (button) return;
  button = createButton();
  onRender(() => { if (!running) renderButton(); });
}
