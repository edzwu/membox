/* CJK-friendly emphasis delimiter handling.
 *
 * CommonMark's punctuation flanking rule rejects constructs such as
 * `**有状态（stateful）**的`. Keep the vendored markdown-it byte-for-byte
 * upstream and install this small, testable plugin instead of patching its
 * minified isPunctChar implementation. The behavior follows the CJK-friendly
 * CommonMark proposal implemented by markdown-it-cjk-friendly.
 * https://github.com/tats-u/markdown-cjk-friendly
 */
export function configureTechnicalMarkdown(md) {
  md.use(cjkFriendlyEmphasis);
  // Preserve identifiers such as (c), (r), --, and +-. Smart quotes remain
  // enabled when the caller opts into typographer mode.
  md.core.ruler.disable(['replacements']);
  return md;
}

export function cjkFriendlyEmphasis(md) {
  const BaseState = md.inline.State;

  class CjkFriendlyState extends BaseState {
    scanDelims(start, canSplitWord) {
      const result = super.scanDelims(start, canSplitWord);
      if (!canSplitWord) return result; // underscores keep CommonMark behavior

      const markerLength = delimiterLength(this.src, start);
      const previous = codePointBefore(this.src, start);
      const next = this.src.codePointAt(start + markerLength) ?? 32;
      if (!isCjk(previous) && !isCjk(next)) return result;

      // CJK scripts do not use spaces as word boundaries. Permit an asterisk
      // run to open/close next to CJK text while retaining whitespace guards.
      return {
        ...result,
        can_open: result.can_open || !isWhitespace(next),
        can_close: result.can_close || !isWhitespace(previous),
      };
    }
  }

  md.inline.State = CjkFriendlyState;
}

function delimiterLength(source, start) {
  const marker = source.charCodeAt(start);
  let end = start;
  while (end < source.length && source.charCodeAt(end) === marker) end++;
  return end - start;
}

function codePointBefore(source, position) {
  if (position <= 0) return 32;
  const low = source.charCodeAt(position - 1);
  if (low < 0xdc00 || low > 0xdfff || position < 2) return low;
  return source.codePointAt(position - 2) ?? low;
}

function isWhitespace(codePoint) {
  return /^\s$/u.test(String.fromCodePoint(codePoint));
}

function isCjk(codePoint) {
  const char = String.fromCodePoint(codePoint);
  return /[\p{Script=Han}\p{Script=Hiragana}\p{Script=Katakana}\p{Script=Hangul}]/u.test(char);
}
