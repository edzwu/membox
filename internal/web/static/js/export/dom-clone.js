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

// mode 'pin': freeze the live geometry for a static snapshot (PNG).
// mode 'site': keep floats alive — ghosts/sentinels keep wrapping and the
// site's inline script re-syncs cards on resize/fold; leaders go stale and
// are stripped.
function cleanExportClone(source, mode = 'site') {
  const clone = source.cloneNode(true);
  if (mode === 'pin') {
    preserveNoteLayout(source, clone);
    clone.querySelectorAll('.annot-note-in-rail').forEach((note) => { note.hidden = false; });
    // Leaders are a transient in-app affordance; snapshots keep the wrap only.
    clone.querySelectorAll('.annot-leader').forEach((node) => node.remove());
  } else {
    clone.classList.remove('has-note-rail');
    clone.querySelectorAll('.annot-note-in-rail').forEach((note) => {
      note.classList.remove('annot-note-in-rail');
      note.style.removeProperty('top');
      note.hidden = false;
    });
    clone.querySelectorAll('.annot-leader').forEach((node) => node.remove());
    clone.querySelectorAll('.annot-note-floated').forEach((note) => { note.hidden = false; });
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
  clone.querySelectorAll('.section-copy, .section-download, .lead-copy, .code-copy, .code-lang, .annot-note-edit, .annot-note-del, .snippet-copy, .snippet-langselect').forEach((n) => n.remove());
  if (clone.matches('.fold-section.is-collapsed')) {
    clone.classList.remove('is-collapsed');
  }
  clone.querySelectorAll('.fold-section.is-collapsed').forEach((section) => {
    section.classList.remove('is-collapsed');
  });
  return clone;
}

export function cleanArticleForExport(mode = 'site') {
  return cleanExportClone(elements.article, mode);
}

// A section needs an article ancestor so all `.article …` typography and
// annotation rules still apply when the subtree is rendered in isolation.
export function cleanSectionForExport(section) {
  const article = document.createElement('article');
  article.className = elements.article.classList.contains('has-note-rail')
    ? 'article has-note-rail'
    : 'article';
  article.appendChild(cleanExportClone(section, 'pin'));
  return article;
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
