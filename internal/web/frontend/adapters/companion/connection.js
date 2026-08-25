/* Connection lifecycle: the top-bar connection button, connect/disconnect,
   and the connection-specific slice of the chrome render pass. */

import { elements } from '../../js/dom.js';
import { showToast } from '../../js/ui/feedback.js';
import { setAnnotHostCapabilitiesEnabled } from '../../js/annotations/toolbar.js';
import { session } from './session.js';
import { emitRender } from './events.js';
import { checkStatus } from './api.js';
import { cancelPendingSaves, scheduleReadingStateSave } from './reading-state.js';
import { autoCreateForAnnotations } from './sync.js';

let connectionButton = null;

function createConnectionButton() {
  const button = document.createElement('button');
  button.type = 'button';
  button.id = 'membox-connection';
  button.className = 'action membox-connection';
  button.dataset.connected = 'false';
  button.innerHTML = `
    <svg viewBox="0 0 24 24" aria-hidden="true">
      <circle cx="7" cy="12" r="3.25" fill="none" stroke="currentColor" stroke-width="1.5"/>
      <circle cx="17" cy="12" r="3.25" fill="none" stroke="currentColor" stroke-width="1.5"/>
      <path class="connection-bridge" d="M10.25 12h3.5" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/>
      <path class="connection-slash" d="M5 5l14 14" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/>
    </svg>
    <span class="sr-only">membox connection</span>`;
  elements.tocToggle.insertAdjacentElement('afterend', button);
  button.addEventListener('click', toggleConnection);
  return button;
}

export function renderConnectionButton() {
  const enabled = session.connected;
  document.documentElement.dataset.memboxConnected = String(enabled);
  setAnnotHostCapabilitiesEnabled(enabled);
  connectionButton.disabled = session.connecting;
  connectionButton.dataset.connected = String(enabled);
  connectionButton.dataset.sync = session.annotationsDirty ? 'dirty' : 'clean';
  connectionButton.classList.toggle('is-connecting', session.connecting);
  const label = session.connecting
    ? 'Checking membox connection…'
    : session.connected
      ? session.annotationsDirty
        ? 'Connected to membox — notes waiting to sync'
        : 'Connected to membox — click to disconnect'
      : session.annotationsDirty
        ? 'Disconnected — notes remain available in this session'
        : 'Disconnected from membox — click to connect';
  connectionButton.setAttribute('aria-label', label);
  connectionButton.title = label;
}

async function toggleConnection() {
  if (session.connecting) return;
  if (session.connected) {
    session.connected = false;
    cancelPendingSaves();
    emitRender();
    showToast(session.annotationsDirty
      ? 'Disconnected — notes remain available locally'
      : 'Disconnected from membox');
    return;
  }
  session.connecting = true;
  emitRender();
  session.connected = await checkStatus();
  session.connecting = false;
  emitRender();
  if (session.connected && session.annotationsDirty) {
    showToast('Connected to membox — syncing session notes');
    session.savePending = false;
    if (session.documentID) scheduleReadingStateSave();
    else void autoCreateForAnnotations();
  } else {
    showToast(session.connected ? 'Connected to membox' : 'Could not connect to membox');
  }
}

export function initConnection() {
  connectionButton = createConnectionButton();
}
