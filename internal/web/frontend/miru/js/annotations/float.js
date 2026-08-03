/* Miru — floated note cards: drag a note card into the prose column and the
   browser's own line layout wraps text around it, pretext-style. An invisible
   float spacer ("ghost") reserves a rectangular hole hugging the left/right
   edge; the card is absolutely positioned over the ghost. (Arbitrary x with
   two-sided wrap is impossible in CSS — a line box is one contiguous interval
   and only ever fills the channel on the float's opposite side — so the hole
   hugs an edge and takes its freedom out on the vertical axis instead.)
   Runtime geometry is container-relative ({ edge, top, containerIndex }) — a
   float never displaces the content above it, so placement is idempotent and
   free of measurement feedback loops. The sidecar stores an anchor-relative
   projection ({ edge, dy: px below the anchor passage }) so positions survive
   viewport changes. */

import { elements } from '../dom.js';
import { state } from '../state.js';
import { NOTE_RAIL_WIDTH, FLOAT_TEXT_GAP, FLOAT_BOTTOM_GAP, FLOAT_DOCK_THRESHOLD } from '../constants.js';
import { prefersReducedMotion } from '../utils.js';
import { findAnnot, insertCardNaturally } from './model.js';
import { onAfterNoteLayout, scheduleNoteLayout, layoutMarginNotes } from './layout.js';

let drag = null;
let suppressClickUntil = 0;
let syncQueued = false;

// The click that follows a real drag must not reach the passage/card click
// handlers (edit buttons etc.). toolbar.js consults this guard.
export function consumeSuppressedClick() {
  return performance.now() < suppressClickUntil;
}

function allContainers() {
  return Array.from(elements.article.querySelectorAll('.lead-section, .fold-body'));
}

function visibleContainers() {
  return allContainers().filter((el) => el.getClientRects().length > 0);
}

function containerIndex(container) {
  return allContainers().indexOf(container);
}

// The deepest prose container under a viewport y, so a card dropped over a
// nested section wraps that section's text rather than its ancestor's.
function deepestContainerAt(y) {
  let best = null;
  let bestDepth = -1;
  visibleContainers().forEach((el) => {
    const rect = el.getBoundingClientRect();
    if (y < rect.top || y > rect.bottom) return;
    let depth = 0;
    for (let node = el.parentElement; node; node = node.parentElement) depth++;
    if (depth > bestDepth) {
      bestDepth = depth;
      best = el;
    }
  });
  return best;
}

function ghostFor(id) {
  return elements.article.querySelector(`.annot-ghost[data-annot-id="${id}"]`);
}

function ensureGhost(id) {
  const existing = ghostFor(id);
  if (existing) return existing;
  const ghost = document.createElement('div');
  ghost.className = 'annot-ghost';
  ghost.dataset.annotId = id;
  ghost.setAttribute('aria-hidden', 'true');
  return ghost;
}

export function removeGhost(ghost) {
  const container = ghost.parentElement;
  ghost.remove();
  cleanupContainer(container);
}

function leaderFor(id) {
  return elements.article.querySelector(`.annot-leader[data-annot-id="${id}"]`);
}

// Leaders are a transient wayfinding aid, not permanent chrome: they show
// while dragging/hovering and fade out shortly after the interaction ends.
// Visibility is kept as state in `visibleLeaders`; syncLeader enforces it
// whenever it runs, so rAF/timer event ordering can never strand a class.
const visibleLeaders = new Set();
const leaderHideTimers = new Map();

function showLeaderNow(id) {
  const pending = leaderHideTimers.get(id);
  if (pending) {
    clearTimeout(pending);
    leaderHideTimers.delete(id);
  }
  visibleLeaders.add(String(id));
  const leader = leaderFor(id);
  if (leader) leader.classList.add('is-visible');
}

function hideLeaderLater(id, delay) {
  const pending = leaderHideTimers.get(id);
  if (pending) clearTimeout(pending);
  leaderHideTimers.set(id, setTimeout(() => {
    leaderHideTimers.delete(id);
    visibleLeaders.delete(String(id));
    // Query at fire time: the leader may not have existed when this was
    // scheduled (first sync can land after pointerup).
    const leader = leaderFor(id);
    if (leader) leader.classList.remove('is-visible');
  }, delay));
}

function ensureLeader(id) {
  const existing = leaderFor(id);
  if (existing) return existing;
  const svg = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
  svg.setAttribute('class', 'annot-leader');
  svg.dataset.annotId = id;
  svg.setAttribute('aria-hidden', 'true');
  const path = document.createElementNS('http://www.w3.org/2000/svg', 'path');
  const dot = document.createElementNS('http://www.w3.org/2000/svg', 'circle');
  dot.setAttribute('r', '2.2');
  svg.appendChild(path);
  svg.appendChild(dot);
  return svg;
}

