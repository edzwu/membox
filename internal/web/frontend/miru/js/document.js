/* Miru — the rendering pipeline: turn pasted/dropped Markdown into the live
   article DOM, and the empty/reading state transitions around it. This is
   the one module that knows the overall shape of "load a document" —
   feature modules it calls into don't need to know about each other. */

import { elements, READING_CONTROLS } from './dom.js';
import { state } from './state.js';
import { prefersReducedMotion } from './utils.js';
import { showToast } from './ui/feedback.js';
import { closeToc } from './ui/chrome.js';
import { parseFrontmatter, renderFrontmatter } from './markdown/frontmatter.js';
import { parseMarkdownStructure } from './markdown/structure.js';
import { configureTechnicalMarkdown } from './markdown/cjk-emphasis.js';
import { preprocessMath } from './markdown/math-preprocess.js';
import { preprocessHtml } from './markdown/html-preprocess.js';
import { wrapTables, markExternalLinks, wrapLeadingContent, addSectionActionButtons, stripHeadingHeaderLinks, attributeSectionSources } from './render/decorations.js';
import { highlightCode, renderMath, addCopyButtons } from './render/code-math.js';
import { renderMermaid } from './render/mermaid.js';
import { renderAnnotationSyntax } from './render/annotations-syntax.js';
import { foldSections, addFoldListeners, updateFoldToggleIcon } from './render/folding.js';
import { assignHeadingIds, buildToc, observeHeadings, resetToc, getHeadingLabel } from './render/toc.js';
import { looksLikeMarkdown, detectSnippetLanguage, renderSnippet } from './render/snippet.js';
import { scheduleNoteLayout } from './annotations/layout.js';
import { resetAnnotationSession } from './annotations/session.js';
import { hideAnnotToolbar } from './annotations/toolbar.js';

let md = null;

function notifyDocumentChange(kind) {
  window.dispatchEvent(new CustomEvent('miru-document-change', { detail: { kind } }));
}

export function initMarkdown() {
  if (typeof window.markdownit !== 'function') {
    console.error('markdown-it not loaded');
    showToast('Markdown renderer not loaded');
    return;
  }

  md = configureTechnicalMarkdown(
    window.markdownit({ html: false, linkify: true, typographer: true }),
  );

  // Use markdown-it-texmath to preserve \\[...\\] and \\(...\\) as raw LaTeX
  // inside custom <eqn> / <eq> tags. KaTeX then renders them after sanitization.
  const texmath = typeof window.texmath === 'function' ? window.texmath : null;
  if (texmath) {
    md.use(texmath, {
      delimiters: 'brackets',
      engine: { renderToString: (tex) => tex },
    });
  }
}

function sanitize(html) {
  if (typeof window.DOMPurify !== 'function') {
    console.error('DOMPurify not loaded');
    return html;
  }
  return window.DOMPurify.sanitize(html, {
    ADD_ATTR: ['target', 'rel'],
    ADD_TAGS: ['eq', 'eqn'],
  });
}

function processArticle() {
  wrapTables();
  highlightCode();
  renderMath();
  // Mermaid is async (lazy-loads vendor). Diagrams replace fences after paint;
  // fold/TOC already ran on the pre blocks, which is fine for navigation.
  void renderMermaid();
  renderAnnotationSyntax();
  foldSections();
  wrapLeadingContent();
  stripHeadingHeaderLinks();
  const headings = assignHeadingIds();
  attributeSectionSources();
  addCopyButtons();
  markExternalLinks();
  buildToc(headings);
  observeHeadings(headings);
  addSectionActionButtons();
  addFoldListeners();
}

function getDocumentTitle() {
  // A dropped file keeps its own name (round-trips on download);
  // pasted content falls back to the first H1, then 'Untitled'.
  if (state.droppedFilename) return state.droppedFilename.replace(/\.[^.]+$/, '');
  const h1 = elements.article.querySelector('h1');
  if (h1) return getHeadingLabel(h1);
  return 'Untitled';
}

