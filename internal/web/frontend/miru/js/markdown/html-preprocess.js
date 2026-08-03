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

  return out;
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
  out = out.replace(/`[^`\n]+`/g, (m) => protect(m));

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

  protectedSpans.forEach(({ placeholder, content }) => {
    out = out.replace(placeholder, content);
  });

  return out;
}
