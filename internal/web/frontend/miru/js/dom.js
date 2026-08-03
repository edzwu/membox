/* Miru — cached DOM references.
   One place to see every element the app touches. Import `elements` rather
   than re-querying the DOM from feature modules. */

export const elements = {
  brand: document.querySelector('.brand'),
  html: document.documentElement,
  body: document.body,
  article: document.getElementById('article'),
  toc: document.getElementById('toc'),
  tocNav: document.getElementById('toc-nav'),
  tocBackdrop: document.getElementById('toc-backdrop'),
  tocToggle: document.getElementById('toc-toggle'),
  tocPin: document.getElementById('toc-pin'),
  newPaste: document.getElementById('new-paste'),
  themeToggle: document.getElementById('theme-toggle'),
  themeIcon: document.querySelector('.theme-icon'),
  foldToggle: document.getElementById('fold-toggle'),
  foldIcon: document.querySelector('.fold-icon'),
  empty: document.getElementById('empty'),
  emptyKbd: document.getElementById('empty-kbd'),
  toast: document.getElementById('toast'),
  copyAll: document.getElementById('copy-all'),
  downloadAll: document.getElementById('download-all'),
  pngAll: document.getElementById('png-all'),
  htmlAll: document.getElementById('html-all'),
  copyPng: document.getElementById('copy-png'),
};

// Topbar and floating controls that only make sense while reading.
export const READING_CONTROLS = [
  elements.newPaste,
  elements.tocToggle,
  elements.foldToggle,
  elements.copyAll,
  elements.downloadAll,
  elements.pngAll,
  elements.htmlAll,
  elements.copyPng,
];
