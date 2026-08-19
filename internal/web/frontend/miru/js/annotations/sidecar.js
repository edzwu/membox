/* Miru — the portable `.miru.json` annotation sidecar: fingerprint the
   source Markdown, anchor each annotation by both text offset and
   exact-text-with-context (so repeated passages restore reliably), and
   validate/restore a sidecar dropped back onto its Markdown. */

import { elements } from '../dom.js';
import { state } from '../state.js';
import { ANNOTATION_FORMAT, ANNOTATION_VERSION, ANNOTATION_LEGACY_VERSIONS, ANNOTATION_CONTEXT_LENGTH, ANNOTATION_TEXT_EXCLUDE } from '../constants.js';
import { applyAnnotationRange } from './model.js';
import { updateMarkdownDownloadControl } from '../ui/chrome.js';

export async function fingerprintMarkdown(text, expectedFormat = '') {
  const bytes = new TextEncoder().encode(text);
  const wantsFallback = expectedFormat.startsWith('fnv1a32:');
  if (!wantsFallback && window.crypto && window.crypto.subtle) {
    const digest = new Uint8Array(await window.crypto.subtle.digest('SHA-256', bytes));
    return 'sha256:' + Array.from(digest, (b) => b.toString(16).padStart(2, '0')).join('');
  }
  if (expectedFormat.startsWith('sha256:')) {
    throw new Error('This browser cannot verify the annotation bundle');
  }
  // Portable fallback for older/insecure browser contexts. This is only a
  // pairing guard, not a security boundary.
  let hash = 0x811c9dc5;
  bytes.forEach((byte) => {
    hash ^= byte;
    hash = Math.imul(hash, 0x01000193);
  });
  return 'fnv1a32:' + (hash >>> 0).toString(16).padStart(8, '0');
}

export function annotationTextFromRange(range) {
  const holder = document.createElement('div');
  holder.appendChild(range.cloneContents());
  holder.querySelectorAll(ANNOTATION_TEXT_EXCLUDE).forEach((node) => node.remove());
  return holder.textContent || '';
}

function canonicalArticleText() {
  const range = document.createRange();
  range.selectNodeContents(elements.article);
  return annotationTextFromRange(range);
}

function captureAnnotationAnchor(entry, canonicalText) {
  const span = elements.article.querySelector(`span.annot[data-annot-id="${entry.id}"]`);
  if (!span) return null;

  const before = document.createRange();
  before.selectNodeContents(elements.article);
  before.setEndBefore(span);
  const selected = document.createRange();
  selected.selectNodeContents(span);

  const start = annotationTextFromRange(before).length;
  const exact = annotationTextFromRange(selected);
  if (!exact) return null;
  const end = start + exact.length;
  const anchor = {
    start,
    end,
    exact,
    prefix: canonicalText.slice(Math.max(0, start - ANNOTATION_CONTEXT_LENGTH), start),
    suffix: canonicalText.slice(end, end + ANNOTATION_CONTEXT_LENGTH),
    highlight: !!entry.hl,
    underline: !!entry.ul,
    strikethrough: !!entry.sl,
    note: entry.note || null,
    // Note subtype: 'qa' for assist Q&A; omitted/empty for plain notes.
    kind: entry.kind || null,
    // Host-neutral identity for matching async persistence responses. `ref` is
    // an optional durable join key assigned by hosts such as membox.
    clientId: entry.clientId || null,
    ref: entry.ref || null,
  };
  return anchor;
}

export async function buildAnnotationSidecar(markdownFile, markdown, progress = null) {
  const canonicalText = canonicalArticleText();
  const title = state.docTitle || 'Untitled';
  const savedAnnotations = state.annotations
    .map((entry) => captureAnnotationAnchor(entry, canonicalText))
    .filter(Boolean)
    .sort((a, b) => a.start - b.start);
  if (savedAnnotations.length !== state.annotations.length) {
    throw new Error('Could not anchor every annotation');
  }
  const sourceHash = await fingerprintMarkdown(markdown);
  return {
    format: ANNOTATION_FORMAT,
    version: ANNOTATION_VERSION,
    markdownFile,
    sourceHash,
    sourceLength: markdown.length,
    textLength: canonicalText.length,
    title,
    exportedAt: new Date().toISOString(),
    annotations: savedAnnotations,
    // Reading progress travels with the notes: it is a bookmark, and bookmarks
    // belong in the same sidecar as annotations.
    progress: normalizeProgress(progress),
  };
}

export function normalizeProgress(progress) {
  if (!progress || !Number.isFinite(progress.y) || progress.y < 0) return null;
  return {
    y: Math.round(progress.y),
    at: typeof progress.at === 'string' && progress.at ? progress.at : new Date().toISOString(),
  };
}

