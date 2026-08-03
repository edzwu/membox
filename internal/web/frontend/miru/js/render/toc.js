/* Miru — table of contents: heading IDs/labels, the TOC panel markup,
   scroll-driven active-section highlighting, and click-to-navigate. */

import { elements } from '../dom.js';
import { slugify, prefersReducedMotion } from '../utils.js';
import { expandSectionForHeading } from './folding.js';
import { closeToc } from '../ui/chrome.js';

let tocObserver = null;
let activeHeadingId = null;

// Full reset of TOC-owned state: called both at startup (empty state) and
// when the document is cleared. Idempotent, so callers never need to guard.
export function resetToc() {
  if (tocObserver) {
    tocObserver.disconnect();
    tocObserver = null;
  }
  elements.tocNav.innerHTML = '';
  activeHeadingId = null;
}

export function getHeadingLabel(heading) {
  const clone = heading.cloneNode(true);
  clone.querySelectorAll('.section-copy, .section-download').forEach((el) => el.remove());
  clone.querySelectorAll('.katex').forEach((math) => {
    const annotation = math.querySelector('annotation[encoding="application/x-tex"]');
    const label = annotation ? annotation.textContent.trim() : math.textContent.trim();
    math.replaceWith(document.createTextNode(label));
  });
  return clone.textContent.replace(/\s+/g, ' ').trim();
}

export function appendHeadingContent(target, heading) {
  const clone = heading.cloneNode(true);
  clone.removeAttribute('id');
  clone.querySelectorAll('.section-copy, .section-download').forEach((el) => el.remove());
  while (clone.firstChild) {
    target.appendChild(clone.firstChild);
  }
}

export function assignHeadingIds() {
  const headings = Array.from(elements.article.querySelectorAll('h1, h2, h3'));
  const used = new Set();

  headings.forEach((heading) => {
    const label = getHeadingLabel(heading);
    heading.dataset.headingLabel = label;

    const base = slugify(label);
    let id = base;
    let counter = 1;
    while (used.has(id)) {
      id = `${base}-${counter}`;
      counter += 1;
    }
    used.add(id);
    heading.id = id;
  });

  return headings;
}

export function buildToc(headings) {
  elements.tocNav.innerHTML = '';

  if (!headings.length) {
    elements.tocNav.innerHTML = '<p class="toc-empty">No headings</p>';
    return;
  }

  const title = document.createElement('div');
  title.className = 'toc-title';
  title.textContent = 'Contents';

  const list = document.createElement('ul');
  list.className = 'toc-list';

  headings.forEach((heading) => {
    const li = document.createElement('li');
    const a = document.createElement('a');
    const label = heading.dataset.headingLabel || getHeadingLabel(heading);
    a.href = `#${heading.id}`;
    a.className = `toc-${heading.tagName.toLowerCase()}`;
    a.dataset.target = heading.id;
    a.setAttribute('aria-label', label);
    a.title = label;
    appendHeadingContent(a, heading);
    li.appendChild(a);
    list.appendChild(li);
  });

  elements.tocNav.appendChild(title);
  elements.tocNav.appendChild(list);
}

function setActiveToc(id) {
  const links = elements.tocNav.querySelectorAll('a');
  links.forEach((link) => {
    link.classList.toggle('active', link.dataset.target === id);
  });
}

export function observeHeadings(headings) {
  if (tocObserver) {
    tocObserver.disconnect();
    tocObserver = null;
  }

  if (!headings.length || !window.IntersectionObserver) return;

  tocObserver = new IntersectionObserver((entries) => {
    const intersecting = entries
      .filter((entry) => entry.isIntersecting)
      .map((entry) => entry.target);

    if (intersecting.length) {
      intersecting.sort((a, b) =>
        a.compareDocumentPosition(b) & Node.DOCUMENT_POSITION_FOLLOWING ? -1 : 1
      );
      activeHeadingId = intersecting[0].id;
    }

    if (activeHeadingId) setActiveToc(activeHeadingId);
  }, {
    rootMargin: '-80px 0px -80% 0px',
    threshold: 0,
  });

  headings.forEach((heading) => tocObserver.observe(heading));
}

export function onTocClick(event) {
  const link = event.target.closest('a');
  if (!link) return;

  const href = link.getAttribute('href');
  if (!href || !href.startsWith('#')) return;

  event.preventDefault();
  const target = document.getElementById(href.slice(1));
  if (!target) return;

  expandSectionForHeading(target);

  const behavior = prefersReducedMotion() ? 'auto' : 'smooth';
  target.scrollIntoView({ behavior, block: 'start' });
  closeToc();
}
