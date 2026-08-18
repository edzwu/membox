import TurndownService from 'turndown';
import { gfm } from 'turndown-plugin-gfm';

const DROP_ELEMENTS = [
  'script',
  'noscript',
  'style',
  'template',
  'iframe',
  'object',
  'embed',
  'canvas',
] as const;

/** Convert already-extracted HTML to the Markdown dialect stored by membox. */
export function htmlToMarkdown(html: string): string {
  if (!html.trim()) return '';
  const cleanHtml = cleanConversionHtml(html);
  return makeTurndown().turndown(cleanHtml).trim();
}

function makeTurndown(): TurndownService {
  const turndown = new TurndownService({
    headingStyle: 'atx',
    codeBlockStyle: 'fenced',
    bulletListMarker: '-',
  });
  turndown.use(gfm);

  // turndown-plugin-gfm only converts tables whose first row is <th>, and
  // keeps every other <table> as raw HTML. Miru renders with html:false, so
  // those leftovers show up as escaped source. Convert body-only tables too
  // (common on hand-written pages like Jeff Dean latency numbers).
  turndown.addRule('tableWithoutHeading', {
    filter: (node) => isTableWithoutHeading(node),
    replacement: (_content, node) => tableToMarkdown(node as HTMLTableElement),
  });

  // Selection clips do not pass through the article extractor, so keep this
  // safety boundary in the serializer as well.
  turndown.addRule('dropNonContent', {
    filter: [...DROP_ELEMENTS],
    replacement: () => '',
  });

  turndown.addRule('fencedCodeBlock', {
    filter: (node) => node.nodeName === 'PRE',
    replacement(_, node) {
      return fenceFromPre(node as HTMLElement);
    },
  });

  // Defuddle gives extracted equations a canonical data-latex attribute.
  // Persist explicit delimiters so Miru never has to infer math from prose.
  turndown.addRule('latexMath', {
    // MathML elements keep a lowercase nodeName in Chromium because they are
    // in the MathML namespace; happy-dom historically uppercases it. localName
    // is namespace-safe in both environments.
    filter: (node) => isLatexMathElement(node),
    replacement(_, node) {
      const math = node as Element;
      const latex = math.getAttribute('data-latex')?.trim() || '';
      if (!latex) return '';
      const display = math.getAttribute('display') === 'block';
      return display ? `\n\n$$\n${latex}\n$$\n\n` : `$${latex}$`;
    },
  });

  // Defuddle standardizes code blocks to <pre><code>. This rule also covers
  // short selection clips that contain an unrendered Mermaid container.
  turndown.addRule('mermaidContainer', {
    filter: (node) => {
      const el = node as HTMLElement;
      if (el.nodeName !== 'DIV' || !el.classList.contains('mermaid')) return false;
      return !el.querySelector('svg') && Boolean(el.textContent?.trim());
    },
    replacement(_, node) {
      return fencedBlock(elementText(node as HTMLElement), 'mermaid');
    },
  });

  return turndown;
}

export function isLatexMathElement(node: Node): boolean {
  const element = node as Element;
  return (
    element.localName?.toLowerCase() === 'math' && element.hasAttribute?.('data-latex') === true
  );
}

function isTableWithoutHeading(node: Node): boolean {
  if (node.nodeName !== 'TABLE') return false;
  const table = node as HTMLTableElement;
  const first = table.rows?.[0];
  if (!first) return false;
  return !rowIsHeading(first);
}

function rowIsHeading(row: HTMLTableRowElement): boolean {
  const parent = row.parentElement;
  if (!parent) return false;
  if (parent.nodeName === 'THEAD') return true;
  if (parent.firstElementChild !== row) return false;
  if (parent.nodeName !== 'TABLE' && parent.nodeName !== 'TBODY') return false;
  // GFM heading row: every cell is <th>.
  const cells = Array.from(row.cells);
  return cells.length > 0 && cells.every((cell) => cell.nodeName === 'TH');
}

/** Serialize any HTML table to a GFM pipe table (synthetic blank header if needed). */
export function tableToMarkdown(table: HTMLTableElement): string {
  const rows = Array.from(table.rows).map((row) =>
    Array.from(row.cells).map((cell) => cellTextForMarkdown(cell)),
  );
  if (!rows.length) return '';

  const colCount = Math.max(0, ...rows.map((row) => row.length));
  if (colCount === 0) return '';

  const pad = (row: string[]): string[] => {
    if (row.length >= colCount) return row.slice(0, colCount);
    return row.concat(Array(colCount - row.length).fill(''));
  };

  const first = table.rows[0];
  const headed = first ? rowIsHeading(first) : false;
  const bodyRows = headed ? rows.slice(1) : rows;
  const header = headed ? pad(rows[0]) : Array(colCount).fill('');

  const lines = [
    `| ${header.join(' | ')} |`,
    `| ${header.map(() => '---').join(' | ')} |`,
    ...bodyRows.map((row) => `| ${pad(row).join(' | ')} |`),
  ];
  return `\n\n${lines.join('\n')}\n\n`;
}

function cellTextForMarkdown(cell: HTMLTableCellElement): string {
  // Prefer structured text (keeps <br> as spaces after normalize) without
  // pulling in nested table noise — rare and not worth recursive conversion.
  const text = elementText(cell).replace(/\s+/g, ' ').trim();
  return text.replace(/\|/g, '\\|');
}

