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

    if (!heading.querySelector('.section-copy')) {
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
