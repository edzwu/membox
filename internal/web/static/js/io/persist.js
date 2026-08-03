/* Miru — keep the current reading session across refresh, and optionally bind
   it to a real local file the user picks (File System Access API).

   Two layers:
     1. IndexedDB session snapshot (Markdown + annotation sidecar + title)
        — always on, so a refresh does not wipe the page.
     2. A FileSystemFileHandle (also in IDB) for Open / Save / Save As.
        Writing goes straight to the user-chosen path; browsers without the
        API fall back to <input type=file> and the existing download path. */

import { state } from '../state.js';
import { sanitizeFilename } from '../utils.js';
import { showToast, downloadBlob } from '../ui/feedback.js';
import { loadDocument, clearDocument } from '../document.js';
import { buildAnnotationSidecar, parseAnnotationSidecar, restoreAnnotationSidecar } from '../annotations/sidecar.js';
import { readZipEntries, zipBasename } from '../zip.js';

const DB_NAME = 'miru';
const DB_VERSION = 1;
const STORE = 'kv';
const SESSION_KEY = 'session';
const HANDLE_KEY = 'fileHandle';
const PERSIST_DELAY_MS = 400;

let dbPromise = null;
let persistTimer = null;
let restoring = false;
let lastSavedMarkdown = null;

function openDb() {
  if (dbPromise) return dbPromise;
  dbPromise = new Promise((resolve, reject) => {
    if (!window.indexedDB) {
      reject(new Error('IndexedDB unavailable'));
      return;
    }
    const req = indexedDB.open(DB_NAME, DB_VERSION);
    req.onupgradeneeded = () => {
      const db = req.result;
      if (!db.objectStoreNames.contains(STORE)) db.createObjectStore(STORE);
    };
    req.onsuccess = () => resolve(req.result);
    req.onerror = () => reject(req.error || new Error('IndexedDB open failed'));
  });
  return dbPromise;
}

function idbGet(key) {
  return openDb().then((db) => new Promise((resolve, reject) => {
    const tx = db.transaction(STORE, 'readonly');
    const req = tx.objectStore(STORE).get(key);
    req.onsuccess = () => resolve(req.result);
    req.onerror = () => reject(req.error);
  }));
}

function idbSet(key, value) {
  return openDb().then((db) => new Promise((resolve, reject) => {
    const tx = db.transaction(STORE, 'readwrite');
    tx.objectStore(STORE).put(value, key);
    tx.oncomplete = () => resolve();
    tx.onerror = () => reject(tx.error);
  }));
}

function idbDelete(key) {
  return openDb().then((db) => new Promise((resolve, reject) => {
    const tx = db.transaction(STORE, 'readwrite');
    tx.objectStore(STORE).delete(key);
    tx.oncomplete = () => resolve();
    tx.onerror = () => reject(tx.error);
  }));
}

export function supportsFileSystemAccess() {
  return typeof window.showOpenFilePicker === 'function'
    && typeof window.showSaveFilePicker === 'function';
}

function setBoundFile(handle, { clean = false } = {}) {
  state.fileHandle = handle || null;
  state.fileName = handle && handle.name ? handle.name : '';
  if (clean) {
    lastSavedMarkdown = state.currentMarkdown;
    state.fileDirty = false;
  } else if (handle) {
    state.fileDirty = state.currentMarkdown !== lastSavedMarkdown;
  } else {
    lastSavedMarkdown = null;
    state.fileDirty = false;
  }
  updateFileChrome();
}

function updateFileChrome() {
  // Soft signal for the command palette / future status UI. No hard DOM
  // dependency here so tests can run headless without the full shell.
  try {
    document.body.dataset.fileBound = state.fileHandle ? '1' : '0';
    document.body.dataset.fileDirty = state.fileDirty ? '1' : '0';
    if (state.fileName) document.body.dataset.fileName = state.fileName;
    else delete document.body.dataset.fileName;
  } catch (e) {
    // document may be unavailable in non-browser contexts
  }
}

export function markFileDirty() {
  if (!state.fileHandle) {
    state.fileDirty = false;
    updateFileChrome();
    return;
  }
  state.fileDirty = state.currentMarkdown !== lastSavedMarkdown;
  updateFileChrome();
}

