/* Miru — note ↔ passage focus.
   Notes sit inline under their passage (translation-style). This module only
   links the two sides: hover glow, click-to-scroll, and a short pulse. */

import { elements } from '../dom.js';
import { prefersReducedMotion } from '../utils.js';
import { expandSectionForHeading } from '../render/folding.js';
import { findNoteCard, scheduleNoteLayout } from './layout.js';

let clearTimer = null;
let activeId = null;

function pairFor(id) {
  const key = String(id);
  return {
    id: key,
    anchor: elements.article.querySelector(`span.annot[data-annot-id="${key}"]`),
    card: findNoteCard(key),
  };
}

function clearFocusClasses() {
  elements.article.querySelectorAll('.anchor-active').forEach((el) => el.classList.remove('anchor-active'));
  document.querySelectorAll('.annot-note.note-focus').forEach((el) => el.classList.remove('note-focus'));
}

export function clearNoteFocus() {
  if (clearTimer) {
    clearTimeout(clearTimer);
    clearTimer = null;
  }
  activeId = null;
  clearFocusClasses();
}

function setPairActive(id, on) {
  const { anchor, card } = pairFor(id);
  if (anchor) anchor.classList.toggle('anchor-active', on);
  if (card) card.classList.toggle('note-focus', on);
}

function expandFor(el) {
  if (!el) return;
  const section = el.closest('.fold-section');
  const heading = section && section.querySelector(':scope > .fold-heading');
  if (heading) expandSectionForHeading(heading);
}

function scrollToEl(el) {
  if (!el || !el.getClientRects().length) return;
  const behavior = prefersReducedMotion() ? 'auto' : 'smooth';
  el.scrollIntoView({ behavior, block: 'center' });
}

/** Toggle the inline note body under a passage. Returns the new collapsed state. */
export function toggleNoteInline(id) {
  const { anchor, card } = pairFor(id);
  if (!card) return false;
  // jp-study keeps chips only — never toggle a body card.
  if (card.classList.contains('is-jp-study') || card.classList.contains('membox-jp-card-hidden')) {
    return true;
  }
  const collapsed = !card.classList.contains('is-collapsed');
  card.classList.toggle('is-collapsed', collapsed);
  card.hidden = collapsed;
  if (anchor) {
    anchor.classList.toggle('note-collapsed', collapsed);
    const badge = anchor.querySelector('.annot-note-num');
    if (badge) {
      badge.classList.toggle('is-collapsed', collapsed);
      badge.title = collapsed ? '显示笔记' : '隐藏笔记';
      badge.setAttribute('aria-expanded', String(!collapsed));
    }
  }
  if (!collapsed) {
    // Brief pulse when reopening — no scroll (inline is already nearby).
    focusNote(id, { duration: 900 });
  } else {
    clearNoteFocus();
  }
  scheduleNoteLayout();
  return collapsed;
}

export function isNoteInlineCollapsed(id) {
  const card = findNoteCard(id);
  return Boolean(card?.classList.contains('is-collapsed') || card?.hidden);
}

// Highlight the note/passage pair. Optionally scroll the opposite side into
// view (`scrollTo`: 'anchor' | 'card' | null).
export function focusNote(id, options = {}) {
  const { scrollTo = null, duration = 2200 } = options;
  const { anchor, card } = pairFor(id);
  if (!anchor && !card) return;

  if (clearTimer) {
    clearTimeout(clearTimer);
    clearTimer = null;
  }
  clearFocusClasses();
  activeId = String(id);
  setPairActive(activeId, true);

  if (scrollTo === 'anchor' && anchor) {
    expandFor(anchor);
    scheduleNoteLayout();
    requestAnimationFrame(() => scrollToEl(anchor));
  } else if (scrollTo === 'card' && card) {
    expandFor(card);
    // Reveal if the user had collapsed it via the reference number.
    if (card.classList.contains('is-collapsed')) {
      card.classList.remove('is-collapsed');
      card.hidden = false;
      const badge = anchor?.querySelector('.annot-note-num');
      if (badge) {
        badge.classList.remove('is-collapsed');
        badge.title = '隐藏笔记';
        badge.setAttribute('aria-expanded', 'true');
      }
      anchor?.classList.remove('note-collapsed');
    } else {
      card.hidden = false;
    }
    scheduleNoteLayout();
    // Inline notes sit next to the passage — avoid jumping the viewport.
    if (!card.classList.contains('membox-inline-note')) {
      requestAnimationFrame(() => scrollToEl(card));
    }
  }

  if (duration > 0) {
    clearTimer = setTimeout(() => {
      clearTimer = null;
      if (activeId === String(id)) clearNoteFocus();
    }, duration);
  }
}

function onHover(e) {
  const over = e.type === 'mouseover';
  const card = e.target.closest && e.target.closest('.annot-note');
  if (card && card.dataset.annotId) {
    // Don't fight an active click-focus pulse.
    if (activeId && activeId !== String(card.dataset.annotId)) return;
    if (!activeId) setPairActive(card.dataset.annotId, over);
    return;
  }
  const span = e.target.closest && e.target.closest('span.annot.annot-note-ref');
  if (span && span.dataset.annotId) {
    if (activeId && activeId !== String(span.dataset.annotId)) return;
    if (!activeId) setPairActive(span.dataset.annotId, over);
  }
}

export function initNoteFocus() {
  elements.article.addEventListener('mouseover', onHover);
  elements.article.addEventListener('mouseout', onHover);
  elements.annotationLayer.addEventListener('mouseover', onHover);
  elements.annotationLayer.addEventListener('mouseout', onHover);
}
