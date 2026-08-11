/* Explicit sync and silent auto-create: the two flows that write the source
   Markdown itself to membox (reading-state.js only ever writes notes and
   progress). */

import { elements } from '../js/dom.js';
import { state } from '../js/state.js';
import { sanitizeFilename } from '../js/utils.js';
import { showToast, flashButton } from '../js/ui/feedback.js';
import { markAnnotationsSaved } from '../js/annotations/session.js';
import { buildAnnotationSidecar } from '../js/annotations/sidecar.js';
import { session } from './session.js';
import { emitRender } from './events.js';
import { postSync } from './api.js';
import { replaceDocumentID } from './document.js';
import {
  applySavedAnnotationRefs,
  assertAnnotationReplacement,
  currentProgress,
  isRestoring,
  mergePendingAnchors,
  refreshRevision,
  scheduleReadingStateSave,
} from './reading-state.js';

// Notes need an identity to persist. When the user takes a note on pasted
// content that has never been synced, silently create the membox note (only
// while connected) so the notes and reading position can flow through the
// normal auto-save path afterwards.
let autoCreating = false;

export async function autoCreateForAnnotations() {
  if (autoCreating || session.documentID || !session.connected) return;
  const body = state.currentMarkdown || '';
  if (!body.trim() || state.annotations.length === 0) return;
  autoCreating = true;
  const savedSessionId = state.annotationSessionId;
  const savedVersion = state.annotationVersion;
  try {
    const title = (state.docTitle || '').trim() || 'Untitled';
    let annotations;
    try {
      annotations = await buildAnnotationSidecar(sanitizeFilename(title) + '.md', body, currentProgress());
    } catch (err) {
      throw new Error(`Could not prepare session notes: ${err.message}`);
    }
    if (!session.connected || savedSessionId !== state.annotationSessionId) return;
    const result = await postSync({ id: '', title, body, annotations });
    if (savedSessionId !== state.annotationSessionId) return;
    replaceDocumentID(result.id);
    session.loadedMarkdown = body;
    if (result.path) state.droppedFilename = result.path.split(/[\\/]/).pop();
    applySavedAnnotationRefs(result.annotations, savedSessionId);
    assertAnnotationReplacement(result);
    session.loadedRevision = Number(result.revision) || session.loadedRevision;
    if (!session.loadedRevision) await refreshRevision(result.id);
    markAnnotationsSaved(savedSessionId, savedVersion);
    session.annotationsDirty = savedSessionId === state.annotationSessionId && savedVersion < state.annotationVersion;
    if (!session.annotationsDirty) session.annotationsMutated = false;
    emitRender();
    showToast(`Notes auto-saved to membox: ${String(result.id).slice(0, 8)}`);
  } catch (err) {
    session.annotationsDirty = true;
    emitRender();
    console.error('membox: auto-save failed; notes remain in this session', err);
    showToast(`Auto-save failed — notes remain in this session: ${err.message}`);
  } finally {
    autoCreating = false;
    if (session.annotationsDirty && session.connected) {
      if (session.documentID) scheduleReadingStateSave();
      else if (savedSessionId !== state.annotationSessionId) queueMicrotask(() => void autoCreateForAnnotations());
    }
  }
}

export async function syncToMembox() {
  if (session.syncing) return;
  const body = state.currentMarkdown || '';
  if (!body.trim()) {
    showToast('Nothing to sync');
    return;
  }
  // An explicit sync replaces stored notes, so it must never run while the
  // stored notes are still being restored into this tab.
  if (isRestoring()) {
    showToast('Still loading notes — try again in a moment');
    return;
  }

  // A paste/drop over a loaded document is a new note, not an implicit
  // overwrite. Explicitly loaded content keeps its UUID while unchanged.
  const id = session.documentID && body === session.loadedMarkdown ? session.documentID : '';
  const title = (state.docTitle || '').trim() || 'Untitled';
  const savedSessionId = state.annotationSessionId;
  const savedVersion = state.annotationVersion;

  session.syncing = true;
  emitRender();
  try {
    // A sidecar-shaped DTO rides along so the backend can reconcile separate
    // Markdown note documents and refresh reading progress in one request.
    let annotations;
    try {
      const markdownFile = sanitizeFilename(title) + '.md';
      annotations = await buildAnnotationSidecar(markdownFile, body, currentProgress());
      // Explicit sync replaces stored notes, so it must carry every note we
      // know about — including ones that failed to re-anchor this load.
      mergePendingAnchors(annotations);
      // Now that the set is complete, the client authorizes deletions itself,
      // scoped to the state it actually saw (revision).
      annotations.replaceAnnotations = true;
      annotations.revision = session.loadedRevision;
    } catch (err) {
      throw new Error(`Could not prepare session notes: ${err.message}`);
    }
    if (!session.connected || savedSessionId !== state.annotationSessionId) return;
    const result = await postSync({ id, title, body, annotations });
    if (savedSessionId !== state.annotationSessionId) return;
    replaceDocumentID(result.id);
    session.loadedMarkdown = body;
    if (result.path) state.droppedFilename = result.path.split(/[\\/]/).pop();
    applySavedAnnotationRefs(result.annotations, savedSessionId);
    assertAnnotationReplacement(result);
    session.loadedRevision = Number(result.revision) || session.loadedRevision;
    if (!session.loadedRevision) await refreshRevision(result.id);
    markAnnotationsSaved(savedSessionId, savedVersion);
    session.annotationsDirty = savedSessionId === state.annotationSessionId && savedVersion < state.annotationVersion;
    if (!session.annotationsDirty) session.annotationsMutated = false;
    emitRender();
    if (session.annotationsDirty) scheduleReadingStateSave();
    flashButton(elements.downloadAll);
    showToast(result.created ? 'Created in membox with notes' : 'Synced Markdown and notes to membox');
  } catch (err) {
    session.annotationsDirty = session.annotationsDirty || savedVersion > state.annotationSavedVersion;
    emitRender();
    console.error('membox: sync failed; notes remain in this session', err);
    showToast(`Sync failed — notes remain in this session: ${err.message}`);
  } finally {
    session.syncing = false;
    emitRender();
  }
}
