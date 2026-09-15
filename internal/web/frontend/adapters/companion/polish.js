/* 一键排版优化 (magic wand).

   Default click — fast outline polish: only the heading lines (fence-aware,
   never the body) go to DeepSeek, which returns corrected levels; the levels
   are applied deterministically to the source. Alt-click — full-document
   rewrite through the local model for heavier cleanup (PDF debris etc.).

   Both paths end in the same preview: rendered result, classified source
   diff, apply/discard. Trust model: the outline contract is "levels only,
   body verbatim", and the diff modal verifies it mechanically — heading
   hunks are green, any body-text change is a red violation. */

import { state } from '../../js/state.js';
import { loadDocument } from '../../js/document.js';
import { setAnnotInteractionsEnabled } from '../../js/annotations/toolbar.js';
import { extractHeadings, applyHeadingEdits } from '../../js/markdown/structure.js';
import { buildDiffHunks } from '../../js/linediff.js';
import { escapeHtml } from '../../js/utils.js';
import { showToast } from '../../js/ui/feedback.js';
import { session } from './session.js';
import { onRender } from './events.js';
import { streamDocRewrite, postPolishOutline } from './api.js';
import { syncReplacementToMembox } from './sync.js';
import { restoreReadingState } from './reading-state.js';
import { addFileAction } from './file-actions.js';

const WAND_ICON = '<svg xmlns="http://www.w3.org/2000/svg" width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">'
  + '<path d="m21.64 3.64-1.28-1.28a1.21 1.21 0 0 0-1.72 0L2.36 18.64a1.21 1.21 0 0 0 0 1.72l1.28 1.28a1.2 1.2 0 0 0 1.72 0L21.64 5.36a1.2 1.2 0 0 0 0-1.72"/>'
  + '<path d="m14 7 3 3"/><path d="M5 6v4"/><path d="M19 14v4"/><path d="M3 8h4"/><path d="M17 18h4"/></svg>';

let dockButton = null;
let confirmBar = null;
let diffOverlay = null;
let running = false;
let controller = null;
// Preview state: originalMarkdown restores on discard; previewDocID detects
// navigation away from the polished document.
let previewing = false;
let originalMarkdown = '';
let optimizedMarkdown = '';
let previewDocID = '';
let previewLabel = '';

function polishAvailable() {
  return session.connected && Boolean(session.documentID);
}

function renderDock() {
  if (!dockButton) return;
  dockButton.hidden = !polishAvailable() || previewing;
  dockButton.classList.toggle('is-running', running);
  dockButton.disabled = running;
}

/* Default click: outline polish via DeepSeek. Only the heading lines plus a
   short sample of each section's opening go to the model — never the body —
   so the round-trip is fast; the returned level/text edits are applied to
   the source deterministically. */
async function startOutlinePolish() {
  if (running || previewing || !polishAvailable()) return;
  const source = state.currentMarkdown || '';
  const headings = extractHeadings(source);
  if (headings.length < 2) {
    showToast('标题太少，无需层级优化');
    return;
  }
  running = true;
  controller = new AbortController();
  renderDock();
  showToast(`分析 ${headings.length} 个标题的层级与语义… (DeepSeek)`);
  try {
    const result = await postPolishOutline(
      headings.map((h) => ({ level: h.level, text: h.text, sample: h.sample })),
      { signal: controller.signal },
    );
    const edits = Array.isArray(result.edits) ? result.edits : [];
    if (edits.length !== headings.length) {
      throw new Error(`模型返回了 ${edits.length} 条修改，应有 ${headings.length} 条`);
    }
    const changed = edits.filter(
      (edit, i) => edit.level !== headings[i].level || String(edit.text || '').trim() !== headings[i].text,
    ).length;
    if (!changed) {
      showToast('标题层级已经很合理');
      return;
    }
    const optimized = applyHeadingEdits(source, headings, edits);
    enterPreview(optimized, result.label || 'deepseek');
  } catch (err) {
    if (err?.name === 'AbortError' || controller?.signal.aborted) {
      showToast('排版优化已取消');
    } else {
      console.error('membox: outline polish failed', err);
      showToast(err?.message || '层级分析失败');
    }
  } finally {
    running = false;
    controller = null;
    renderDock();
  }
}

