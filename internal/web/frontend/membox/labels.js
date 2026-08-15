/* Display labels shared with the TUI tree for generated PDF→MD files.
   Mirrors internal/interfaces/tui/model_view.go convertedTreeLabel. */

const CHAPTER_RE = /^-chapter-(\d+)$/;
const PART_RE = /^-part-([a-z]+)$/;
const PART_NAMES = {
  introduction: 'intro',
  prologue: 'prologue',
  preface: 'preface',
  epilogue: 'epilogue',
  afterword: 'afterword',
  appendix: 'appendix',
  conclusion: 'conclusion',
};

function basename(path) {
  const value = String(path || '').trim();
  if (!value) return '';
  const parts = value.split(/[\\/]/);
  return parts[parts.length - 1] || value;
}

function compactConversionSuffix(rest) {
  const chapter = rest.match(CHAPTER_RE);
  if (chapter) return ` ch.${Number(chapter[1])}`;
  const part = rest.match(PART_RE);
  if (part) {
    const key = part[1];
    return ` ${PART_NAMES[key] || key}`;
  }
  return ` ${rest.replace(/^-/, '')}`;
}

// Returns a short label for generated conversion filenames, or '' if the
// path is not a conversion product. Example:
//   just-for-fun-pdf-<32hex>-chapter-012.md → "just-for-fun ch.12"
export function convertedDisplayLabel(filename) {
  const base = basename(filename).toLowerCase();
  if (!base.endsWith('.md')) return '';
  const stem = base.slice(0, -3);
  const marker = stem.lastIndexOf('-pdf-');
  if (marker < 0) return '';
  const prefix = stem.slice(0, marker).replace(/-+$/, '');
  let rest = stem.slice(marker + '-pdf-'.length);
  if (rest.length < 32) return '';
  rest = rest.slice(32); // drop 32-hex identity
  if (!rest) return prefix;
  return prefix + compactConversionSuffix(rest);
}

// Prefer the TUI-style conversion label; otherwise catalog title; else stem.
export function documentDisplayLabel({ filename = '', catalogTitle = '' } = {}) {
  const short = convertedDisplayLabel(filename);
  if (short) return short;
  const title = String(catalogTitle || '').trim();
  if (title) {
    // Catalog title is often the raw conversion stem — still shorten it.
    const fromTitle = convertedDisplayLabel(`${title}.md`);
    if (fromTitle) return fromTitle;
    return title;
  }
  const base = basename(filename);
  if (base) return base.replace(/\.[^.]+$/, '');
  return 'Untitled';
}
