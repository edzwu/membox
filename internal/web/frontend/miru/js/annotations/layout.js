/* Miru — margin note layout.
   Wide screens: notes sit in a collision-resolved right rail next to the
   reading column. Narrow screens: notes fall back to in-flow asides after
   their anchor block. The reading column itself is never punctured. */

import { elements } from '../dom.js';
import { NOTE_RAIL_WIDTH, NOTE_RAIL_GAP, NOTE_RAIL_OUTER_GUTTER, NOTE_RAIL_STACK_GAP } from '../constants.js';

let noteLayoutTimer = null;

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

// Drop leftover float-era chrome if an old session or export left any behind.
function scrubLegacyFloatChrome() {
  elements.article.querySelectorAll('.annot-ghost, .annot-ghost-clear, .annot-leader').forEach((n) => n.remove());
  elements.article.querySelectorAll('.annot-note-floated').forEach((card) => {
    card.classList.remove('annot-note-floated', 'is-dragging', 'will-dock');
    card.style.removeProperty('left');
    card.style.removeProperty('width');
  });
  elements.article.querySelectorAll('.has-annot-ghost').forEach((el) => {
    el.classList.remove('has-annot-ghost');
  });
}

export function layoutMarginNotes() {
  scrubLegacyFloatChrome();
  refreshNoteNumbers();
  layoutRailCards();
}

function layoutRailCards() {
  const cards = Array.from(elements.article.querySelectorAll('.annot-note:not(.annot-note-in-cell)'));
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
        // Anchor is in a collapsed section (or gone): park the card out of
        // the way until the section opens and layout runs again.
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
  positioned.forEach((item, index) => {
    const top = Math.max(item.anchorTop, previousBottom + NOTE_RAIL_STACK_GAP);
    const parentTop = item.parent.getBoundingClientRect().top;
    item.card.style.top = Math.round(top - parentTop) + 'px';
    // Later cards stack above earlier ones when they overlap during scroll.
    item.card.style.zIndex = String(4 + index);
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
