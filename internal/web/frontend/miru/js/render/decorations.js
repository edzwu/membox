/* Miru — small, independent DOM decorations applied to the rendered article:
   table scroll wrapper, external-link target/rel, the leading-content copy
   button, and each section heading's copy/download-PNG buttons. */

import { elements } from '../dom.js';
import { state } from '../state.js';
import { copyText } from '../ui/feedback.js';
import { downloadSectionPNG } from '../export/png.js';

export function wrapTables() {
  const tables = elements.article.querySelectorAll('table');
  tables.forEach((table) => {
    if (table.parentElement && table.parentElement.classList.contains('table-wrapper')) return;

    const wrapper = document.createElement('div');
    wrapper.className = 'table-wrapper';
    table.parentNode.insertBefore(wrapper, table);
    wrapper.appendChild(table);
  });
}

// Links whose entire label is a heading-anchor glyph (or the word "Permalink")
// are "headerlinks" from generated docs (Sphinx, Hugo, go.dev, MDN, ...):
// navigation artifacts, not content. Dropping them from headings keeps TOC
// labels, anchor ids, and the reading view clean without rewriting source.
const HEADERLINK_GLYPHS = new Set(['¶', '§', '#', '＃', '🔗', '∞']);
const HEADERLINK_LABELS = /^(permalink|anchor|link)$/i;
const HEADERLINK_CLASSES = ['headerlink', 'anchor', 'anchorjs-link', 'permalink', 'direct-link'];

function isHeadingHeaderLink(link) {
  const text = (link.textContent || '').replace(/\u00a0/g, ' ').trim();
  if (HEADERLINK_GLYPHS.has(text) || HEADERLINK_LABELS.test(text)) return true;
  const title = (link.getAttribute('title') || '').trim();
  if (HEADERLINK_LABELS.test(title)) return true;
  const aria = (link.getAttribute('aria-label') || '').trim();
  if (HEADERLINK_LABELS.test(aria)) return true;
  for (const name of HEADERLINK_CLASSES) {
    if (link.classList.contains(name)) return true;
  }
  // Bare "#section-id" self-link with no/empty visible label.
  const href = link.getAttribute('href') || '';
  if (href.startsWith('#') && text === '') return true;
  return false;
}

export function stripHeadingHeaderLinks() {
  const headings = elements.article.querySelectorAll('h1, h2, h3, h4, h5, h6');
  headings.forEach((heading) => {
    heading.querySelectorAll('a').forEach((link) => {
      if (isHeadingHeaderLink(link)) link.remove();
    });
    heading.normalize();
  });
}

// Rendered headings and parsed source sections are not guaranteed to line up
// one-to-one by position: a heading can exist only in the DOM (`> ## Excerpt`
// inside a blockquote is rendered but the structure parser never sees it) or
// only in the source (a heading line swallowed by preprocessing). Index-based
// attribution then shifts every later section's copy button. Instead, match
// each fold-section's heading label against the raw section headings,
// consuming source sections in order so duplicates still resolve correctly.
function normalizeSourceHeading(text) {
  return text
    .replace(/!?\[([^\[\]]*)\]\([^)]*\)/g, (_, label) =>
      HEADERLINK_GLYPHS.has(label.trim()) ? ' ' : ` ${label} `)
    .replace(/[*_`]/g, '')
    .replace(/\s+/g, ' ')
    .trim();
}

export function attributeSectionSources() {
  if (!state.sectionSources.length) return;
  const sections = elements.article.querySelectorAll('.fold-section');
  let cursor = 0;
  sections.forEach((section) => {
    const heading = section.querySelector(':scope > .fold-heading');
    const label = normalizeSourceHeading((heading && heading.dataset.headingLabel) || '');
    let matched = -1;
    for (let i = cursor; i < state.sectionSources.length; i++) {
      if (normalizeSourceHeading(state.sectionSources[i].heading) === label) {
        matched = i;
        break;
      }
    }
    if (matched === -1) {
      // No matching source section (e.g. a blockquote heading): leave the
      // section without a source so no misleading copy button is offered.
      delete section.dataset.source;
      return;
    }
    section.dataset.source = state.sectionSources[matched].source;
    cursor = matched + 1;
  });
}

export function markExternalLinks() {
  const links = elements.article.querySelectorAll('a[href]');
  links.forEach((link) => {
    const href = link.getAttribute('href');
    if (href && /^(https?:)?\/\//.test(href)) {
      link.setAttribute('target', '_blank');
      link.setAttribute('rel', 'noopener noreferrer');
    }
  });
}

export function wrapLeadingContent() {
  if (!state.leadingSource) return;

  const nodes = [];
  let current = elements.article.firstChild;
  while (current) {
    const isFoldSection = current.nodeType === Node.ELEMENT_NODE &&
      current.classList.contains('fold-section');
    if (isFoldSection) break;

    const isMeta = current.nodeType === Node.ELEMENT_NODE &&
      current.classList.contains('article-meta');
    if (!isMeta) {
      nodes.push(current);
    }

    current = current.nextSibling;
  }

  const hasVisibleContent = nodes.some((node) =>
    node.nodeType === Node.ELEMENT_NODE || (node.textContent && node.textContent.trim())
  );
  if (!hasVisibleContent) return;

  const section = document.createElement('section');
  section.className = 'lead-section';
  section.dataset.source = state.leadingSource;
  elements.article.insertBefore(section, nodes[0]);
  nodes.forEach((node) => section.appendChild(node));

  const button = document.createElement('button');
  button.type = 'button';
  button.className = 'lead-copy';
  button.setAttribute('aria-label', 'Copy leading section');
  button.title = 'Copy leading section';
  button.textContent = '⧉';
  button.addEventListener('click', (event) => {
    event.stopPropagation();
    copyText(state.leadingSource.trim(), button);
  });
  section.appendChild(button);
}

export function addSectionActionButtons() {
  const headings = elements.article.querySelectorAll('.fold-heading');
  headings.forEach((heading) => {
    const section = heading.closest('.fold-section');
    if (!section) return;

    // Sections without an attributed source (see attributeSectionSources)
    // get no copy button rather than one that copies the wrong content.
    if (!heading.querySelector('.section-copy') && section.dataset.source) {
      const copyBtn = document.createElement('button');
      copyBtn.type = 'button';
      copyBtn.className = 'section-copy';
      copyBtn.setAttribute('aria-label', 'Copy section');
      copyBtn.title = 'Copy section';
      copyBtn.textContent = '⧉';
      copyBtn.addEventListener('click', (event) => {
        event.stopPropagation();
        const source = section.dataset.source || '';
        copyText(source.trim(), copyBtn);
      });
      heading.appendChild(copyBtn);
    }

    if (!heading.querySelector('.section-download')) {
      const dlBtn = document.createElement('button');
      dlBtn.type = 'button';
      dlBtn.className = 'section-download';
      dlBtn.setAttribute('aria-label', 'Download section as PNG');
      dlBtn.title = 'Download section as PNG';
      dlBtn.textContent = '↓';
      dlBtn.addEventListener('click', (event) => {
        event.stopPropagation();
        downloadSectionPNG(section, heading, dlBtn);
      });
      heading.appendChild(dlBtn);
    }
  });
}
