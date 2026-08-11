/* Chrome render composition: the single place that knows how a session-state
   change fans out to every piece of adapter chrome. Feature modules never
   import each other's renderers — they emit a render event (events.js) and
   this layer translates it. Keeps the dependency graph one-directional:
   features → events ← chrome → features. */

import { onRender } from './events.js';
import { renderConnectionButton } from './connection.js';
import { syncPresence } from './presence.js';
import { renderDocStatus, setDownloadMeaning } from './status.js';
import { renderDocumentSwitcher } from './document.js';

function renderAll() {
  renderConnectionButton();
  syncPresence();
  setDownloadMeaning();
  renderDocStatus();
  renderDocumentSwitcher();
}

export function initChrome() {
  onRender(renderAll);
}

// For the composition root's initial paint, before any event fires.
export function renderChromeNow() {
  renderAll();
}
