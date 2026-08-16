/* Relative Markdown links (TOC chapter files, backlinks) → /?id=<uuid>.
   Conversion indexes ship bare filenames like chapter-014.md; without this
   the browser requests /chapter-014.md and Miru 404s. */

import { elements } from '../js/dom.js';
import { showToast } from '../js/ui/feedback.js';
import { resolveDocumentByPath } from './api.js';
import { session } from './session.js';

const resolveCache = new Map(); // filename → id | null

function basename(href) {
  let clean = String(href || '').split('#')[0].split('?')[0].trim();
  if (!clean) return '';
  // markdown-it percent-encodes non-ASCII in hrefs (e.g. C-%E5%8E%9F….md).
  // Series maps and by-path resolution use the real Unicode filename — decode
  // so TOC chapter links from PDF conversion indexes resolve again.
  try {
    clean = decodeURIComponent(clean);
  } catch {
    // leave malformed % sequences as-is
  }
  const parts = clean.replace(/\\/g, '/').split('/');
  return parts[parts.length - 1] || '';
}

export function isRelativeMarkdownHref(href) {
  const value = String(href || '').trim();
  if (!value || value.startsWith('#') || value.startsWith('?')) return false;
  if (/^(https?:|mailto:|javascript:|data:)/i.test(value)) return false;
  if (value.startsWith('//')) return false;
  // Identity links baked by PDF conversion: /?id=<uuid>
  if (memboxIDFromHref(value)) return false;
  const file = basename(value);
  return /\.md$/i.test(file);
}

/** Extract a document UUID from /?id=… or ?id=… companion links. */
export function memboxIDFromHref(href) {
  const value = String(href || '').trim();
  if (!value) return '';
  try {
    const url = value.startsWith('?') || value.startsWith('/')
      ? new URL(value, window.location.origin)
      : null;
    if (url) {
      const id = (url.searchParams.get('id') || '').trim();
      if (/^[0-9a-fA-F-]{8,}$/.test(id)) return id;
    }
  } catch {
    // ignore
  }
  return '';
}

export function docURL(id) {
  const url = new URL('/', window.location.origin);
  url.searchParams.set('id', id);
  return url.href;
}

export async function resolveMarkdownFilename(filename) {
  const key = String(filename || '').toLowerCase();
  if (!key) return '';
  if (resolveCache.has(key)) return resolveCache.get(key) || '';
  try {
    const result = await resolveDocumentByPath(filename);
    const id = result?.id || '';
    resolveCache.set(key, id || null);
    return id;
  } catch {
    resolveCache.set(key, null);
    return '';
  }
}

// Rewrite bare .md hrefs using a filename→id map (e.g. conversion series).
export function rewriteMarkdownHrefs(filenameToId) {
  if (!elements.article || !filenameToId) return;
  const map = new Map();
  filenameToId.forEach((id, name) => {
    if (name && id) map.set(String(name).toLowerCase(), id);
  });
  elements.article.querySelectorAll('a[href]').forEach((link) => {
    const href = link.getAttribute('href') || '';
    if (!isRelativeMarkdownHref(href)) return;
    const name = basename(href).toLowerCase();
    const id = map.get(name);
    if (!id) return;
    link.href = docURL(id);
    link.dataset.memboxDoc = id;
  });
}

async function onArticleClick(event) {
  if (!session.connected) return;
  if (event.defaultPrevented) return;
  if (event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
  const link = event.target?.closest?.('a[href]');
  if (!link || !elements.article.contains(link)) return;
  // Already rewritten to a document id.
  if (link.dataset.memboxDoc) {
    event.preventDefault();
    window.location.assign(docURL(link.dataset.memboxDoc));
    return;
  }
  const href = link.getAttribute('href') || '';
  // Conversion TOC / backlinks published as /?id=<uuid> (catalog identity).
  const bakedID = memboxIDFromHref(href);
  if (bakedID) {
    event.preventDefault();
    link.dataset.memboxDoc = bakedID;
    window.location.assign(docURL(bakedID));
    return;
  }
  if (!isRelativeMarkdownHref(href)) return;
  event.preventDefault();
  const name = basename(href);
  const id = await resolveMarkdownFilename(name);
  if (!id) {
    showToast(`Document not found: ${name}`);
    return;
  }
  link.dataset.memboxDoc = id;
  link.href = docURL(id);
  window.location.assign(docURL(id));
}

export function initWikiLinks() {
  // Capture phase so we beat any default navigation to /file.md.
  document.addEventListener('click', onArticleClick, true);
}
