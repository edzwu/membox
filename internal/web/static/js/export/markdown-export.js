/* Miru — copy/download the current Markdown, packing it with the annotation
   sidecar into a `.miru.zip` bundle whenever annotations exist. */

import { elements } from '../dom.js';
import { state } from '../state.js';
import { sanitizeFilename } from '../utils.js';
import { showToast, downloadBlob, flashButton, writeClipboard, flashCopied } from '../ui/feedback.js';
import { buildAnnotationSidecar } from '../annotations/sidecar.js';
import { buildZip } from '../zip.js';

export function copyAllMarkdown() {
  if (!state.currentMarkdown) return;
  writeClipboard(state.currentMarkdown, () => flashCopied(elements.copyAll, { preserveContent: true }));
}

export async function downloadAllMarkdown() {
  if (!state.currentMarkdown) return;
  const base = sanitizeFilename(state.docTitle);
  if (!state.annotations.length) {
    downloadBlob(new Blob([state.currentMarkdown], { type: 'text/markdown' }), base + '.md');
    flashButton(elements.downloadAll);
    showToast('Markdown downloaded');
    return;
  }

  showToast('Packing Markdown + annotations...');
  try {
    const markdown = state.currentMarkdown;
    const markdownFile = base + '.md';
    const annotationFile = base + '.miru.json';
    const sidecar = await buildAnnotationSidecar(markdownFile, markdown);
    const encoder = new TextEncoder();
    const zipBytes = buildZip([
      { name: markdownFile, data: encoder.encode(markdown) },
      { name: annotationFile, data: encoder.encode(JSON.stringify(sidecar, null, 2) + '\n') },
    ]);
    downloadBlob(new Blob([zipBytes], { type: 'application/zip' }), base + '.miru.zip');
    flashButton(elements.downloadAll);
    showToast('Miru bundle saved');
  } catch (err) {
    console.error('Miru bundle export failed:', err);
    showToast('Bundle export failed');
  }
}