export function parseAnnotationSidecar(text) {
  if (!text || text.length > 5 * 1024 * 1024) {
    throw new Error('Invalid Miru annotation file');
  }
  let data;
  try {
    data = JSON.parse(text);
  } catch (err) {
    throw new Error('Invalid Miru annotation JSON');
  }
  if (!data || data.format !== ANNOTATION_FORMAT ||
      (data.version !== ANNOTATION_VERSION && !(ANNOTATION_LEGACY_VERSIONS || []).includes(data.version))) {
    throw new Error('Unsupported Miru annotation format');
  }
  if (typeof data.sourceHash !== 'string' ||
      !/^(?:sha256:[a-f0-9]{64}|fnv1a32:[a-f0-9]{8})$/i.test(data.sourceHash) ||
      !Array.isArray(data.annotations) || data.annotations.length > 2000) {
    throw new Error('Invalid Miru annotation file');
  }

  const normalized = data.annotations.map((item) => {
    const validPosition = item && Number.isInteger(item.start) && item.start >= 0;
    const validExact = item && typeof item.exact === 'string' && item.exact.length > 0 && item.exact.length <= 100000;
    const validNote = item && (item.note === null || item.note === undefined ||
      (typeof item.note === 'string' && item.note.length <= 100000));
    if (!validPosition || !validExact || !validNote) {
      throw new Error('Invalid annotation anchor');
    }
    const highlight = !!item.highlight;
    const underline = !!item.underline;
    const strikethrough = !!item.strikethrough;
    const note = typeof item.note === 'string' && item.note ? item.note : null;
    if (!highlight && !underline && !strikethrough && !note) {
      throw new Error('Empty annotation entry');
    }
    return {
      start: item.start,
      end: item.start + item.exact.length,
      exact: item.exact,
      prefix: typeof item.prefix === 'string' ? item.prefix.slice(-ANNOTATION_CONTEXT_LENGTH) : '',
      suffix: typeof item.suffix === 'string' ? item.suffix.slice(0, ANNOTATION_CONTEXT_LENGTH) : '',
      highlight,
      underline,
      strikethrough,
      note,
      kind: typeof item.kind === 'string' && (item.kind === 'qa' || item.kind === 'summary') ? item.kind : '',
      clientId: typeof item.clientId === 'string' ? item.clientId.slice(0, 128) : '',
      ref: typeof item.ref === 'string' ? item.ref.slice(0, 64) : '',
    };
  });

  return {
    format: data.format,
    version: data.version,
    markdownFile: typeof data.markdownFile === 'string' ? data.markdownFile : '',
    sourceHash: data.sourceHash,
    sourceLength: Number.isInteger(data.sourceLength) ? data.sourceLength : null,
    title: typeof data.title === 'string' ? data.title.slice(0, 500) : '',
    annotations: normalized,
    // Additive field: unknown/invalid progress never invalidates a sidecar.
    progress: parseProgress(data.progress),
  };
}

function parseProgress(value) {
  if (!value || typeof value !== 'object') return null;
  const y = value.y;
  if (!Number.isFinite(y) || y < 0 || y > 100000000) return null;
  return { y: Math.round(y), at: typeof value.at === 'string' ? value.at : '' };
}

export async function verifyAnnotationSource(data, markdown) {
  if (data.sourceLength !== null && data.sourceLength !== markdown.length) {
    throw new Error('Annotations belong to a different Markdown file');
  }
  const hash = await fingerprintMarkdown(markdown, data.sourceHash);
  if (hash !== data.sourceHash) {
    throw new Error('Annotations belong to a different Markdown file');
  }
}

function annotationTextNodes() {
  const walker = document.createTreeWalker(elements.article, NodeFilter.SHOW_TEXT, {
    acceptNode(node) {
      const parent = node.parentElement;
      return parent && parent.closest(ANNOTATION_TEXT_EXCLUDE)
        ? NodeFilter.FILTER_REJECT
        : NodeFilter.FILTER_ACCEPT;
    },
  });
  const nodes = [];
  let node;
  while ((node = walker.nextNode())) {
    if (node.nodeValue.length) nodes.push(node);
  }
  return nodes;
}

function rangeFromAnnotationOffsets(start, end) {
  const nodes = annotationTextNodes();
  if (!nodes.length || start < 0 || end <= start) return null;
  const total = nodes.reduce((sum, node) => sum + node.nodeValue.length, 0);
  if (end > total) return null;

  function locate(offset, isEnd) {
    let cursor = 0;
    for (let i = 0; i < nodes.length; i++) {
      const node = nodes[i];
      const next = cursor + node.nodeValue.length;
      if (offset < next || (isEnd && offset === next) || i === nodes.length - 1) {
        return { node, offset: Math.max(0, Math.min(node.nodeValue.length, offset - cursor)) };
      }
      cursor = next;
    }
    return null;
  }

  const from = locate(start, false);
  const to = locate(end, true);
  if (!from || !to) return null;
  const range = document.createRange();
  range.setStart(from.node, from.offset);
  range.setEnd(to.node, to.offset);
  return range;
}

