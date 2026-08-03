/* membox integration — save the currently loaded Markdown back into membox.
   The server creates a new document (assigning a fresh UUID) in the default
   configured path and returns its id. */

import { state } from './js/state.js';
import { showToast } from './js/ui/feedback.js';

async function saveToMembox() {
  const body = (state.currentMarkdown || '').trim();
  if (!body) {
    showToast('Nothing to save');
    return;
  }
  const title = (state.docTitle || '').trim() || 'Untitled';

  const button = document.getElementById('save-note');
  if (button) {
    button.disabled = true;
    button.textContent = 'Saving…';
  }

  try {
    const response = await fetch('/api/save', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ title, body }),
    });
    if (!response.ok) {
      const message = await response.text();
      throw new Error(message || `HTTP ${response.status}`);
    }
    const result = await response.json();
    showToast(`Saved to membox: ${result.id}`);
  } catch (err) {
    console.error('membox: save failed', err);
    showToast(`Save failed: ${err.message}`);
  } finally {
    if (button) {
      button.disabled = false;
      button.textContent = 'Save';
    }
  }
}

export function initSave() {
  const button = document.getElementById('save-note');
  if (button) {
    button.addEventListener('click', saveToMembox);
  }
}
