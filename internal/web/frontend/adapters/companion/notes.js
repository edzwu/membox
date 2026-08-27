/* Notes model and presentation: exposes the restored annotation notes to the
   unified document picker and owns long-note rail previews. */

import { elements } from '../../js/dom.js';
import { state } from '../../js/state.js';
import { focusNote } from '../../js/annotations/focus.js';
import { layoutMarginNotes } from '../../js/annotations/layout.js';
import { session } from './session.js';

// Short notes render full inline body (translation-style). Longer ones collapse
// to a titled hyperlink that opens the durable note document.
const INLINE_NOTE_MAX_RUNES = 140;
const INLINE_NOTE_MAX_LINES = 3;
const INLINE_NOTE_TITLE_RUNES = 42;

let notePreviewTimer = null;

export function noteDocumentURL(ref) {
  const url = new URL(window.location.href);
  url.searchParams.set('id', ref);
  // Carry the source document so the full-note page can render 「← 原文」.
  // `note` stays a source-page focus target only — do not put it on the note.
  if (session.documentID) {
    url.searchParams.set('from', session.documentID);
  } else {
    url.searchParams.delete('from');
  }
  url.searchParams.delete('note');
  url.hash = '';
  return url.href;
}

function sourceBacklinkURL(sourceID, noteRef) {
  const url = new URL(window.location.href);
  url.searchParams.set('id', sourceID);
  if (noteRef) url.searchParams.set('note', noteRef);
  else url.searchParams.delete('note');
  url.searchParams.delete('from');
  url.hash = '';
  return url.href;
}

/** Prominent return chrome when viewing a full selection-note document.
 *  Pass related items when available. Omitting them (or passing null) only
 *  paints from `?from=` and never tears down an existing backlink. */
export function renderNoteSourceBacklink(relatedItems = null) {
  const noteRef = session.documentID || '';
  if (!noteRef) {
    document.querySelectorAll('.membox-note-backlink').forEach((node) => node.remove());
    return;
  }

  // Prefer explicit ?from= (set by long-note / chip openers), else the related
  // graph edge that marks this document as an annotation note on a target.
  let sourceID = String(session.noteSourceFromRef || '').trim();
  let sourceTitle = '';
  const items = Array.isArray(relatedItems) ? relatedItems : null;
  if (items) {
    const annotated = items.find((item) => item && String(item.annotation_ref || '').trim());
    if (annotated) {
      if (!sourceID) sourceID = String(annotated.id || '').trim();
      sourceTitle = String(annotated.title || '').trim();
    }
  }
  if (!sourceID) {
    // Only remove when related finished and confirmed there is no source edge.
    if (items) {
      document.querySelectorAll('.membox-note-backlink').forEach((node) => node.remove());
    }
    return;
  }

  const href = sourceBacklinkURL(sourceID, noteRef);
  const label = sourceTitle || sourceID;

  let link = document.querySelector('.membox-note-backlink');
  if (!link) {
    link = document.createElement('a');
    link.className = 'membox-note-backlink';
    link.innerHTML =
      '<span class="membox-note-backlink-arrow" aria-hidden="true">←</span>' +
      '<span class="membox-note-backlink-kicker">原文</span>' +
      '<span class="membox-note-backlink-title"></span>';
  }
  link.href = href;
  link.title = `返回原文 ${label}`;
  link.setAttribute('aria-label', `返回原文 ${label}`);
  const titleEl = link.querySelector('.membox-note-backlink-title');
  if (titleEl) titleEl.textContent = label;

  // Prefer inside the article (survives surface layout quirks; scrolls with doc).
  // Fall back to reading-surface so it still shows before the title paints.
  if (elements.article?.isConnected) {
    const first = elements.article.firstChild;
    if (link.parentElement !== elements.article || first !== link) {
      elements.article.insertBefore(link, first);
    }
    return;
  }
  const surface = elements.readingSurface;
  if (surface?.isConnected) {
    surface.insertBefore(link, surface.firstChild);
  }
}

function captureReadingAnchor() {
  const viewportTop = document.querySelector('.topbar')?.getBoundingClientRect().bottom || 0;
  const candidates = [
    ...elements.article.querySelectorAll('.fold-heading, p, li, pre, blockquote, table, img, .annot-note'),
    ...elements.annotationLayer.querySelectorAll('.annot-note'),
  ];
  let best = null;
  for (const element of candidates) {
    const rect = element.getBoundingClientRect();
    if (rect.bottom <= viewportTop || rect.top >= window.innerHeight) continue;
    const score = Math.abs(rect.top - viewportTop);
    if (!best || score < best.score) best = { element, top: rect.top, score };
  }
  return best;
}

