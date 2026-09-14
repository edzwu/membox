/* File-level action rail: a quiet vertical strip docked to the LEFT of the
   sheet (the TOC rail mirrors it on the right). Its actions operate on the
   whole document, not a passage — summarize through the local model, or
   soft-delete into the trash (restorable via `mm trash restore`). */

import { showToast } from '../../js/ui/feedback.js';
import { session } from './session.js';
import { startDocSummarize } from './doc-summarize.js';
import { unbindDocument } from './document.js';
import { trashDocument } from './api.js';

let rail = null;

function visible() {
  return Boolean(session.documentID);
}

function syncVisibility() {
  if (rail) rail.hidden = !visible();
}

function makeButton(action, glyph, title) {
  const button = document.createElement('button');
  button.type = 'button';
  button.className = 'file-action';
  button.dataset.action = action;
  button.textContent = glyph;
  button.title = title;
  button.setAttribute('aria-label', title);
  return button;
}

let deleteTimer = null;

async function onDelete(button) {
  // Two-step confirm: first click arms, second click within 3s executes.
  if (!button.classList.contains('is-armed')) {
    button.classList.add('is-armed');
    button.textContent = '删?';
    deleteTimer = setTimeout(() => {
      button.classList.remove('is-armed');
      button.textContent = '删';
    }, 3000);
    return;
  }
  clearTimeout(deleteTimer);
  button.classList.remove('is-armed');
  button.textContent = '删';

  const id = session.documentID;
  if (!id) return;
  try {
    await trashDocument(id);
    showToast('已移入回收站（mm trash restore 可恢复）');
    unbindDocument();
    window.location.assign(window.location.pathname);
  } catch (error) {
    showToast('删除失败：' + error.message);
  }
}

export function initFileActions() {
  const layout = document.querySelector('.layout');
  if (!layout || rail) return;

  rail = document.createElement('div');
  rail.className = 'membox-file-rail';

  const summarize = makeButton('summarize', '总', '总结本文（本地模型）');
  summarize.addEventListener('click', () => {
    if (!visible()) return;
    void startDocSummarize(false);
  });

  const del = makeButton('delete', '删', '删除本文（移入回收站，可恢复）');
  del.classList.add('file-action-danger');
  del.addEventListener('click', () => void onDelete(del));

  rail.appendChild(summarize);
  rail.appendChild(del);
  layout.prepend(rail);

  syncVisibility();
  window.addEventListener('miru-document-change', syncVisibility);
}
