/* Miru — table of contents: heading IDs/labels, the TOC panel markup,
   scroll-driven active-section highlighting, and click-to-navigate. */

import { elements } from '../dom.js';
import { slugify, prefersReducedMotion } from '../utils.js';
import { expandSectionForHeading } from './folding.js';
import { closeToc } from '../ui/chrome.js';
import { outlineDepth, tocNestLevel, HTML_WEIGHT } from './toc-nest.js';

export { outlineDepth, tocNestLevel, HTML_WEIGHT };

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
  if (elements.tocRail) elements.tocRail.innerHTML = '';
  if (elements.toc) {
    elements.toc.style.removeProperty('--toc-box-height');
    elements.toc.style.removeProperty('height');
  }
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
  // Never nest <a> inside the TOC link — invalid HTML and click targets steal
  // the event with the wrong (source-site) hash, so jump-to-heading fails.
  clone.querySelectorAll('a').forEach((link) => {
    const parent = link.parentNode;
    if (!parent) return;
    while (link.firstChild) parent.insertBefore(link.firstChild, link);
    parent.removeChild(link);
  });
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

function makeTocLink(heading) {
  const a = document.createElement('a');
  const label = heading.dataset.headingLabel || getHeadingLabel(heading);
  // Prefer setAttribute so getAttribute stays a bare "#id" (assigning .href
  // can expand to an absolute URL in some browsers).
  a.setAttribute('href', `#${heading.id}`);
  a.className = `toc-${heading.tagName.toLowerCase()}`;
  a.dataset.target = heading.id;
  a.setAttribute('aria-label', label);
  a.title = label;
  appendHeadingContent(a, heading);
  return a;
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

  // Nest by composite key (HTML major + outline minor). See tocNestLevel.
  // Sublists use progressive disclosure via setActiveToc.
  // stack frames: { level, outline, li, childList }
  const stack = [{ level: 0, outline: false, htmlLevel: 0, li: null, subList: list }];

  headings.forEach((heading) => {
    const label = heading.dataset.headingLabel || getHeadingLabel(heading);
    const { level, outline, htmlLevel } = tocNestLevel(heading.tagName, label, stack);
    const link = makeTocLink(heading);
    const li = document.createElement('li');
    li.appendChild(link);

    while (stack.length > 1 && stack[stack.length - 1].level >= level) {
      stack.pop();
    }
    const parent = stack[stack.length - 1];

    if (parent.level === 0) {
      parent.subList.appendChild(li);
    } else {
      if (!parent.childList) {
        parent.li.classList.add('toc-group');
        const wrap = document.createElement('div');
        wrap.className = 'toc-sub-wrap';
        parent.childList = document.createElement('ul');
        parent.childList.className = 'toc-sub';
        wrap.appendChild(parent.childList);
        parent.li.appendChild(wrap);
      }
      parent.childList.appendChild(li);
    }

    stack.push({ level, outline, htmlLevel, li, childList: null });
  });

  elements.tocNav.appendChild(title);
  elements.tocNav.appendChild(list);
  buildTocRail();
  syncTocBoxHeight();
}

// Collapsed rail ticks follow the TOC outline, with one practical peel:
// a lone top-level title (common H1 wrapper) yields its direct children so
// long articles don't collapse into a single solid bar.
function collectRailLinks() {
  const topItems = Array.from(elements.tocNav.querySelectorAll('.toc-list > li'));
  if (!topItems.length) return [];

  if (topItems.length === 1) {
    const childLinks = Array.from(
      topItems[0].querySelectorAll(':scope > .toc-sub-wrap > .toc-sub > li > a[data-target]'),
    );
    if (childLinks.length >= 2) return childLinks;
  }

  return topItems
    .map((item) => item.querySelector(':scope > a[data-target]'))
    .filter(Boolean);
}

function buildTocRail() {
  if (!elements.tocRail) return;
  elements.tocRail.innerHTML = '';

  const roots = collectRailLinks();
  if (!roots.length) return;

  // Collapsed mode only: compact hairline index (drawn in CSS). Expanded pane
  // stays a normal progressive TOC — modes share active chapter, not geometry.
  roots.forEach((link) => {
    const tick = document.createElement('button');
    tick.type = 'button';
    tick.className = 'toc-rail-tick';
    tick.dataset.target = link.dataset.target;
    const label = link.getAttribute('aria-label') || link.textContent.trim();
    tick.setAttribute('aria-label', label);
    tick.title = label;
    elements.tocRail.appendChild(tick);
  });
}

