/* Miru — convert common pasted HTML fragments into Markdown equivalents
   before markdown-it runs. Pure text-to-text transform; no DOM.

   Miru renders with markdown-it's `html: false`, so raw HTML pasted from
   READMEs (header cards like <p align="center"><img …><a …></p>) would
   otherwise show up as escaped source text. Rather than enabling raw HTML
   (which would silently swallow tag-shaped prose and widen the XSS surface),
   we recognize the small, safe subset that READMEs actually use and rewrite
   it as Markdown:

     <p align="center">…</p>   →  the inner content as a plain paragraph
     <img src alt>             →  ![alt](src)   (width/align dropped)
     <a href>…</a>             →  [text](href)
     <b>/<strong>              →  **text**
     <br>                      →  line break
     <table>…</table>          →  GFM pipe table (blank header if no <th>)
     lone <div>/</div> lines   →  removed

   Copy-paste from Word/WeChat/chat apps also turns straight quotes into
   smart quotes (“ ” ‘ ’), which breaks attribute parsing, so quotes are
   normalized inside tag-shaped text before attributes are read.

   Everything outside the recognized subset is left untouched — including
   prose that merely *mentions* tags (e.g. "wrap it in a <div>"), which is
   why conversions are anchored to whole blocks/lines rather than applied
   globally. Fenced code blocks and inline code spans are protected so HTML
   tutorials survive verbatim. */

function unsmartQuotes(text) {
  // Only fix quotes inside <...> tag-shaped regions; prose keeps its
  // typographic quotes, which may be intentional (e.g. Chinese text).
  return text.replace(/<[^<>\n]*>/g, (tag) =>
    tag.replace(/[“”]/g, '"').replace(/[‘’]/g, "'")
  );
}

function readAttr(tag, name) {
  const m =
    tag.match(new RegExp(`${name}\\s*=\\s*"([^"]*)"`, 'i')) ||
    tag.match(new RegExp(`${name}\\s*=\\s*'([^']*)'`, 'i')) ||
    tag.match(new RegExp(`${name}\\s*=\\s*([^\\s>]+)`, 'i'));
  return m ? m[1] : '';
}

// Linear-time check for a code span whose entire content is one Markdown
// link. Avoid regexes with nested quantifiers here: imported documents can be
// hundreds of KB and one malformed link must never block the browser thread.
function isCodeWrappedMarkdownLink(value) {
  if (!value.startsWith('[') || !value.endsWith(')')) return false;
  const labelEnd = value.indexOf('](');
  if (labelEnd <= 1 || value.indexOf('[', 1) !== -1 || value.indexOf(']', labelEnd + 1) !== -1) return false;

  const destinationStart = labelEnd + 2;
  const destinationEnd = value.length - 1;
  if (destinationStart >= destinationEnd) return false;
  let depth = 0;
  for (let i = destinationStart; i < destinationEnd; i++) {
    const char = value[i];
    if (/\s/.test(char)) return false;
    if (char === '(') depth++;
    if (char === ')') {
      if (depth === 0) return false;
      depth--;
    }
  }
  return depth === 0;
}

function convertInlineHtml(text) {
  let out = text;

  // <b>/<strong> → bold markers (before links, so <a><b>t</b></a> nests well)
  out = out.replace(/<(?:b|strong)\b[^>]*>([\s\S]*?)<\/(?:b|strong)>/gi, '**$1**');

  // <a href>…</a> → [text](href); without href, keep just the text
  out = out.replace(/<a\b[^>]*>([\s\S]*?)<\/a>/gi, (whole, inner) => {
    const href = readAttr(whole, 'href');
    const label = inner.trim();
    if (!href) return label;
    return `[${label}](${href})`;
  });

  // <img> → ![alt](src); without src, drop the tag entirely
  out = out.replace(/<img\b[^>]*\/?>/gi, (whole) => {
    const src = readAttr(whole, 'src');
    if (!src) return '';
    return `![${readAttr(whole, 'alt')}](${src})`;
  });

  // <br> → newline
  out = out.replace(/<br\s*\/?>/gi, '\n');

  // Superscripts common in unit glosses (10<sup>-9</sup>).
  out = out.replace(/<sup\b[^>]*>([\s\S]*?)<\/sup>/gi, '^$1');
  out = out.replace(/<sub\b[^>]*>([\s\S]*?)<\/sub>/gi, '~$1');

  return out;
}