async function buildSessionPayload() {
  if (!state.currentMarkdown) return null;
  let sidecar = null;
  if (state.annotations.length) {
    try {
      const base = sanitizeFilename(state.docTitle || state.fileName || 'untitled');
      sidecar = await buildAnnotationSidecar(base + '.md', state.currentMarkdown);
    } catch (err) {
      console.warn('Could not snapshot annotations:', err);
    }
  }
  return {
    markdown: state.currentMarkdown,
    docTitle: state.docTitle || '',
    droppedFilename: state.droppedFilename || '',
    fileName: state.fileName || '',
    fileDirty: !!state.fileDirty,
    sidecar,
    savedAt: new Date().toISOString(),
  };
}

async function writeSessionNow() {
  if (restoring) return;
  try {
    const payload = await buildSessionPayload();
    if (!payload) {
      await idbDelete(SESSION_KEY).catch(() => {});
      return;
    }
    await idbSet(SESSION_KEY, payload);
  } catch (err) {
    console.warn('Session persist failed:', err);
  }
}

export function schedulePersist() {
  markFileDirty();
  if (persistTimer) clearTimeout(persistTimer);
  persistTimer = setTimeout(() => {
    persistTimer = null;
    writeSessionNow();
  }, PERSIST_DELAY_MS);
}

export async function flushPersist() {
  if (persistTimer) {
    clearTimeout(persistTimer);
    persistTimer = null;
  }
  await writeSessionNow();
}

async function rememberHandle(handle) {
  if (!handle) {
    state.fileHandle = null;
    state.fileName = '';
    await idbDelete(HANDLE_KEY).catch(() => {});
    updateFileChrome();
    return;
  }
  setBoundFile(handle, { clean: true });
  try {
    await idbSet(HANDLE_KEY, handle);
  } catch (err) {
    console.warn('Could not remember file handle:', err);
  }
}

async function ensurePermission(handle, mode = 'readwrite') {
  if (!handle || typeof handle.queryPermission !== 'function') return true;
  let status = await handle.queryPermission({ mode });
  if (status === 'granted') return true;
  if (typeof handle.requestPermission === 'function') {
    status = await handle.requestPermission({ mode });
  }
  return status === 'granted';
}

async function readTextFromHandle(handle) {
  const file = await handle.getFile();
  return file.text();
}

async function writeTextToHandle(handle, text) {
  const writable = await handle.createWritable();
  await writable.write(text);
  await writable.close();
}

function isMarkdownName(name) {
  return /\.(md|markdown|mdown|mkd|txt)$/i.test(name || '');
}

function isZipName(name) {
  return /\.zip$/i.test(name || '');
}

function isAnnotationName(name) {
  return /\.miru\.json$/i.test(name || '');
}

async function loadMarkdownText(text, filename) {
  if (!text || !String(text).trim()) throw new Error('File is empty');
  state.droppedFilename = filename || '';
  loadDocument(String(text).replace(/^\uFEFF/, ''));
}

async function loadZipBundle(fileOrBuffer, fallbackName) {
  const entries = await readZipEntries(fileOrBuffer);
  const decoder = new TextDecoder('utf-8');
  const annotationEntries = entries.filter((entry) => /\.miru\.json$/i.test(entry.name));
  if (annotationEntries.length !== 1) throw new Error('Not a Miru document bundle');

  const { parseAnnotationSidecar: parse, verifyAnnotationSource } = await import('../annotations/sidecar.js');
  const annotationData = parse(decoder.decode(annotationEntries[0].data));
  const markdownEntries = entries.filter((entry) => isMarkdownName(entry.name));
  const expectedName = zipBasename(annotationData.markdownFile).toLowerCase();
  const markdownEntry = markdownEntries.find((entry) =>
    expectedName && zipBasename(entry.name).toLowerCase() === expectedName)
    || (markdownEntries.length === 1 ? markdownEntries[0] : null);
  if (!markdownEntry) throw new Error('Bundle Markdown file is missing');

  const markdown = decoder.decode(markdownEntry.data);
  if (!markdown || !markdown.trim()) throw new Error('Bundle Markdown is empty');
  await verifyAnnotationSource(annotationData, markdown);
  state.droppedFilename = zipBasename(markdownEntry.name) || fallbackName || '';
  loadDocument(markdown);
  const restored = restoreAnnotationSidecar(annotationData);
  showToast(restored === annotationData.annotations.length
    ? `Restored ${restored} annotation${restored === 1 ? '' : 's'}`
    : `Restored ${restored} of ${annotationData.annotations.length} annotations`);
}

