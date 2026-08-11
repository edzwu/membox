/* Reading state and annotations share Miru's sidecar-shaped wire DTO, but not
   a persistence model: progress is DB UI state, while every annotation is an
   individual *-note.md document plus a normalized UUID/anchor relation. The
   source Markdown itself is only written by an explicit sync (see sync.js).

   DATA-LOSS INVARIANT — the replace protocol deletes every stored note that
   is absent from a save whose `replaceAnnotations` is true and whose revision
   is fresh. Such a save must therefore never be built from an incomplete
   in-memory model. Two rules enforce it:
     1. While a restore is in flight (fetch → verify → apply), every save is
        deferred (`restoring` below covers the whole window, including awaits).
     2. `session.loadedRevision` is only assigned AFTER the stored notes have
        been applied to the page; a tab that never completed a restore has
        revision 0 and the server refuses its replace-saves. */

import { state } from '../js/state.js';
import { sanitizeFilename } from '../js/utils.js';
import { markAnnotationsSaved } from '../js/annotations/session.js';
import {
  buildAnnotationSidecar,
  parseAnnotationSidecar,
  restoreAnnotationSidecar,
  verifyAnnotationSource,
} from '../js/annotations/sidecar.js';
import { session } from './session.js';
import { emitRender } from './events.js';
import { fetchAnnotationsRaw, fetchAnnotationsRevision, postAnnotations, postReadStatus } from './api.js';

let restoring = false;
let sidecarSaveTimer = null;
let saveAbortController = null;
// Anchors that failed to re-anchor on load. Kept so the next save merges
// them back into the sidecar instead of silently dropping them.
let pendingAnchors = [];
// Wipe protection: when a sidecar loaded with annotations but none survived
// restoration and the user did not delete any, never overwrite the stored
// notes with an empty set (keep the saved ones, update progress only).
let loadedAnnotationCount = 0;
let pendingProgressY = 0;
// Semantic reading state: opening marks reading, scrolling to the end marks
// finished. Kept local so we only POST on actual transitions.
let lastReportedStatus = '';

export function isRestoring() {
  return restoring;
}

export function currentProgress() {
  return { y: Math.round(window.scrollY), at: new Date().toISOString() };
}

function sidecarFilename() {
  return state.droppedFilename || sanitizeFilename(state.docTitle || 'document') + '.md';
}

// Merge back annotations that could not be re-anchored this load so no save
// (auto or explicit sync) ever destroys notes it merely failed to display.
// Dedupe by excerpt AND note text: several notes can share one passage, and
// keying by excerpt alone would drop every sibling of a restored note.
function pendingAnchorKey(a) {
  return (a.exact || '') + '\u0000' + (a.note || '');
}

export function mergePendingAnchors(sidecar) {
  if (!pendingAnchors.length) return;
  const have = new Set(sidecar.annotations.map((a) => pendingAnchorKey(a)));
  const kept = pendingAnchors.filter((a) => !have.has(pendingAnchorKey(a)));
  if (kept.length) {
    sidecar.annotations = [...sidecar.annotations, ...kept].sort((a, b) => a.start - b.start);
  }
}

export function assertAnnotationReplacement(result) {
  if (result && result.replacement_applied === false) {
    throw new Error('Notes changed in another session; reload before replacing them');
  }
}

export function applySavedAnnotationRefs(saved, sessionId) {
  if (sessionId !== state.annotationSessionId || !Array.isArray(saved)) return;
  const refsByClientID = new Map(saved
    .filter((item) => item && item.clientId && item.ref)
    .map((item) => [item.clientId, item.ref]));
  for (const entry of state.annotations) {
    const ref = refsByClientID.get(entry.clientId);
    if (ref) entry.ref = ref;
  }
}

