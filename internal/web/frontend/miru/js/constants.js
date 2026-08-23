/* Miru — shared constants.
   Pure data: no DOM access, no state. Safe to import from anywhere. */

export const STORAGE_KEY = 'miru-theme';

export const ANNOTATION_FORMAT = 'miru-annotations';
// v2 wire format retained; underline/strikethrough fields stay false.
export const ANNOTATION_VERSION = 2;
export const ANNOTATION_LEGACY_VERSIONS = [1];
export const ANNOTATION_CONTEXT_LENGTH = 32;
export const ANNOTATION_TEXT_EXCLUDE = '.doc-title, .article-meta, .annot-note, .annot-note-num, .snippet-titlebar, button, .code-lang';

export const NOTE_RAIL_WIDTH = 220;
export const NOTE_RAIL_GAP = 24;
export const NOTE_RAIL_OUTER_GUTTER = 16;
export const NOTE_RAIL_STACK_GAP = 12;

// Ray's default "M" frame uses 64px. Keep the same breathing room around
// both section snapshots and full-document PNGs.
export const PNG_EXPORT_PADDING = 64;

// Overrides for the static PNG snapshot: expand all sections, hide fold
// indicators and every interactive affordance (it is a flat image).
export const EXPORT_STATIC_CSS = `
    .fold-heading { cursor: default !important; }
    .fold-heading::before { display: none !important; }
    .section-copy, .section-download, .lead-copy, .code-copy, .code-lang { display: none !important; }
    .article pre, .fold-body pre { max-width: 100% !important; }
    .article.has-note-rail > .fold-section > .fold-heading,
    .article.has-note-rail > .fold-section > .fold-body,
    .article.has-note-rail > :not(.fold-section):not(pre) { margin-left: 0 !important; margin-right: 0 !important; }
    .fold-body { display: block !important; }
    .fold-section.is-collapsed .fold-body { display: block !important; }
  `;

// Overrides for the interactive site (ZIP) export: folding stays live via a
// small inline script, so we only strip the in-app action buttons.
export const EXPORT_INTERACTIVE_CSS = `
    .section-copy, .section-download, .lead-copy, .code-copy, .code-lang { display: none !important; }
  `;

export const EMBED_FONTS = [
  { family: 'Commit Mono', weight: 400, style: 'normal', file: 'vendor/fonts/CommitMono-400-Regular.woff2' },
  { family: 'Commit Mono', weight: 400, style: 'italic', file: 'vendor/fonts/CommitMono-400-Italic.woff2' },
  { family: 'KaTeX_AMS', weight: 400, style: 'normal', file: 'vendor/fonts/KaTeX_AMS-Regular.woff2' },
  { family: 'KaTeX_Caligraphic', weight: 700, style: 'normal', file: 'vendor/fonts/KaTeX_Caligraphic-Bold.woff2' },
  { family: 'KaTeX_Caligraphic', weight: 400, style: 'normal', file: 'vendor/fonts/KaTeX_Caligraphic-Regular.woff2' },
  { family: 'KaTeX_Fraktur', weight: 700, style: 'normal', file: 'vendor/fonts/KaTeX_Fraktur-Bold.woff2' },
  { family: 'KaTeX_Fraktur', weight: 400, style: 'normal', file: 'vendor/fonts/KaTeX_Fraktur-Regular.woff2' },
  { family: 'KaTeX_Main', weight: 700, style: 'normal', file: 'vendor/fonts/KaTeX_Main-Bold.woff2' },
  { family: 'KaTeX_Main', weight: 700, style: 'italic', file: 'vendor/fonts/KaTeX_Main-BoldItalic.woff2' },
  { family: 'KaTeX_Main', weight: 400, style: 'italic', file: 'vendor/fonts/KaTeX_Main-Italic.woff2' },
  { family: 'KaTeX_Main', weight: 400, style: 'normal', file: 'vendor/fonts/KaTeX_Main-Regular.woff2' },
  { family: 'KaTeX_Math', weight: 700, style: 'italic', file: 'vendor/fonts/KaTeX_Math-BoldItalic.woff2' },
  { family: 'KaTeX_Math', weight: 400, style: 'italic', file: 'vendor/fonts/KaTeX_Math-Italic.woff2' },
  { family: 'KaTeX_SansSerif', weight: 700, style: 'normal', file: 'vendor/fonts/KaTeX_SansSerif-Bold.woff2' },
  { family: 'KaTeX_SansSerif', weight: 400, style: 'italic', file: 'vendor/fonts/KaTeX_SansSerif-Italic.woff2' },
  { family: 'KaTeX_SansSerif', weight: 400, style: 'normal', file: 'vendor/fonts/KaTeX_SansSerif-Regular.woff2' },
  { family: 'KaTeX_Script', weight: 400, style: 'normal', file: 'vendor/fonts/KaTeX_Script-Regular.woff2' },
  { family: 'KaTeX_Size1', weight: 400, style: 'normal', file: 'vendor/fonts/KaTeX_Size1-Regular.woff2' },
  { family: 'KaTeX_Size2', weight: 400, style: 'normal', file: 'vendor/fonts/KaTeX_Size2-Regular.woff2' },
  { family: 'KaTeX_Size3', weight: 400, style: 'normal', file: 'vendor/fonts/KaTeX_Size3-Regular.woff2' },
  { family: 'KaTeX_Size4', weight: 400, style: 'normal', file: 'vendor/fonts/KaTeX_Size4-Regular.woff2' },
  { family: 'KaTeX_Typewriter', weight: 400, style: 'normal', file: 'vendor/fonts/KaTeX_Typewriter-Regular.woff2' },
];