function cleanConversionHtml(html: string): string {
  const doc = new DOMParser().parseFromString(
    `<div id="membox-conversion-root">${html}</div>`,
    'text/html',
  );
  const root = doc.getElementById('membox-conversion-root');
  if (!root) return html;

  root.querySelectorAll(DROP_ELEMENTS.join(',')).forEach((el) => el.remove());
  normalizeRenderedMath(root);
  // Turndown classifies empty custom elements before consulting custom rules.
  // Give source-only MathML a textual child so the latexMath rule can claim it.
  root.querySelectorAll('math[data-latex]').forEach((math) => {
    if (!math.textContent?.trim()) math.textContent = math.getAttribute('data-latex') || '';
  });
  root.querySelectorAll('h1,h2,h3,h4,h5,h6').forEach((heading) => {
    heading.querySelectorAll('a').forEach((link) => {
      if (isHeadingPermalink(link)) link.remove();
    });
  });
  return root.innerHTML;
}

// KaTeX keeps three equivalent forms in the DOM: accessible MathML, a TeX
// annotation, and aria-hidden visual HTML. Turndown does not apply page CSS or
// ARIA semantics, so serializing that tree verbatim duplicates every formula.
// Collapse it to the canonical shape already consumed by the latexMath rule.
function normalizeRenderedMath(root: HTMLElement): void {
  for (const katex of Array.from(root.querySelectorAll('.katex'))) {
    if (!root.contains(katex)) continue; // removed with an outer display node
    const latex = texAnnotation(katex);
    if (!latex) continue;

    const math = root.ownerDocument.createElement('math');
    math.setAttribute('data-latex', latex);
    const displayContainer = katex.closest('.katex-display');
    const mathML = katex.querySelector('math');
    if (displayContainer || mathML?.getAttribute('display') === 'block') {
      math.setAttribute('display', 'block');
    }

    if (displayContainer && root.contains(displayContainer)) {
      displayContainer.replaceWith(math);
    } else {
      katex.replaceWith(math);
    }
  }

  // Some renderers expose source-bearing MathML without a KaTeX wrapper.
  root.querySelectorAll('math:not([data-latex])').forEach((math) => {
    const latex = texAnnotation(math);
    if (latex) math.setAttribute('data-latex', latex);
  });
}

function texAnnotation(element: Element): string {
  const annotation = Array.from(element.querySelectorAll('annotation')).find(
    (candidate) =>
      (candidate.getAttribute('encoding') || '').toLowerCase() === 'application/x-tex',
  );
  return annotation?.textContent?.trim() || '';
}

function isHeadingPermalink(link: HTMLAnchorElement): boolean {
  const text = (link.textContent || '').replace(/\u00a0/g, ' ').trim();
  const title = (link.getAttribute('title') || '').trim();
  const className = link.className || '';
  const href = link.getAttribute('href') || '';
  return (
    /^(permalink|anchor|link)$/i.test(text) ||
    /^(permalink|anchor|link)$/i.test(title) ||
    /headerlink|permalink|anchorjs-link|direct-link|\banchor\b/i.test(className) ||
    (href.startsWith('#') && text === '') ||
    ['¶', '§', '#', '＃', '🔗'].includes(text)
  );
}

function fenceFromPre(pre: HTMLElement): string {
  const code = pre.querySelector('code');
  const source = (code || pre) as HTMLElement;
  const language = detectCodeLanguage(pre, code);
  return fencedBlock(elementText(source), language);
}

function fencedBlock(raw: string, language: string): string {
  const text = raw.replace(/(?:\r?\n)+$/g, '');
  if (!text.trim()) return '';
  const longestRun = Math.max(0, ...Array.from(text.matchAll(/`+/g), (m) => m[0].length));
  const fence = '`'.repeat(Math.max(3, longestRun + 1));
  const safeLanguage = /^[\w+-]+$/.test(language) ? language.toLowerCase() : '';
  return `\n\n${fence}${safeLanguage}\n${text}\n${fence}\n\n`;
}

/** textContent with HTML <br> represented as a source newline. */
function elementText(el: HTMLElement): string {
  const clone = el.cloneNode(true) as HTMLElement;
  for (const br of Array.from(clone.querySelectorAll('br'))) {
    br.replaceWith(clone.ownerDocument.createTextNode('\n'));
  }
  return (clone.textContent || '').replace(/\u00a0/g, ' ');
}

function detectCodeLanguage(pre: HTMLElement, code: Element | null): string {
  const declared =
    code?.getAttribute('data-lang') ||
    code?.getAttribute('data-language') ||
    pre.getAttribute('data-lang') ||
    pre.getAttribute('data-language');
  if (declared) return declared;

  const classes = `${code?.className || ''} ${pre.className || ''}`;
  const match = classes.match(/(?:language|lang)-([\w+-]+)/i);
  if (match?.[1]) return match[1];
  if (pre.classList.contains('mermaid') || code?.classList.contains('mermaid')) {
    return 'mermaid';
  }
  return looksLikeMermaid(elementText((code || pre) as HTMLElement)) ? 'mermaid' : '';
}

export function looksLikeMermaid(text: string): boolean {
  // Anchor at the start of the whole text only — never /m. A multiline anchor
  // matches a diagram keyword on ANY line, so a large pre whose body merely
  // contains a mermaid example (e.g. a raw Markdown page wrapped in one pre)
  // would be misclassified as a diagram and fenced as ```mermaid.
  return /^(?:flowchart|graph|sequenceDiagram|classDiagram|stateDiagram|erDiagram|journey|gantt|pie|gitGraph|mindmap|timeline|quadrantChart|sankey|xychart)\b/.test(
    text.trimStart(),
  );
}