async function openWithFilePicker() {
  const [handle] = await window.showOpenFilePicker({
    multiple: false,
    types: [
      {
        description: 'Markdown',
        accept: {
          'text/markdown': ['.md', '.markdown', '.mdown', '.mkd'],
          'text/plain': ['.txt'],
        },
      },
      {
        description: 'Miru bundle',
        accept: { 'application/zip': ['.zip'], 'application/json': ['.json'] },
      },
    ],
  });
  return handle;
}

function openWithInputElement() {
  return new Promise((resolve, reject) => {
    const input = document.createElement('input');
    input.type = 'file';
    input.accept = '.md,.markdown,.mdown,.mkd,.txt,.zip,.json,text/markdown,text/plain,application/zip,application/json';
    input.style.display = 'none';
    document.body.appendChild(input);
    const cleanup = () => {
      input.remove();
    };
    input.addEventListener('change', () => {
      const file = input.files && input.files[0];
      cleanup();
      if (!file) {
        reject(new DOMException('The user aborted a request.', 'AbortError'));
        return;
      }
      resolve(file);
    }, { once: true });
    // If the dialog is cancelled, some browsers never fire change. Sweep up.
    window.addEventListener('focus', () => {
      setTimeout(() => {
        if (!input.isConnected) return;
        if (!input.files || !input.files.length) {
          cleanup();
          reject(new DOMException('The user aborted a request.', 'AbortError'));
        }
      }, 400);
    }, { once: true });
    input.click();
  });
}

export async function openLocalFile() {
  try {
    if (supportsFileSystemAccess()) {
      const handle = await openWithFilePicker();
      const name = handle.name || '';
      if (isZipName(name)) {
        const file = await handle.getFile();
        await loadZipBundle(file, name);
        // Bundles are not a single writable Markdown path.
        await rememberHandle(null);
      } else if (isAnnotationName(name)) {
        const text = await readTextFromHandle(handle);
        const data = parseAnnotationSidecar(text);
        if (!state.currentMarkdown) throw new Error('Open the matching Markdown file first');
        const { verifyAnnotationSource } = await import('../annotations/sidecar.js');
        await verifyAnnotationSource(data, state.currentMarkdown);
        loadDocument(state.currentMarkdown);
        const restored = restoreAnnotationSidecar(data);
        showToast(`Restored ${restored} annotation${restored === 1 ? '' : 's'}`);
        schedulePersist();
        return;
      } else {
        const text = await readTextFromHandle(handle);
        await loadMarkdownText(text, name);
        await rememberHandle(handle);
      }
    } else {
      const file = await openWithInputElement();
      const name = file.name || '';
      if (isZipName(name) || file.type === 'application/zip') {
        await loadZipBundle(file, name);
        await rememberHandle(null);
      } else if (isAnnotationName(name)) {
        const data = parseAnnotationSidecar(await file.text());
        if (!state.currentMarkdown) throw new Error('Open the matching Markdown file first');
        const { verifyAnnotationSource } = await import('../annotations/sidecar.js');
        await verifyAnnotationSource(data, state.currentMarkdown);
        loadDocument(state.currentMarkdown);
        const restored = restoreAnnotationSidecar(data);
        showToast(`Restored ${restored} annotation${restored === 1 ? '' : 's'}`);
        schedulePersist();
        return;
      } else {
        await loadMarkdownText(await file.text(), name);
        await rememberHandle(null);
      }
    }
    schedulePersist();
    showToast(state.fileName ? `Opened ${state.fileName}` : 'Opened file');
  } catch (err) {
    if (err && err.name === 'AbortError') return;
    console.error('Open failed:', err);
    showToast(err && err.message ? err.message : 'Failed to open file');
  }
}

async function saveAsWithPicker(text, suggestedName) {
  const handle = await window.showSaveFilePicker({
    suggestedName,
    types: [{
      description: 'Markdown',
      accept: { 'text/markdown': ['.md'], 'text/plain': ['.txt'] },
    }],
  });
  await writeTextToHandle(handle, text);
  await rememberHandle(handle);
  return handle;
}

export async function saveLocalFileAs() {
  if (!state.currentMarkdown) {
    showToast('Nothing to save');
    return;
  }
  const suggested = sanitizeFilename(state.docTitle || state.fileName || 'untitled') + '.md';
  try {
    if (supportsFileSystemAccess()) {
      await saveAsWithPicker(state.currentMarkdown, suggested);
      lastSavedMarkdown = state.currentMarkdown;
      state.fileDirty = false;
      updateFileChrome();
      await flushPersist();
      showToast(`Saved ${state.fileName}`);
    } else {
      downloadBlob(new Blob([state.currentMarkdown], { type: 'text/markdown' }), suggested);
      showToast('Markdown downloaded');
    }
  } catch (err) {
    if (err && err.name === 'AbortError') return;
    console.error('Save As failed:', err);
    showToast(err && err.message ? err.message : 'Save failed');
  }
}

