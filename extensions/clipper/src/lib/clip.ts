import { Readability } from '@mozilla/readability';
import TurndownService from 'turndown';
import type { ClipPayload } from './types';
import { normalizeSourceURL } from './url';

function yamlQuote(value: string): string {
  return `"${value.replace(/\\/g, '\\\\').replace(/"/g, '\\"').replace(/\n/g, ' ')}"`;
}

function buildMarkdown(
  title: string,
  sourceUrl: string,
  body: string,
  extraFrontMatter: Record<string, string> = {},
): string {
  const clippedAt = new Date().toISOString();
  const lines = [
    '---',
    `title: ${yamlQuote(title)}`,
    `source_url: ${yamlQuote(sourceUrl)}`,
    `clipped_at: ${yamlQuote(clippedAt)}`,
    'clipper: membox-clipper',
  ];
  for (const [key, value] of Object.entries(extraFrontMatter)) {
    lines.push(`${key}: ${yamlQuote(value)}`);
  }
  lines.push('---', '');
  const trimmed = body.trim();
  if (trimmed) {
    lines.push(trimmed, '');
  } else {
    lines.push(`# ${title}`, '', `Source: ${sourceUrl}`, '');
  }
  return lines.join('\n');
}

function makeTurndown(): TurndownService {
  const turndown = new TurndownService({
    headingStyle: 'atx',
    codeBlockStyle: 'fenced',
    bulletListMarker: '-',
  });
  // Preserve fenced language tags (```mermaid, ```typescript, …).
  turndown.addRule('fencedCodeBlock', {
    filter: (node) => {
      const el = node as HTMLElement;
      return el.nodeName === 'PRE' && Boolean(el.textContent?.trim());
    },
    replacement(_, node) {
      return fenceFromPre(node as HTMLElement);
    },
  });
  // mdbook mermaid-init rewrites <pre><code class="language-mermaid"> into
  // <pre class="mermaid"> before rendering. Capture still-textual nodes.
  turndown.addRule('mermaidContainer', {
    filter: (node) => {
      const el = node as HTMLElement;
      if (el.nodeName !== 'DIV' && el.nodeName !== 'PRE') return false;
      if (!el.classList.contains('mermaid')) return false;
      if (el.querySelector('svg')) return false;
      return Boolean(el.textContent?.trim());
    },
    replacement(_, node) {
      const text = trimTrailingNewlines((node as HTMLElement).textContent || '');
      return '\n\n```mermaid\n' + text + '\n```\n\n';
    },
  });
  return turndown;
}

function trimTrailingNewlines(text: string): string {
  return text.replace(/(?:\r?\n)+$/g, '');
}

function fenceFromPre(pre: HTMLElement): string {
  const code = pre.querySelector('code');
  const lang = detectCodeLanguage(pre, code);
  const text = trimTrailingNewlines((code || pre).textContent || '');
  return '\n\n```' + lang + '\n' + text + '\n```\n\n';
}

function detectCodeLanguage(pre: HTMLElement, code: Element | null): string {
  const classes = `${code?.className || ''} ${pre.className || ''}`;
  const match =
    classes.match(/language-([\w+-]+)/i) ||
    classes.match(/lang-([\w+-]+)/i);
  if (match) return match[1].toLowerCase();
  if (pre.classList.contains('mermaid') || code?.classList.contains('mermaid')) {
    return 'mermaid';
  }
  const text = ((code || pre).textContent || '').trimStart();
  if (
    /^(flowchart|graph|sequenceDiagram|classDiagram|stateDiagram|erDiagram|journey|gantt|pie|gitGraph|mindmap|timeline|quadrantChart|sankey|xychart)\b/.test(
      text,
    )
  ) {
    return 'mermaid';
  }
  return '';
}

