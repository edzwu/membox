/* Miru — the global paste listener: read clipboard text and load it. Code
   vs Markdown is decided inside loadDocument (see js/render/snippet.js), so
   every entry point behaves the same. */

import { state } from '../state.js';
import { isEditableTarget } from '../utils.js';
import { loadDocument } from '../document.js';

export function onPaste(event) {
  if (isEditableTarget(event.target)) return;

  const clipboard = event.clipboardData || window.clipboardData;
  const text = clipboard.getData('text/plain');
  if (!text || !text.trim()) return;

  event.preventDefault();
  state.droppedFilename = '';
  loadDocument(text.trim());
}
