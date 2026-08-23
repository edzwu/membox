/* Miru — produce a static, export-ready clone of the article (or a single
   section): strip edit affordances, resolve fold state and the note-rail
   layout, and inline remote images as data URIs. Shared by the PNG exporter
   and the interactive site ZIP exporter. */

import { elements } from '../dom.js';

function preserveNoteLayout(source, clone) {
  const sourceNotes = Array.from(source.querySelectorAll('.annot-note'));
  const clonedNotes = Array.from(clone.querySelectorAll('.annot-note'));
  sourceNotes.forEach((note, index) => {
    const clonedNote = clonedNotes[index];
    if (!clonedNote) return;
    // A PNG snapshot can be narrower than the browser viewport. Without
    // pinning these computed values, the SVG's max-width media query can turn
    // a desktop margin note into the mobile full-width layout.
    const style = window.getComputedStyle(note);
    clonedNote.style.cssFloat = style.cssFloat;
    clonedNote.style.width = style.width;
    clonedNote.style.marginTop = style.marginTop;
    clonedNote.style.marginRight = style.marginRight;
    clonedNote.style.marginBottom = style.marginBottom;
    clonedNote.style.marginLeft = style.marginLeft;
  });
}

// mode 'pin': freeze the live rail geometry for a static snapshot (PNG).
// mode 'site': drop absolute rail positioning so notes flow as asides in the
// portable HTML (no live layout pass there).
function cleanExportClone(source, mode = 'site') {
  const clone = source.cloneNode(true);
  // Legacy float-era chrome never ships in exports.
  clone.querySelectorAll('.annot-ghost, .annot-ghost-clear, .annot-leader').forEach((n) => n.remove());
  clone.querySelectorAll('.annot-note-floated').forEach((note) => {
    note.classList.remove('annot-note-floated', 'is-dragging', 'will-dock');
    note.style.removeProperty('left');
    note.style.removeProperty('width');
    note.hidden = false;
  });
  if (mode === 'pin') {
    preserveNoteLayout(source, clone);
    clone.querySelectorAll('.annot-note-in-rail').forEach((note) => { note.hidden = false; });
  } else {
    clone.classList.remove('has-note-rail');
    clone.querySelectorAll('.annot-note-in-rail').forEach((note) => {
      note.classList.remove('annot-note-in-rail');
      note.style.removeProperty('top');
      note.style.removeProperty('z-index');
      note.hidden = false;
    });
  }
  // Keep heading IDs so TOC anchor links still work in exports; strip the rest.
  if (clone.id && !clone.matches('h1, h2, h3, h4, h5, h6')) {
    clone.removeAttribute('id');
  }
  clone.querySelectorAll('[id]').forEach((n) => {
    if (!n.matches('h1, h2, h3, h4, h5, h6')) {
      n.removeAttribute('id');
    }
  });
  // Strip edit affordances so the export renders as a static document.
  clone.querySelectorAll('.doc-title, .snippet-filename').forEach((n) => {
    n.removeAttribute('contenteditable');
    n.removeAttribute('spellcheck');
    n.removeAttribute('role');
    n.removeAttribute('aria-label');
    n.removeAttribute('aria-multiline');
  });
  clone.querySelectorAll('.section-copy, .section-download, .lead-copy, .code-copy, .code-lang, .annot-note-edit, .annot-note-del, .snippet-copy, .snippet-langselect, [data-export-remove]').forEach((n) => n.remove());
  clone.querySelectorAll('[data-export-remove-class]').forEach((n) => {
    n.classList.remove(...n.dataset.exportRemoveClass.split(/\s+/).filter(Boolean));
    delete n.dataset.exportRemoveClass;
  });
  if (clone.matches('.fold-section.is-collapsed')) {
    clone.classList.remove('is-collapsed');
  }
  clone.querySelectorAll('.fold-section.is-collapsed').forEach((section) => {
    section.classList.remove('is-collapsed');
  });
  return clone;
}

function cleanAnnotationLayerForExport(mode, annotationIds = null) {
  // Notes now live inline under their passage inside the article clone.
  // The annotation layer is only a park for orphans / jp-study shells — skip
  // exporting it as a bottom stack so we do not duplicate inline notes.
  void mode;
  void annotationIds;
  return null;
}

function exportSurface(article, annotationLayer) {
  const surface = document.createElement('div');
  surface.className = 'reading-surface export-reading-surface';
  surface.appendChild(article);
  if (annotationLayer) surface.appendChild(annotationLayer);
  return surface;
}

export function cleanArticleForExport(mode = 'site') {
  const article = cleanExportClone(elements.article, mode);
  const annotationLayer = cleanAnnotationLayerForExport(mode);
  return exportSurface(article, annotationLayer);
}

// A section needs an article ancestor so all `.article …` typography rules
// still apply. Its notes remain a separate sibling layer and are filtered to
// anchors contained by this section.
export function cleanSectionForExport(section) {
  const article = document.createElement('article');
  article.className = 'article';
  article.appendChild(cleanExportClone(section, 'pin'));
  const annotationIds = new Set(Array.from(section.querySelectorAll('span.annot-note-ref[data-annot-id]'))
    .map((anchor) => String(anchor.dataset.annotId)));
  const annotationLayer = cleanAnnotationLayerForExport('site', annotationIds);
  return exportSurface(article, annotationLayer);
}

// Fetch every <img> in the subtree and inline it as a base64 data URI. An
// SVG loaded as an image is in "secure static mode" and cannot fetch
// external resources, so linked images would otherwise be blank in exports.
// Images that fail (CORS, network) are left as-is and simply won't render.
export function inlineArticleImages(root) {
  const imgs = Array.from(root.querySelectorAll('img'));
  return Promise.all(imgs.map((img) => {
    const src = img.getAttribute('src');
    if (!src || src.startsWith('data:')) return Promise.resolve();
    return fetch(src)
      .then((r) => {
        if (!r.ok) throw new Error('image fetch failed: ' + src);
        return r.blob();
      })
      .then((blob) => new Promise((resolve, reject) => {
        const reader = new FileReader();
        reader.onload = () => resolve(reader.result);
        reader.onerror = reject;
        reader.readAsDataURL(blob);
      }))
      .then((dataUri) => { img.src = dataUri; })
      .catch((err) => console.warn('PNG export: could not inline image', src, err));
  }));
}