/** Build a selection/excerpt note from the current DOM selection. */
export function clipSelection(args: {
  excerptText: string;
  excerptHTML: string;
  note: string;
  /** Override (e.g. original article URL when clipping inside Miru). */
  sourceUrl?: string;
}): ClipPayload {
  const sourceUrl = normalizeSourceURL(args.sourceUrl || location.href);
  const pageTitle = (document.title || 'Clipped page').trim();
  const excerptText = args.excerptText.trim();
  if (!excerptText) {
    throw new Error('Selection is empty');
  }

  const turndown = makeTurndown();
  let excerptMd = '';
  try {
    const html = args.excerptHTML?.trim()
      ? stripHeadingPermalinkHtml(args.excerptHTML)
      : `<p>${escapeHtml(excerptText).replace(/\n\n+/g, '</p><p>').replace(/\n/g, '<br>')}</p>`;
    excerptMd = turndown.turndown(html).trim();
  } catch {
    excerptMd = excerptText;
  }
  if (!excerptMd) excerptMd = excerptText;

  const note = args.note.trim();
  const cleanExcerpt = excerptText.replace(/\s+/g, ' ').trim();
  const titleBase = cleanExcerpt.slice(0, 10).trim() || pageTitle;
  const title = `${titleBase}${cleanExcerpt.length > 10 ? '…' : ''} — note`;

  const bodyParts = [
    `> ${excerptMd.replace(/\n/g, '\n> ')}`,
    '',
    note || '_No note._',
    '',
    `Source: [${pageTitle.replace(/\]/g, '')}](${sourceUrl})`,
    '',
  ];

  return {
    title,
    sourceUrl,
    clipMode: 'selection',
    excerptRaw: excerptText.replace(/\u00a0/g, ' ').trim(),
    body: buildMarkdown(title, sourceUrl, bodyParts.join('\n'), {
      clip_mode: 'selection',
      source_title: pageTitle,
    }),
  };
}

export function readSelection(): {
  excerptText: string;
  excerptHTML: string;
  rect: DOMRect;
} | null {
  const sel = window.getSelection();
  if (!sel || sel.isCollapsed || sel.rangeCount === 0) return null;
  const range = sel.getRangeAt(0);
  const excerptText = sel.toString().replace(/\u00a0/g, ' ').trim();
  if (!excerptText) return null;

  const fragment = range.cloneContents();
  const holder = document.createElement('div');
  holder.appendChild(fragment);
  const excerptHTML = holder.innerHTML;
  const rect = range.getBoundingClientRect();
  if (!rect || (rect.width === 0 && rect.height === 0)) return null;
  return { excerptText, excerptHTML, rect };
}

/** Estimated readable-text length of a body (rendered text incl. open shadow). */
function bodyTextLength(body: HTMLElement | null): number {
  return (body?.innerText || '').replace(/\s+/g, ' ').trim().length;
}

/** Prefer network HTML (pre-JS) so client-side mermaid init has not replaced
 *  ```mermaid sources with SVG yet. Falls back to a live DOM clone.
 *
 *  Client-rendered SPAs (React/Next/Vite…) ship a *shell*: the raw HTML has
 *  only nav text, while the real content exists in the live DOM. When the raw
 *  body is much smaller than the live one, use the live DOM so we never clip
 *  an empty root or a “Sign In” page. */
async function loadDocumentForClip(): Promise<Document> {
  try {
    const response = await fetch(location.href, {
      credentials: 'same-origin',
      cache: 'force-cache',
    });
    if (response.ok) {
      const text = await response.text();
      if (text && /<html[\s>]/i.test(text)) {
        const parsed = new DOMParser().parseFromString(text, 'text/html');
        if (parsed.body) {
          const raw = bodyTextLength(parsed.body);
          const live = bodyTextLength(document.body);
          if (live > 200 && raw < live * 0.5) {
            return document.cloneNode(true) as Document;
          }
          return parsed;
        }
      }
    }
  } catch {
    // fall through
  }
  return document.cloneNode(true) as Document;
}