function restoreReadingAnchor(anchor) {
  if (!anchor || !anchor.element.isConnected) return;
  const delta = anchor.element.getBoundingClientRect().top - anchor.top;
  if (Math.abs(delta) > 0.5) window.scrollBy({ top: delta, behavior: 'auto' });
}

function focusRequestedSourceNote() {
  if (!session.pendingSourceNoteRef) return false;
  const entry = state.annotations.find((item) => item.ref === session.pendingSourceNoteRef);
  if (!entry) return false;
  session.pendingSourceNoteRef = '';
  session.sourceNoteFocused = true;
  focusNote(entry.id, { scrollTo: 'anchor', duration: 3000 });
  return true;
}

function notePlainForInline(entry) {
  let raw = String(entry?.note || '').trim();
  if (!raw) return '';
  // Match Miru card rendering: summary/jp notes hide the restore excerpt.
  if (entry.kind === 'summary' || /\*\*总结：?\*\*/.test(raw)) {
    const m = raw.match(/\*\*总结：?\*\*\s*\n+([\s\S]*)/);
    if (m) raw = m[1].trim();
  }
  if (entry.kind === 'translation' || /\*\*翻译：?\*\*/.test(raw)) {
    const m = raw.match(/\*\*翻译：?\*\*\s*\n+([\s\S]*)/);
    if (m) raw = m[1].trim();
  }
  if (entry.kind === 'jp-study' || /\*\*语：?\*\*/.test(raw)) {
    const m = raw.match(/\*\*语：?\*\*\s*\n+([\s\S]*)/);
    if (m) raw = m[1].trim();
  }
  // Strip common Markdown noise for length / title decisions.
  return raw
    .replace(/^>.*$/gm, '')
    .replace(/^#{1,6}\s+/gm, '')
    .replace(/\*\*([^*]+)\*\*/g, '$1')
    .replace(/\*([^*]+)\*/g, '$1')
    .replace(/`([^`]+)`/g, '$1')
    .replace(/\[([^\]]+)\]\([^)]+\)/g, '$1')
    .replace(/\s+/g, ' ')
    .trim();
}

function isLongInlineNote(plain) {
  if (!plain) return false;
  const runes = Array.from(plain);
  if (runes.length > INLINE_NOTE_MAX_RUNES) return true;
  // Approximate multi-sentence dumps even when under the rune budget.
  const sentences = plain.split(/[。！？.!?]+/).filter((part) => part.trim());
  return sentences.length > INLINE_NOTE_MAX_LINES;
}

function inlineNoteTitle(entry, plain) {
  const kind = entry?.kind || '';
  let title = plain.split(/[。！？\n]/)[0].trim() || plain;
  if (!title) {
    if (kind === 'summary') title = '总结';
    else if (kind === 'translation') title = '翻译';
    else if (kind === 'qa') title = 'Q&A';
    else if (kind === 'jp-study') title = '语';
    else title = '笔记';
  }
  const runes = Array.from(title);
  if (runes.length > INLINE_NOTE_TITLE_RUNES) {
    title = runes.slice(0, INLINE_NOTE_TITLE_RUNES - 1).join('') + '…';
  }
  return title;
}

function shortNoteRef(ref) {
  const value = String(ref || '').trim().replace(/-/g, '');
  if (!value) return '';
  return value.length >= 4 ? value.slice(-4) : value;
}

// Inline presentation: short notes keep their full body under the passage;
// long notes collapse to a titled hyperlink (full text stays in the model and
// edit textarea). Layout moves cards next to their anchors.
function renderLongNotePreviews() {
  const readingAnchor = captureReadingAnchor();
  elements.annotationLayer.classList.add('membox-notes-relayout');
  const entries = new Map(state.annotations.map((entry) => [String(entry.id), entry]));
  const cards = [
    ...elements.article.querySelectorAll('.annot-note[data-annot-id]'),
    ...elements.annotationLayer.querySelectorAll('.annot-note[data-annot-id]'),
  ];
  const seen = new Set();
  for (const card of cards) {
    const id = String(card.dataset.annotId || '');
    if (!id || seen.has(id)) continue;
    seen.add(id);
    const entry = entries.get(id);
    const body = card.querySelector('.annot-note-body');
    const text = body && body.querySelector('.annot-note-text');
    let link = body && body.querySelector('.annot-open-note');
    card.classList.remove('annot-note-preview', 'annot-note-link-only');
    delete card.dataset.exportRemoveClass;
    if (!entry || !text) {
      if (link) link.remove();
      continue;
    }
    // jp-study: UUID chip on the passage only — never show body/link card.
    if (entry.kind === 'jp-study' || card.classList.contains('is-jp-study')) {
      if (link) link.remove();
      continue;
    }

    // Generated Q&A and translations always stay full inline under the
    // passage — their body is the reading aid, not a link to another page.
    if (entry.kind === 'qa' || entry.kind === 'translation' ||
        card.classList.contains('is-qa') || card.classList.contains('is-translation')) {
      text.hidden = false;
      if (link) link.remove();
      card.classList.remove('annot-note-preview', 'annot-note-link-only');
      continue;
    }

    const plain = notePlainForInline(entry);
    const long = isLongInlineNote(plain);
    if (!long) {
      text.hidden = false;
      if (link) link.remove();
      continue;
    }

    // Long note → titled hyperlink inline; hide the prose body.
    card.classList.add('annot-note-preview', 'annot-note-link-only');
    card.dataset.exportRemoveClass = 'annot-note-preview';
    text.hidden = true;
    const title = inlineNoteTitle(entry, plain);
    if (!link) {
      link = document.createElement('a');
      link.className = 'annot-open-note';
      link.target = '_blank';
      link.rel = 'noopener noreferrer';
      // Keep the titled link in PNG/site exports — it is the note content.
      link.addEventListener('click', (event) => event.stopPropagation());
      body.appendChild(link);
    }
    link.textContent = title;
    link.title = plain.slice(0, 200) + (plain.length > 200 ? '…' : '');
    if (entry.ref) {
      link.href = noteDocumentURL(entry.ref);
      const short = shortNoteRef(entry.ref);
      link.setAttribute('aria-label', short ? `打开笔记 ${short}：${title}` : `打开笔记：${title}`);
      link.classList.remove('is-pending');
    } else {
      link.removeAttribute('href');
      link.classList.add('is-pending');
      link.setAttribute('aria-label', `笔记保存中：${title}`);
    }
  }
  layoutMarginNotes();
  restoreReadingAnchor(readingAnchor);
  elements.annotationLayer.classList.remove('membox-notes-relayout');
  focusRequestedSourceNote();
}

function scheduleLongNotePreviews() {
  if (notePreviewTimer !== null) return;
  notePreviewTimer = window.setTimeout(() => {
    notePreviewTimer = null;
    renderLongNotePreviews();
  }, 0);
}

// Restore applies notes with notify:false, so no miru-annotations-changed
// fires after a page load and long cards would stay expanded until the first
// edit or resize. document.js calls this once the restore has landed so the
// height clamp runs on the initial paint too.
export function scheduleNotePreviewPass() {
  scheduleLongNotePreviews();
}

// Local picker model consumed by the unified Documents / Notes modal.
export function notePickerItems() {
  const entries = new Map(state.annotations
    .filter((entry) => typeof entry.note === 'string' && entry.note.trim())
    .map((entry) => [String(entry.id), entry]));
  const items = [];
  const anchors = elements.article.querySelectorAll('span.annot-note-ref[data-annot-id]');
  anchors.forEach((anchor, index) => {
    const id = String(anchor.dataset.annotId);
    const entry = entries.get(id);
    if (!entry) return;
    const clone = anchor.cloneNode(true);
    clone.querySelectorAll('.annot-note-num').forEach((badge) => badge.remove());
    const excerpt = clone.textContent.replace(/\s+/g, ' ').trim();
    const note = entry.note.replace(/\s+/g, ' ').trim();
    items.push({ id, number: index + 1, note, excerpt });
  });
  return items;
}

export function focusPickerNote(id) {
  focusNote(id, { scrollTo: 'anchor', duration: 3000 });
}

export function initNotes() {
  window.addEventListener('miru-annotations-changed', renderLongNotePreviews);
  window.addEventListener('miru-annotations-saved', renderLongNotePreviews);
  window.addEventListener('resize', scheduleLongNotePreviews);
}