async function persistReadingState(keepalive) {
  if (!session.connected || !session.documentID || !state.currentMarkdown) return;
  // Never persist mid-restore: the annotation model is incomplete and a
  // replace-save would authorize the server to trash notes this tab simply
  // has not applied yet. Defer; restoreReadingState drains savePending.
  if (restoring) {
    session.savePending = true;
    return;
  }
  if (session.saveInFlight) {
    session.savePending = true;
    return;
  }
  session.saveInFlight = true;
  emitRender();
  saveAbortController = new AbortController();
  const savedSessionId = state.annotationSessionId;
  const savedVersion = state.annotationVersion;
  try {
    const sidecar = await buildAnnotationSidecar(sidecarFilename(), state.currentMarkdown, currentProgress());
    if (!session.connected || savedSessionId !== state.annotationSessionId) return;
    // Only an actual annotation mutation makes the submitted set authoritative
    // for deletions. Scroll/progress saves must never delete note documents.
    sidecar.replaceAnnotations = session.annotationsMutated;
    sidecar.revision = session.loadedRevision;
    mergePendingAnchors(sidecar);
    // Wipe protection: annotations loaded, none survived, and the user never
    // deleted any → keep the stored annotations, only refresh progress.
    if (sidecar.annotations.length === 0 && loadedAnnotationCount > 0 && !session.annotationsMutated) {
      try {
        const resp = await fetchAnnotationsRaw(session.documentID);
        if (resp.ok && resp.status !== 204) {
          const existing = await resp.json();
          if (existing && Array.isArray(existing.annotations) && existing.annotations.length) {
            sidecar.annotations = existing.annotations;
          }
        }
      } catch (err) {
        /* keep whatever we have */
      }
    }
    const result = await postAnnotations(session.documentID, sidecar, {
      keepalive: !!keepalive,
      signal: saveAbortController.signal,
    });
    if (savedSessionId !== state.annotationSessionId) return;
    applySavedAnnotationRefs(result && result.annotations, savedSessionId);
    assertAnnotationReplacement(result);
    if (result && Number.isFinite(Number(result.revision))) {
      session.loadedRevision = Math.max(session.loadedRevision, Number(result.revision));
    }
    markAnnotationsSaved(savedSessionId, savedVersion);
    session.annotationsDirty = savedSessionId === state.annotationSessionId && savedVersion < state.annotationVersion;
    if (!session.annotationsDirty) session.annotationsMutated = false;
    if (session.annotationsDirty) session.savePending = true;
    emitRender();
  } catch (err) {
    session.annotationsDirty = session.annotationsDirty || savedVersion > state.annotationSavedVersion;
    emitRender();
    if (err.name !== 'AbortError') {
      console.error('membox: failed to save reading state; notes remain in this session', err);
    }
  } finally {
    saveAbortController = null;
    session.saveInFlight = false;
    emitRender();
    if (session.savePending && session.connected && !restoring) {
      session.savePending = false;
      void persistReadingState(false);
    }
  }
}

export function scheduleReadingStateSave() {
  if (!session.connected || !session.documentID || restoring) return;
  // Scrolled to the end → mark finished once.
  if (atEndOfDocument()) {
    void setReadStatus('finished');
  } else if (lastReportedStatus === 'finished') {
    // User scrolled back up after finishing — revert to reading.
    void setReadStatus('reading');
  }
  if (sidecarSaveTimer) clearTimeout(sidecarSaveTimer);
  sidecarSaveTimer = setTimeout(() => {
    sidecarSaveTimer = null;
    void persistReadingState(false);
  }, 800);
}

export function flushReadingStateSave() {
  if (sidecarSaveTimer) {
    clearTimeout(sidecarSaveTimer);
    sidecarSaveTimer = null;
  }
  if (!session.connected) return;
  void persistReadingState(true);
}

// Cancel scheduled work and abort any in-flight save (disconnect, unbind).
export function cancelPendingSaves() {
  if (sidecarSaveTimer) {
    clearTimeout(sidecarSaveTimer);
    sidecarSaveTimer = null;
  }
  if (saveAbortController) saveAbortController.abort();
}

// Forget everything restore/persist learned about the current document
// (paste/drop over a loaded document starts a fresh identity).
export function resetReadingSession() {
  cancelPendingSaves();
  pendingAnchors = [];
  loadedAnnotationCount = 0;
  pendingProgressY = 0;
  lastReportedStatus = '';
  session.loadedRevision = 0;
  session.savePending = false;
}

// Re-read the server revision after operations that bypass the annotation
// POST response (explicit sync, auto-create).
export async function refreshRevision(id) {
  try {
    const revision = await fetchAnnotationsRevision(id);
    if (revision) session.loadedRevision = revision;
  } catch (err) {
    /* keep the previous revision */
  }
}

async function setReadStatus(status) {
  if (!session.connected || !session.documentID || !state.currentMarkdown) return;
  if (lastReportedStatus === status) return;
  lastReportedStatus = status;
  try {
    await postReadStatus(session.documentID, status);
  } catch (err) {
    // Non-fatal: reading status is best-effort UI state.
  }
}

