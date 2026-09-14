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

const ICONS = {
  // Summary: a sheet of condensed lines.
  summarize: '<svg viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" aria-hidden="true"><path d="M5 4.5h14"/><path d="M5 9h14"/><path d="M5 13.5h9"/><path d="M5 18h6"/></svg>',
  // Trash can.
  delete: '<svg viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M4 7h16"/><path d="M9 7V4.5h6V7"/><path d="M6.5 7l1 12.2a2 2 0 0 0 2 1.8h5a2 2 0 0 0 2-1.8l1-12.2"/><path d="M10 11v5.5M14 11v5.5"/></svg>',
  // Armed (second-click confirm): warning mark.
  armed: '<svg viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true"><path d="M12 5v9"/><circle cx="12" cy="18.2" r="0.6" fill="currentColor"/></svg>',
};

function makeButton(action, iconName, title) {
  const button = document.createElement('button');
  button.type = 'button';
  button.className = 'file-action';
  button.dataset.action = action;
  button.innerHTML = ICONS[iconName];
  button.title = title;
  button.setAttribute('aria-label', title);
  return button;
}

let deleteTimer = null;

async function onDelete(button) {
  // Two-step confirm: first click arms, second click within 3s executes.
  if (!button.classList.contains('is-armed')) {
    button.classList.add('is-armed');
    button.innerHTML = ICONS.armed;
    button.title = '再次点击确认删除';
    showToast('再次点击确认删除');
    deleteTimer = setTimeout(() => {
      button.classList.remove('is-armed');
      button.innerHTML = ICONS.delete;
      button.title = '删除本文（移入回收站，可恢复）';
    }, 3000);
    return;
  }
  clearTimeout(deleteTimer);
  button.classList.remove('is-armed');
  button.innerHTML = ICONS.delete;
  button.title = '删除本文（移入回收站，可恢复）';

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

  const summarize = makeButton('summarize', 'summarize', '总结本文（本地模型）');
  summarize.addEventListener('click', () => {
    if (!visible()) return;
    void startDocSummarize(false);
  });

  const del = makeButton('delete', 'delete', '删除本文（移入回收站，可恢复）');
  del.classList.add('file-action-danger');
  del.addEventListener('click', () => void onDelete(del));

  rail.appendChild(summarize);
  rail.appendChild(del);
  layout.prepend(rail);

  syncVisibility();
  window.addEventListener('miru-document-change', syncVisibility);
}
