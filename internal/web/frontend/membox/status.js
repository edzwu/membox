/* Bottom-left status cluster: save/copy badge and the add-related button,
   plus the download button's connected-mode wording. The badge's hollow dot
   means there are changes to save; a filled dot means the document is durable
   and the badge copies its UUID. */

import { elements } from '../js/dom.js';
import { showToast, writeClipboard } from '../js/ui/feedback.js';
import { updateMarkdownDownloadControl } from '../js/ui/chrome.js';
import { session } from './session.js';
import { syncToMembox } from './sync.js';
import { getDocumentNavigation } from './document.js';
import { renderBrowseNotesButton, setBrowseNotesVisible } from './notes.js';
import { hideRelatedPanel, loadRelated, openRelatedModal } from './related.js';

let statusCluster = null;
let statusBadge = null;
let addRelatedButton = null;

function createStatusCluster() {
  const cluster = document.createElement('div');
  cluster.className = 'membox-status-cluster';
  cluster.hidden = true;
  document.body.appendChild(cluster);
  return cluster;
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
  if (!session.connected) {
    updateMarkdownDownloadControl();
    return;
  }
  const label = session.syncing
    ? 'Syncing to membox…'
    : session.annotationsDirty
      ? 'Sync Markdown and unsaved notes to membox'
      : 'Sync Markdown and notes to membox';
  elements.downloadAll.setAttribute('aria-label', label);
  elements.downloadAll.title = label;
  elements.downloadAll.classList.toggle('is-syncing', session.syncing);
}

export function renderDocStatus() {
  if (!session.connected) {
    getDocumentNavigation().hidden = true;
    statusCluster.hidden = true;
    statusBadge.hidden = true;
    addRelatedButton.hidden = true;
    setBrowseNotesVisible(false);
    hideRelatedPanel();
    return;
  }
  statusCluster.hidden = false;
  statusBadge.hidden = false;
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

export function initStatus() {
  statusCluster = createStatusCluster();
  statusBadge = createStatusBadge();
  addRelatedButton = createAddRelatedButton();

  // Miru updates this title when annotations change; connected mode owns its
  // sync wording, so immediately re-apply it after those generic updates.
  new MutationObserver(() => {
    if (session.connected && !elements.downloadAll.title.startsWith('Sync Markdown') && !session.syncing) {
      setDownloadMeaning();
    }
  }).observe(elements.downloadAll, { attributes: true, attributeFilter: ['title'] });
}