function atEndOfDocument() {
  const max = Math.max(0, document.documentElement.scrollHeight - window.innerHeight);
  return max > 0 && window.scrollY >= max - 40;
}

function applyProgressScroll() {
  if (!pendingProgressY) return;
  const max = Math.max(0, document.documentElement.scrollHeight - window.innerHeight);
  // 'auto' overrides the page-wide smooth-scroll so the restore is instant.
  window.scrollTo({ top: Math.min(pendingProgressY, max), behavior: 'auto' });
}

function restoreProgress(progress) {
  pendingProgressY = progress && progress.y > 0 ? progress.y : 0;
  if (!pendingProgressY) return;
  applyProgressScroll();
  // Note cards lay out asynchronously and images arrive later, so re-apply
  // once on the next frame and again when the page finishes loading.
  requestAnimationFrame(applyProgressScroll);
}

export async function restoreReadingState(id, markdown) {
  pendingAnchors = [];
  // Block every save path for the whole restore window (fetch + verify +
  // apply). A replace-save fired before the stored notes are applied submits
  // an incomplete set, and the server trashes the "missing" note documents.
  restoring = true;
  const sessionId = state.annotationSessionId;
  const versionAtStart = state.annotationVersion;
  try {
    const response = await fetchAnnotationsRaw(id);
    // A paste/drop during the fetch replaced the document; abandon quietly.
    if (sessionId !== state.annotationSessionId) return;
    if (response.ok && response.status !== 204) {
      const sidecarText = await response.text();
      if (sidecarText.trim()) {
        let revision = 0;
        try {
          revision = Number(JSON.parse(sidecarText).revision) || 0;
        } catch (err) {
          revision = 0;
        }
        const data = parseAnnotationSidecar(sidecarText);
        try {
          await verifyAnnotationSource(data, markdown);
        } catch (verifyErr) {
          // The page Markdown changed since this on-read DTO was assembled
          // (e.g. a concurrent edit). Re-anchor by text anyway; whatever still
          // fails is kept as a pending anchor so the next save does not
          // discard it.
          console.warn('membox: sidecar source mismatch — re-anchoring by text', verifyErr);
        }
        if (sessionId !== state.annotationSessionId) return;
        restoreAnnotationSidecar(data);
        // The stored notes are on the page; only now may this tab present the
        // server revision as its own on replace-saves.
        session.loadedRevision = Math.max(session.loadedRevision, revision);
        loadedAnnotationCount = Array.isArray(data.annotations) ? data.annotations.length : 0;
        if (Array.isArray(data.unrestored) && data.unrestored.length) {
          pendingAnchors = data.unrestored;
        }
        // An explicit return-from-note target wins over the source document's
        // ordinary saved scroll position.
        if (!session.sourceNoteFocused) restoreProgress(data.progress);
      }
    }
    // Only the state at restore start is durable. Restored notes apply with
    // notify:false (no version bump), so a newer version here means the user
    // mutated during the fetch/verify window — keep that note dirty and let
    // the finally-block below persist it once the model is complete.
    markAnnotationsSaved(sessionId, versionAtStart);
    const mutatedDuringRestore = state.annotationSessionId === sessionId
      && state.annotationVersion > versionAtStart;
    session.annotationsMutated = mutatedDuringRestore;
    session.annotationsDirty = mutatedDuringRestore;
    emitRender();
    // Opening the document marks it reading (auto-transition).
    lastReportedStatus = '';
    void setReadStatus('reading');
  } catch (err) {
    // Fail closed: loadedRevision stays 0, so the server rejects any
    // replace-save from this tab until a successful restore.
    console.warn('membox: could not restore reading state', err);
  } finally {
    restoring = false;
    // Drain work deferred while the restore was in flight.
    if (session.savePending && session.connected) {
      session.savePending = false;
      void persistReadingState(false);
    } else if (session.annotationsDirty) {
      scheduleReadingStateSave();
    }
  }
}

export function initReadingState() {
  window.addEventListener('scroll', scheduleReadingStateSave, { passive: true });
  window.addEventListener('pagehide', flushReadingStateSave);
  window.addEventListener('load', applyProgressScroll);
  document.addEventListener('visibilitychange', () => {
    if (document.visibilityState === 'hidden') flushReadingStateSave();
  });
}