function commonPrefixLength(a, b) {
  const limit = Math.min(a.length, b.length);
  let i = 0;
  while (i < limit && a[i] === b[i]) i++;
  return i;
}

function commonSuffixLength(a, b) {
  const limit = Math.min(a.length, b.length);
  let i = 0;
  while (i < limit && a[a.length - 1 - i] === b[b.length - 1 - i]) i++;
  return i;
}

function resolveAnnotationRange(anchor, canonicalText) {
  let range = rangeFromAnnotationOffsets(anchor.start, anchor.end);
  if (range && annotationTextFromRange(range) === anchor.exact) return range;

  // Text-quote fallback makes the sidecar tolerant of harmless renderer/DOM
  // changes while prefix/suffix context disambiguates repeated passages.
  let bestStart = -1;
  let bestScore = -Infinity;
  let index = canonicalText.indexOf(anchor.exact);
  while (index !== -1) {
    const before = canonicalText.slice(Math.max(0, index - anchor.prefix.length), index);
    const afterAt = index + anchor.exact.length;
    const after = canonicalText.slice(afterAt, afterAt + anchor.suffix.length);
    const contextScore = commonSuffixLength(before, anchor.prefix) +
      commonPrefixLength(after, anchor.suffix);
    const score = contextScore * 1000 - Math.min(Math.abs(index - anchor.start), 999);
    if (score > bestScore) {
      bestScore = score;
      bestStart = index;
    }
    index = canonicalText.indexOf(anchor.exact, index + 1);
  }
  if (bestStart !== -1) {
    range = rangeFromAnnotationOffsets(bestStart, bestStart + anchor.exact.length);
    if (range && annotationTextFromRange(range) === anchor.exact) return range;
  }
  return resolveDense(anchor, canonicalText);
}

// Browser selections never contain markdown markup, but the rendered text can
// carry literal markup characters (e.g. backticks the clipper escaped and the
// renderer shows). Match with markup/whitespace stripped, then map back to
// canonical offsets.
function denseAnchorText(text) {
  const map = [];
  let out = '';
  for (let i = 0; i < text.length; i++) {
    const ch = text[i];
    if (ch === '`' || ch === '*' || ch === '_') continue;
    if (ch === ' ' || ch === '\n' || ch === '\r' || ch === '\t' || ch === '\u00a0') continue;
    map.push(i);
    out += ch;
  }
  return { text: out, map };
}

function resolveDense(anchor, canonicalText) {
  const denseC = denseAnchorText(canonicalText);
  const denseE = denseAnchorText(anchor.exact).text;
  if (!denseE) return null;
  const densePrefix = denseAnchorText(anchor.prefix || '').text;
  const denseSuffix = denseAnchorText(anchor.suffix || '').text;

  let bestStart = -1;
  let bestScore = -Infinity;
  let idx = denseC.text.indexOf(denseE);
  while (idx !== -1) {
    const before = denseC.text.slice(Math.max(0, idx - densePrefix.length), idx);
    const afterAt = idx + denseE.length;
    const after = denseC.text.slice(afterAt, afterAt + denseSuffix.length);
    const contextScore = commonSuffixLength(before, densePrefix) +
      commonPrefixLength(after, denseSuffix);
    const score = contextScore * 1000 - Math.min(Math.abs(idx - anchor.start), 999);
    if (score > bestScore) {
      bestScore = score;
      bestStart = idx;
    }
    idx = denseC.text.indexOf(denseE, idx + 1);
  }
  if (bestStart === -1) return null;
  const start = denseC.map[bestStart];
  const end = denseC.map[bestStart + denseE.length - 1] + 1;
  const range = rangeFromAnnotationOffsets(start, end);
  if (!range) return null;
  return denseAnchorText(annotationTextFromRange(range)).text === denseE ? range : null;
}

export function restoreAnnotationSidecar(data) {
  if (data.title) {
    state.docTitle = data.title.trim() || 'Untitled';
    const title = elements.article.querySelector('.doc-title');
    if (title) title.textContent = state.docTitle;
  }

  const canonicalText = canonicalArticleText();
  let restored = 0;
  const unrestored = [];
  data.annotations
    .slice()
    .sort((a, b) => a.start - b.start)
    .forEach((anchor) => {
      const range = resolveAnnotationRange(anchor, canonicalText);
      if (!range) {
        // Keep the anchor around so hosts can merge it back on save instead
        // of silently dropping a note that merely failed to re-anchor.
        unrestored.push(anchor);
        return;
      }
      try {
        applyAnnotationRange(range, {
          hl: anchor.highlight,
          ul: anchor.underline,
          sl: anchor.strikethrough,
          note: anchor.note,
          kind: anchor.kind || null,
          clientId: anchor.clientId || null,
          ref: anchor.ref || null,
          notify: false,
        });
        restored++;
      } catch (err) {
        console.warn('Could not restore annotation:', err);
        unrestored.push(anchor);
      }
    });
  updateMarkdownDownloadControl();
  data.unrestored = unrestored;
  return restored;
}
