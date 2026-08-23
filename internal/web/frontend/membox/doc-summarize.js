/* 「摘」 button: selection summarize when text is highlighted; otherwise
   full-document map-reduce summarize through the local mmd model.
   Lives in the low-frequency extras tray next to translate/search. */

import { showToast } from '../js/ui/feedback.js';
import { session } from './session.js';
import { onRender } from './events.js';
import { getStatusExtras, setStatusExtrasPinned } from './status.js';
import { replaceDocumentID, loadFromMembox } from './document.js';
import { streamDocSummarize } from './api.js';
import { captureArticleSelection, hasArticleSelectionText, runSelectionSummarize } from './summarize.js';

let button = null;
let labelEl = null;
let running = false;
let controller = null;
/** @type {{ text: string, range: Range } | null} */
let pendingSelection = null;

function createButton() {
  const value = document.createElement('button');
  value.type = 'button';
  value.id = 'membox-doc-summarize';
  value.className = 'membox-doc-summarize';
  value.hidden = true;
  value.innerHTML =
    '<span class="membox-doc-summarize-glyph" aria-hidden="true">摘</span>' +
    '<span class="membox-doc-summarize-progress" aria-hidden="true"></span>';
  // Click focus collapses the page selection — snapshot on pointerdown.
  value.addEventListener('pointerdown', () => {
    pendingSelection = captureArticleSelection();
  });
  value.addEventListener('click', (event) => {
    if (running) {
      controller?.abort();
      return;
    }
    void startSummarize({
      force: Boolean(event?.altKey),
      selection: pendingSelection,
    });
    pendingSelection = null;
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

function hasSelectionHint() {
  return Boolean(pendingSelection || hasArticleSelectionText());
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
  const selectionMode = !running && hasSelectionHint();
  button.dataset.selection = String(selectionMode);
  if (running) {
    button.title = `Summarizing… ${progressText} · click to stop`;
    button.setAttribute('aria-label', `Summarizing ${progressText}, click to stop`);
  } else if (selectionMode) {
    button.title = '总结选中内容 · 本地模型 · 已有总结则复用 · Alt-click 强制重摘';
    button.setAttribute('aria-label', '总结选中内容');
  } else {
    button.title = '全文摘录 · 本地模型 · 已有则打开 · Alt-click 强制重摘 · 选中文字可摘选段';
    button.setAttribute('aria-label', 'Summarize document');
  }
  if (labelEl) labelEl.textContent = progressText;
  setStatusExtrasPinned('doc-summarize', running);
}

async function startSelectionSummarize(captured, force = false) {
  running = true;
  controller = new AbortController();
  renderButton('选…');
  try {
    // Fast path: existing summary for the same passage (unless Alt-click force).
    const result = await runSelectionSummarize(captured, { force });
    if (controller?.signal.aborted) {
      showToast('Summarize stopped');
      return;
    }
    if (result?.existing) {
      showToast('该段已有总结');
      return;
    }
    showToast('总结已写入原文');
  } catch (err) {
    if (err?.name === 'AbortError' || controller?.signal.aborted) {
      showToast('Summarize stopped');
      return;
    }
    console.error('membox: selection summarize failed', err);
    showToast(err?.message || '总结失败');
  } finally {
    running = false;
    controller = null;
    renderButton('');
  }
}

async function startDocSummarize(force = false) {
  running = true;
  controller = new AbortController();
  renderButton('…');
  try {
    let done = null;
    await streamDocSummarize(session.documentID, { force }, (event) => {
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

async function startSummarize({ force = false, selection = null } = {}) {
  if (!session.connected) {
    showToast('Connect to membox before summarizing');
    return;
  }
  if (!session.documentID) {
    showToast('Open a membox document to summarize');
    return;
  }
  const captured = selection || captureArticleSelection();
  if (captured) {
    await startSelectionSummarize(captured, force);
    return;
  }
  await startDocSummarize(force);
}

export function initDocSummarize() {
  if (button) return;
  button = createButton();
  onRender(() => { if (!running) renderButton(); });
  document.addEventListener('selectionchange', () => {
    if (!running) renderButton();
  });
}
