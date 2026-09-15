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
  // Summary: text lines converging into a leftward arrow.
  summarize: '<svg viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M5 4.5h14"/><path d="M5 9h11"/><path d="M5 13.5h8"/><path d="M5 18h5"/><path d="M18 13l2.5 2.5L18 18"/><path d="M20.5 15.5h-3.5"/></svg>',
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
    button.title = 'Click again to confirm delete';
    showToast('Click again to confirm delete');
    deleteTimer = setTimeout(() => {
      button.classList.remove('is-armed');
      button.innerHTML = ICONS.delete;
      button.title = 'Move to trash (restorable via mm trash restore)';
    }, 3000);
    return;
  }
  clearTimeout(deleteTimer);
  button.classList.remove('is-armed');
  button.innerHTML = ICONS.delete;
  button.title = 'Move to trash (restorable via mm trash restore)';

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

  const summarize = makeButton('summarize', 'summarize', 'Summarize this document (local model)');
  summarize.addEventListener('click', () => {
    if (!visible()) return;
    void startDocSummarize(false);
  });

  const del = makeButton('delete', 'delete', 'Move to trash (restorable via mm trash restore)');
  del.classList.add('file-action-danger');
  del.addEventListener('click', () => void onDelete(del));

  rail.appendChild(summarize);
  rail.appendChild(del);
  layout.prepend(rail);

  syncVisibility();
  window.addEventListener('miru-document-change', syncVisibility);
}

/* Other whole-document features (e.g. layout polish) share this rail instead
   of growing their own floating chrome. */
export function addFileAction(button, { beforeAction } = {}) {
  if (!rail) return;
  const anchor = beforeAction ? rail.querySelector(`[data-action="${beforeAction}"]`) : null;
  rail.insertBefore(button, anchor);
}
