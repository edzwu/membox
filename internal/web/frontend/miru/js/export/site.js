/* Miru — export the current document as a small interactive static site
   (index.html inside a ZIP): sticky TOC, live section folding, and a theme
   toggle that defaults to light. */

import { elements } from '../dom.js';
import { state } from '../state.js';
import { EXPORT_INTERACTIVE_CSS } from '../constants.js';
import { sanitizeFilename, escapeHtml } from '../utils.js';
import { showToast, downloadBlob, flashButton } from '../ui/feedback.js';
import { getExportStyles } from './export-styles.js';
import { cleanArticleForExport, inlineArticleImages } from './dom-clone.js';
import { buildZip } from '../zip.js';

export function exportSiteZip() {
  if (!state.currentMarkdown) {
    showToast('Nothing to export');
    return;
  }
  showToast('Rendering site...');
  getExportStyles().then(async (stylesText) => {
    const layout = document.createElement('div');
    layout.className = 'layout';

    const main = document.createElement('main');
    main.className = 'main';
    const articleClone = cleanArticleForExport('site');
    await inlineArticleImages(articleClone);
    main.appendChild(articleClone);
    layout.appendChild(main);

    const toc = document.createElement('aside');
    toc.className = 'toc';
    toc.setAttribute('aria-label', 'Table of contents');
    const tocNavClone = elements.tocNav.cloneNode(true);
    tocNavClone.removeAttribute('id');
    toc.appendChild(tocNavClone);
    layout.appendChild(toc);

    const title = state.docTitle || 'Article';

    // Layout CSS for the interactive site export. The TOC keeps its sticky
    // behaviour from the app (top:0 since there is no topbar here) so it
    // stays visible while scrolling; on narrow screens it stacks on top.
    const layoutCSS = `
        body { margin: 0; background: var(--paper); color: var(--ink); }
        .layout { padding-top: 0 !important; }
        .toc { top: 0 !important; max-height: 100vh !important; }
        .article { width: 100% !important; max-width: var(--article-max) !important; margin: 0 auto !important; }
        .export-actions {
          position: fixed;
          top: 14px;
          right: 14px;
          z-index: 100;
          display: flex;
          gap: 8px;
        }
        .export-action {
          width: 36px;
          height: 36px;
          appearance: none;
          border: 1px solid var(--line);
          background: var(--paper-raised);
          color: var(--ink-soft);
          border-radius: var(--radius);
          cursor: pointer;
          display: inline-flex;
          align-items: center;
          justify-content: center;
          font-size: 16px;
          line-height: 1;
          box-shadow: 0 4px 16px var(--shadow);
          transition: color 0.15s ease, background 0.15s ease, border-color 0.15s ease;
        }
        .export-action:hover {
          color: var(--accent);
          border-color: var(--accent);
          background: var(--accent-soft);
        }
        @media (max-width: 768px) {
          .layout { flex-direction: column !important; }
          .toc {
            position: relative !important;
            transform: none !important;
            left: auto !important;
            width: auto !important;
            border-right: 0 !important;
            box-shadow: none !important;
            padding: 24px !important;
            max-height: none !important;
          }
          .toc-backdrop { display: none !important; }
          .main { padding: 24px 20px !important; }
        }
      `;

    // Minimal interactivity for the static export: theme toggle (light by
    // default, remembers a manual choice) and click-to-fold headings.
    const interactiveScript = `
(function () {
  var root = document.documentElement;
  var STORAGE_KEY = 'miru-theme';

  function currentTheme() {
    return root.getAttribute('data-theme') || 'light';
  }
  function setIcon(theme) {
    var icon = document.getElementById('export-theme-icon');
    if (icon) icon.textContent = theme === 'dark' ? '\\u2600' : '\\u263e';
  }
  function applyTheme(theme) {
    root.setAttribute('data-theme', theme);
    setIcon(theme);
  }

  var saved = null;
  try { saved = localStorage.getItem(STORAGE_KEY); } catch (e) {}
  applyTheme(saved === 'dark' || saved === 'light' ? saved : 'light');

  var toggle = document.getElementById('export-theme-toggle');
  if (toggle) {
    toggle.addEventListener('click', function () {
      var next = currentTheme() === 'dark' ? 'light' : 'dark';
      applyTheme(next);
      try { localStorage.setItem(STORAGE_KEY, next); } catch (e) {}
    });
  }

  // Fold / expand all sections at once (mirrors the app's fold-all button).
  var foldIcon = document.getElementById('export-fold-icon');
  function setFoldIcon(open) {
    if (foldIcon) foldIcon.textContent = open ? '\u229f' : '\u229e';
  }
  var foldToggle = document.getElementById('export-fold-toggle');
  if (foldToggle) {
    foldToggle.addEventListener('click', function () {
      var sections = document.querySelectorAll('.fold-section');
      var anyOpen = Array.prototype.some.call(sections, function (s) {
        return !s.classList.contains('is-collapsed');
      });
      var open = !anyOpen;
      sections.forEach(function (s) { s.classList.toggle('is-collapsed', !open); });
      setFoldIcon(open);
    });
  }

  document.querySelectorAll('.fold-heading').forEach(function (heading) {
    heading.addEventListener('click', function (event) {
      if (event.target.closest('a')) return;
      var section = heading.closest('.fold-section');
      if (section) section.classList.toggle('is-collapsed');
    });
  });
})();
      `;

    const doc = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>${escapeHtml(title)}</title>
<style>
${stylesText}
${EXPORT_INTERACTIVE_CSS}
${layoutCSS}
</style>
</head>
<body>
<div class="export-actions">
  <button type="button" class="export-action" id="export-fold-toggle" aria-label="Toggle all sections" title="Toggle all sections"><span id="export-fold-icon">⊟</span></button>
  <button type="button" class="export-action" id="export-theme-toggle" aria-label="Toggle theme" title="Toggle theme"><span id="export-theme-icon"></span></button>
</div>
${layout.outerHTML}
<script>${interactiveScript}</script>
</body>
</html>`;

    const zipBytes = buildZip([{ name: 'index.html', data: new TextEncoder().encode(doc) }]);
    downloadBlob(new Blob([zipBytes], { type: 'application/zip' }), sanitizeFilename(state.docTitle) + '.zip');
    flashButton(elements.htmlAll);
    showToast('Site ZIP saved');
  }).catch((err) => {
    console.error('Site export failed:', err);
    showToast('Site export failed');
  });
}
