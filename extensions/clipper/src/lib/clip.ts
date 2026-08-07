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

/** Text of a subtree including open shadow roots; skips script/style/noscript. */
function deepText(root: Node | null | undefined): string {
  if (!root) return '';
  const parts: string[] = [];
  const walk = (node: Node) => {
    if (node.nodeType === 3) {
      parts.push(node.textContent || '');
      return;
    }
    if (node.nodeType !== 1) return;
    const el = node as HTMLElement;
    const tag = el.tagName;
    if (tag === 'SCRIPT' || tag === 'STYLE' || tag === 'NOSCRIPT') return;
    if (el.shadowRoot) walk(el.shadowRoot);
    for (const child of el.childNodes) walk(child);
  };
  walk(root);
  return parts.join(' ');
}

/** Visible text of a body: innerText on the live doc, deepText otherwise. */
function visibleBodyText(doc: Document): string {
  const body = doc.body as HTMLElement | null;
  if (!body) return '';
  const inner = (body as HTMLElement & { innerText?: string }).innerText;
  if (typeof inner === 'string' && inner.trim()) return inner;
  return deepText(body);
}

/** Fetch the pristine (pre-JS) HTML when possible; null otherwise.
 *  Tries same-origin then cookie-less: WeChat flags same-origin XHR/fetch and
 *  serves a verify page, but its public articles load fine without cookies.
 *  Returns whichever copy carries the most real content. */
async function fetchRawDocument(): Promise<Document | null> {
  let best: Document | null = null;
  let bestScore = 0;
  const attempts: RequestCredentials[] = ['same-origin', 'omit'];
  for (const credentials of attempts) {
    try {
      const response = await fetch(location.href, {
        credentials,
        cache: 'no-store',
      });
      if (!response.ok) continue;
      const text = await response.text();
      if (!text || !/<html[\s>]/i.test(text)) continue;
      const parsed = new DOMParser().parseFromString(text, 'text/html');
      if (!parsed.body) continue;
      const score = contentScore(parsed);
      if (score > bestScore) {
        bestScore = score;
        best = parsed;
      }
    } catch {
      // try the next credential mode
    }
  }
  // Require substantial content; shells / verify pages should not shadow the
  // live DOM, which is compared in pickBestDocument.
  if (best && bestScore >= 500) return best;
  return null;
}

/** Wait for the page body to fill in, up to ms. Lazy-loaded pages (WeChat
 *  bodies) may need a scroll to trigger loading; scroll once if the content
 *  is stable but still tiny. Returns the visible text length reached. */
async function waitForBodySettle(ms: number, minChars = 2000): Promise<number> {
  const deadline = Date.now() + ms;
  let last = -1;
  let stable = 0;
  let scrolled = false;
  while (Date.now() < deadline) {
    const len = (document.body as HTMLElement | null)?.innerText?.length ?? 0;
    if (len >= minChars) return len;
    if (len === last) {
      stable++;
      if (stable >= 3) {
        if (!scrolled) {
          scrolled = true;
          window.scrollTo(0, document.body.scrollHeight); // trigger lazy load
          window.scrollTo(0, 0);
        } else {
          return len;
        }
      }
    } else {
      stable = 0;
      last = len;
    }
    await new Promise((resolve) => setTimeout(resolve, 300));
  }
  return (document.body as HTMLElement | null)?.innerText?.length ?? 0;
}

/**
 * Pick the document with the most real content. Client-rendered SPAs ship a
 * shell in the raw HTML (nav only) while the live DOM holds the content, so
 * the live DOM wins when it has noticeably more text. Static/mermaid pages
 * have equal content in both — the raw copy wins the tie so mermaid sources
 * that client-side init replaced with SVG are preserved.
 */
function pickBestDocument(raw: Document | null, live: Document): Document {
  if (!raw) return live;
  const rawScore = contentScore(raw);
  const liveScore = contentScore(live);
  return liveScore > rawScore * 1.2 ? live : raw;
}