// The leader connects the card's edge to the anchor passage's first line, so
// a card dragged far away stays traceable. All coordinates are
// container-relative; the svg itself is a 1px box with visible overflow.
function syncLeader(card, container, entry) {
  const leader = leaderFor(entry.id) || ensureLeader(entry.id);
  const anchor = elements.article.querySelector(`span.annot[data-annot-id="${entry.id}"]`);
  const anchorRect = anchor && anchor.getClientRects()[0];
  if (!anchorRect) {
    leader.style.display = 'none';
    return;
  }
  if (leader.parentElement !== container) container.appendChild(leader);
  leader.style.display = '';
  const containerRect = container.getBoundingClientRect();
  const cardRect = card.getBoundingClientRect();
  const onLeftEdge = entry.float && entry.float.edge === 'left';
  const startX = (onLeftEdge ? cardRect.right : cardRect.left) - containerRect.left;
  const startY = cardRect.top - containerRect.top + 14;
  const endX = (onLeftEdge ? anchorRect.left - 6 : anchorRect.right + 6) - containerRect.left;
  const endY = anchorRect.top - containerRect.top + anchorRect.height / 2;
  leader.querySelector('path').setAttribute('d', `M ${startX} ${startY} L ${endX} ${endY}`);
  const dot = leader.querySelector('circle');
  dot.setAttribute('cx', endX);
  dot.setAttribute('cy', endY);
  leader.classList.toggle('is-visible', visibleLeaders.has(String(entry.id)));
}

// Blocks the ghost may sit between: prose blocks, not annotation UI.
function isFlowBlock(child) {
  return child.nodeType === Node.ELEMENT_NODE &&
    !child.classList.contains('annot-ghost') &&
    !child.classList.contains('annot-ghost-clear') &&
    !child.classList.contains('annot-note') &&
    child.tagName !== 'BUTTON';
}

// The clear sentinel is the classic clearfix: an empty block at the end of
// the container gets pushed below all floats, so the container grows to
// contain the hole and following sections start below it. Unlike
// `display: flow-root`, this changes NO margin collapsing when it appears —
// no spacing jump while dragging a card between containers.
function ensureSentinel(container) {
  if (container.querySelector(':scope > .annot-ghost-clear')) return;
  const sentinel = document.createElement('div');
  sentinel.className = 'annot-ghost-clear';
  sentinel.setAttribute('aria-hidden', 'true');
  container.appendChild(sentinel);
}

function cleanupContainer(container) {
  if (!container || container.querySelector(':scope > .annot-ghost')) return;
  container.classList.remove('has-annot-ghost');
  const sentinel = container.querySelector(':scope > .annot-ghost-clear');
  if (sentinel) sentinel.remove();
}

function insertGhost(ghost, container, ref) {
  if (ref) {
    if (ghost.parentElement !== container || ghost.nextElementSibling !== ref) {
      container.insertBefore(ghost, ref);
    }
  } else {
    // Appending at the end: keep ghosts ahead of the floated cards and in
    // their creation order so float stacking is deterministic.
    const ghosts = container.querySelectorAll(':scope > .annot-ghost');
    const lastGhost = ghosts[ghosts.length - 1];
    if (lastGhost && lastGhost !== ghost) {
      container.insertBefore(ghost, lastGhost.nextSibling);
    } else if (!lastGhost) {
      container.appendChild(ghost);
    }
  }
  if (ghost.parentElement === container) ensureSentinel(container);
}

