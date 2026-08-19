/* Miru — layout the annotation layer independently from article content.
   Wide screens align cards in a right rail beside their anchors. Narrower
   screens keep the same sibling layer as a dedicated notes region below the
   article; cards are never inserted into the prose DOM. */

import { elements } from '../dom.js';
import { NOTE_RAIL_WIDTH, NOTE_RAIL_GAP, NOTE_RAIL_OUTER_GUTTER, NOTE_RAIL_STACK_GAP } from '../constants.js';

let noteLayoutTimer = null;

export function refreshNoteNumbers() {
  const refs = Array.from(elements.article.querySelectorAll('span.annot-note-ref'));
  refs.forEach((ref, index) => {
    const label = String(index + 1);
    const badge = ref.querySelector('.annot-note-num');
    if (badge) badge.textContent = label;
    const card = elements.annotationLayer.querySelector(`.annot-note[data-annot-id="${ref.dataset.annotId}"]`);
    const cardLabel = card && card.querySelector('.annot-note-label');
    if (cardLabel) cardLabel.textContent = label;
  });
}

function scrubLegacyFloatChrome() {
  elements.article.querySelectorAll('.annot-ghost, .annot-ghost-clear, .annot-leader').forEach((node) => node.remove());
  elements.annotationLayer.querySelectorAll('.annot-note-floated').forEach((card) => {
    card.classList.remove('annot-note-floated', 'is-dragging', 'will-dock');
    card.style.removeProperty('left');
    card.style.removeProperty('width');
  });
  elements.article.querySelectorAll('.has-annot-ghost').forEach((element) => {
    element.classList.remove('has-annot-ghost');
  });
}

function cardsInAnchorOrder() {
  const cards = Array.from(elements.article.querySelectorAll('span.annot-note-ref[data-annot-id]'))
    .map((anchor) => elements.annotationLayer.querySelector(`.annot-note[data-annot-id="${anchor.dataset.annotId}"]`))
    .filter((card) => card
      && !card.classList.contains('is-jp-study')
      && !card.classList.contains('membox-jp-card-hidden'));
  cards.forEach((card, index) => {
    const current = elements.annotationLayer.children[index];
    if (current !== card) elements.annotationLayer.insertBefore(card, current || null);
  });
  return cards;
}

export function layoutMarginNotes() {
  scrubLegacyFloatChrome();
  refreshNoteNumbers();

  const cards = cardsInAnchorOrder();
  if (!cards.length) {
    elements.article.classList.remove('has-note-rail');
    elements.annotationLayer.classList.remove('is-rail', 'is-stack');
    return;
  }

  const surfaceRect = elements.readingSurface.getBoundingClientRect();
  const articleMax = parseFloat(getComputedStyle(elements.article).getPropertyValue('--article-max')) || 860;
  const neededWidth = articleMax + NOTE_RAIL_GAP + NOTE_RAIL_WIDTH + NOTE_RAIL_OUTER_GUTTER * 2;
  const useRail = window.innerWidth > 900 && surfaceRect.width >= neededWidth;

  elements.article.classList.toggle('has-note-rail', useRail);
  elements.annotationLayer.classList.toggle('is-rail', useRail);
  elements.annotationLayer.classList.toggle('is-stack', !useRail);
  cards.forEach((card) => {
    // jp-study notes are inline UUID chips only — never float in the rail.
    if (card.classList.contains('is-jp-study') || card.classList.contains('membox-jp-card-hidden')) {
      card.hidden = true;
      card.style.removeProperty('top');
      card.style.removeProperty('z-index');
      return;
    }
    card.hidden = false;
    card.classList.toggle('annot-note-in-rail', useRail);
    card.classList.remove('annot-note-in-cell');
    if (!useRail) {
      card.style.removeProperty('top');
      card.style.removeProperty('z-index');
    }
  });
  if (!useRail) return;

  const layerRect = elements.annotationLayer.getBoundingClientRect();
  const positioned = cards
    .map((card) => {
      const anchor = elements.article.querySelector(`span.annot[data-annot-id="${card.dataset.annotId}"]`);
      if (!anchor || anchor.getClientRects().length === 0) {
        card.hidden = true;
        return null;
      }
      return {
        anchorTop: anchor.getBoundingClientRect().top - layerRect.top,
        card,
        height: card.getBoundingClientRect().height,
      };
    })
    .filter(Boolean)
    .sort((left, right) => left.anchorTop - right.anchorTop);

  let previousBottom = -Infinity;
  positioned.forEach((item, index) => {
    const top = Math.max(item.anchorTop, previousBottom + NOTE_RAIL_STACK_GAP);
    item.card.style.top = `${Math.round(top)}px`;
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
