/* Bottom-left status cluster: save/copy badge and the add-related button,
   plus the download button's connected-mode wording. The badge's hollow dot
   means there are changes to save; a filled dot means the document is durable
   and the badge copies its UUID.

   Low-frequency actions (search, translate) live in a collapsible extras tray
   that expands when the pointer nears the dock, on focus, or while pinned. */

import { elements } from '../js/dom.js';
import { showToast, writeClipboard } from '../js/ui/feedback.js';
import { updateMarkdownDownloadControl } from '../js/ui/chrome.js';
import { downloadAllMarkdown } from '../js/export/markdown-export.js';
import { session } from './session.js';
import { syncToMembox } from './sync.js';
import { getDocumentNavigation } from './document.js';
import { renderBrowseNotesButton, setBrowseNotesVisible } from './notes.js';
import { hideRelatedPanel, loadRelated, openRelatedModal } from './related.js';

let statusDock = null;
let statusCluster = null;
let statusExtras = null;
let statusBadge = null;
let addRelatedButton = null;
/** @type {Set<string>} */
const extrasPins = new Set();

// Hover the bottom-left proximity pad to reveal; hide after idle leave.
const STATUS_DOCK_HIDE_MS = 3000;
let statusDockHideTimer = null;
let statusDockPointerInside = false;
let statusDockAutohideBound = false;

function createStatusDock() {
  const dock = document.createElement('div');
  dock.className = 'membox-status-dock is-auto-hidden';
  dock.hidden = true;

  const cluster = document.createElement('div');
  cluster.className = 'membox-status-cluster';
  cluster.setAttribute('role', 'group');
  cluster.setAttribute('aria-label', 'Document status');

  const extras = document.createElement('div');
  extras.className = 'membox-status-extras';
  extras.dataset.collapsed = 'true';

  dock.appendChild(cluster);
  document.body.appendChild(dock);
  statusCluster = cluster;
  statusExtras = extras;
  // extras is appended after primary controls in initStatus
  return dock;
}

function clearStatusDockHideTimer() {
  if (statusDockHideTimer !== null) {
    clearTimeout(statusDockHideTimer);
    statusDockHideTimer = null;
  }
}

function shouldKeepStatusDockVisible() {
  if (!statusDock || statusDock.hidden) return false;
  if (statusDockPointerInside) return true;
  if (extrasPins.size > 0) return true;
  if (statusDock.contains(document.activeElement)) return true;
  return false;
}

function revealStatusDock() {
  if (!statusDock || statusDock.hidden) return;
  statusDock.classList.remove('is-auto-hidden');
  clearStatusDockHideTimer();
}

function scheduleStatusDockHide() {
  if (!statusDock || statusDock.hidden) return;
  clearStatusDockHideTimer();
  if (shouldKeepStatusDockVisible()) {
    statusDock.classList.remove('is-auto-hidden');
    return;
  }
  statusDockHideTimer = setTimeout(() => {
    statusDockHideTimer = null;
    if (shouldKeepStatusDockVisible()) return;
    statusDock.classList.add('is-auto-hidden');
  }, STATUS_DOCK_HIDE_MS);
}

function bindStatusDockAutohide() {
  if (!statusDock || statusDockAutohideBound) return;
  statusDockAutohideBound = true;

  statusDock.addEventListener('pointerenter', () => {
    statusDockPointerInside = true;
    revealStatusDock();
  });
  statusDock.addEventListener('pointerleave', () => {
    statusDockPointerInside = false;
    scheduleStatusDockHide();
  });
  statusDock.addEventListener('focusin', () => {
    revealStatusDock();
  });
  statusDock.addEventListener('focusout', (event) => {
    if (!statusDock.contains(event.relatedTarget)) scheduleStatusDockHide();
  });
}

function syncExtrasPinClass() {
  if (!statusDock) return;
  statusDock.classList.toggle('is-extras-open', extrasPins.size > 0);
  if (statusExtras) statusExtras.dataset.collapsed = extrasPins.size > 0 ? 'false' : 'true';
  // A pinned tool keeps the dock revealed; releasing pins restarts hide timer.
  if (extrasPins.size > 0) revealStatusDock();
  else if (!statusDockPointerInside) scheduleStatusDockHide();
}

function createStatusBadge() {
  const badge = document.createElement('button');
  badge.type = 'button';
  badge.id = 'membox-doc-status';
  badge.className = 'membox-doc-status';
  badge.hidden = true;
  badge.innerHTML = '<span class="membox-status-dot" aria-hidden="true"></span><span class="membox-status-label" aria-hidden="true"></span>';
  statusCluster.appendChild(badge);
  badge.addEventListener('click', () => {
    // Offline / no backend: left badge is download Markdown (or .miru.zip).
    if (!session.connected) {
      void downloadAllMarkdown();
      return;
    }
    if (session.syncing || session.saveInFlight) return;
    const saved = Boolean(session.documentID) && !session.annotationsDirty;
    if (!saved) {
      void syncToMembox();
      return;
    }
    writeClipboard(session.documentID, () => showToast('Copied document ID'));
  });
  return badge;
}