function placeGhost(ghost, container, geom, cardW, cardH) {
  const edge = geom.edge === 'left' ? 'left' : 'right';
  const containerRect = container.getBoundingClientRect();
  const holeW = Math.min(cardW + FLOAT_TEXT_GAP, containerRect.width);
  const holeH = cardH + FLOAT_BOTTOM_GAP;

  const sig = [containerIndex(container), edge, Math.round(geom.top), holeW, holeH].join('|');
  if (ghost.dataset.sig === sig && ghost.parentElement === container) return;
  ghost.dataset.sig = sig;

  let desiredTop = containerRect.top + Math.max(0, geom.top);

  // Insert ahead of the first block reaching past the target. A float never
  // displaces earlier content, so the insert-then-measure-then-margin sequence
  // converges exactly on the requested offset.
  const blocks = Array.from(container.children).filter((child) => isFlowBlock(child) && child !== ghost);
  let ref = null;
  let lastBottom = null;
  for (const child of blocks) {
    const rect = child.getBoundingClientRect();
    if (rect.width === 0 && rect.height === 0) continue;
    lastBottom = rect.bottom;
    if (rect.bottom > desiredTop) {
      ref = child;
      break;
    }
  }
  if (!ref && lastBottom !== null) {
    desiredTop = Math.min(desiredTop, Math.max(containerRect.top, lastBottom - holeH));
  }

  const oldContainer = ghost.parentElement;
  insertGhost(ghost, container, ref);
  if (oldContainer && oldContainer !== container) cleanupContainer(oldContainer);
  container.classList.add('has-annot-ghost');

  ghost.style.float = edge;
  ghost.style.width = holeW + 'px';
  ghost.style.height = holeH + 'px';
  ghost.style.marginTop = '0px';
  const settledTop = ghost.getBoundingClientRect().top;
  ghost.style.marginTop = Math.max(0, Math.round(desiredTop - settledTop)) + 'px';
}

function syncCardToGhost(card, ghost, container, edge) {
  const containerRect = container.getBoundingClientRect();
  const ghostRect = ghost.getBoundingClientRect();
  const top = ghostRect.top - containerRect.top;
  const left = edge === 'left'
    ? ghostRect.left - containerRect.left
    : ghostRect.right - containerRect.left - card.offsetWidth;
  const prevTop = parseFloat(card.style.top);
  const prevLeft = parseFloat(card.style.left);
  if (!Number.isFinite(prevTop) || Math.abs(prevTop - top) > 0.5) card.style.top = top + 'px';
  if (!Number.isFinite(prevLeft) || Math.abs(prevLeft - left) > 0.5) card.style.left = left + 'px';
}

// Reconcile every floated card with its entry.float geometry. Runs after each
// margin-note layout pass (rail toggle, resize, font load, edits) and on
// every animation frame while dragging.
export function syncFloatedCards() {
  const containers = allContainers();
  state.annotations.forEach((entry) => {
    if (!entry.float) return;
    const card = elements.article.querySelector(`.annot-note[data-annot-id="${entry.id}"]`);
    if (!card) return;
    const container = containers[entry.float.containerIndex];
    if (!container || container.getClientRects().length === 0) return;
    if (!card.classList.contains('annot-note-floated')) {
      card.classList.add('annot-note-floated');
      card.classList.remove('annot-note-in-rail');
    }
    if (card.parentElement !== container) container.appendChild(card);
    // Keep the card (and therefore the hole) inside a narrow column.
    const containerW = container.getBoundingClientRect().width;
    const cardW = Math.max(120, Math.min(NOTE_RAIL_WIDTH, containerW - 2 * FLOAT_TEXT_GAP));
    if (card.style.width !== cardW + 'px') card.style.width = cardW + 'px';
    const ghost = ensureGhost(entry.id);
    placeGhost(ghost, container, entry.float, cardW, card.offsetHeight);
    syncCardToGhost(card, ghost, container, entry.float.edge);
    // Restore-time refinement: dy was captured in ghost-present layout, but
    // floatGeometryFromAnchor measured the anchor in ghost-free layout (the
    // containment toggle and the wrap itself shift content slightly). One
    // correction pass lands the card exactly dy below the anchor.
    if (entry.float.dyHint !== undefined) {
      const anchor = elements.article.querySelector(`span.annot[data-annot-id="${entry.id}"]`);
      if (anchor && anchor.getClientRects().length > 0) {
        const actual = card.getBoundingClientRect().top - anchor.getBoundingClientRect().top;
        const error = actual - entry.float.dyHint;
        if (Math.abs(error) > 1) {
          entry.float.top = Math.max(0, entry.float.top - error);
          placeGhost(ghost, container, entry.float, cardW, card.offsetHeight);
          syncCardToGhost(card, ghost, container, entry.float.edge);
        }
        delete entry.float.dyHint;
      }
    }
    syncLeader(card, container, entry);
  });
}

function queueFloatSync() {
  if (syncQueued) return;
  syncQueued = true;
  requestAnimationFrame(() => {
    syncQueued = false;
    syncFloatedCards();
  });
}

