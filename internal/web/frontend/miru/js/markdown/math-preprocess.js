/* Miru — canonicalize explicit math delimiters before markdown-it-texmath.
 *
 * Rendering must not guess whether ordinary prose in (...) or [...] is math.
 * HTML ingestion is responsible for emitting explicit LaTeX delimiters; this
 * module only translates explicit $/$$ forms into the bracket dialect used by
 * markdown-it-texmath. Code spans and fenced blocks are copied byte-for-byte.
 */

export function preprocessMath(source) {
  let output = '';
  let index = 0;

  while (index < source.length) {
    const fence = readFence(source, index);
    if (fence) {
      output += fence.text;
      index = fence.end;
      continue;
    }

    if (source[index] === '`') {
      const code = readInlineCode(source, index);
      output += code.text;
      index = code.end;
      continue;
    }

    if (source[index] === '\\') {
      // Existing \(...\), \[...\], escaped dollars, and ordinary Markdown
      // escapes are already canonical. Copy the escaped character verbatim.
      output += source.slice(index, index + 2);
      index += Math.min(2, source.length - index);
      continue;
    }

    // A Markdown link/image destination — `](url)` — must never be scanned
    // for math delimiters: URL query strings routinely contain `$` (e.g.
    // Substack's /image/fetch/$s_!xxx!,...), and pairing two of them across a
    // single line (as in [![img](a$…)](b$…)) corrupted the destination.
    if (source[index] === ']' && source[index + 1] === '(') {
      const close = findClosingParen(source, index + 1);
      if (close >= 0) {
        output += source.slice(index, close + 1);
        index = close + 1;
        continue;
      }
    }

    if (source[index] === '$') {
      const math = readDollarMath(source, index);
      if (math) {
        output += math.text;
        index = math.end;
        continue;
      }
    }

    output += source[index];
    index++;
  }

  return output;
}

function readDollarMath(source, start) {
  const display = source[start + 1] === '$';
  const width = display ? 2 : 1;
  const contentStart = start + width;
  const close = findUnescapedDelimiter(source, contentStart, '$'.repeat(width), !display);
  if (close < 0 || close === contentStart) return null;

  const content = source.slice(contentStart, close);
  if (!display && /\r?\n/.test(content)) return null;

  if (display && isBlankSeparated(source, start, close + width)) {
    return { text: `\\[${content}\\]`, end: close + width };
  }
  return {
    text: `\\(${display ? content.replace(/\s+/g, ' ').trim() : content}\\)`,
    end: close + width,
  };
}

function findClosingParen(source, open) {
  // `open` points at '(' — copy the balanced destination verbatim so URL
  // query strings can never be mistaken for math ($...$ inside links/images).
  let depth = 1;
  for (let i = open + 1; i < source.length; i++) {
    const ch = source[i];
    if (ch === '\\') {
      i++;
      continue;
    }
    if (ch === '(') depth++;
    else if (ch === ')') {
      depth--;
      if (depth === 0) return i;
    }
  }
  return -1;
}

function findUnescapedDelimiter(source, start, delimiter, stopAtNewline) {
  for (let index = start; index <= source.length - delimiter.length; index++) {
    if (stopAtNewline && source[index] === '\n') return -1;
    if (source[index] === '\\') {
      index++;
      continue;
    }
    if (source.startsWith(delimiter, index)) return index;
  }
  return -1;
}

function readInlineCode(source, start) {
  const width = runLength(source, start, '`');
  const delimiter = '`'.repeat(width);
  const close = source.indexOf(delimiter, start + width);
  if (close < 0) return { text: delimiter, end: start + width };
  return {
    text: source.slice(start, close + width),
    end: close + width,
  };
}

function readFence(source, start) {
  if (!isLineStart(source, start)) return null;
  let markerStart = start;
  while (markerStart < source.length && markerStart - start < 3 && source[markerStart] === ' ') {
    markerStart++;
  }
  const marker = source[markerStart];
  if (marker !== '`' && marker !== '~') return null;
  const width = runLength(source, markerStart, marker);
  if (width < 3) return null;

  const openingEnd = lineEnd(source, markerStart);
  let cursor = openingEnd;
  while (cursor < source.length) {
    const nextLine = cursor < source.length && source[cursor] === '\n' ? cursor + 1 : cursor;
    let candidate = nextLine;
    while (candidate < source.length && candidate - nextLine < 3 && source[candidate] === ' ') {
      candidate++;
    }
    const closingWidth = runLength(source, candidate, marker);
    const end = lineEnd(source, candidate);
    if (closingWidth >= width && source.slice(candidate + closingWidth, end).trim() === '') {
      return { text: source.slice(start, end), end };
    }
    cursor = lineEnd(source, nextLine);
    if (cursor === nextLine) break;
  }
  return { text: source.slice(start), end: source.length };
}

function runLength(source, start, character) {
  let end = start;
  while (source[end] === character) end++;
  return end - start;
}

function lineEnd(source, start) {
  const newline = source.indexOf('\n', start);
  return newline < 0 ? source.length : newline;
}

function isLineStart(source, index) {
  return index === 0 || source[index - 1] === '\n';
}

function isBlankSeparated(source, start, end) {
  const before = source.slice(0, start);
  const after = source.slice(end);
  const cleanStart = before.length === 0 || /\n[ \t]*\n[ \t]*$/.test(before);
  const cleanEnd = after.length === 0 || /^[ \t]*\n[ \t]*\n/.test(after);
  return cleanStart && cleanEnd;
}
