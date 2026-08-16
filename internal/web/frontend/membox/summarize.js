/* Selection summarize: compress the selected excerpt to ≤140 chars with the
   local mmd model (qwen3:14b), then save it as an annotation note
   (kind=summary) through the normal *-note.md reconcile pipeline. */

import { applyNote, findAnnot, setNoteOnPassage } from '../js/annotations/model.js';
import { registerAnnotAction } from '../js/annotations/toolbar.js';
import { showToast } from '../js/ui/feedback.js';
import { session } from './session.js';

const SUMMARIZE_ICON =
  '<svg viewBox="0 0 24 24" width="16" height="16" aria-hidden="true">' +
  '<path fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" ' +
  'd="M4 6h16M4 12h10M4 18h7"/>' +
  '<path fill="currentColor" d="M17 13.5l.8 2.2 2.2.8-2.2.8-.8 2.2-.8-2.2-2.2-.8 2.2-.8.8-2.2z"/>' +
  '</svg>';

const SPIN_ICON =
  '<svg viewBox="0 0 24 24" width="16" height="16" aria-hidden="true" class="membox-summarize-spin">' +
  '<circle cx="12" cy="12" r="8" fill="none" stroke="currentColor" stroke-width="1.8" stroke-dasharray="36 20" stroke-linecap="round"/>' +
  '</svg>';

function hasNoteAnchor(ctx) {
  if (ctx.annotEl && ctx.annotEl.isConnected) return true;
  if (!ctx.range) return false;
  try {
    const root = ctx.range.commonAncestorContainer;
    return Boolean(root && root.isConnected !== false);
  } catch {
    return false;
  }
}

async function summarizeSelection(selection) {
  const response = await fetch(`/api/doc/${encodeURIComponent(session.documentID)}/summarize`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ selection }),
  });
  if (!response.ok) throw new Error((await response.text()).trim() || `HTTP ${response.status}`);
  return response.json();
}

function summarizeNoteBody(selection, summary) {
  const q = String(selection || '').trim();
  const a = String(summary || '').trim();
  if (!a) return '';
  // The quote stays in the note file as the restore anchor; the card hides it
  // via .is-summary CSS, so only the summary + label are visible inline.
  return `> ${q}

**总结：**

${a}
`;
}

export function initSelectionSummarize() {
  registerAnnotAction({
    id: 'summarize',
    icon: SUMMARIZE_ICON,
    title: '总结选中内容（本地模型）',
    when: ({ mode, text }) => {
      if (mode !== 'create') return false;
      const value = String(text || '').trim();
      return value.length >= 20;
    },
    run: async (ctx) => {
      const selection = (ctx.text || '').trim();
      if (!selection) {
        showToast('先选中内容');
        return;
      }
      if (!session.connected || !session.documentID) {
        showToast('需要连接 membox 才能总结');
        return;
      }
      if (!hasNoteAnchor(ctx)) {
        showToast('选区已失效，请重新选择');
        return;
      }
      // Swap the toolbar icon for a spinner so the ~30s local-model run has a
      // visible in-progress state; restore it when done.
      const btn = ctx.toolbar.querySelector('button[data-action="ext:summarize"]');
      const originalIcon = btn ? btn.innerHTML : '';
      if (btn) {
        btn.disabled = true;
        btn.innerHTML = SPIN_ICON;
        btn.title = '总结中…';
      }
      showToast('总结中…（本地模型，约 30 秒）');
      try {
        const result = await summarizeSelection(selection);
        const summary = (result.summary || '').trim();
        if (!summary) throw new Error('模型返回空总结');
        const noteText = summarizeNoteBody(selection, summary);
        if (ctx.annotEl && ctx.annotEl.isConnected) {
          const entry = findAnnot(ctx.annotEl.dataset.annotId);
          if (!entry) throw new Error('annotation missing');
          setNoteOnPassage(entry, ctx.annotEl, noteText, { kind: 'summary' });
        } else {
          applyNote(ctx.range, noteText, { kind: 'summary' });
        }
        if (btn) {
          btn.disabled = false;
          btn.innerHTML = originalIcon;
          btn.title = '总结选中内容（本地模型）';
        }
        ctx.finish();
        showToast('总结已存为笔记');
      } catch (err) {
        console.error('membox: selection summarize failed', err);
        if (btn) {
          btn.disabled = false;
          btn.innerHTML = originalIcon;
          btn.title = '总结选中内容（本地模型）';
        }
        showToast(err.message || '总结失败');
      }
    },
  });
}