/* Alt-click: full-document rewrite through the chunked docrewrite pipeline
   (local qwen3:14b via mmd) — slower but also repairs broken prose, fences
   and formulas, e.g. PDF conversion debris. */
async function startFullRewrite() {
  if (running || previewing || !polishAvailable()) return;
  running = true;
  controller = new AbortController();
  renderDock();
  showToast('全文排版重写中… (本地模型)');
  try {
    const result = await streamDocRewrite(
      session.documentID,
      {},
      (event) => {
        if (event.type === 'progress' && event.total > 1) {
          showToast(`排版重写中… ${event.done}/${event.total} ${event.detail || ''}`);
        }
      },
      controller.signal,
    );
    const optimized = String(result.markdown || '');
    if (!optimized.trim()) throw new Error('模型返回了空结果');
    if (optimized.trim() === (state.currentMarkdown || '').trim()) {
      showToast('排版已经很好，无需调整');
      return;
    }
    enterPreview(optimized, result.label || '');
  } catch (err) {
    if (err?.name === 'AbortError' || controller?.signal.aborted) {
      showToast('排版优化已取消');
    } else {
      console.error('membox: polish failed', err);
      showToast(err?.message || '排版优化失败');
    }
  } finally {
    running = false;
    controller = null;
    renderDock();
  }
}

function enterPreview(optimized, label) {
  originalMarkdown = state.currentMarkdown || '';
  optimizedMarkdown = optimized;
  previewDocID = session.documentID;
  previewLabel = label;
  previewing = true;
  setAnnotInteractionsEnabled(false);
  document.body.classList.add('polish-preview');
  loadDocument(optimized);
  renderConfirmBar();
  renderDock();
  showToast('预览排版结果 — 底部确认或放弃');
}

function exitPreview() {
  previewing = false;
  originalMarkdown = '';
  optimizedMarkdown = '';
  previewDocID = '';
  previewLabel = '';
  setAnnotInteractionsEnabled(true);
  document.body.classList.remove('polish-preview');
  confirmBar?.remove();
  confirmBar = null;
  closeDiff();
  renderDock();
}

async function discardPreview() {
  const original = originalMarkdown;
  const docID = previewDocID;
  exitPreview();
  loadDocument(original);
  if (docID) await restoreReadingState(docID, original);
  showToast('已放弃排版优化');
}

async function applyPreview() {
  const docID = previewDocID;
  const optimized = optimizedMarkdown;
  exitPreview();
  try {
    // Re-anchor the stored notes onto the optimized text BEFORE the replace
    // sync, so the sidecar carries them (text-quote anchors survive layout
    // changes) instead of submitting the empty post-loadDocument set.
    if (docID) await restoreReadingState(docID, optimized);
    await syncReplacementToMembox();
    showToast('排版已应用');
  } catch (err) {
    console.error('membox: applying polish failed', err);
    showToast(err?.message || '保存失败');
  }
}

function diffSummary() {
  return buildDiffHunks(originalMarkdown, optimizedMarkdown);
}

function renderConfirmBar() {
  confirmBar?.remove();
  const { counts } = diffSummary();
  const layout = counts.heading + counts.whitespace;
  const parts = [];
  if (counts.heading) parts.push(`${counts.heading} 处标题调整`);
  if (counts.whitespace) parts.push(`${counts.whitespace} 处空白/缩进`);
  const summaryText = parts.length ? parts.join(' · ') : '无排版变化';
  const violation = counts.content > 0;

  confirmBar = document.createElement('div');
  confirmBar.className = 'polish-bar';
  confirmBar.innerHTML =
    `<span class="polish-bar-icon" aria-hidden="true">${WAND_ICON}</span>` +
    `<span class="polish-bar-summary${violation ? ' is-warning' : ''}">` +
    escapeHtml(summaryText) +
    (violation ? ` · <strong>${counts.content} 处正文变更!</strong>` : '') +
    (previewLabel ? `<span class="polish-bar-model">${escapeHtml(previewLabel)}</span>` : '') +
    '</span>' +
    '<span class="polish-bar-spacer"></span>' +
    `<button type="button" class="polish-btn polish-btn-diff"${layout + counts.content === 0 ? ' disabled' : ''}>差异</button>` +
    '<button type="button" class="polish-btn polish-btn-apply">应用</button>' +
    '<button type="button" class="polish-btn polish-btn-discard">放弃</button>';
  confirmBar.querySelector('.polish-btn-diff').addEventListener('click', toggleDiff);
  confirmBar.querySelector('.polish-btn-apply').addEventListener('click', () => void applyPreview());
  confirmBar.querySelector('.polish-btn-discard').addEventListener('click', () => void discardPreview());
  document.body.appendChild(confirmBar);
}