/** How much real content a document carries (main block text length). */
function contentScore(doc: Document): number {
  const main = findMainContent(doc);
  if (main) return textLenOf(main);
  return deepText(doc.body).replace(/\s+/g, ' ').trim().length;
}

/** Wrap plain text as paragraphs for turndown. */
function plainTextHtml(text: string): string {
  return `<p>${escapeHtml(text).replace(/\n\n+/g, '</p><p>').replace(/\n/g, '<br>')}</p>`;
}

/** Extract the main article HTML from one document (never mutates it). */
function extractMainHtml(doc: Document): string {
  // 1. Readability (on a clone — it mutates its input).
  try {
    const article = new Readability(doc.cloneNode(true) as Document).parse();
    if (article?.content && textLenOf(article.content) >= 200) return article.content;
  } catch {
    // continue
  }
  // 2. Main content block (semantic landmark, else largest chrome-free block).
  const main = findMainContent(doc);
  if (main) return main.innerHTML;
  // 3. Plain visible text (shadow-aware; preserves paragraph breaks on live).
  const text = visibleBodyText(doc).replace(/\s+/g, ' ').trim();
  return text ? plainTextHtml(text) : '';
}

/** Same-origin iframes often hold paper viewers / embeds. Return their best
 *  main-content HTML when it beats the given score. */
function extractFromSameOriginIframes(current: number): { html: string; score: number } {
  let best = { html: '', score: current };
  const frames = Array.from(document.querySelectorAll('iframe'));
  for (const frame of frames) {
    let frameDoc: Document | null = null;
    try {
      frameDoc = frame.contentDocument;
    } catch {
      /* cross-origin */
    }
    if (!frameDoc || !frameDoc.body) continue;
    const score = contentScore(frameDoc);
    if (score > best.score) {
      const html = extractMainHtml(frameDoc);
      const s = textLenOf(html);
      if (s > best.score) best = { html, score: s };
    }
  }
  return best;
}

/** Runs inside the extension content script (isolated world + full DOM). */
export async function clipCurrentDocument(overrideSourceUrl?: string): Promise<ClipPayload> {
  const sourceUrl = normalizeSourceURL(overrideSourceUrl || location.href);
  const titleHint = (document.title || 'Clipped page').trim();

  // Choose the document with the most real content: the pristine HTML keeps
  // mermaid sources alive (mdbook), the live DOM carries SPA content that only
  // exists after client-side rendering (alphaxiv etc.).
  const liveDoc = document;
  const rawDoc = await fetchRawDocument();
  // Lazy-loaded pages (WeChat bodies) may still be filling in — wait up to 5s,
  // scrolling once to trigger lazy load, so the live DOM competes fairly.
  const liveChars = await waitForBodySettle(5000, 2000);
  const doc = pickBestDocument(rawDoc, liveDoc);
  console.debug('[membox-clip] raw fetched:', !!rawDoc,
    '| live body chars:', liveChars,
    '| raw score:', rawDoc ? contentScore(rawDoc) : 0,
    '| live score:', contentScore(liveDoc));
  if (!doc.body) {
    const body = doc.createElement('body');
    body.innerHTML = document.body?.innerHTML || titleHint;
    doc.documentElement.appendChild(body);
  }

  const mermaidSources = collectMermaidSources(doc);
  if (!mermaidSources.length) {
    mermaidSources.push(...collectMermaidSources(document));
  }

  const turndown = makeTurndown();
  let html = extractMainHtml(doc);
  console.debug('[membox-clip] extracted html chars:', textLenOf(html));
  if (textLenOf(html) < 800) {
    // Suspiciously little content: the page may still be lazy-loading its
    // body (WeChat). Wait a beat (with a scroll to trigger lazy load), let the
    // live DOM settle, then re-extract from whichever document has more.
    await waitForBodySettle(5000, 2000);
    const retryDoc = pickBestDocument(rawDoc, document);
    const retryHtml = extractMainHtml(retryDoc);
    console.debug('[membox-clip] retry extracted chars:', textLenOf(retryHtml));
    if (textLenOf(retryHtml) > textLenOf(html)) {
      html = retryHtml;
    }
  }
  if (textLenOf(html) < 200) {
    // The main document yielded little — check same-origin iframes (paper
    // viewers / embeds) before giving up.
    const frame = extractFromSameOriginIframes(textLenOf(html));
    if (frame.html) html = frame.html;
  }
  if (!html.trim()) {
    throw new Error('Page has no extractable text');
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
    const text = visibleBodyText(document).trim();
    markdownBody = text || `_(No extractable content from ${sourceUrl})_`;
  }
  markdownBody = stripHeadingPermalinkMarkdown(markdownBody);
  markdownBody = stripWeChatPromo(markdownBody);
  markdownBody = restoreMermaidFences(markdownBody, mermaidSources);
  const title = titleHint;
  if (!/^#\s/m.test(markdownBody)) {
    markdownBody = `# ${title}\n\n${markdownBody}`;
  }

  return {
    title,
    sourceUrl,
    clipMode: 'page',
    body: buildMarkdown(title, sourceUrl, markdownBody, { clip_mode: 'page' }),
    bodyLength: markdownBody.length,
  };
}