export async function saveLocalFile() {
  if (!state.currentMarkdown) {
    showToast('Nothing to save');
    return;
  }

  // No bound file yet — Save becomes Save As.
  if (!state.fileHandle || !supportsFileSystemAccess()) {
    if (!state.fileHandle && supportsFileSystemAccess()) {
      await saveLocalFileAs();
      return;
    }
    // Fallback browsers: keep the familiar download behavior.
    if (!supportsFileSystemAccess()) {
      await saveLocalFileAs();
      return;
    }
  }

  try {
    const ok = await ensurePermission(state.fileHandle, 'readwrite');
    if (!ok) {
      showToast('Permission denied — try Save As');
      return;
    }
    await writeTextToHandle(state.fileHandle, state.currentMarkdown);
    lastSavedMarkdown = state.currentMarkdown;
    state.fileDirty = false;
    updateFileChrome();
    await flushPersist();
    showToast(`Saved ${state.fileName || 'file'}`);
  } catch (err) {
    if (err && err.name === 'AbortError') return;
    console.error('Save failed:', err);
    // Stale handle (file moved/deleted) — offer a fresh picker.
    showToast('Could not write file — choose a new location');
    await saveLocalFileAs();
  }
}

export async function clearBoundFile() {
  await rememberHandle(null);
  lastSavedMarkdown = null;
  state.fileDirty = false;
  updateFileChrome();
}

export async function clearSessionAndDocument() {
  await clearBoundFile();
  try {
    await idbDelete(SESSION_KEY);
  } catch (e) {
    // ignore
  }
  clearDocument();
}

export async function restoreSession() {
  restoring = true;
  try {
    let session = null;
    let handle = null;
    try {
      session = await idbGet(SESSION_KEY);
      handle = await idbGet(HANDLE_KEY);
    } catch (err) {
      // Private mode / disabled IDB — just start empty.
      return false;
    }

    if (handle && handle.name) {
      // Re-bind quietly. Reading may require a user gesture for permission,
      // so we restore the last in-browser snapshot rather than re-read disk.
      state.fileHandle = handle;
      state.fileName = handle.name;
    }

    if (!session || !session.markdown || !String(session.markdown).trim()) {
      updateFileChrome();
      return false;
    }

    state.droppedFilename = session.droppedFilename || session.fileName || '';
    loadDocument(session.markdown);

    if (session.docTitle) {
      state.docTitle = session.docTitle;
      const title = document.querySelector('.doc-title');
      if (title) title.textContent = state.docTitle;
    }

    if (session.sidecar && session.sidecar.annotations && session.sidecar.annotations.length) {
      try {
        const restored = restoreAnnotationSidecar(session.sidecar);
        if (restored < session.sidecar.annotations.length) {
          console.warn(`Restored ${restored}/${session.sidecar.annotations.length} annotations`);
        }
      } catch (err) {
        console.warn('Could not restore session annotations:', err);
      }
    }

    lastSavedMarkdown = session.fileDirty ? null : session.markdown;
    // If we had a bound file and the snapshot was dirty, keep dirty=true so
    // Save is obviously needed. If clean, trust the snapshot.
    if (state.fileHandle) {
      state.fileDirty = session.fileDirty !== false && session.markdown !== lastSavedMarkdown
        ? true
        : !!session.fileDirty;
      if (!state.fileDirty) lastSavedMarkdown = session.markdown;
    } else {
      state.fileDirty = false;
    }
    updateFileChrome();
    return true;
  } finally {
    restoring = false;
    // Re-write so a half-restored sidecar shape is normalized.
    schedulePersist();
  }
}

export function initPersist() {
  updateFileChrome();
  window.addEventListener('pagehide', () => {
    if (persistTimer) {
      clearTimeout(persistTimer);
      persistTimer = null;
    }
    // Best-effort synchronous kick; browsers may still drop async IDB work
    // during unload, but the debounced write usually already landed.
    writeSessionNow();
  });
  document.addEventListener('visibilitychange', () => {
    if (document.visibilityState === 'hidden') flushPersist();
  });
}