function createAddRelatedButton() {
  const button = document.createElement('button');
  button.type = 'button';
  button.id = 'membox-add-related';
  button.className = 'membox-add-related';
  button.hidden = true;
  button.textContent = '+';
  button.title = 'Add related document';
  button.setAttribute('aria-label', 'Add related document');
  statusCluster.appendChild(button);
  button.addEventListener('click', openRelatedModal);
  return button;
}

export function setDownloadMeaning() {
  // Connected sync wording used to live on the right-rail download button.
  // Primary action is now the left status badge (save when connected).
  if (!session.connected) {
    updateMarkdownDownloadControl();
    return;
  }
  if (elements.downloadAll) {
    elements.downloadAll.classList.remove('is-syncing');
  }
}

function isReading() {
  return document.body.classList.contains('is-reading');
}

function renderOfflineDownloadBadge() {
  getDocumentNavigation().hidden = true;
  addRelatedButton.hidden = true;
  setBrowseNotesVisible(false);
  hideRelatedPanel();

  const reading = isReading();
  if (statusDock) {
    statusDock.hidden = !reading;
    if (!reading) {
      statusDock.classList.add('is-auto-hidden');
      clearStatusDockHideTimer();
    } else if (!shouldKeepStatusDockVisible()) {
      statusDock.classList.add('is-auto-hidden');
    }
  }
  if (!statusBadge) return;
  statusBadge.hidden = !reading;
  statusBadge.dataset.mode = 'download';
  statusBadge.dataset.saved = 'false';
  statusBadge.dataset.saving = 'false';
  statusBadge.dataset.hasDocument = 'false';
  statusBadge.disabled = false;
  const label = statusBadge.querySelector('.membox-status-label');
  if (label) label.textContent = '';
  updateMarkdownDownloadControl();
}

export function renderDocStatus() {
  // Membox owns the bottom-left corner whenever the adapter is loaded.
  if (elements.localSaveDock) elements.localSaveDock.hidden = true;
  if (elements.downloadAll) elements.downloadAll.hidden = true;

  if (!session.connected) {
    renderOfflineDownloadBadge();
    return;
  }

  if (statusDock) {
    statusDock.hidden = false;
    // Stay quiet until the pointer enters the bottom-left proximity pad.
    if (!shouldKeepStatusDockVisible()) statusDock.classList.add('is-auto-hidden');
  }
  statusBadge.hidden = false;
  statusBadge.dataset.mode = 'save';
  const saved = Boolean(session.documentID) && !session.annotationsDirty;
  const saving = session.syncing || session.saveInFlight;
  const label = statusBadge.querySelector('.membox-status-label');
  statusBadge.dataset.saved = String(saved);
  statusBadge.dataset.saving = String(saving);
  statusBadge.dataset.hasDocument = String(Boolean(session.documentID));
  statusBadge.disabled = saving;
  if (saved) {
    const suffix = session.documentID.slice(-5);
    label.textContent = suffix;
    statusBadge.setAttribute('aria-label', `Copy document ID ${session.documentID}`);
    statusBadge.title = suffix;
  } else {
    label.textContent = '';
    statusBadge.setAttribute('aria-label', saving ? 'Saving changes' : 'Save changes');
    statusBadge.title = saving ? 'Saving changes…' : 'Unsaved changes · click to save';
  }
  renderBrowseNotesButton();
  if (session.documentID) {
    addRelatedButton.hidden = false;
    setBrowseNotesVisible(true);
    void loadRelated();
  } else {
    addRelatedButton.hidden = true;
    setBrowseNotesVisible(false);
    hideRelatedPanel();
  }
}

/** Visual pill inside the bottom-left dock. */
export function getStatusCluster() {
  return statusCluster;
}

/** Collapsible tray for low-frequency actions (search, translate). */
export function getStatusExtras() {
  return statusExtras;
}

/** Keep extras expanded while a low-frequency tool is in use. */
export function setStatusExtrasPinned(reason, pinned) {
  const key = String(reason || '').trim() || 'default';
  if (pinned) extrasPins.add(key);
  else extrasPins.delete(key);
  syncExtrasPinClass();
}

export function initStatus() {
  statusDock = createStatusDock();
  statusBadge = createStatusBadge();
  addRelatedButton = createAddRelatedButton();
  statusCluster.appendChild(statusExtras);
  bindStatusDockAutohide();

  // Offline download badge should appear as soon as a document is open.
  new MutationObserver(() => {
    if (!session.connected) renderDocStatus();
  }).observe(document.body, { attributes: true, attributeFilter: ['class'] });
}