function decodeBasicEntities(text) {
  return String(text || '')
    .replace(/&nbsp;/gi, ' ')
    .replace(/&amp;/gi, '&')
    .replace(/&lt;/gi, '<')
    .replace(/&gt;/gi, '>')
    .replace(/&quot;/gi, '"')
    .replace(/&#39;/gi, "'")
    .replace(/&#(\d+);/g, (_, n) => String.fromCodePoint(Number(n)))
    .replace(/&#x([0-9a-f]+);/gi, (_, h) => String.fromCodePoint(parseInt(h, 16)));
}

function cellTextFromHtml(inner) {
  const withBreaks = String(inner || '').replace(/<br\s*\/?>/gi, ' ');
  // Drop residual tags inside a cell (spans, etc.) after inline conversions.
  const converted = convertInlineHtml(withBreaks).replace(/<[^>]+>/g, '');
  return decodeBasicEntities(converted).replace(/\s+/g, ' ').trim().replace(/\|/g, '\\|');
}

// HTML tables that survived clip/paste (turndown keeps body-only tables as
// raw HTML). Rewrite to GFM so markdown-it (html:false) can render them.
function convertHtmlTables(text) {
  return text.replace(/<table\b[^>]*>[\s\S]*?<\/table>/gi, (tableHtml) => {
    const rows = [];
    let headed = false;
    const rowRe = /<tr\b[^>]*>([\s\S]*?)<\/tr>/gi;
    let rowMatch;
    while ((rowMatch = rowRe.exec(tableHtml)) !== null) {
      const rowHtml = rowMatch[1];
      const cells = [];
      const cellRe = /<t([dh])\b[^>]*>([\s\S]*?)<\/t\1>/gi;
      let cellMatch;
      let rowHasTh = false;
      while ((cellMatch = cellRe.exec(rowHtml)) !== null) {
        if (cellMatch[1].toLowerCase() === 'h') rowHasTh = true;
        cells.push(cellTextFromHtml(cellMatch[2]));
      }
      if (!cells.length) continue;
      if (rows.length === 0 && rowHasTh) headed = true;
      rows.push(cells);
    }
    if (!rows.length) return tableHtml;

    const colCount = Math.max(...rows.map((r) => r.length));
    const pad = (row) => {
      if (row.length >= colCount) return row.slice(0, colCount);
      return row.concat(Array(colCount - row.length).fill(''));
    };

    const bodyRows = headed ? rows.slice(1) : rows;
    const header = headed ? pad(rows[0]) : Array(colCount).fill('');
    const lines = [
      `| ${header.join(' | ')} |`,
      `| ${header.map(() => '---').join(' | ')} |`,
      ...bodyRows.map((row) => `| ${pad(row).join(' | ')} |`),
    ];
    return `\n\n${lines.join('\n')}\n\n`;
  });
}

export function preprocessHtml(text) {
  // Protect fenced code blocks and inline code spans so HTML discussed as
  // code is never rewritten.
  const protectedSpans = [];
  const protect = (content) => {
    const placeholder = `__MIRU_HTMLCODE_${protectedSpans.length}__`;
    protectedSpans.push({ placeholder, content });
    return placeholder;
  };
  let out = text.replace(/(^|\n)([ \t]*(```+|~~~+)[\s\S]*?\n[ \t]*\3[ \t]*(?=\n|$))/g,
    (m, lead, block) => lead + protect(block));

  // Some HTML→Markdown converters produce a code span around the *entire*
  // Markdown link (`[runtime.NumCPU](https://...)`) when the source was an
  // <a><code>…</code></a>. Repair that artifact; protect every ordinary code
  // span in the same single linear pass. Fenced examples are already safe.
  out = out.replace(/`([^`\n]+)`/g, (whole, content) =>
    isCodeWrappedMarkdownLink(content) ? content : protect(whole));

  // Normalize smart quotes inside tags before reading attributes.
  out = unsmartQuotes(out);

  // <p …>…</p> blocks (possibly multiline): unwrap and convert the inner
  // content. Alignment is dropped — Markdown has no centering, and Miru's
  // stylesheet owns layout.
  out = out.replace(/<p\b[^>]*>([\s\S]*?)<\/p>/gi, (whole, inner) => {
    const converted = convertInlineHtml(inner).trim();
    return converted ? `\n\n${converted}\n\n` : '';
  });

  // Lines that are entirely a single <img> or <a> tag (the usual README
  // badge/teaser pattern outside a <p> wrapper).
  out = out.replace(/^[ \t]*(<img\b[^>]*\/?>)[ \t]*$/gim, (m, tag) => convertInlineHtml(tag));
  out = out.replace(/^[ \t]*(<a\b[^>]*>[\s\S]*?<\/a>)[ \t]*$/gim, (m, tag) => convertInlineHtml(tag));

  // Lone <div …> / </div> lines are pure layout wrappers — drop them.
  out = out.replace(/^[ \t]*<\/?div\b[^>]*>[ \t]*$/gim, '');

  // Raw HTML tables (common leftover from clippers / pasted pages).
  out = convertHtmlTables(out);

  // <ul>/<ol> with simple <li> children — common under truncated clips.
  out = out.replace(/<(ul|ol)\b[^>]*>([\s\S]*?)<\/\1>/gi, (whole, type, inner) => {
    const ordered = String(type).toLowerCase() === 'ol';
    let i = 0;
    const items = [];
    const liRe = /<li\b[^>]*>([\s\S]*?)<\/li>/gi;
    let m;
    while ((m = liRe.exec(inner)) !== null) {
      i += 1;
      const item = convertInlineHtml(m[1]).replace(/<[^>]+>/g, '');
      const text = decodeBasicEntities(item).replace(/\s+/g, ' ').trim();
      if (!text) continue;
      items.push(ordered ? `${i}. ${text}` : `- ${text}`);
    }
    if (!items.length) return whole;
    return `\n\n${items.join('\n')}\n\n`;
  });

  protectedSpans.forEach(({ placeholder, content }) => {
    out = out.replace(placeholder, content);
  });

  return out;
}