/** Runs inside the extension content script (isolated world + full DOM). */
export async function clipCurrentDocument(overrideSourceUrl?: string): Promise<ClipPayload> {
  const sourceUrl = normalizeSourceURL(overrideSourceUrl || location.href);
  const titleHint = (document.title || 'Clipped page').trim();

  // Prefer pristine HTML: mdbook/mermaid-init rewrites language-mermaid blocks
  // into rendered SVGs in the live DOM, which turndown then flattens to junk
  // ("Unsupported markdown: list" + concatenated node labels).
  const documentClone = await loadDocumentForClip();
  if (!documentClone.body) {
    const body = documentClone.createElement('body');
    body.innerHTML = document.body?.innerHTML || titleHint;
    documentClone.documentElement.appendChild(body);
  }

  const mermaidSources = collectMermaidSources(documentClone);
  if (!mermaidSources.length) {
    mermaidSources.push(...collectMermaidSources(document));
  }

  let article: { title?: string | null; content?: string | null } | null = null;
  try {
    article = new Readability(documentClone).parse();
  } catch {
    article = null;
  }

  const turndown = makeTurndown();
  const title = (article?.title || titleHint || 'Clipped page').trim();
  let html = article?.content || '';
  if (!html || textLenOf(html) < 200) {
    // Readability produced nothing usable (complex SPAs often do). Hunt for a
    // main content container instead of grabbing the whole body (which would
    // include nav chrome, as seen on Tailwind/React pages without landmarks).
    const main = findMainContent(documentClone);
    if (main) html = main.innerHTML;
  }
  if (!html) {
    const text = document.body?.innerText?.trim() || '';
    if (!text) {
      throw new Error('Page has no extractable text');
    }
    html = `<p>${escapeHtml(text).replace(/\n\n+/g, '</p><p>').replace(/\n/g, '<br>')}</p>`;
  }

  html = stripHeadingPermalinkHtml(html);

  let markdownBody = '';
  try {
    markdownBody = turndown.turndown(html).trim();
  } catch (err) {
    throw new Error(
      'Turndown failed: ' + (err instanceof Error ? err.message : String(err)),
    );
  }
  if (!markdownBody) {
    const text = document.body?.innerText?.trim() || '';
    markdownBody = text || `_(No extractable content from ${sourceUrl})_`;
  }
  markdownBody = stripHeadingPermalinkMarkdown(markdownBody);
  markdownBody = restoreMermaidFences(markdownBody, mermaidSources);
  if (!/^#\s/m.test(markdownBody)) {
    markdownBody = `# ${title}\n\n${markdownBody}`;
  }

  return {
    title,
    sourceUrl,
    clipMode: 'page',
    body: buildMarkdown(title, sourceUrl, markdownBody, { clip_mode: 'page' }),
  };
}

// Chrome elements that never hold primary article content.
const CONTENT_CHROME_SELECTOR =
  'nav, header, footer, aside, form, script, style, [role="navigation"], [role="banner"], ' +
  '.nav, .navbar, .menu, .topbar, .sidebar, .breadcrumb, .footer, .header, .ad, .advert, .toolbar';

const MAIN_CONTENT_SELECTOR =
  'article, main, [role="main"], .post, .entry-content, .article-content, .content, ' +
  '.overview, .paper, .paper-content, .discussion, .reading-content, .doc-content, .markdown-body';

/** Layout-independent text length (scripts/styles stripped by chrome filter). */
function textLenOf(el: Element | string | null | undefined): number {
  if (!el) return 0;
  if (typeof el === 'string') {
    return el.replace(/\s+/g, ' ').trim().length;
  }
  return (el.textContent || '').replace(/\s+/g, ' ').trim().length;
}

/**
 * Locate the main content container in a page that may lack semantic
 * landmarks (Tailwind/React SPAs). Strategy: prefer semantic containers;
 * otherwise drop chrome elements and find the largest text-bearing block,
 * then squeeze down to the smallest descendant still holding most of the text.
 */
function findMainContent(root: ParentNode): HTMLElement | null {
  const body = (root as Document).body;
  if (!body) return null;

  // Pass 1: semantic landmarks.
  let best: HTMLElement | null = null;
  let bestScore = 0;
  body.querySelectorAll(MAIN_CONTENT_SELECTOR).forEach((el) => {
    const s = textLenOf(el);
    if (s > bestScore) {
      bestScore = s;
      best = el as HTMLElement;
    }
  });
  if (best && bestScore >= 200) return best;

  // Pass 2: chrome-free largest block.
  best = null;
  bestScore = 0;
  body.querySelectorAll<HTMLElement>('div, section, main, article, td, li').forEach((el) => {
    if (el.closest(CONTENT_CHROME_SELECTOR)) return;
    const s = textLenOf(el);
    if (s > bestScore) {
      bestScore = s;
      best = el;
    }
  });
  if (!best || bestScore < 200) return null;

  // Squeeze: descend to the smallest descendant carrying ~80% of the text so
  // we don't clip a giant wrapper that also contains nav.
  let current: HTMLElement = best;
  let guard = 0;
  while (current.children.length && guard++ < 12) {
    const total = textLenOf(current);
    if (total === 0) break;
    let next: HTMLElement | null = null;
    let nextScore = 0;
    for (const child of Array.from(current.children)) {
      const c = child as HTMLElement;
      if (c.closest(CONTENT_CHROME_SELECTOR)) continue;
      const s = textLenOf(c);
      if (s > nextScore) {
        nextScore = s;
        next = c;
      }
    }
    if (!next || nextScore < total * 0.8) break;
    current = next;
  }
  return current;
}

