/* Miru — composition root.
   Paste Markdown, render it, build a TOC, and stay quiet.

   This file only wires things together: "when the user does X, call Y".
   The actual behavior lives in feature modules under js/ — see js/README.md
   (or the project README's "Project structure" section) for the map. */

import { elements } from './js/dom.js';
import { initTheme, toggleTheme, toggleToc, toggleTocPin, closeToc, initTocCollapse, updateEmptyKbd } from './js/ui/chrome.js';
import { initMarkdown, setEmptyState, clearDocument } from './js/document.js';
import { toggleFoldAll } from './js/render/folding.js';
import { onTocClick } from './js/render/toc.js';
import { initAnnotations, hideAnnotToolbar } from './js/annotations/toolbar.js';
import { hasUnsavedAnnotationChanges } from './js/annotations/session.js';
import { copyAllMarkdown, downloadAllMarkdown } from './js/export/markdown-export.js';
import { exportPNG, copyPNG } from './js/export/png.js';
import { exportSiteZip } from './js/export/site.js';
import { onPaste } from './js/io/paste.js';
import { initDropZone } from './js/io/drop.js';

function bindEvents() {
  document.addEventListener('paste', onPaste);
  elements.brand.addEventListener('click', clearDocument);
  elements.themeToggle.addEventListener('click', toggleTheme);
  elements.foldToggle.addEventListener('click', toggleFoldAll);
  elements.tocToggle.addEventListener('click', toggleToc);
  elements.tocPin.addEventListener('click', toggleTocPin);
  elements.tocBackdrop.addEventListener('click', closeToc);
  elements.tocNav.addEventListener('click', onTocClick);
  if (elements.tocRail) elements.tocRail.addEventListener('click', onTocClick);
  elements.copyAll.addEventListener('click', copyAllMarkdown);
  elements.downloadAll.addEventListener('click', downloadAllMarkdown);
  elements.pngAll.addEventListener('click', exportPNG);
  elements.htmlAll.addEventListener('click', exportSiteZip);
  elements.copyPng.addEventListener('click', copyPNG);
  document.addEventListener('keydown', onKeydown);
  window.addEventListener('beforeunload', onBeforeUnload);
}

function onBeforeUnload(event) {
  if (!hasUnsavedAnnotationChanges()) return;
  event.preventDefault();
  event.returnValue = '';
}

function onKeydown(event) {
  if (event.key === 'Escape') {
    closeToc();
    hideAnnotToolbar();
  }
}

function init() {
  initTheme();
  initTocCollapse();
  updateEmptyKbd();
  setEmptyState();
  bindEvents();
  initMarkdown();
  initDropZone();
  initAnnotations();
}

init();

// Stable integration hook: host adapters may load a document or add their own
// persistence controls after Miru has finished wiring its UI.
window.__miruReady = true;
window.dispatchEvent(new CustomEvent('miru-ready'));