function renderTitle() {
  const title = document.createElement('div');
  title.className = 'doc-title';
  title.id = 'doc-title';
  title.contentEditable = 'true';
  title.spellcheck = false;
  title.setAttribute('role', 'textbox');
  title.setAttribute('aria-label', 'Document title');
  title.setAttribute('aria-multiline', 'false');

  state.docTitle = getDocumentTitle();
  title.textContent = state.docTitle;

  // Track IME composition so an Enter that merely confirms an IME candidate
  // is not mistaken for "finish editing". Blurring mid-composition makes the
  // IME re-insert the pending text at the start of the field, duplicating it.
  let composing = false;
  title.addEventListener('compositionstart', () => { composing = true; });
  title.addEventListener('compositionend', () => { composing = false; });

  title.addEventListener('keydown', (e) => {
    if (e.key === 'Enter') {
      if (composing || e.isComposing) return;
      e.preventDefault();
      title.blur();
    } else if (e.key === 'Escape') {
      if (composing || e.isComposing) return;
      title.textContent = state.docTitle;
      title.blur();
    }
  });

  title.addEventListener('blur', () => {
    const text = title.textContent.replace(/\s+/g, ' ').trim();
    state.docTitle = text || 'Untitled';
    title.textContent = state.docTitle;
  });

  title.addEventListener('paste', (e) => {
    e.preventDefault();
    const text = (e.clipboardData || window.clipboardData).getData('text/plain');
    document.execCommand('insertText', false, text);
  });

  elements.article.insertBefore(title, elements.article.firstChild);
}

export function setEmptyState() {
  elements.body.classList.remove('is-reading', 'is-snippet');
  elements.body.classList.add('is-empty');
  READING_CONTROLS.forEach((el) => { el.hidden = true; });
  resetToc();
}

function setReadingState() {
  elements.body.classList.remove('is-empty', 'is-snippet');
  elements.body.classList.add('is-reading');
  READING_CONTROLS.forEach((el) => { el.hidden = false; });
  updateFoldToggleIcon(true);
}

function loadSnippetDocument(code, lang) {
  state.currentMarkdown = code;
  resetAnnotationSession();
  state.leadingSource = '';
  state.sectionSources = [];
  hideAnnotToolbar();
  elements.article.innerHTML = '';
  elements.article.classList.remove('has-note-rail');
  elements.annotationLayer.classList.remove('is-rail', 'is-stack');
  elements.annotationLayer.replaceChildren();

  const { filename } = renderSnippet(code, lang);
  const base = (state.droppedFilename || filename).replace(/\.[^.]+$/, '');
  state.docTitle = base || 'snippet';

  resetToc();
  setReadingState();
  elements.body.classList.add('is-snippet');
  closeToc();

  const behavior = prefersReducedMotion() ? 'auto' : 'smooth';
  window.scrollTo({ top: 0, behavior });
  scheduleNoteLayout();
  notifyDocumentChange('snippet');
}

export function loadDocument(text) {
  if (!md) {
    showToast('Markdown renderer not ready');
    return;
  }

  // Code pastes get a dedicated ray-style snippet card rather than a mangled
  // Markdown rendering. Prose and structured text keep the Markdown path.
  if (!looksLikeMarkdown(text)) {
    const lang = detectSnippetLanguage(text);
    if (lang) {
      loadSnippetDocument(text, lang);
      return;
    }
  }

  state.currentMarkdown = text;
  resetAnnotationSession();
  hideAnnotToolbar();
  const { meta, body } = parseFrontmatter(text);
  const structure = parseMarkdownStructure(body);
  state.leadingSource = structure.leadingSource;
  state.sectionSources = structure.sections;
  const processedText = preprocessMath(preprocessHtml(body));
  const rawHtml = md.render(processedText);
  const cleanHtml = sanitize(rawHtml);
  elements.article.innerHTML = cleanHtml;
  elements.article.classList.remove('has-note-rail');
  elements.annotationLayer.classList.remove('is-rail', 'is-stack');
  elements.annotationLayer.replaceChildren();
  processArticle();

  if (meta) {
    renderFrontmatter(meta);
  }
  renderTitle();

  setReadingState();
  closeToc();

  const behavior = prefersReducedMotion() ? 'auto' : 'smooth';
  window.scrollTo({ top: 0, behavior });
  scheduleNoteLayout();
  notifyDocumentChange('markdown');
}

export function clearDocument() {
  state.currentMarkdown = '';
  state.docTitle = '';
  state.droppedFilename = '';
  resetAnnotationSession();
  hideAnnotToolbar();
  elements.article.classList.remove('has-note-rail');
  elements.annotationLayer.classList.remove('is-rail', 'is-stack');
  state.leadingSource = '';
  state.sectionSources = [];
  elements.article.innerHTML = '';
  elements.annotationLayer.replaceChildren();
  resetToc();
  setEmptyState();
  const behavior = prefersReducedMotion() ? 'auto' : 'smooth';
  window.scrollTo({ top: 0, behavior });
  notifyDocumentChange('empty');
}