function toggleDiff() {
  if (diffOverlay) {
    closeDiff();
    return;
  }
  const { hunks, counts } = diffSummary();
  diffOverlay = document.createElement('div');
  diffOverlay.className = 'polish-diff-overlay';
  diffOverlay.setAttribute('role', 'dialog');
  diffOverlay.setAttribute('aria-label', '排版差异');

  const body = hunks.length
    ? hunks.map(renderHunk).join('')
    : '<p class="polish-diff-empty">没有差异</p>';
  diffOverlay.innerHTML =
    '<div class="polish-diff-panel">' +
    '<div class="polish-diff-head">' +
    `<span>${counts.heading} 标题 · ${counts.whitespace} 空白 · ` +
    `<span class="${counts.content ? 'polish-diff-violation' : ''}">${counts.content} 正文变更</span></span>` +
    '<span class="polish-bar-spacer"></span>' +
    '<button type="button" class="polish-btn polish-diff-close">关闭</button>' +
    '</div>' +
    `<div class="polish-diff-body">${body}</div>` +
    '</div>';
  diffOverlay.querySelector('.polish-diff-close').addEventListener('click', closeDiff);
  diffOverlay.addEventListener('click', (event) => {
    if (event.target === diffOverlay) closeDiff();
  });
  document.body.appendChild(diffOverlay);
}

function renderHunk(hunk) {
  const lines = hunk.lines
    .map((op) => {
      if (op.type === 'same') {
        return `<div class="polish-diff-line is-same">${escapeHtml(op.line) || ' '}</div>`;
      }
      const kind = op.type === 'del' ? 'is-del' : 'is-add';
      const sign = op.type === 'del' ? '−' : '+';
      return `<div class="polish-diff-line ${kind}"><span class="polish-diff-sign">${sign}</span>${escapeHtml(op.line) || ' '}</div>`;
    })
    .join('');
  const cls = hunk.kind === 'content' ? ' is-violation' : '';
  return `<div class="polish-diff-hunk${cls}">${lines}</div>`;
}

function closeDiff() {
  diffOverlay?.remove();
  diffOverlay = null;
}

export function initPolish() {
  dockButton = document.createElement('button');
  dockButton.type = 'button';
  dockButton.className = 'file-action polish-action';
  dockButton.dataset.action = 'polish';
  dockButton.title = 'Fix heading hierarchy (DeepSeek, outline only) · Alt-click for full rewrite (local model)';
  dockButton.setAttribute('aria-label', dockButton.title);
  dockButton.innerHTML = WAND_ICON;
  dockButton.hidden = true;
  dockButton.addEventListener('click', (event) => {
    void (event.altKey ? startFullRewrite() : startOutlinePolish());
  });
  // Shares the left file rail with summarize/delete, ahead of the danger
  // zone button.
  addFileAction(dockButton, { beforeAction: 'delete' });

  // Esc discards the preview (or closes the diff first), mirroring how Miru
  // closes the TOC / annotation toolbar.
  document.addEventListener('keydown', (event) => {
    if (event.key !== 'Escape' || !previewing) return;
    if (diffOverlay) {
      closeDiff();
      return;
    }
    void discardPreview();
  });

  // Navigating to another document while previewing tears the preview down
  // without restoring — the new document owns the pane now.
  onRender(() => {
    if (previewing && session.documentID !== previewDocID) exitPreview();
    renderDock();
  });
  renderDock();
}
