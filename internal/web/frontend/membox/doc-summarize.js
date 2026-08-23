/* 「摘」 compose action: summarize the selected passage through the local mmd
   model and save it as an inline summary note. Selection-only — the full-doc
   map-reduce summarize keeps its code path but no longer has a dock entry. */

import { showToast } from '../js/ui/feedback.js';
import { detachComposeSelection, registerAnnotAction } from '../js/annotations/toolbar.js';
import { session } from './session.js';
import { replaceDocumentID, loadFromMembox } from './document.js';
import { streamDocSummarize } from './api.js';
import { runSelectionSummarize } from './summarize.js';

let running = false;
let controller = null;

async function startSelectionSummarize(captured, force = false) {
  running = true;
  controller = new AbortController();
  showToast('总结中…');
  try {
    // Fast path: existing summary for the same passage (unless Alt-click force).
    const result = await runSelectionSummarize(captured, { force, signal: controller.signal });
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
  }
}

/** Full-document map-reduce summarize. No dock entry after the 「摘」/「译」
 *  tray was moved into the compose dialog; kept for programmatic reuse. */
export async function startDocSummarize(force = false) {
  if (!session.connected) {
    showToast('Connect to membox before summarizing');
    return;
  }
  if (!session.documentID) {
    showToast('Open a membox document to summarize');
    return;
  }
  running = true;
  controller = new AbortController();
  showToast('全文摘录中…');
  try {
    let done = null;
    await streamDocSummarize(session.documentID, { force }, (event) => {
      if (event.type === 'progress') {
        // progress surfaced via toast only; the compose dialog owns no dock slot
        showToast(`全文摘录 ${stageText(event)}`);
      } else if (event.type === 'done') {
        done = event;
      }
    }, controller.signal);
    running = false;
    if (done?.id) {
      showToast(done.existing ? 'Summary already exists — opening' : `Summary saved · ${done.chars || ''}字`);
      replaceDocumentID(done.id);
      await loadFromMembox();
    } else {
      showToast('Summary finished');
    }
  } catch (err) {
    running = false;
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

export function initDocSummarize() {
  registerAnnotAction({
    id: 'summarize',
    icon: '摘',
    title: '总结选中内容 · 已有总结则复用 · Alt-click 强制重摘',
    when: ({ mode, text }) => mode === 'create' && Boolean(text) && text.trim().length >= 20,
    run: (_ctx, event) => {
      if (running) {
        controller?.abort();
        return;
      }
      if (!session.connected) {
        showToast('Connect to membox before summarizing');
        return;
      }
      if (!session.documentID) {
        showToast('Open a membox document to summarize');
        return;
      }
      const detached = detachComposeSelection();
      if (!detached) return;
      void startSelectionSummarize(detached, Boolean(event?.altKey));
    },
  });
}
