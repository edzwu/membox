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

// Desktop TOC always keeps the narrow rail slot. Pin only toggles whether the
// overlay pane stays open — it must never change layout width (no jitter).
function applyTocPinState(isPinned) {
  // Rail layout is permanent on desktop; mobile drawer CSS ignores it.
  elements.toc.classList.add('is-collapsed');
  elements.toc.classList.toggle('is-pinned', isPinned);
  if (isPinned) {
    elements.toc.classList.remove('is-hover-expanded');
  }
  const label = isPinned ? 'Unpin contents' : 'Pin contents open';
  elements.tocPin.setAttribute('aria-expanded', String(isPinned));
  elements.tocPin.setAttribute('aria-label', label);
  elements.tocPin.setAttribute('title', label);
  if (elements.tocRail) {
    elements.tocRail.setAttribute('aria-hidden', String(isPinned));
  }
}

export function toggleTocPin() {
  const isPinned = !elements.toc.classList.contains('is-pinned');
  applyTocPinState(isPinned);
  try {
    localStorage.setItem('miru-toc-pinned', String(isPinned));
    // Clear legacy key so old collapsed semantics cannot fight the new model.
    localStorage.removeItem('miru-toc-collapsed');
  } catch (e) {
    // Storage may be unavailable in private mode or when quota is full.
  }
}

export function initTocCollapse() {
  if (!elements.tocPin) return;
  // Default: rail + hover-to-open. Pin keeps the same pane fixed open.
  let isPinned = false;
  try {
    const pinned = localStorage.getItem('miru-toc-pinned');
    if (pinned !== null) {
      isPinned = pinned === 'true';
    } else {
      // Migrate: old "not collapsed" meant the wide pinned-open TOC.
      const legacy = localStorage.getItem('miru-toc-collapsed');
      if (legacy === 'false') isPinned = true;
    }
  } catch (e) {
    // Storage may be unavailable in private mode or when cookies are disabled.
  }
  applyTocPinState(isPinned);
  initTocHoverExpand();
}

// Opens the overlay pane while the pointer is over the rail or the floating
// pane. The pane overflows the 28px .toc box, so parent pointerleave is not
// reliable — use elementFromPoint against rail+pane hit targets instead.
function initTocHoverExpand() {
  if (!elements.toc) return;

  const isPinned = () => elements.toc.classList.contains('is-pinned');

  const hitTargets = () =>
    [elements.tocRail, elements.tocPane, elements.tocPin, elements.toc].filter(Boolean);

  const isPointerOverToc = (clientX, clientY) => {
    const stack = typeof document.elementsFromPoint === 'function'
      ? document.elementsFromPoint(clientX, clientY)
      : [document.elementFromPoint(clientX, clientY)].filter(Boolean);
    const targets = hitTargets();
    return stack.some((node) =>
      targets.some((root) => root === node || root.contains(node)),
    );
  };

  const setHoverExpanded = (open) => {
    if (isPinned()) return;
    elements.toc.classList.toggle('is-hover-expanded', open);
  };

  const syncFromEvent = (event) => {
    if (event.pointerType === 'touch') return;
    setHoverExpanded(isPointerOverToc(event.clientX, event.clientY));
  };

  // Track continuously: leaving the overflowing pane must collapse immediately.
  document.addEventListener('pointermove', syncFromEvent, { passive: true });
  document.addEventListener('pointerdown', syncFromEvent, { passive: true });

  // Keyboard: open while focus is inside TOC chrome; close when it leaves.
  elements.toc.addEventListener('focusin', () => setHoverExpanded(true));
  elements.toc.addEventListener('focusout', (event) => {
    if (!elements.toc.contains(event.relatedTarget)) setHoverExpanded(false);
  });
}

export function updateEmptyKbd() {
  if (!elements.emptyKbd) return;
  const isMac = /mac|iphone|ipad|ipod/i.test(navigator.platform || '');
  elements.emptyKbd.textContent = isMac ? '⌘V' : 'Ctrl+V';
}

// Reading mode: hide the topbar after idle, reveal again on vertical movement.
const TOPBAR_HIDE_MS = 3000;

export function initTopbarAutohide() {
  const topbar = elements.topbar;
  if (!topbar) return;

  let hideTimer = null;
  let pointerOnTopbar = false;
  let lastTouchY = null;

  const isReading = () => elements.body.classList.contains('is-reading');

  const clearHideTimer = () => {
    if (hideTimer !== null) {
      clearTimeout(hideTimer);
      hideTimer = null;
    }
  };

  const showTopbar = () => {
    topbar.classList.remove('is-auto-hidden');
    clearHideTimer();
    if (!isReading() || pointerOnTopbar) return;
    if (topbar.contains(document.activeElement)) return;
    hideTimer = setTimeout(() => {
      if (!isReading() || pointerOnTopbar) return;
      if (topbar.contains(document.activeElement)) return;
      topbar.classList.add('is-auto-hidden');
      hideTimer = null;
    }, TOPBAR_HIDE_MS);
  };

  const onVerticalActivity = () => {
    if (!isReading()) return;
    showTopbar();
  };

  window.addEventListener('scroll', onVerticalActivity, { passive: true, capture: true });
  window.addEventListener('wheel', onVerticalActivity, { passive: true });

  window.addEventListener('touchstart', (event) => {
    lastTouchY = event.touches[0] ? event.touches[0].clientY : null;
  }, { passive: true });
  window.addEventListener('touchmove', (event) => {
    const y = event.touches[0] ? event.touches[0].clientY : null;
    if (lastTouchY != null && y != null && Math.abs(y - lastTouchY) > 2) {
      onVerticalActivity();
    }
    lastTouchY = y;
  }, { passive: true });

  // Keep visible while interacting with the bar itself.
  topbar.addEventListener('pointerenter', () => {
    pointerOnTopbar = true;
    topbar.classList.remove('is-auto-hidden');
    clearHideTimer();
  });
  topbar.addEventListener('pointerleave', () => {
    pointerOnTopbar = false;
    showTopbar();
  });
  topbar.addEventListener('focusin', () => {
    topbar.classList.remove('is-auto-hidden');
    clearHideTimer();
  });
  topbar.addEventListener('focusout', (event) => {
    if (!topbar.contains(event.relatedTarget)) showTopbar();
  });

  // Empty state: always show. Entering reading mode starts the idle timer.
  const modeObserver = new MutationObserver(() => {
    if (!isReading()) {
      clearHideTimer();
      topbar.classList.remove('is-auto-hidden');
      return;
    }
    showTopbar();
  });
  modeObserver.observe(elements.body, { attributes: true, attributeFilter: ['class'] });

  if (isReading()) showTopbar();
}