// Size the shared TOC box to the full outline (all groups open), capped by
// --toc-panel-max. Short docs stay compact; long docs scroll inside the pane.
function syncTocBoxHeight() {
  if (!elements.toc || !elements.tocPane || !elements.tocNav) return;

  const toc = elements.toc;
  const pane = elements.tocPane;
  const nav = elements.tocNav;
  const groups = Array.from(nav.querySelectorAll('.toc-group'));
  const expanded = groups.map((group) => group.classList.contains('is-expanded'));

  // Expand every group so height reflects the full outline, not the folded view.
  groups.forEach((group) => group.classList.add('is-expanded'));
  pane.classList.add('is-measuring');

  const maxCss = getComputedStyle(toc).maxHeight;
  const maxPx = maxCss && maxCss !== 'none' ? parseFloat(maxCss) : window.innerHeight * 0.64;
  const contentH = Math.ceil(nav.scrollHeight);
  const tickCount = elements.tocRail
    ? elements.tocRail.querySelectorAll('.toc-rail-tick').length
    : 0;
  // padding 10*2 + ticks 11px + gaps 5px
  const railMin = tickCount > 0
    ? 20 + tickCount * 11 + Math.max(0, tickCount - 1) * 5
    : 64;
  const boxH = Math.max(72, Math.min(Math.max(contentH, railMin), Math.max(120, maxPx || 480)));

  toc.style.setProperty('--toc-box-height', `${boxH}px`);
  toc.style.height = `${boxH}px`;

  pane.classList.remove('is-measuring');
  groups.forEach((group, index) => {
    group.classList.toggle('is-expanded', expanded[index]);
  });
}

function setActiveToc(id) {
  const links = elements.tocNav.querySelectorAll('a');
  links.forEach((link) => {
    link.classList.toggle('active', link.dataset.target === id);
  });

  // Progressive disclosure: expand the group containing the active heading,
  // fold the others away.
  const activeLink = id
    ? elements.tocNav.querySelector(`a[data-target="${CSS.escape(id)}"]`)
    : null;
  elements.tocNav.querySelectorAll('.toc-group').forEach((grp) => {
    grp.classList.toggle('is-expanded', Boolean(activeLink && grp.contains(activeLink)));
  });

  // Rail highlights the deepest tick target that contains the active heading.
  if (elements.tocRail) {
    const ticks = Array.from(elements.tocRail.querySelectorAll('.toc-rail-tick[data-target]'));
    let activeTarget = '';
    if (activeLink && ticks.length) {
      const tickTargets = new Set(ticks.map((tick) => tick.dataset.target));
      let item = activeLink.closest('li');
      while (item) {
        const link = item.querySelector(':scope > a[data-target]');
        if (link && tickTargets.has(link.dataset.target)) {
          activeTarget = link.dataset.target;
          break;
        }
        const parentList = item.parentElement;
        item = parentList ? parentList.closest('li') : null;
      }
      if (!activeTarget) {
        const activeHeading = document.getElementById(activeLink.dataset.target);
        if (activeHeading) {
          const activeTop = activeHeading.getBoundingClientRect().top + window.scrollY;
          for (let i = ticks.length - 1; i >= 0; i -= 1) {
            const heading = document.getElementById(ticks[i].dataset.target);
            if (!heading) continue;
            if (heading.getBoundingClientRect().top + window.scrollY <= activeTop + 1) {
              activeTarget = ticks[i].dataset.target;
              break;
            }
          }
        }
      }
    }
    ticks.forEach((tick) => {
      tick.classList.toggle('active', Boolean(activeTarget) && tick.dataset.target === activeTarget);
    });
  }
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
  const railTick = event.target.closest('.toc-rail-tick[data-target]');
  const link = event.target.closest('a[data-target], a[href^="#"]');
  const fromRail = Boolean(railTick && elements.tocRail && elements.tocRail.contains(railTick));
  const fromNav = Boolean(link && elements.tocNav.contains(link));
  if (!fromRail && !fromNav) return;

  event.preventDefault();
  event.stopPropagation();

  // data-target is authoritative (assigned heading id). Fall back to hash only
  // for exported/static TOC markup that may omit the dataset.
  const id = fromRail
    ? (railTick.dataset.target || '').trim()
    : (link.dataset.target || '').trim() ||
    (() => {
      const href = link.getAttribute('href') || '';
      const hash = href.includes('#') ? href.slice(href.indexOf('#') + 1) : '';
      try {
        return decodeURIComponent(hash);
      } catch {
        return hash;
      }
    })();
  if (!id) return;

  const target = document.getElementById(id);
  if (!target) return;

  expandSectionForHeading(target);

  const behavior = prefersReducedMotion() ? 'auto' : 'smooth';
  target.scrollIntoView({ behavior, block: 'start' });
  try {
    history.replaceState(null, '', `#${id}`);
  } catch {
    // history may be unavailable in some embedded contexts
  }
  closeToc();
}
