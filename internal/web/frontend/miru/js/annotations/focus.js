/* Miru — note ↔ passage focus.
   Notes stay in the margin rail (or in-flow asides on narrow screens).
   This module only links the two sides: hover glow, click-to-scroll, and a
   short pulse so the pair is easy to find without ever punching holes in the
   reading column. */

import { elements } from '../dom.js';
import { prefersReducedMotion } from '../utils.js';
import { expandSectionForHeading } from '../render/folding.js';
import { scheduleNoteLayout } from './layout.js';

let clearTimer = null;
let activeId = null;

function pairFor(id) {
  const key = String(id);
  return {
    id: key,
    anchor: elements.article.querySelector(`span.annot[data-annot-id="${key}"]`),
    card: elements.annotationLayer.querySelector(`.annot-note[data-annot-id="${key}"]`),
  };
}

function clearFocusClasses() {
  elements.article.querySelectorAll('.anchor-active').forEach((el) => el.classList.remove('anchor-active'));
  elements.annotationLayer.querySelectorAll('.note-focus').forEach((el) => el.classList.remove('note-focus'));
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
    // Unhide if a previous layout pass hid a card whose anchor was collapsed.
    card.hidden = false;
    scheduleNoteLayout();
    requestAnimationFrame(() => scrollToEl(card));
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