// Project sidecar float geometry ({ edge, dy: px below the anchor }) onto
// the live layout. Returns container-relative runtime geometry, or null when
// the anchor's section is not measurable (collapsed).
export function floatGeometryFromAnchor(saved, span) {
  const container = span.closest('.lead-section, .fold-body');
  if (!container) return null;
  const index = allContainers().indexOf(container);
  if (index === -1) return null;
  const containerRect = container.getBoundingClientRect();
  if (containerRect.width === 0) return null;
  const spanRect = span.getBoundingClientRect();
  return {
    edge: saved.edge,
    top: Math.max(0, Math.round(spanRect.top + saved.dy - containerRect.top)),
    containerIndex: index,
    dyHint: saved.dy,
  };
}

// Inverse of floatGeometryFromAnchor: capture anchor-relative geometry for
// the sidecar. Called at save time by sidecar.js.
export function captureFloatGeometry(entry, span) {
  const card = elements.article.querySelector(`.annot-note[data-annot-id="${entry.id}"]`);
  if (!card || !span) return null;
  const cardRect = card.getBoundingClientRect();
  const spanRect = span.getBoundingClientRect();
  return {
    edge: entry.float && entry.float.edge === 'left' ? 'left' : 'right',
    dy: Math.round(cardRect.top - spanRect.top),
  };
}

// FLIP: animate the card from its previous rect to the freshly laid-out one.
function flipFrom(card, first) {
  if (prefersReducedMotion()) return;
  const last = card.getBoundingClientRect();
  const dx = first.left - last.left;
  const dy = first.top - last.top;
  if (Math.abs(dx) < 1 && Math.abs(dy) < 1) return;
  card.style.transition = 'none';
  card.style.transform = `translate(${dx}px, ${dy}px)`;
  let started = false;
  const start = () => {
    if (started || !card.isConnected) return;
    started = true;
    card.style.transition = 'transform 240ms cubic-bezier(0.2, 0.7, 0.3, 1)';
    card.style.transform = '';
    const done = () => {
      card.style.transition = '';
      card.removeEventListener('transitionend', done);
    };
    card.addEventListener('transitionend', done);
    setTimeout(done, 400); // fallback when transitionend never fires
  };
  requestAnimationFrame(start);
  setTimeout(start, 50); // rAF starvation fallback
}

// Return a floated card to its natural position (margin rail when the layout
// has one, otherwise an in-flow aside after its anchor block).
export function dockCard(entry) {
  entry.float = null;
  visibleLeaders.delete(String(entry.id));
  const card = elements.article.querySelector(`.annot-note[data-annot-id="${entry.id}"]`);
  const ghost = ghostFor(entry.id);
  if (ghost) removeGhost(ghost);
  const leader = leaderFor(entry.id);
  if (leader) leader.remove();
  if (card) {
    const first = card.getBoundingClientRect();
    card.classList.remove('annot-note-floated', 'is-dragging', 'will-dock');
    card.style.removeProperty('top');
    card.style.removeProperty('left');
    const anchor = elements.article.querySelector(`span.annot[data-annot-id="${entry.id}"]`);
    if (anchor) insertCardNaturally(card, anchor);
    layoutMarginNotes(); // synchronous: the rail takes the card back right now
    flipFrom(card, first);
  } else {
    scheduleNoteLayout();
  }
}

function currentContainerOf(entry) {
  const card = elements.article.querySelector(`.annot-note[data-annot-id="${entry.id}"]`);
  const fromCard = card && card.closest('.lead-section, .fold-body');
  if (fromCard) return fromCard;
  const anchor = elements.article.querySelector(`span.annot[data-annot-id="${entry.id}"]`);
  return anchor && anchor.closest('.lead-section, .fold-body');
}

function updateDragTarget(x, y) {
  const desiredTop = y - drag.grabDY;
  const container = deepestContainerAt(desiredTop) || deepestContainerAt(y) || currentContainerOf(drag.entry);
  if (!container) return;
  const rect = container.getBoundingClientRect();
  drag.willDock = x > rect.right + FLOAT_DOCK_THRESHOLD;
  drag.card.classList.toggle('will-dock', drag.willDock);
  if (drag.willDock) return;
  drag.entry.float = {
    edge: x < rect.left + rect.width / 2 ? 'left' : 'right',
    top: Math.max(0, desiredTop - rect.top),
    containerIndex: containerIndex(container),
  };
  // The leader is created lazily by the sync pass; keep claiming visibility
  // on every move so it shows from its first frame.
  showLeaderNow(drag.entry.id);
  queueFloatSync();
}

function cleanupDrag() {
  if (!drag) return;
  drag.card.classList.remove('is-dragging', 'will-dock');
  document.body.classList.remove('is-note-dragging');
  window.removeEventListener('pointermove', onPointerMove);
  window.removeEventListener('pointerup', onPointerUp);
  window.removeEventListener('pointercancel', onPointerCancel);
  drag = null;
}

