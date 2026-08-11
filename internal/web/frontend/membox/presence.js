/* Presence heartbeat: this tab reports itself to the Web Companion so the TUI
   can show how many readers are connected and whether any hold unsaved notes.
   Best-effort; failures never disturb reading. */

import { session } from './session.js';
import { postPresence } from './api.js';

const presenceTabId = (() => {
  try {
    const key = 'membox-presence-tab';
    let id = window.sessionStorage.getItem(key);
    if (!id) {
      id = Math.random().toString(36).slice(2) + Date.now().toString(36);
      window.sessionStorage.setItem(key, id);
    }
    return id;
  } catch (err) {
    return Math.random().toString(36).slice(2) + Date.now().toString(36);
  }
})();

let presenceTimer = 0;
let lastPresenceAt = 0;

export function reportPresence(gone) {
  if (!session.connected && !gone) return;
  // Throttle live heartbeats; renders fire faster than the server needs them.
  const now = Date.now();
  if (!gone && now - lastPresenceAt < 1000) return;
  lastPresenceAt = now;
  const params = new URLSearchParams();
  params.set('tab', presenceTabId);
  if (gone) {
    params.set('gone', '1');
  } else {
    params.set('dirty', session.annotationsDirty ? '1' : '0');
  }
  postPresence(params, gone).catch(() => {});
}

export function startPresenceHeartbeat() {
  window.clearInterval(presenceTimer);
  presenceTimer = window.setInterval(() => reportPresence(false), 10000);
}

// Keep the companion's tab census in step with connection/dirty changes;
// the interval covers quiet periods. Called by the chrome render pass.
export function syncPresence() {
  if (session.connected) {
    startPresenceHeartbeat();
    reportPresence(false);
  } else {
    window.clearInterval(presenceTimer);
  }
}

export function initPresence() {
  window.addEventListener('pagehide', () => reportPresence(true));
}
