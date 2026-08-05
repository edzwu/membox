/* Miru — topbar chrome: theme toggle, TOC drawer pin/collapse, and the
   empty-state paste hint. Pure UI/localStorage preferences, no document or
   annotation state. */

import { STORAGE_KEY } from '../constants.js';
import { elements } from '../dom.js';
import { state } from '../state.js';

// Pure presentation update. Annotation mutation events are emitted separately
// by annotations/session.js so document loads and restores cannot look like
// user edits to optional persistence adapters.
export function updateMarkdownDownloadControl() {
  const hasAnnotations = state.annotations.length > 0;
  const label = hasAnnotations
    ? 'Download Markdown + annotations (.miru.zip)'
    : 'Download Markdown';
  elements.downloadAll.setAttribute('aria-label', label);
  elements.downloadAll.title = label;
}

export function applyTheme(theme) {
  elements.html.setAttribute('data-theme', theme);
  if (elements.themeIcon) {
    elements.themeIcon.textContent = theme === 'dark' ? '☀' : '☾';
  }
  elements.themeToggle.setAttribute('aria-label', `Switch to ${theme === 'dark' ? 'light' : 'dark'} theme`);
}

export function initTheme() {
  let theme = 'light';
  const saved = localStorage.getItem(STORAGE_KEY);

  if (saved === 'dark' || saved === 'light') {
    theme = saved;
  }

  applyTheme(theme);
}

export function toggleTheme() {
  const current = elements.html.getAttribute('data-theme') || 'light';
  const next = current === 'dark' ? 'light' : 'dark';
  applyTheme(next);
  try {
    localStorage.setItem(STORAGE_KEY, next);
  } catch (e) {
    // Storage may be unavailable in private mode or when quota is full.
  }
}

export function toggleToc() {
  const isOpen = elements.toc.classList.toggle('is-open');
  elements.tocToggle.setAttribute('aria-expanded', String(isOpen));
  elements.tocBackdrop.hidden = !isOpen;
}

export function closeToc() {
  elements.toc.classList.remove('is-open');
  elements.tocToggle.setAttribute('aria-expanded', 'false');
  elements.tocBackdrop.hidden = true;
}

function applyTocPinState(isCollapsed) {
  elements.toc.classList.toggle('is-collapsed', isCollapsed);
  const label = isCollapsed ? 'Expand contents' : 'Collapse contents';
  elements.tocPin.setAttribute('aria-expanded', String(!isCollapsed));
  elements.tocPin.setAttribute('aria-label', label);
  elements.tocPin.setAttribute('title', label);
}

export function toggleTocPin() {
  const isCollapsed = !elements.toc.classList.contains('is-collapsed');
  applyTocPinState(isCollapsed);
  try {
    localStorage.setItem('miru-toc-collapsed', String(isCollapsed));
  } catch (e) {
    // Storage may be unavailable in private mode or when quota is full.
  }
}

export function initTocCollapse() {
  if (!elements.tocPin) return;
  let isCollapsed = false;
  try {
    isCollapsed = localStorage.getItem('miru-toc-collapsed') === 'true';
  } catch (e) {
    // Storage may be unavailable in private mode or when cookies are disabled.
  }
  applyTocPinState(isCollapsed);
}

export function updateEmptyKbd() {
  if (!elements.emptyKbd) return;
  const isMac = /mac|iphone|ipad|ipod/i.test(navigator.platform || '');
  elements.emptyKbd.textContent = isMac ? '⌘V' : 'Ctrl+V';
}
