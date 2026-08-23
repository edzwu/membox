/* Selection summarize: compress a selected excerpt to ≤140 chars with the
   local mmd model, then save it as an annotation note (kind=summary).
   Triggered from the bottom-left 「摘」 button when a page selection exists
   (same pattern as 「译」). Idempotent: re-summarizing the same passage reuses
   the existing summary note unless force=true (Alt-click). */

import { applyNote, findAnnot, setNoteOnPassage } from '../js/annotations/model.js';
import { focusNote } from '../js/annotations/focus.js';
import { elements } from '../js/dom.js';
import { state } from '../js/state.js';
import { session } from './session.js';

export async function summarizeSelection(selection) {
  const response = await fetch(`/api/doc/${encodeURIComponent(session.documentID)}/summarize`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ selection }),
  });
  if (!response.ok) throw new Error((await response.text()).trim() || `HTTP ${response.status}`);
  return response.json();
}

export function summarizeNoteBody(_selection, summary) {
  const a = String(summary || '').trim();
  if (!a) return '';
  // Do NOT re-embed the selection here. bridge_annotate already prefixes the
  // note file with a proper line-by-line blockquote of the exact restore
  // anchor.
  return `**总结：**

${a}
`;
}

function normalizeSelectionText(text) {
  return String(text || '').replace(/\s+/g, ' ').trim();
}

function isSummaryEntry(entry) {
  if (!entry) return false;
  if (entry.kind === 'summary') return true;
  return /\*\*总结：?\*\*|^---[\s\S]*?\nkind:\s*["']?summary["']?/m.test(String(entry.note || ''));
}

function anchorPlainText(annotEl) {
  if (!annotEl) return '';
  const clone = annotEl.cloneNode(true);
  clone.querySelectorAll('.annot-note-num, .membox-jp-note-id, a').forEach((node) => node.remove());
  return normalizeSelectionText(clone.textContent || '');
}

function rangeIntersectsElement(range, element) {
  if (!range || !element) return false;
  try {
    return range.intersectsNode(element);
  } catch {
    return false;
  }
}

/**
 * Find an existing summary note for this passage (overlap + similar anchor text).
 * Returns { entry, el } or null.
 */
export function findExistingSummaryForSelection(range, selectionText) {
  const want = normalizeSelectionText(selectionText);
  if (!want || !elements.article) return null;

  const annots = elements.article.querySelectorAll('span.annot.annot-note-ref[data-annot-id]');
  let best = null;
  let bestScore = -1;

  for (const el of annots) {
    const entry = findAnnot(el.dataset.annotId);
    if (!isSummaryEntry(entry)) continue;

    const anchor = anchorPlainText(el);
    if (!anchor) continue;

    const overlaps = range ? rangeIntersectsElement(range, el) : false;
    // Text match on normalized selection, or either contains the other when
    // the user re-selects a slightly larger/smaller slice of the same passage.
    let score = 0;
    if (anchor === want) score = 100;
    else if (anchor.includes(want) || want.includes(anchor)) {
      const shorter = Math.min(anchor.length, want.length);
      const longer = Math.max(anchor.length, want.length);
      score = Math.round((shorter / longer) * 80);
    }
    if (overlaps) score += 15;
    // Prefer notes whose in-memory entry still carries a summary body.
    if (/\*\*总结：?\*\*/.test(String(entry.note || ''))) score += 5;

    if (score >= 50 && score > bestScore) {
      bestScore = score;
      best = { entry, el };
    }
  }
  return best;
}

/** Snapshot article selection text + a Range before focus steals the selection. */
export function captureArticleSelection() {
  const selection = window.getSelection();
  if (!selection || selection.isCollapsed || !selection.rangeCount || !elements.article) {
    return null;
  }
  let range;
  try {
    range = selection.getRangeAt(0).cloneRange();
  } catch {
    return null;
  }
  if (!elements.article.contains(range.commonAncestorContainer)) return null;
  const text = normalizeSelectionText(selection.toString());
  if (text.length < 20) return null;
  return { text, range };
}

/**
 * Summarize a captured article selection into an inline summary note.
 * @param {{ text: string, range: Range }} captured
 * @param {{ force?: boolean }} [opts]
 * @returns {Promise<{ existing: boolean }>}
 */
export async function runSelectionSummarize(captured, opts = {}) {
  const force = Boolean(opts.force);
  const selection = normalizeSelectionText(captured?.text);
  const range = captured?.range;
  if (!selection || !range) throw new Error('先选中内容');
  if (!session.connected || !session.documentID) {
    throw new Error('需要连接 membox 才能总结');
  }
  if (!range.commonAncestorContainer?.isConnected) {
    throw new Error('选区已失效，请重新选择');
  }

  // Idempotent hit: same (or nearly same) passage already has a summary note.
  if (!force) {
    const hit = findExistingSummaryForSelection(range, selection);
    if (hit?.entry) {
      focusNote(hit.entry.id, { scrollTo: 'anchor', duration: 1600 });
      return { existing: true };
    }
  }

  const result = await summarizeSelection(selection);
  const summary = (result.summary || '').trim();
  if (!summary) throw new Error('模型返回空总结');
  const noteText = summarizeNoteBody(selection, summary);

  // Prefer updating an overlapping annotation (incl. force-refresh of summary).
  const hit = findExistingSummaryForSelection(range, selection);
  if (hit?.entry && hit.el) {
    setNoteOnPassage(hit.entry, hit.el, noteText, { kind: 'summary' });
    focusNote(hit.entry.id, { scrollTo: 'anchor', duration: 1200 });
    return { existing: false };
  }

  // Selection already inside some other annot (hl/ul) — attach note there.
  const anchorNode = range.commonAncestorContainer;
  const el = anchorNode.nodeType === 1 ? anchorNode : anchorNode.parentElement;
  const existing = el?.closest?.('span.annot');
  if (existing?.dataset?.annotId) {
    const entry = findAnnot(existing.dataset.annotId);
    if (entry) {
      setNoteOnPassage(entry, existing, noteText, { kind: 'summary' });
      focusNote(entry.id, { scrollTo: 'anchor', duration: 1200 });
      return { existing: false };
    }
  }

  applyNote(range, noteText, { kind: 'summary' });
  // Newest annotation is the one we just created.
  const created = state.annotations[state.annotations.length - 1];
  if (created?.id != null) {
    focusNote(created.id, { scrollTo: 'anchor', duration: 1200 });
  }
  return { existing: false };
}

/** Kept for integration.js; toolbar action removed. */
export function initSelectionSummarize() {
  // no-op: selection summarize is owned by the bottom-left 「摘」 button.
}
