/* Miru — host-neutral annotation session lifecycle.
   The live in-memory model is always authoritative for the current page;
   optional persistence adapters observe versioned change events and may mark a
   version durable without becoming a prerequisite for editing. */

import { state } from '../state.js';
import { updateMarkdownDownloadControl } from '../ui/chrome.js';

export function createAnnotationClientId() {
  if (window.crypto && typeof window.crypto.randomUUID === 'function') {
    return window.crypto.randomUUID();
  }
  const random = Math.random().toString(36).slice(2);
  return `miru-${Date.now().toString(36)}-${random}`;
}

export function resetAnnotationSession() {
  state.annotations = [];
  state.noteCounter = 0;
  state.annotationSessionId++;
  state.annotationVersion = 0;
  state.annotationSavedVersion = 0;
  updateMarkdownDownloadControl();
}

export function notifyAnnotationsChanged() {
  state.annotationVersion++;
  updateMarkdownDownloadControl();
  window.dispatchEvent(new CustomEvent('miru-annotations-changed', {
    detail: {
      sessionId: state.annotationSessionId,
      version: state.annotationVersion,
      origin: 'user',
    },
  }));
}

// A durable sink (bundle download, membox, future browser storage) calls this
// with the version/session it actually saved. A stale async response must not
// mark a newly loaded document as durable.
export function markAnnotationsSaved(sessionId = state.annotationSessionId, version = state.annotationVersion) {
  if (sessionId !== state.annotationSessionId) return false;
  state.annotationSavedVersion = Math.max(state.annotationSavedVersion, Math.min(version, state.annotationVersion));
  window.dispatchEvent(new CustomEvent('miru-annotations-saved', {
    detail: { sessionId, version: state.annotationSavedVersion },
  }));
  return true;
}

export function hasUnsavedAnnotationChanges() {
  return state.annotationVersion !== state.annotationSavedVersion;
}
