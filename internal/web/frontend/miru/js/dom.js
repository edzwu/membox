/* Miru — cached DOM references.
   One place to see every element the app touches. Import `elements` rather
   than re-querying the DOM from feature modules. */

export const elements = {
  brand: document.querySelector('.brand'),
  topbar: document.querySelector('.topbar'),
  html: document.documentElement,
  body: document.body,
  readingSurface: document.getElementById('reading-surface'),
  article: document.getElementById('article'),
  annotationLayer: document.getElementById('annotation-layer'),
  toc: document.getElementById('toc'),
  tocNav: document.getElementById('toc-nav'),
  tocPane: document.getElementById('toc-pane'),
  tocRail: document.getElementById('toc-rail'),
  tocBackdrop: document.getElementById('toc-backdrop'),
  tocToggle: document.getElementById('toc-toggle'),
  tocPin: document.getElementById('toc-pin'),
  themeToggle: document.getElementById('theme-toggle'),
  themeIcon: document.querySelector('.theme-icon'),
  foldToggle: document.getElementById('fold-toggle'),
  foldIcon: document.querySelector('.fold-icon'),
  empty: document.getElementById('empty'),
  emptyKbd: document.getElementById('empty-kbd'),
  toast: document.getElementById('toast'),
  exportDock: document.getElementById('export-dock'),
  localSaveDock: document.getElementById('local-save-dock'),
  copyAll: document.getElementById('copy-all'),
  downloadAll: document.getElementById('download-all'),
  pngAll: document.getElementById('png-all'),
  htmlAll: document.getElementById('html-all'),
  copyPng: document.getElementById('copy-png'),
};

// Topbar and floating controls that only make sense while reading.
// Download lives on the left (local-save-dock / membox status), not the right export tray.
export const READING_CONTROLS = [
  elements.tocToggle,
  elements.foldToggle,
  elements.copyAll,
  elements.downloadAll,
  elements.pngAll,
  elements.htmlAll,
  elements.copyPng,
  elements.exportDock,
  elements.localSaveDock,
];
