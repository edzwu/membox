/* Document identity and loading: binds this tab to a membox document,
   renders it, restores its reading state, and owns the top-bar document
   switcher plus the rename flow. */

import { elements } from '../js/dom.js';
import { state } from '../js/state.js';
import { loadDocument } from '../js/document.js';
import { sanitizeFilename } from '../js/utils.js';
import { showToast } from '../js/ui/feedback.js';
import { assignHeadingIds, buildToc } from '../js/render/toc.js';
import { session } from './session.js';
import { emitRender } from './events.js';
import { fetchDocument, renameDocument } from './api.js';
import { convertedDisplayLabel, documentDisplayLabel } from './labels.js';
import { cancelPendingSaves, resetReadingSession, restoreReadingState } from './reading-state.js';
import { hideSeriesNav, loadSeriesNav } from './series-nav.js';

let documentNavigation = null;
let documentSwitcher = null;
let titleBeforeEdit = '';

export function getDocumentNavigation() {
  return documentNavigation;
}

function createDocumentSwitcher() {
  const button = document.createElement('button');
  button.type = 'button';
  button.id = 'membox-document-switcher';
  button.className = 'membox-document-switcher';
  button.hidden = true;
  button.title = 'Switch document · Ctrl+O';
  button.setAttribute('aria-label', 'Switch document');
  button.innerHTML = `
    <svg class="membox-document-switcher-icon" viewBox="0 0 24 24" aria-hidden="true">
      <path d="M7 7V5a2 2 0 0 1 2-2h8l3 3v11a2 2 0 0 1-2 2h-2" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/>
      <rect x="4" y="7" width="12" height="14" rx="2" fill="none" stroke="currentColor" stroke-width="1.5"/>
      <path d="M7 12h6M7 16h6" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/>
    </svg>
    <span class="membox-document-switcher-label">Open document</span>
    <svg class="membox-document-switcher-chevron" viewBox="0 0 16 16" aria-hidden="true">
      <path d="M4 6l4 4 4-4" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/>
    </svg>`;
  documentNavigation.appendChild(button);
  return button;
}

export function renderDocumentSwitcher() {
  documentNavigation.hidden = !session.connected;
  documentSwitcher.hidden = !session.connected;
  const liveTitle = document.getElementById('doc-title')?.textContent?.replace(/\s+/g, ' ').trim();
  const title = liveTitle || state.docTitle || 'Open document';
  documentSwitcher.querySelector('.membox-document-switcher-label').textContent = title;
  documentSwitcher.setAttribute('aria-label', session.documentID ? `Switch document from ${title}` : 'Open document');
}

export function replaceDocumentID(id) {
  session.documentID = id || '';
  const url = new URL(window.location.href);
  if (session.documentID) url.searchParams.set('id', session.documentID);
  else url.searchParams.delete('id');
  // Preserve one-way navigation intent (e.g. `note=<ref>` from the review
  // feed's 原文 link) — replaceDocumentID only rewrites the document id.
  window.history.replaceState(null, '', url);
  emitRender();
}

// Keep the adapter identity honest when the user starts a fresh paste/session.
export function unbindDocument() {
  replaceDocumentID('');
  session.loadedMarkdown = '';
  session.annotationsMutated = false;
  session.annotationsDirty = false;
  resetReadingSession();
  hideSeriesNav();
}

async function renameCurrentDocument(titleElement, previousTitle, nextTitle) {
  const renamedDocumentID = session.documentID;
  const currentFilename = state.droppedFilename || '';
  const extensionMatch = currentFilename.match(/\.(?:md|markdown)$/i);
  const extension = extensionMatch ? extensionMatch[0] : '.md';
  const filename = `${sanitizeFilename(nextTitle)}${extension}`;
  titleElement.contentEditable = 'false';
  titleElement.dataset.saving = 'true';
  try {
    const result = await renameDocument(renamedDocumentID, filename, nextTitle);
    if (session.documentID !== renamedDocumentID) return;
    const savedFilename = result.filename || filename;
    const savedTitle = (result.title || '').trim() || nextTitle;
    state.droppedFilename = savedFilename;
    state.docTitle = savedTitle;
    titleElement.textContent = savedTitle;
    document.title = `${savedTitle} — membox`;
    // Body H1/front matter may have changed server-side — refresh the article
    // so the in-document heading matches the chrome title.
    if (session.connected) {
      try {
        const fresh = await fetchDocument(renamedDocumentID);
        session.loadedMarkdown = fresh.markdown;
        // Preserve scroll while re-rendering title-bearing content.
        const y = window.scrollY;
        loadDocument(fresh.markdown);
        state.droppedFilename = savedFilename;
        state.docTitle = savedTitle;
        const live = document.getElementById('doc-title');
        if (live) live.textContent = savedTitle;
        window.scrollTo(0, y);
      } catch {
        /* non-fatal: chrome title already updated */
      }
    }
    renderDocumentSwitcher();
    showToast(`Renamed document to ${savedTitle}`);
  } catch (err) {
    if (session.documentID !== renamedDocumentID) return;
    state.docTitle = previousTitle;
    titleElement.textContent = previousTitle;
    renderDocumentSwitcher();
    console.error('membox: document rename failed', err);
    showToast(`Could not rename document: ${err.message}`);
  } finally {
    if (titleElement.isConnected) {
      titleElement.contentEditable = 'true';
      delete titleElement.dataset.saving;
    }
  }
}

