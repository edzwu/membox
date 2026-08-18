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

// outlineDepth reads hierarchical numbers in the heading label:
// "10.1 多 Agent…" → 2, "10.1.1 维度一" → 3, "第 10 章" → null.
// PDF conversions often flatten every section to ## while keeping the printed
// outline in the title text — without this, progressive disclosure cannot nest.
//
// Reject false positives that blow up nesting + progressive disclosure:
//   "2001 年：…" / "2026 年的…" — years, not section 2001
//   bare integers with 3+ digits — not realistic chapter indexes
export function outlineDepth(label) {
  const text = String(label || '').trim();
  const match = text.match(/^(\d+(?:\.\d+)*)\b/);
  if (!match) return null;

  // "2001 年…" / "2026年…" — calendar years, never outline numbers.
  const after = text.slice(match[0].length);
  if (/^\s*年/.test(after)) return null;

  const parts = match[1].split('.');
  // Each outline segment is a small index (1, 10, 99). Years (2001) and other
  // large bare integers fail this check and fall back to HTML heading levels.
  if (parts.some((part) => part.length > 2)) return null;

  return parts.length;
}

function tocNestLevel(heading, label, stack) {
  const outline = outlineDepth(label);
  if (outline != null) return { level: outline, outline: true };

  const htmlLevel = Number(heading.tagName.slice(1)) || 1;
  if (htmlLevel === 1) return { level: 1, outline: false };

  // Unnumbered H2/H3 body-ish headings ("实验要求", long sentences promoted to
  // ##) nest under the current numbered section instead of sitting as peers of
  // 10.1 / 10.2 and blowing up the top-level outline.
  for (let i = stack.length - 1; i >= 1; i -= 1) {
    if (stack[i].outline) {
      return { level: stack[i].level + 1, outline: false };
    }
  }
  return { level: htmlLevel, outline: false };
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

  // Nest by outline number when present (10.1 → 10.1.1), else HTML level
  // (H1→H2→H3). Sublists use progressive disclosure via setActiveToc.
  // stack frames: { level, outline, li, childList }
  const stack = [{ level: 0, outline: false, li: null, subList: list }];

  headings.forEach((heading) => {
    const label = heading.dataset.headingLabel || getHeadingLabel(heading);
    const { level, outline } = tocNestLevel(heading, label, stack);
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

    stack.push({ level, outline, li, childList: null });
  });

  elements.tocNav.appendChild(title);
  elements.tocNav.appendChild(list);
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
  const link = event.target.closest('a[data-target], a[href^="#"]');
  if (!link || !elements.tocNav.contains(link)) return;

  event.preventDefault();
  event.stopPropagation();

  // data-target is authoritative (assigned heading id). Fall back to hash only
  // for exported/static TOC markup that may omit the dataset.
  const id = (link.dataset.target || '').trim() ||
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
