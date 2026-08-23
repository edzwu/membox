/* Miru — Vim-inspired section folding: wrap each H1–H3 into a collapsible
   <section>, and toggle collapse state on click or via the fold-all button. */

import { elements } from '../dom.js';
import { prefersReducedMotion } from '../utils.js';
import { scheduleNoteLayout } from '../annotations/layout.js';
import { setAllNotesCollapsed } from '../annotations/focus.js';

export function foldSections() {
  const headings = Array.from(elements.article.querySelectorAll('h1, h2, h3'));
  if (headings.length === 0) return;

  const headingInfos = headings.map((heading) => ({
    element: heading,
    level: parseInt(heading.tagName[1], 10),
  }));

  // Process in reverse so child sections are wrapped before their parent sections.
  for (let i = headingInfos.length - 1; i >= 0; i--) {
    const { element: heading, level } = headingInfos[i];

    const section = document.createElement('section');
    section.className = 'fold-section';
    section.dataset.foldLevel = String(level);

    const body = document.createElement('div');
    body.className = 'fold-body';

    heading.parentNode.insertBefore(section, heading);
    section.appendChild(heading);
    heading.classList.add('fold-heading');
    section.appendChild(body);

    // Source attribution happens later, by heading-text match rather than by
    // index, because rendered headings and parsed source sections are not
    // guaranteed to line up one-to-one (see attributeSectionSources).

    // Move following siblings into the body until a section boundary of the same or higher level.
    let current = section.nextSibling;
    while (current) {
      const isBoundary = current.nodeType === Node.ELEMENT_NODE &&
        current.classList.contains('fold-section') &&
        parseInt(current.dataset.foldLevel, 10) <= level;

      if (isBoundary) break;

      const next = current.nextSibling;
      body.appendChild(current);
      current = next;
    }
  }
}

export function toggleSection(section, open) {
  section.classList.toggle('is-collapsed', !open);
  scheduleNoteLayout();
}

export function addFoldListeners() {
  const headings = elements.article.querySelectorAll('.fold-heading');
  headings.forEach((heading) => {
    heading.addEventListener('click', (event) => {
      // Ignore clicks on links inside the heading (e.g. anchor links).
      if (event.target.closest('a')) return;

      const section = heading.closest('.fold-section');
      if (!section) return;

      const wasCollapsed = section.classList.contains('is-collapsed');
      toggleSection(section, wasCollapsed);

      // When expanding, scroll the heading into view and update the URL hash.
      if (wasCollapsed && heading.id) {
        const behavior = prefersReducedMotion() ? 'auto' : 'smooth';
        heading.scrollIntoView({ behavior, block: 'start' });
        try {
          history.replaceState(null, '', `#${heading.id}`);
        } catch (e) {
          // Ignore if history API is unavailable.
        }
      }
    });
  });
}

export function expandSectionForHeading(heading) {
  if (!heading) return;
  let section = heading.closest('.fold-section');
  while (section) {
    toggleSection(section, true);
    section = section.parentElement.closest('.fold-section');
  }
}

export function updateFoldToggleIcon(open) {
  if (!elements.foldIcon) return;
  elements.foldIcon.textContent = open ? '⊟' : '⊞';
  if (elements.foldToggle) {
    elements.foldToggle.title = open ? 'Collapse all sections' : 'Expand all sections';
    elements.foldToggle.setAttribute('aria-label', open ? 'Collapse all sections' : 'Expand all sections');
  }
}

export function foldAll(open) {
  const sections = elements.article.querySelectorAll('.fold-section');
  sections.forEach((section) => toggleSection(section, open));
  // Collapsing everything also tucks away every inline note body (summary / QA /
  // plain notes); reference numbers stay as the per-note reopen affordance.
  if (!open) setAllNotesCollapsed(true);
  updateFoldToggleIcon(open);
}

export function toggleFoldAll() {
  const sections = elements.article.querySelectorAll('.fold-section');
  if (!sections.length) return;

  const anyOpen = Array.from(sections).some((section) => !section.classList.contains('is-collapsed'));
  const open = !anyOpen;
  foldAll(open);
}