function collectMermaidSources(root: ParentNode): string[] {
  const out: string[] = [];
  const seen = new Set<string>();
  const push = (raw: string) => {
    const text = raw.replace(/\u00a0/g, ' ').replace(/(?:\r?\n)+$/g, '').trim();
    if (!text || seen.has(text)) return;
    if (
      !/^(flowchart|graph|sequenceDiagram|classDiagram|stateDiagram|erDiagram|journey|gantt|pie|gitGraph|mindmap|timeline)\b/m.test(
        text,
      ) &&
      !/-->|==>|-.->/.test(text)
    ) {
      return;
    }
    seen.add(text);
    out.push(text);
  };

  root.querySelectorAll('code.language-mermaid, code.lang-mermaid').forEach((el) => {
    push(el.textContent || '');
  });
  root.querySelectorAll('pre.mermaid, div.mermaid, .mermaid').forEach((el) => {
    if ((el as HTMLElement).querySelector?.('svg')) return;
    push(el.textContent || '');
  });
  root.querySelectorAll('[data-original-code], [data-mermaid], [data-graph]').forEach((el) => {
    const attr =
      el.getAttribute('data-original-code') ||
      el.getAttribute('data-mermaid') ||
      el.getAttribute('data-graph') ||
      '';
    push(attr);
  });
  return out;
}

/** Re-insert mermaid sources that turndown lost or flattened. */
function restoreMermaidFences(markdown: string, sources: string[]): string {
  if (!sources.length) return markdown;
  let body = markdown;
  const missing = sources.filter((src) => {
    if (body.includes(src.slice(0, Math.min(80, src.length)))) return false;
    return true;
  });
  if (!missing.length) return body;

  body = body.replace(/```(?:\w*)\n([^`]*?)\n```/g, (full, inner: string) => {
    const flat = inner.replace(/\s+/g, '');
    if (
      flat.includes('Unsupportedmarkdown') ||
      (flat.length > 40 && !inner.includes('\n') && /首次渲染|内容变化|输出全部/.test(inner))
    ) {
      const next = missing.shift();
      if (next) return '```mermaid\n' + next + '\n```';
    }
    return full;
  });

  if (missing.length) {
    body =
      body.replace(/\s*$/, '') +
      '\n\n' +
      missing.map((src) => '```mermaid\n' + src + '\n```').join('\n\n') +
      '\n';
  }
  return body;
}

function escapeHtml(value: string): string {
  return value
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;');
}

function stripHeadingPermalinkHtml(html: string): string {
  const holder = document.createElement('div');
  holder.innerHTML = html;
  holder.querySelectorAll('h1, h2, h3, h4, h5, h6').forEach((heading) => {
    heading.querySelectorAll('a').forEach((link) => {
      const text = (link.textContent || '').replace(/\u00a0/g, ' ').trim();
      const title = (link.getAttribute('title') || '').trim();
      const cls = link.className || '';
      const href = link.getAttribute('href') || '';
      const looksPermalink =
        /^(permalink|anchor|link)$/i.test(text) ||
        /^(permalink|anchor|link)$/i.test(title) ||
        /headerlink|permalink|anchorjs-link|direct-link|\banchor\b/i.test(cls) ||
        (href.startsWith('#') && text === '') ||
        ['¶', '§', '#', '＃', '🔗'].includes(text);
      if (looksPermalink) link.remove();
    });
  });
  return holder.innerHTML;
}

function stripHeadingPermalinkMarkdown(markdown: string): string {
  return markdown.replace(
    /^(#{1,6}[ \t].+?)\s*\[Permalink\]\([^)]*\)/gim,
    '$1',
  );
}
