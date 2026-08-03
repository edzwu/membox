/* Miru — normalize bare math delimiters before markdown-it-texmath runs.
   Pure text-to-text transform; no DOM. Kept isolated because the delimiter
   detection heuristics are the densest, most independently testable part of
   the rendering pipeline (see README "Known limitations" for edge cases). */

export function preprocessMath(text) {
  // Convert bare [ ... ] and ( ... ) math delimiters — commonly produced by
  // copy-paste from PDFs or chat apps — into standard LaTeX delimiters that
  // markdown-it-texmath recognizes. Only convert when the content looks like
  // LaTeX math (contains a backslash command, subscript, or superscript), to
  // avoid turning arrays or citations into math.
  //
  // Also normalize accidental separator lines (e.g. ==== or ----) inside math
  // back into single = or -, which can happen when copy-pasting from docs that
  // use ASCII art for equals signs or fraction bars.
  //
  // Additionally, code blocks and inline code are protected so that the math
  // rules do not touch them. During protection, escaped parens used as array
  // indices (e.g. Q\(q_block\)) are normalized to square brackets (Q[q_block]).
  const hasMathMarker = (s) => /\\[a-zA-Z]/.test(s) || /[a-zA-Z][_^]/.test(s);
  const looksLikeParenMath = (s) => {
    const value = s.trim();
    if (!value || value.length > 80) return false;
    if (/\\[a-zA-Z]/.test(value) || /[a-zA-Z][_^]/.test(value)) return true;
    if (/^[a-zA-Z]$/.test(value)) return true;
    if (/^[a-zA-Z][a-zA-Z0-9_]*(\s*,\s*[a-zA-Z][a-zA-Z0-9_]*)+$/.test(value)) return true;
    if (/^[a-zA-Z0-9_{}\\\s,+\-*/=.^<>]+$/.test(value) && /[=^_]|\\[a-zA-Z]/.test(value) && /[a-zA-Z]/.test(value)) return true;
    return false;
  };
  const protectedBlocks = [];
  const inlineLinks = [];
  let counter = 0;

  // markdown-it-texmath's "brackets" delimiter set implements \[...\] as a
  // *block* rule: it only fires when the block parser is looking for a new
  // block (broadly, right after a blank line, or at a boundary a block like a
  // heading always closes). A display formula glued directly to surrounding
  // text — e.g. a $$...$$ block pasted with no blank line around it, very
  // common inside an LLM-generated bullet point — instead gets swallowed as a
  // paragraph's lazy continuation line. By the time it reaches the inline
  // parser, CommonMark's backslash-escape rule has already eaten the `\[`/`\]`
  // markers (backslash + punctuation → literal punctuation), leaving bare
  // `[`/`]` and the raw LaTeX source visible as plain text. A blank line on
  // both sides is a sufficient (if not strictly necessary) condition for the
  // block rule to fire, so require it before trusting \[...\]; otherwise fall
  // back to inline math (`\(...\)`, handled by an *inline* rule that works
  // anywhere inside a paragraph — see the O(n^2)-in-a-table-cell case), which
  // always renders, just without centered "display" styling.
  function isBlankSeparated(source, matchStart, matchEnd) {
    const before = source.slice(0, matchStart);
    const after = source.slice(matchEnd);
    const cleanStart = before.length === 0 || /\n[ \t]*\n[ \t]*$/.test(before);
    const cleanEnd = after.length === 0 || /^[ \t]*\n[ \t]*\n/.test(after);
    return cleanStart && cleanEnd;
  }

  function wrapDisplayMath(content, blockSafe) {
    if (blockSafe) return '\\[' + content + '\\]';
    return '\\(' + content.replace(/\s+/g, ' ').trim() + '\\)';
  }

  function normalize(content) {
    return content
      // Copying formulas from rich text can turn an equals sign or fraction
      // bar into a standalone ASCII ruler. Normalize it back into an operator
      // line before markdown-it sees it as a Setext heading / horizontal rule.
      .replace(/\n[ \t]*={3,}[ \t]*\n/g, '\n=\n')
      .replace(/\n[ \t]*-{3,}[ \t]*\n/g, '\n-\n')
      .replace(/={3,}/g, '=')
      .replace(/-{3,}/g, '-')
      .replace(/\n{3,}/g, '\n\n');
  }

  function protect(content, isDisplay, blockSafe) {
    const placeholder = `__MIRU_MATH_${counter++}__`;
    protectedBlocks.push({ placeholder, content: normalize(content), isDisplay, blockSafe });
    return placeholder;
  }

  // 0. Protect code blocks and inline code so math rules do not touch them.
  //    Also normalize escaped parens like Q\(q_block\) → Q[q_block] inside code.
  const codeBlocks = [];
  function protectCodeBlock(match) {
    const placeholder = `__MIRU_CODE_${codeBlocks.length}__`;
    const normalized = match.replace(/([A-Z])\\\(([a-zA-Z_][a-zA-Z0-9_]*)\\\)/g, '$1[$2]');
    codeBlocks.push(normalized);
    return placeholder;
  }

  text = text.replace(/```[a-zA-Z0-9]*\n[\s\S]*?\n```/g, protectCodeBlock);
  text = text.replace(/`[^`]+`/g, protectCodeBlock);

  // 0.5 Protect inline markdown links [text](url) so the URL parentheses are
  //     not mistaken for math delimiters. Whole links are restored after math
  //     processing so markdown-it can parse them normally. We replace one link
  //     at a time and restart the search so nested links (e.g. a linked image
  //     [![alt](img)](url)) are handled from the inside out.
  function protectInlineLink(match) {
    const placeholder = `__MIRU_LINK_${inlineLinks.length}__`;
    inlineLinks.push(match);
    return placeholder;
  }
  const inlineLinkPattern = /\[[^\[\]\n]*\]\([^()\s\n]*\)/;
  let inlineLinkMatch;
  while ((inlineLinkMatch = text.match(inlineLinkPattern)) !== null) {
    text = text.replace(inlineLinkMatch[0], protectInlineLink(inlineLinkMatch[0]));
  }

  // 0.75 Protect existing math blocks ($$...$$ and inline $...$) so the
  //      paren conversion below does not rewrite their inner parentheses.
  //      Display blocks are restored as \[...\] afterwards when cleanly
  //      block-positioned (texmath renders those natively as display math;
  //      KaTeX auto-render treated $$...$$ here as inline), or as \(...\)
  //      otherwise (see isBlankSeparated/wrapDisplayMath above). Inline
  //      $...$ is restored as \(...\) so that texmath's *inline* rule wraps
  //      it in an <eq> tag before markdown-it's CommonMark inline parser
  //      runs. This matters because that parser's backslash-escape rule
  //      turns `\$` inside a raw `$...$` span into a bare `$`, which then
  //      mis-pairs delimiters and makes KaTeX auto-render swallow whole
  //      table rows / paragraphs as broken display math.
  const mathBlocks = [];
  function protectMathBlock(content, isDisplay, blockSafe) {
    const placeholder = `__MIRU_MATHBLOCK_${mathBlocks.length}__`;
    mathBlocks.push({ placeholder, content, isDisplay, blockSafe });
    return placeholder;
  }
  // Single left-to-right, escape-aware scan that tokenizes math in one pass.
  // At each unescaped $, a following $ opens display math (closed by the
  // next unescaped $$, may span lines); otherwise $ opens inline math
  // (closed by the next unescaped $ before end of line). Two independent
  // regex passes mis-paired delimiters: the trailing \$$ of inline math
  // like $\$$ faked a $$ display opener, which swallowed everything up to
  // the next real display block in the document (tables included) into one
  // giant "math" span, and the resulting nested placeholders then leaked
  // __MIRU_MATHBLOCK_n__ into the rendered output.
  {
    const src = text;
    let scanned = '';
    let i = 0;
    while (i < src.length) {
      if (src[i] === '\\') {
        // Escaped char (\$, \\, etc.): copy both chars, never a delimiter.
        scanned += src.slice(i, i + 2);
        i += 2;
        continue;
      }
      if (src[i] === '$' && src[i + 1] === '$') {
        let j = i + 2; // find next unescaped $$
        while (j < src.length) {
          if (src[j] === '\\') { j += 2; continue; }
          if (src[j] === '$' && src[j + 1] === '$') break;
          j++;
        }
        if (j < src.length) {
          scanned += protectMathBlock(src.slice(i + 2, j), true, isBlankSeparated(src, i, j + 2));
          i = j + 2;
        } else {
          scanned += '$$'; // unclosed: leave literal, rescan as inline below
          i += 2;
        }
        continue;
      }
      if (src[i] === '$') {
        let j = i + 1; // find next unescaped $ on the same line
        while (j < src.length && src[j] !== '\n') {
          if (src[j] === '\\') { j += 2; continue; }
          if (src[j] === '$') break;
          j++;
        }
        if (j < src.length && src[j] === '$' && j > i + 1) {
          scanned += protectMathBlock(src.slice(i + 1, j), false);
          i = j + 1;
        } else {
          scanned += '$'; // unclosed or empty: literal dollar sign
          i += 1;
        }
        continue;
      }
      scanned += src[i];
      i++;
    }
    text = scanned;
  }

  // 1. Protect [ ... ] blocks first so inner ( ... ) are not double-converted.
  text = text.replace(/^[ \t]*\[\s*\n([\s\S]*?)\n[ \t]*\][ \t]*$/gm, (match, content, offset, source) => {
    return hasMathMarker(content) ? protect(content, true, isBlankSeparated(source, offset, offset + match.length)) : match;
  });
  text = text.replace(/^[ \t]*\[([\s\S]*?)\][ \t]*$/gm, (match, content, offset, source) => {
    return hasMathMarker(content) ? protect(content, true, isBlankSeparated(source, offset, offset + match.length)) : match;
  });
  text = text.replace(/\[([^\[\]\n]*(?:\\[a-zA-Z]|[a-zA-Z][_^])[^\[\]\n]*)\]/g, (match, content) => {
    return hasMathMarker(content) ? protect(content, false) : match;
  });

  // 2. Convert standalone ( ... ) that look like math.
  // Skip parentheses preceded by a letter or backslash (\left, \right, f(x), etc.).
  // This also protects short variables like (r) from markdown-it typographer,
  // which otherwise turns them into symbols such as ®.
  text = text.replace(/(?<![a-zA-Z\\])\(([^()\n]{1,80})\)/g, (match, content) => {
    return looksLikeParenMath(content) ? '\\(' + content + '\\)' : match;
  });

  // 3. Restore protected blocks with proper LaTeX delimiters. Use a function
  //    replacement so `$` characters in the LaTeX (e.g. `\$`) are not
  //    interpreted as String.replace `$` patterns.
  protectedBlocks.forEach((block) => {
    const replacement = block.isDisplay ? wrapDisplayMath(block.content, block.blockSafe) : '\\(' + block.content + '\\)';
    text = text.replace(block.placeholder, () => replacement);
  });

  // 4. Restore protected inline markdown links. Restore from the outside in
  //    (reverse order) so nested placeholders inside earlier protected links
  //    are resolved correctly.
  for (let i = inlineLinks.length - 1; i >= 0; i--) {
    const link = inlineLinks[i];
    text = text.replace(`__MIRU_LINK_${i}__`, () => link);
  }

  // 5. Restore code blocks and inline code.
  codeBlocks.forEach((content, i) => {
    text = text.replace(`__MIRU_CODE_${i}__`, () => content);
  });

  // 6. Restore math blocks: display as \[...\] for texmath (or \(...\) when
  //    not cleanly block-positioned), inline as \(...\) so texmath's inline
  //    rule claims it before markdown-it's backslash-escape rule can mangle
  //    any `\$` inside (see step 0.75 note).
  mathBlocks.forEach((block) => {
    const replacement = block.isDisplay ? wrapDisplayMath(block.content, block.blockSafe) : '\\(' + block.content + '\\)';
    text = text.replace(block.placeholder, () => replacement);
  });

  return text;
}
