/* Miru — drag & drop import: a bare Markdown file, a Markdown + `.miru.json`
   sidecar pair, a sidecar dropped onto its already-open Markdown, or a full
   `.miru.zip` bundle. */

import { elements } from '../dom.js';
import { state } from '../state.js';
import { showToast } from '../ui/feedback.js';
import { loadDocument } from '../document.js';
import { parseAnnotationSidecar, verifyAnnotationSource, restoreAnnotationSidecar } from '../annotations/sidecar.js';
import { readZipEntries, zipBasename } from '../zip.js';

function hasDraggedFiles(e) {
  if (!e.dataTransfer || !e.dataTransfer.types) return false;
  return Array.from(e.dataTransfer.types).includes('Files');
}

function isMarkdownName(name) {
  return /\.(md|markdown|mdown|mkd|txt)$/i.test(name);
}

function isMarkdownFile(file) {
  if (isMarkdownName(file.name)) return true;
  return file.type === 'text/markdown' || file.type === 'text/plain';
}

function isAnnotationFile(file) {
  return /\.miru\.json$/i.test(file.name);
}

function isZipFile(file) {
  return /\.zip$/i.test(file.name) || file.type === 'application/zip';
}

async function importAnnotationBundle(file) {
  showToast('Opening Miru bundle...');
  const entries = await readZipEntries(file);
  const annotationEntries = entries.filter((entry) => /\.miru\.json$/i.test(entry.name));
  if (annotationEntries.length !== 1) throw new Error('Not a Miru document bundle');

  const decoder = new TextDecoder('utf-8');
  const annotationData = parseAnnotationSidecar(decoder.decode(annotationEntries[0].data));
  const markdownEntries = entries.filter((entry) => isMarkdownName(entry.name));
  const expectedName = zipBasename(annotationData.markdownFile).toLowerCase();
  const markdownEntry = markdownEntries.find((entry) =>
    expectedName && zipBasename(entry.name).toLowerCase() === expectedName) ||
    (markdownEntries.length === 1 ? markdownEntries[0] : null);
  if (!markdownEntry) throw new Error('Bundle Markdown file is missing');

  const markdown = decoder.decode(markdownEntry.data);
  if (!markdown || !markdown.trim()) throw new Error('Bundle Markdown is empty');
  await verifyAnnotationSource(annotationData, markdown);
  state.droppedFilename = zipBasename(markdownEntry.name);
  loadDocument(markdown);
  const restored = restoreAnnotationSidecar(annotationData);
  if (restored === annotationData.annotations.length) {
    showToast(`Restored ${restored} annotation${restored === 1 ? '' : 's'}`);
  } else {
    showToast(`Restored ${restored} of ${annotationData.annotations.length} annotations`);
  }
}

async function importDroppedFiles(files) {
  const zipFile = files.find(isZipFile);
  if (zipFile) {
    await importAnnotationBundle(zipFile);
    return;
  }

  const markdownFile = files.find((file) => !isAnnotationFile(file) && isMarkdownFile(file));
  const annotationFile = files.find(isAnnotationFile);
  if (annotationFile) {
    const annotationData = parseAnnotationSidecar(await annotationFile.text());
    if (markdownFile) {
      const text = await markdownFile.text();
      if (!text || !text.trim()) throw new Error('Markdown file is empty');
      await verifyAnnotationSource(annotationData, text);
      state.droppedFilename = markdownFile.name;
      loadDocument(text);
    } else {
      if (!state.currentMarkdown) throw new Error('Drop the matching Markdown file too');
      await verifyAnnotationSource(annotationData, state.currentMarkdown);
      // Re-render the clean source before applying the sidecar so importing a
      // JSON file replaces, rather than overlaps, current session annotations.
      loadDocument(state.currentMarkdown);
    }
    const restored = restoreAnnotationSidecar(annotationData);
    showToast(`Restored ${restored} annotation${restored === 1 ? '' : 's'}`);
    return;
  }

  if (!markdownFile) {
    throw new Error('Drop Markdown or a Miru bundle');
  }
  const text = await markdownFile.text();
  if (!text || !text.trim()) throw new Error('Markdown file is empty');
  state.droppedFilename = markdownFile.name;
  loadDocument(text);
}

export function initDropZone() {
  let dragDepth = 0;

  document.addEventListener('dragenter', (e) => {
    if (!hasDraggedFiles(e)) return;
    dragDepth++;
    elements.body.classList.add('is-dragover');
  });

  document.addEventListener('dragleave', (e) => {
    if (!hasDraggedFiles(e)) return;
    dragDepth--;
    if (dragDepth <= 0) {
      dragDepth = 0;
      elements.body.classList.remove('is-dragover');
    }
  });

  // Always cancel dragover/drop so the browser never navigates to the
  // dropped file (its default action). The 'Files' type check is unreliable
  // across browsers on drop (e.g. WebKit can report an empty types list),
  // so prevention must be unconditional; the file is validated after.
  document.addEventListener('dragover', (e) => {
    e.preventDefault();
    if (e.dataTransfer) e.dataTransfer.dropEffect = 'copy';
  });

  document.addEventListener('drop', (e) => {
    e.preventDefault();
    dragDepth = 0;
    elements.body.classList.remove('is-dragover');

    const files = Array.from((e.dataTransfer && e.dataTransfer.files) || []);
    if (!files.length) return;
    importDroppedFiles(files).catch((err) => {
      console.error('Document import failed:', err);
      showToast(err && err.message ? err.message : 'Failed to open files');
    });
  });
}