// Chrome elements that never hold primary article content.
const CONTENT_CHROME_SELECTOR =
  'nav, header, footer, aside, form, script, style, [role="navigation"], [role="banner"], ' +
  '.nav, .navbar, .menu, .topbar, .sidebar, .breadcrumb, .footer, .header, .ad, .advert, .toolbar';

const MAIN_CONTENT_SELECTOR =
  'article, main, [role="main"], .post, .entry-content, .article-content, .content, ' +
  '.overview, .paper, .paper-content, .discussion, .reading-content, .doc-content, .markdown-body, ' +
  // WeChat articles: the canonical containers (js_content is static HTML; the
  // live DOM keeps it after JS runs).
  '#js_content, .rich_media_content, .rich_media_area_primary, .rich_media_title';

/** True for elements that are not rendered at all (display:none, hidden, etc.).
 *  visibility:hidden deliberately does NOT count: those elements still render
 *  and layout, and WeChat marks its article body (#js_content) with
 *  "visibility:hidden; opacity:0" until JS reveals it — skipping it would
 *  lose the entire article. */
function isHiddenElement(el: Element): boolean {
  const htmlEl = el as HTMLElement;
  if (htmlEl.hidden) return true;
  const style = htmlEl.getAttribute && htmlEl.getAttribute('style');
  if (style && /display\s*:\s*none/i.test(style)) return true;
  const aria = htmlEl.getAttribute && htmlEl.getAttribute('aria-hidden');
  if (aria && aria !== 'false') return true;
  return false;
}

/** Layout-independent text length (shadow-aware; scripts/styles skipped). */
function textLenOf(el: Element | string | null | undefined): number {
  if (!el) return 0;
  if (typeof el === 'string') {
    return el.replace(/\s+/g, ' ').trim().length;
  }
  return deepText(el).replace(/\s+/g, ' ').trim().length;
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
    if (isHiddenElement(el)) return;
    const s = textLenOf(el);
    if (s > bestScore) {
      bestScore = s;
      best = el as HTMLElement;
    }
  });
  if (best && bestScore >= 200) return best;

  // Pass 2: chrome-free largest block (skips hidden/prefetched content).
  best = null;
  bestScore = 0;
  body.querySelectorAll<HTMLElement>('div, section, main, article, td, li').forEach((el) => {
    if (el.closest(CONTENT_CHROME_SELECTOR)) return;
    if (isHiddenElement(el)) return;
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
      if (isHiddenElement(c)) continue;
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

/** Remove WeChat's in-article "read in the novel reader" promo block. */
function stripWeChatPromo(markdown: string): string {
  return markdown.replace(
    /^在小说阅读器读本章\s*[\s\S]*?在小说阅读器中沉浸阅读\s*/m,
    '',
  );
}
