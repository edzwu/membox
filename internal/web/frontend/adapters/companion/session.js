/* Shared adapter session state: a mutable singleton in the same style as
   miru/js/state.js. Only state that crosses module boundaries lives here;
   everything else is module-local to the feature that owns it. */

const initialURLParams = new URLSearchParams(window.location.search);

export const session = {
  // Backend reachability. `connecting` is true until the first status probe
  // resolves so the UI can show an indeterminate state.
  connected: false,
  connecting: true,
  syncing: false,

  // Identity of the document this tab is bound to ('' = local-only paste).
  documentID: initialURLParams.get('id') || '',

  // One-way return target when arriving from a note document: the source
  // page focuses the annotation whose durable ref matches `note`.
  pendingSourceNoteRef: (initialURLParams.get('note') || '').slice(0, 64),
  sourceNoteFocused: false,

  // When opening a full note from a long inline link, `from` is the source
  // document so the note page can offer a clear 「← 原文」 backlink.
  noteSourceFromRef: (initialURLParams.get('from') || '').slice(0, 64),

  // Markdown as last loaded from / synced to the backend. A paste that
  // diverges from this is treated as a new document, not an overwrite.
  loadedMarkdown: '',

  // Newest annotation updated_at (ms) this session has seen. Sent back on
  // replace-saves so the server refuses to delete notes that appeared after
  // this tab loaded (created or restored elsewhere).
  loadedRevision: 0,

  // Wipe-protection flags: a replace-save is only authorized after a real
  // user mutation, and the dirty flag drives every "unsaved" affordance.
  annotationsMutated: false,
  annotationsDirty: false,

  // Reading-state save pipeline (owned by reading-state.js; read by status
  // chrome to show the saving indicator).
  saveInFlight: false,
  savePending: false,
};