// Shorten chrome + body titles for generated PDF→MD chapters so Miru matches
// the TUI tree ("just-for-fun ch.12") instead of the identity-laden stem.
export function applyConversionDisplayLabelIfNeeded(filename, catalogTitle) {
  return applyConversionDisplayLabel(filename, catalogTitle);
}

function applyConversionDisplayLabel(filename, catalogTitle) {
  const label = documentDisplayLabel({ filename, catalogTitle });
  state.docTitle = label;
  const live = document.getElementById('doc-title');
  if (live) live.textContent = label;
  document.title = `${label} — membox`;

  const stem = String(filename || '')
    .split(/[\\/]/)
    .pop()
    .replace(/\.md$/i, '');
  const h1 = elements.article.querySelector('h1');
  if (h1) {
    const raw = (h1.textContent || '').replace(/\s+/g, ' ').trim();
    const h1IsConversion = Boolean(convertedDisplayLabel(`${raw}.md`));
    const h1MatchesFile = stem && raw.toLowerCase() === stem.toLowerCase();
    if (h1IsConversion || h1MatchesFile) {
      h1.textContent = label;
      // TOC was built before the rewrite; rebuild so nav labels stay in sync.
      buildToc(assignHeadingIds());
    }
  }
  renderDocumentSwitcher();
  return label;
}

export async function loadFromMembox() {
  if (!session.documentID) return;
  try {
    const { markdown, filename, catalogTitle } = await fetchDocument(session.documentID);
    if (filename) {
      state.droppedFilename = filename;
    }
    session.loadedMarkdown = markdown;
    loadDocument(markdown);
    // Notes before progress: note cards change the layout, so the saved
    // scroll position only means something once they are in place.
    await restoreReadingState(session.documentID, markdown);
    // Apply AFTER restore: the annotation sidecar can rewrite .doc-title with
    // a previously saved long conversion stem. Also shorten a body H1 that is
    // just the raw -pdf-<uuid> filename so the page matches the TUI tree.
    applyConversionDisplayLabel(filename, catalogTitle);
    void loadSeriesNav();
    if (session.pendingSourceNoteRef) {
      session.pendingSourceNoteRef = '';
      showToast('The note is saved, but its passage could not be located in this document');
    }
  } catch (err) {
    console.error('membox: failed to load document', err);
    showToast(`Could not load from membox: ${err.message}`);
  }
}

export function initDocumentUI({ onOpenPicker }) {
  documentNavigation = document.createElement('div');
  documentNavigation.className = 'membox-document-navigation';
  documentNavigation.hidden = true;
  document.querySelector('.topbar-center')?.appendChild(documentNavigation);

  documentSwitcher = createDocumentSwitcher();
  documentSwitcher.addEventListener('click', onOpenPicker);

  new MutationObserver(renderDocumentSwitcher).observe(elements.article, {
    childList: true,
    subtree: true,
    characterData: true,
  });

  elements.article.addEventListener('focusin', (event) => {
    if (event.target.id !== 'doc-title') return;
    titleBeforeEdit = event.target.textContent.replace(/\s+/g, ' ').trim() || state.docTitle || 'Untitled';
  });
  elements.article.addEventListener('focusout', (event) => {
    if (event.target.id !== 'doc-title') return;
    const nextTitle = (state.docTitle || '').trim() || 'Untitled';
    if (nextTitle === titleBeforeEdit || !session.documentID) return;
    if (!session.connected) {
      state.docTitle = titleBeforeEdit;
      event.target.textContent = titleBeforeEdit;
      showToast('Connect to membox before renaming this document');
      return;
    }
    void renameCurrentDocument(event.target, titleBeforeEdit, nextTitle);
  });
}
