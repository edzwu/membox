/* Miru — PNG export (SVG -> canvas at 2x, ported from Earendil Ray): render
   the whole document or a single section to a raster image, and the
   clipboard-copy variant. Ray's default "M" frame uses 64px of padding. */

import { elements } from '../dom.js';
import { state } from '../state.js';
import { PNG_EXPORT_PADDING, EXPORT_STATIC_CSS, NOTE_RAIL_GAP, NOTE_RAIL_WIDTH } from '../constants.js';
import { sanitizeFilename } from '../utils.js';
import { showToast, downloadBlob, flashButton } from '../ui/feedback.js';
import { getExportStyles } from './export-styles.js';
import { cleanArticleForExport, cleanSectionForExport, inlineArticleImages } from './dom-clone.js';
import { layoutMarginNotes } from '../annotations/layout.js';
import { getHeadingLabel } from '../render/toc.js';

function getPNGExportWidth(source) {
  // Export the natural reading column rather than browser-width whitespace.
  // Include scrollWidth so intentionally wide code blocks are never clipped.
  let parts;
  if (source.matches('.fold-section')) {
    parts = Array.from(source.children).filter((child) =>
      child.classList.contains('fold-heading') || child.classList.contains('fold-body'));
  } else {
    parts = Array.from(source.children).filter((child) =>
      !child.classList.contains('fold-section'));
    parts.push(...source.querySelectorAll('.fold-heading, .fold-body'));
  }
  if (!parts.length) parts = [source];
  let width = parts.reduce((max, part) => {
    return Math.max(max, part.getBoundingClientRect().width, part.scrollWidth);
  }, 0);
  const includesLiveRail = source === elements.article &&
    elements.annotationLayer.classList.contains('is-rail') &&
    elements.annotationLayer.querySelector('.annot-note-in-rail');
  if (includesLiveRail) {
    const articleMax = parseFloat(getComputedStyle(elements.article).getPropertyValue('--article-max')) || 860;
    width = Math.max(width, articleMax + NOTE_RAIL_GAP + NOTE_RAIL_WIDTH);
  }
  return Math.max(1, Math.ceil(width));
}

function measureExportHeight(node, width) {
  const host = document.createElement('div');
  host.setAttribute('aria-hidden', 'true');
  host.style.position = 'fixed';
  host.style.left = '-100000px';
  host.style.top = '0';
  host.style.width = width + 'px';
  host.style.visibility = 'hidden';
  host.style.pointerEvents = 'none';
  host.style.overflow = 'hidden';
  host.appendChild(node);
  document.body.appendChild(host);
  const nodeRect = node.getBoundingClientRect();
  let height = Math.max(1, Math.ceil(node.scrollHeight), Math.ceil(nodeRect.height));
  node.querySelectorAll('.annot-note-in-rail').forEach((note) => {
    height = Math.max(height, Math.ceil(note.getBoundingClientRect().bottom - nodeRect.top));
  });
  host.removeChild(node);
  host.remove();
  return height;
}

function buildPNGSVG(source = elements.article) {
  layoutMarginNotes();
  return getExportStyles().then(async (stylesText) => {
    const isSection = source !== elements.article;
    const node = isSection ? cleanSectionForExport(source) : cleanArticleForExport('pin');
    await inlineArticleImages(node);
    const contentWidth = getPNGExportWidth(source);
    // Measure the expanded clone rather than the live DOM, which may currently
    // have this section (or one of its descendants) folded closed.
    const contentHeight = measureExportHeight(node, contentWidth);
    const width = contentWidth + PNG_EXPORT_PADDING * 2;
    const height = contentHeight + PNG_EXPORT_PADDING * 2;
    const theme = elements.html.getAttribute('data-theme') || 'light';

    const wrapper = document.createElement('div');
    wrapper.setAttribute('xmlns', 'http://www.w3.org/1999/xhtml');
    wrapper.style.boxSizing = 'border-box';
    wrapper.style.width = width + 'px';
    wrapper.style.minHeight = height + 'px';
    wrapper.style.padding = PNG_EXPORT_PADDING + 'px';
    wrapper.style.background = 'var(--paper)';
    wrapper.style.color = 'var(--ink)';
    wrapper.style.overflow = 'hidden';
    wrapper.appendChild(node);

    const xhtml = new XMLSerializer().serializeToString(wrapper);
    // The stylesheet lives inside <style> within an XML document, so any raw
    // '&' in the CSS (e.g. a comment like "Drag & drop") would break XML
    // parsing and make the SVG undecodable. CDATA makes the style opaque to
    // the XML parser; the stylesheets contain no ']]>' sequences.
    const svg = `<svg xmlns="http://www.w3.org/2000/svg" data-theme="${theme}" width="${width}" height="${height}" viewBox="0 0 ${width} ${height}">
<style><![CDATA[
${stylesText}
${EXPORT_STATIC_CSS}
.article { width: 100% !important; }
]]></style>
<foreignObject width="100%" height="100%">
${xhtml}
</foreignObject>
</svg>`;
    return { svg, width, height };
  });
}

export function renderPNG(scale, source = elements.article) {
  return buildPNGSVG(source).then(({ svg, width, height }) => {
    const img = new Image();
    img.src = 'data:image/svg+xml;charset=utf-8,' + encodeURIComponent(svg);
    return img.decode().then(() => {
      return new Promise((res) => setTimeout(res, 80));
    }).then(() => {
      const canvas = document.createElement('canvas');
      canvas.width = width * scale;
      canvas.height = height * scale;
      const ctx = canvas.getContext('2d');
      ctx.scale(scale, scale);
      ctx.drawImage(img, 0, 0);
      return new Promise((resolve, reject) => {
        canvas.toBlob((blob) => {
          if (blob) resolve(blob);
          else reject(new Error('toBlob failed'));
        }, 'image/png');
      });
    });
  });
}

export function exportPNG() {
  if (!state.currentMarkdown) {
    showToast('Nothing to export');
    return;
  }
  showToast('Rendering PNG...');
  renderPNG(2)
    .then((blob) => {
      downloadBlob(blob, sanitizeFilename(state.docTitle) + '.png');
      flashButton(elements.pngAll);
      showToast('PNG saved (2x)');
    })
    .catch((err) => {
      console.error('PNG export failed:', err);
      showToast('PNG export failed');
    });
}

export function downloadSectionPNG(section, heading, button) {
  const label = heading.dataset.headingLabel || getHeadingLabel(heading);
  // Prefix the document title so per-section files stay unique across docs.
  const base = state.docTitle ? `${state.docTitle}-${label}` : label;
  showToast('Rendering section PNG...');
  renderPNG(2, section)
    .then((blob) => {
      downloadBlob(blob, sanitizeFilename(base) + '.png');
      flashButton(button);
      showToast('Section PNG saved (2x)');
    })
    .catch((err) => {
      console.error('Section PNG export failed:', err);
      showToast('Section PNG export failed');
    });
}

export function copyPNG() {
  if (!state.currentMarkdown) {
    showToast('Nothing to copy');
    return;
  }
  if (!navigator.clipboard || !window.ClipboardItem) {
    showToast('Clipboard unsupported');
    return;
  }
  showToast('Copying PNG...');
  const item = new ClipboardItem({ 'image/png': renderPNG(2) });
  navigator.clipboard.write([item])
    .then(() => {
      flashButton(elements.copyPng);
      showToast('PNG copied');
    })
    .catch((err) => {
      console.error('Copy PNG failed:', err);
      showToast('Copy PNG failed');
    });
}
