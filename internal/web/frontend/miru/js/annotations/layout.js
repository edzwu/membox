/* Miru — present annotation notes inline with the source passage (translation-
   style), not in a sidebar rail. Short notes expand in-flow under the host
   block; long notes collapse to a titled hyperlink that opens the full note. */

import { elements } from '../dom.js';

let noteLayoutTimer = null;

const INLINE_HOST_SELECTOR = 'p, li, blockquote, pre, td, th, dd, dt, h1, h2, h3, h4, h5, h6';

export function refreshNoteNumbers() {
  const refs = Array.from(elements.article.querySelectorAll('span.annot-note-ref'));
  refs.forEach((ref, index) => {
    const label = String(index + 1);
    const badge = ref.querySelector(':scope > .annot-note-num');
    if (badge) badge.textContent = label;
    const card = findNoteCard(ref.dataset.annotId);
    const cardLabel = card && card.querySelector('.annot-note-label');
    if (cardLabel) cardLabel.textContent = label;
  });
}

export function findNoteCard(id) {
  const key = String(id);
  return elements.article.querySelector(`.annot-note[data-annot-id="${key}"]`)
    || elements.annotationLayer.querySelector(`.annot-note[data-annot-id="${key}"]`);
}

function scrubLegacyFloatChrome() {
  elements.article.querySelectorAll('.annot-ghost, .annot-ghost-clear, .annot-leader').forEach((node) => node.remove());
  document.querySelectorAll('.annot-note-floated, .annot-note-in-rail').forEach((card) => {
    card.classList.remove('annot-note-floated', 'annot-note-in-rail', 'is-dragging', 'will-dock');
    card.style.removeProperty('left');
    card.style.removeProperty('width');
    card.style.removeProperty('top');
    card.style.removeProperty('z-index');
  });
  elements.article.querySelectorAll('.has-annot-ghost').forEach((element) => {
    element.classList.remove('has-annot-ghost');
  });
}

function noteHostBlock(anchor) {
  if (!anchor) return null;
  // Multi-paragraph selections wrap several blocks INSIDE the annot span
  // (surroundContents fails across element boundaries). closest() only walks
  // ancestors, so it would miss those and fall back to fold-body/article —
  // which parked the note after the whole section (end of the document).
  const innerBlocks = anchor.querySelectorAll(INLINE_HOST_SELECTOR);
  if (innerBlocks.length) {
    return innerBlocks[innerBlocks.length - 1];
  }

  const outer = anchor.closest(INLINE_HOST_SELECTOR);
  if (outer) return outer;

  // Bare text under fold-body / article: place after the element child that
  // contains the anchor, never after the whole fold-body.
  const root = anchor.closest('.fold-body, .article, article') || elements.article;
  if (root) {
    let el = anchor;
    while (el.parentElement && el.parentElement !== root) {
      el = el.parentElement;
    }
    if (el && el !== root) return el;
  }
  return anchor;
}

function placeNoteInline(anchor, card) {
  if (!anchor || !card || !anchor.isConnected) {
    if (card) card.hidden = true;
    return;
  }
  // jp-study keeps UUID chips only; never show a floating/inline body card.
  if (card.classList.contains('is-jp-study') || card.classList.contains('membox-jp-card-hidden')) {
    card.hidden = true;
    if (card.parentElement !== elements.annotationLayer) {
      elements.annotationLayer.appendChild(card);
    }
    return;
  }

  // Honor user collapse via the reference-number toggle.
  const collapsed = card.classList.contains('is-collapsed');
  card.hidden = collapsed;
  card.classList.add('membox-inline-note');
  card.classList.remove('annot-note-in-rail', 'annot-note-in-cell');
  card.style.removeProperty('top');
  card.style.removeProperty('z-index');
  if (anchor) {
    anchor.classList.toggle('note-collapsed', collapsed);
    const badge = anchor.querySelector(':scope > .annot-note-num');
    if (badge) {
      badge.classList.toggle('is-collapsed', collapsed);
      badge.title = collapsed ? '显示笔记' : '隐藏笔记';
      badge.setAttribute('aria-expanded', String(!collapsed));
      badge.setAttribute('role', 'button');
    }
  }

  const host = noteHostBlock(anchor);
  if (!host || !host.isConnected) {
    elements.annotationLayer.appendChild(card);
    return;
  }

  // Never place after structural shells — that dumps the note at section end.
  if (host.matches?.('.fold-body, .fold-section, .article, article, .reading-surface')) {
    // Last resort: append as the final child so it still stays inside the shell
    // next to the trailing content, not after the whole section.
    if (card.parentElement !== host) host.appendChild(card);
    return;
  }

  // Mirror translation placement: list/table cells append inside; blocks get
  // a sibling under the host so the note sits under the quoted passage.
  if (host.matches('li, td, th, dd, dt')) {
    if (card.parentElement !== host) host.appendChild(card);
    return;
  }

  // If the host lives inside the annot span (multi-block wrap), the note must
  // leave the span — insert after the annot itself when host is the last inner
  // block, so we do not nest a block note inside the highlight span incorrectly.
  // Prefer: after the host block, which may still be inside the span; promote
  // to after the annot span when host is contained by anchor.
  let placeAfter = host;
  if (anchor.contains(host) && host !== anchor) {
    placeAfter = anchor;
  }
  if (card.previousElementSibling === placeAfter && card.parentElement === placeAfter.parentElement) {
    return;
  }
  placeAfter.insertAdjacentElement('afterend', card);
}

export function layoutMarginNotes() {
  scrubLegacyFloatChrome();
  refreshNoteNumbers();

  // Sidebar rail is retired — notes always live inline with the prose.
  elements.article.classList.remove('has-note-rail');
  elements.annotationLayer.classList.remove('is-rail', 'is-stack');
  elements.annotationLayer.classList.add('is-inline-park');

  const anchors = Array.from(elements.article.querySelectorAll('span.annot-note-ref[data-annot-id]'));
  const seen = new Set();
  for (const anchor of anchors) {
    const id = String(anchor.dataset.annotId || '');
    if (!id || seen.has(id)) continue;
    seen.add(id);
    const card = findNoteCard(id);
    if (!card) continue;
    placeNoteInline(anchor, card);
  }

  // Park orphan cards (anchor gone / collapsed) back in the hidden layer.
  document.querySelectorAll('.annot-note[data-annot-id]').forEach((card) => {
    const id = String(card.dataset.annotId || '');
    if (seen.has(id)) return;
    if (card.classList.contains('is-jp-study') || card.classList.contains('membox-jp-card-hidden')) {
      card.hidden = true;
      return;
    }
    card.hidden = true;
    if (card.parentElement !== elements.annotationLayer) {
      elements.annotationLayer.appendChild(card);
    }
  });
}

export function scheduleNoteLayout() {
  if (noteLayoutTimer !== null) return;
  noteLayoutTimer = window.setTimeout(() => {
    noteLayoutTimer = null;
    layoutMarginNotes();
  }, 0);
}
