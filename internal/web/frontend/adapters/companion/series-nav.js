/* Bottom navigator for PDF→MD conversion series: TOC · prev · next. */

import { elements } from '../../js/dom.js';
import { assignHeadingIds, buildToc } from '../../js/render/toc.js';
import { state } from '../../js/state.js';
import { onRender } from './events.js';
import { fetchConversionSeries } from './api.js';
import { convertedDisplayLabel } from './labels.js';
import { rewriteMarkdownHrefs } from './wiki-links.js';
import { session } from './session.js';

let nav = null;
let loadGeneration = 0;

function createNav() {
  const el = document.createElement('nav');
  el.className = 'membox-series-nav';
  el.hidden = true;
  el.setAttribute('aria-label', 'Chapter navigation');
  el.innerHTML = `
    <div class="membox-series-side membox-series-side-prev">
      <a class="membox-series-link membox-series-prev" hidden aria-label="Previous chapter">← Prev</a>
    </div>
    <div class="membox-series-mid">
      <a class="membox-series-link membox-series-toc" hidden>Contents</a>
      <span class="membox-series-position" hidden></span>
    </div>
    <div class="membox-series-side membox-series-side-next">
      <a class="membox-series-link membox-series-next" hidden aria-label="Next chapter">Next →</a>
    </div>
  `;
  // Sit below the article so it scrolls with the document end.
  const host = elements.article?.parentElement || document.body;
  host.appendChild(el);
  return el;
}

function docURL(id) {
  const url = new URL('/', window.location.origin);
  url.searchParams.set('id', id);
  return url.href;
}

function bindLink(anchor, item, kind, fallbackLabel) {
  if (!item || !item.id) {
    anchor.hidden = true;
    anchor.removeAttribute('href');
    anchor.removeAttribute('title');
    anchor.textContent = fallbackLabel;
    anchor.onclick = null;
    return;
  }
  anchor.hidden = false;
  anchor.href = docURL(item.id);
  const label = item.label || item.title || fallbackLabel;
  if (kind === 'toc') {
    anchor.textContent = '↑ Contents';
  } else if (kind === 'prev') {
    anchor.textContent = `← ${label}`;
  } else {
    anchor.textContent = `${label} →`;
  }
  anchor.title = label;
  // Same-tab navigation keeps companion session / scroll restore simple.
  anchor.onclick = (event) => {
    if (event.metaKey || event.ctrlKey || event.shiftKey || event.altKey || event.button !== 0) return;
    event.preventDefault();
    window.location.assign(anchor.href);
  };
}

export function hideSeriesNav() {
  if (!nav) return;
  nav.hidden = true;
}

function hideNav() {
  hideSeriesNav();
}

function rewriteSeriesBodyLinks(series) {
  if (!series?.items?.length) return;
  const map = new Map();
  for (const item of series.items) {
    if (item.filename && item.id) map.set(item.filename, item.id);
  }
  rewriteMarkdownHrefs(map);
}

// Use the real chapter title from the index TOC as the page heading, instead
// of the raw -pdf-<uuid> filename stem.
function applySeriesHeading(series) {
  const current = series.current;
  if (!current) return;
  const title = String(current.toc_title || '').trim();
  if (!title) return;
  // Chrome title + document title.
  const label = String(current.label || title).trim();
  const live = document.getElementById('doc-title');
  if (live) live.textContent = title;
  if (typeof state !== 'undefined') state.docTitle = title;
  document.title = `${title} — membox`;
  // Body H1 (the biggest visible heading).
  const h1 = elements.article?.querySelector('h1');
  if (h1) {
    const raw = (h1.textContent || '').replace(/\s+/g, ' ').trim();
    const isConversion = Boolean(convertedDisplayLabel(`${raw}.md`));
    const stemMatch = String(current.filename || '')
      .replace(/\.md$/i, '')
      .toLowerCase();
    if (isConversion || (stemMatch && raw.toLowerCase() === stemMatch)) {
      h1.textContent = title;
      // Rebuild the side TOC so it matches the new heading.
      buildToc(assignHeadingIds());
    }
  }
}

function renderSeries(series) {
  if (!nav) nav = createNav();
  if (!series || series.kind !== 'pdf-conversion' || !series.current) {
    hideNav();
    return;
  }
  rewriteSeriesBodyLinks(series);
  applySeriesHeading(series);
  // Index page with no chapters: nothing to navigate.
  if (series.current.kind === 'index' && !series.next && !series.prev) {
    hideNav();
    return;
  }

  const toc = nav.querySelector('.membox-series-toc');
  const prev = nav.querySelector('.membox-series-prev');
  const next = nav.querySelector('.membox-series-next');
  const position = nav.querySelector('.membox-series-position');

  // On a chapter/part, offer TOC. On the index itself, hide TOC self-link.
  if (series.current.kind === 'index') {
    toc.hidden = true;
    toc.removeAttribute('href');
    toc.onclick = null;
  } else {
    bindLink(toc, series.index, 'toc', 'Contents');
  }
  bindLink(prev, series.prev, 'prev', 'Previous');
  bindLink(next, series.next, 'next', 'Next');

  if (series.position > 0 && series.total > 0) {
    position.hidden = false;
    position.textContent = `${series.position} / ${series.total}`;
  } else {
    position.hidden = true;
    position.textContent = '';
  }

  nav.hidden = false;
}

export async function loadSeriesNav() {
  const generation = ++loadGeneration;
  if (!session.connected || !session.documentID) {
    hideNav();
    return;
  }
  try {
    const series = await fetchConversionSeries(session.documentID);
    if (generation !== loadGeneration) return;
    renderSeries(series);
  } catch (err) {
    if (generation !== loadGeneration) return;
    console.warn('membox: conversion series unavailable', err);
    hideNav();
  }
}

export function initSeriesNav() {
  nav = createNav();
  onRender(() => {
    // Visibility follows connection/document chrome; data reloads on document open.
    if (!session.connected || !session.documentID) hideNav();
  });
  window.addEventListener('miru-document-change', () => {
    if (!session.connected || !session.documentID) {
      hideNav();
      return;
    }
    // Body was just re-rendered; series rewrite runs again after fetch.
    void loadSeriesNav();
  });
}