function onPointerDown(e) {
  if (e.button !== 0 || !e.isPrimary) return;
  const card = e.target.closest && e.target.closest('.annot-note');
  if (!card || card.classList.contains('annot-note-in-cell')) return;
  if (e.target.closest('button, input, textarea, select, a')) return;
  const entry = findAnnot(card.dataset.annotId);
  if (!entry) return;
  // A new gesture cancels any still-running dock animation.
  card.style.transition = '';
  card.style.transform = '';
  const rect = card.getBoundingClientRect();
  drag = {
    pointerId: e.pointerId,
    card,
    entry,
    startX: e.clientX,
    startY: e.clientY,
    grabDX: e.clientX - rect.left,
    grabDY: e.clientY - rect.top,
    active: false,
    willDock: false,
    snapshot: entry.float ? { ...entry.float } : null,
  };
  window.addEventListener('pointermove', onPointerMove);
  window.addEventListener('pointerup', onPointerUp);
  window.addEventListener('pointercancel', onPointerCancel);
}

function onPointerMove(e) {
  if (!drag || e.pointerId !== drag.pointerId) return;
  if (!drag.active) {
    if (Math.hypot(e.clientX - drag.startX, e.clientY - drag.startY) < 5) return;
    drag.active = true;
    drag.card.classList.add('is-dragging');
    document.body.classList.add('is-note-dragging');
    showLeaderNow(drag.entry.id);
    const sel = window.getSelection();
    if (sel) sel.removeAllRanges();
    try { drag.card.setPointerCapture(drag.pointerId); } catch (err) { /* synthetic pointers */ }
  }
  e.preventDefault();
  updateDragTarget(e.clientX, e.clientY);
}

function onPointerUp(e) {
  if (!drag || e.pointerId !== drag.pointerId) return;
  const { active, willDock, entry } = drag;
  cleanupDrag();
  if (!active) return; // plain click — let the card's own handlers see it
  suppressClickUntil = performance.now() + 350;
  if (willDock) dockCard(entry);
  else {
    hideLeaderLater(entry.id, 3000);
    scheduleNoteLayout();
  }
}

function onPointerCancel(e) {
  if (!drag || (e.pointerId !== undefined && e.pointerId !== drag.pointerId)) return;
  drag.entry.float = drag.snapshot;
  const id = drag.entry.id;
  cleanupDrag();
  hideLeaderLater(id, 800);
  scheduleNoteLayout();
}

function onEscapeCancel(e) {
  if (e.key !== 'Escape' || !drag) return;
  drag.entry.float = drag.snapshot;
  const id = drag.entry.id;
  cleanupDrag();
  hideLeaderLater(id, 800);
  scheduleNoteLayout();
}

// Double-click is the explicit "return to rail" gesture.
function onDoubleClick(e) {
  const card = e.target.closest && e.target.closest('.annot-note.annot-note-floated');
  if (!card || e.target.closest('button, input')) return;
  const entry = findAnnot(card.dataset.annotId);
  if (entry) dockCard(entry);
}

// Hover linkage: a floated card can sit far from its anchor passage, so
// hovering either side highlights the other (and briefly revives the leader).
function onHoverGlow(e) {
  const card = e.target.closest && e.target.closest('.annot-note-floated');
  if (card) {
    const over = e.type === 'mouseover';
    const anchor = elements.article.querySelector(`span.annot[data-annot-id="${card.dataset.annotId}"]`);
    if (anchor) anchor.classList.toggle('anchor-active', over);
    if (over) showLeaderNow(card.dataset.annotId);
    else hideLeaderLater(card.dataset.annotId, 800);
    return;
  }
  const span = e.target.closest && e.target.closest('span.annot.annot-note-ref');
  if (span) {
    const over = e.type === 'mouseover';
    const linked = elements.article.querySelector(`.annot-note-floated[data-annot-id="${span.dataset.annotId}"]`);
    if (linked) linked.classList.toggle('anchor-active', over);
    if (leaderFor(span.dataset.annotId)) {
      if (over) showLeaderNow(span.dataset.annotId);
      else hideLeaderLater(span.dataset.annotId, 800);
    }
  }
}

export function initNoteFloats() {
  elements.article.addEventListener('pointerdown', onPointerDown);
  elements.article.addEventListener('dblclick', onDoubleClick);
  elements.article.addEventListener('mouseover', onHoverGlow);
  elements.article.addEventListener('mouseout', onHoverGlow);
  document.addEventListener('keydown', onEscapeCancel);
  onAfterNoteLayout(syncFloatedCards);
}
