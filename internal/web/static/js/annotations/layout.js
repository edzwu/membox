/* Miru — Pretext-inspired margin note layout: treat annotation cards as
   independently placed obstacles, then preserve one uninterrupted reading
   column. On wide screens cards occupy a collision-resolved margin rail;
   otherwise they fall back to normal in-flow asides. */

import { elements } from '../dom.js';
import { NOTE_RAIL_WIDTH, NOTE_RAIL_GAP, NOTE_RAIL_OUTER_GUTTER, NOTE_RAIL_STACK_GAP } from '../constants.js';

let noteLayoutTimer = null;
let afterNoteLayout = null;

// Extra pass (floated note cards) that must run after every rail layout
// without coupling this module to js/annotations/float.js.
export function onAfterNoteLayout(fn) {
  afterNoteLayout = fn;
}

export function refreshNoteNumbers() {
  const refs = Array.from(elements.article.querySelectorAll('span.annot-note-ref'));
  refs.forEach((ref, index) => {
    const label = String(index + 1);
    const badge = ref.querySelector('.annot-note-num');
    if (badge) badge.textContent = label;
    const card = elements.article.querySelector(`.annot-note[data-annot-id="${ref.dataset.annotId}"]`);
    const cardLabel = card && card.querySelector('.annot-note-label');
    if (cardLabel) cardLabel.textContent = label;
  });
}

export function layoutMarginNotes() {
  refreshNoteNumbers();
  layoutRailCards();
  if (afterNoteLayout) afterNoteLayout();
}

function layoutRailCards() {
  const cards = Array.from(elements.article.querySelectorAll('.annot-note:not(.annot-note-in-cell):not(.annot-note-floated)'));
  if (!cards.length) {
    elements.article.classList.remove('has-note-rail');
    return;
  }

  const articleRect = elements.article.getBoundingClientRect();
  const articleMax = parseFloat(getComputedStyle(elements.article).getPropertyValue('--article-max')) || 860;
  const readingWidth = Math.min(articleMax, articleRect.width);
  const neededWidth = readingWidth + NOTE_RAIL_GAP + NOTE_RAIL_WIDTH + NOTE_RAIL_OUTER_GUTTER * 2;
  const useRail = window.innerWidth > 900 && articleRect.width >= neededWidth;
  elements.article.classList.toggle('has-note-rail', useRail);

  cards.forEach((card) => {
    card.hidden = false;
    card.classList.toggle('annot-note-in-rail', useRail);
    if (!useRail) card.style.removeProperty('top');
  });
  if (!useRail) return;

  const positioned = cards
    .map((card) => {
      const anchor = elements.article.querySelector(`span.annot[data-annot-id="${card.dataset.annotId}"]`);
      if (!anchor || anchor.getClientRects().length === 0) {
        card.hidden = true;
        return null;
      }
      const parent = card.offsetParent;
      if (!(parent instanceof HTMLElement)) return null;
      return {
        anchorTop: anchor.getBoundingClientRect().top,
        card,
        height: card.getBoundingClientRect().height,
        parent,
      };
    })
    .filter(Boolean)
    .sort((a, b) => a.anchorTop - b.anchorTop);

  let previousBottom = -Infinity;
  positioned.forEach((item) => {
    const top = Math.max(item.anchorTop, previousBottom + NOTE_RAIL_STACK_GAP);
    const parentTop = item.parent.getBoundingClientRect().top;
    item.card.style.top = Math.round(top - parentTop) + 'px';
    previousBottom = top + item.height;
  });
}

export function scheduleNoteLayout() {
  if (noteLayoutTimer !== null) return;
  noteLayoutTimer = window.setTimeout(() => {
    noteLayoutTimer = null;
    layoutMarginNotes();
  }, 0);
}
