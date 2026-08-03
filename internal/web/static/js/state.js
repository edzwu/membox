/* Miru — shared mutable app state.
   Kept as one object (not separate `let` exports) because ES module bindings
   are read-only from importing modules — mutating a property works, but
   reassigning an imported `let` does not. Feature-local state (e.g. the
   annotation toolbar's current selection) stays inside its own module
   instead of living here; only state genuinely shared across modules
   belongs in this file. */

export const state = {
  // The Markdown currently loaded, and the parsed structure used to power
  // per-section copy/download/fold (see js/markdown/parse.js).
  currentMarkdown: '',
  leadingSource: '',
  sectionSources: [],

  // Document identity, used for filenames and the annotation sidecar title.
  docTitle: '',
  droppedFilename: '',

  // Annotation state lives in memory unless the user explicitly saves a Miru
  // bundle. Visual exports capture the DOM; Markdown itself stays untouched.
  annotations: [],
  noteCounter: 0,
};
