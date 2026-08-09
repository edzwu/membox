import Defuddle from 'defuddle';
import type { DefuddleOptions, DefuddleResponse } from 'defuddle';
import { looksLikeMermaid } from './markdown';

const MIN_ARTICLE_CHARS = 200;
const RAW_FORMAT_COMPLETENESS = 0.75;
const LIVE_TIE_COMPLETENESS = 0.9;

export type CandidateSource = 'live' | 'raw' | 'iframe';

export interface ArticleCandidate {
  source: CandidateSource;
  title: string;
  html: string;
  textLength: number;
  wordCount: number;
  hasMermaidSource: boolean;
}

export interface ExtractedArticle extends ArticleCandidate {
  author: string;
  published: string;
}

interface SiteProfile {
  contentSelector?: string;
  preserveHidden?: boolean;
  prepare?: (doc: Document) => void;
}

interface RawDocument {
  doc: Document;
  url: string;
}

/**
 * Extract a clean article from the current browser page.
 *
 * The live DOM is canonical. Raw HTML and same-origin iframes are independent
 * candidates, parsed through the same extractor, and only win after extraction
 * when they are demonstrably more complete or preserve a destroyed source form.
 */
export async function extractCurrentArticle(): Promise<ExtractedArticle> {
  const pageUrl = location.href;
  const profile = siteProfile(pageUrl);
  const renderedMermaid = hasRenderedMermaid(document);
  const candidates: ExtractedArticle[] = [];
  let liveError: unknown = null;
  try {
    candidates.push(extractCandidate(snapshotDocument(document), pageUrl, 'live', profile));
  } catch (error) {
    liveError = error;
  }

  const live = candidates.find((candidate) => candidate.source === 'live');
  if (
    renderedMermaid ||
    profile.preserveHidden ||
    !live ||
    live.textLength < MIN_ARTICLE_CHARS
  ) {
    const rawDocuments = await fetchRawDocuments(pageUrl, Boolean(profile.preserveHidden));
    for (const raw of rawDocuments) {
      try {
        candidates.push(extractCandidate(raw.doc, raw.url, 'raw', siteProfile(raw.url)));
      } catch {
        // One malformed or blocked response must not discard a valid candidate.
      }
    }
  }

  if (!candidates.length) {
    throw liveError instanceof Error ? liveError : new Error('Page has no extractable text');
  }
  let best = selectBestCandidate(candidates, renderedMermaid);
  if (best.textLength < MIN_ARTICLE_CHARS) {
    for (const frame of Array.from(document.querySelectorAll('iframe'))) {
      let frameDoc: Document | null = null;
      try {
        frameDoc = frame.contentDocument;
      } catch {
        // Cross-origin frames are intentionally inaccessible.
      }
      if (!frameDoc?.body) continue;
      let frameUrl = pageUrl;
      try {
        frameUrl = frame.contentWindow?.location.href || pageUrl;
      } catch {
        // Keep the outer URL.
      }
      try {
        candidates.push(
          extractCandidate(snapshotDocument(frameDoc), frameUrl, 'iframe', siteProfile(frameUrl)),
        );
      } catch {
        // Continue with the remaining same-origin viewers.
      }
    }
    best = selectBestCandidate(candidates, renderedMermaid);
  }

  if (!best.html.trim()) throw new Error('Page has no extractable text');
  return best;
}

/** Parse one fully isolated document. Exported for fixture tests. */
export function extractCandidate(
  doc: Document,
  url: string,
  source: CandidateSource,
  profile = siteProfile(url),
): ExtractedArticle {
  profile.prepare?.(doc);
  normalizeMermaidSources(doc);

  const options: DefuddleOptions = {
    url,
    useAsync: false,
    ...(profile.contentSelector ? { contentSelector: profile.contentSelector } : {}),
    ...(profile.preserveHidden
      ? {
          removeHiddenElements: false,
          removePartialSelectors: false,
        }
      : {}),
  };

  let result: DefuddleResponse;
  try {
    result = new Defuddle(doc, options).parse();
  } catch (error) {
    throw new Error(
      `Article extraction failed: ${error instanceof Error ? error.message : String(error)}`,
    );
  }

  const html = result.content || '';
  return {
    source,
    title: cleanTitle(result.title || doc.title || 'Clipped page'),
    html,
    textLength: htmlTextLength(html),
    wordCount: result.wordCount || 0,
    hasMermaidSource: htmlHasMermaidSource(html),
    author: result.author || '',
    published: result.published || '',
  };
}

/**
 * Prefer the user's live view when candidates are similarly complete. A raw
 * candidate may win to recover source-only formats (notably Mermaid), but only
 * if it still contains most of the clean article. This compares extracted
 * articles, never noisy whole-page text lengths.
 */
export function selectBestCandidate<T extends ArticleCandidate>(
  candidates: T[],
  requireMermaidSource: boolean,
): T {
  if (!candidates.length) throw new Error('No article candidates');
  const live = candidates.find((candidate) => candidate.source === 'live');
  const longest = candidates.reduce((best, candidate) =>
    candidate.textLength > best.textLength ? candidate : best,
  );

  if (requireMermaidSource) {
    const sourceCandidates = candidates
      .filter(
        (candidate) =>
          candidate.hasMermaidSource &&
          candidate.textLength >= longest.textLength * RAW_FORMAT_COMPLETENESS,
      )
      .sort((a, b) => b.textLength - a.textLength);
    if (sourceCandidates[0]) return sourceCandidates[0];
  }

  // Near ties stay live: it is the page the user actually clipped. Materially
  // richer clean candidates (lazy raw pages or iframe viewers) win naturally.
  if (live && live.textLength >= longest.textLength * LIVE_TIE_COMPLETENESS) return live;
  return longest;
}

function snapshotDocument(source: Document): Document {
  const clone = source.cloneNode(true) as Document;
  if (!clone.body && clone.documentElement) {
    clone.documentElement.appendChild(clone.createElement('body'));
  }
  copyOpenShadowRoots(source, clone);
  return clone;
}

/** Flatten open shadow roots into the isolated snapshot without touching the page. */
function copyOpenShadowRoots(source: Document, clone: Document): void {
  if (!source.body || !clone.body) return;
  const sourceElements = Array.from(source.body.querySelectorAll('*'));
  const cloneElements = Array.from(clone.body.querySelectorAll('*'));
  for (let index = sourceElements.length - 1; index >= 0; index--) {
    const shadow = sourceElements[index]?.shadowRoot;
    const target = cloneElements[index];
    if (!shadow || !target || !shadow.childNodes.length) continue;
    target.replaceChildren(...Array.from(shadow.childNodes, (node) => node.cloneNode(true)));
  }
}

async function fetchRawDocuments(url: string, cookieLessFirst: boolean): Promise<RawDocument[]> {
  const credentials: RequestCredentials[] = cookieLessFirst
    ? ['omit', 'same-origin']
    : ['same-origin'];
  const documents: RawDocument[] = [];
  const seen = new Set<string>();
  for (const mode of credentials) {
    try {
      const response = await fetch(url, { credentials: mode, cache: 'no-store' });
      if (!response.ok) continue;
      const html = await response.text();
      if (!/<html[\s>]/i.test(html) || seen.has(html)) continue;
      seen.add(html);
      const doc = new DOMParser().parseFromString(html, 'text/html');
      if (doc.body) documents.push({ doc, url: response.url || url });
    } catch {
      // Fall through to the next acquisition mode.
    }
  }
  return documents;
}

function siteProfile(url: string): SiteProfile {
  let host = '';
  try {
    host = new URL(url).hostname;
  } catch {
    return {};
  }
  if (host === 'mp.weixin.qq.com') {
    return {
      contentSelector: '#js_content',
      preserveHidden: true,
      prepare: removeWeChatReaderPromo,
    };
  }
  return {};
}

/** Site adapter: remove the bounded reader CTA from the article DOM, not Markdown. */
function removeWeChatReaderPromo(doc: Document): void {
  const root = doc.querySelector('#js_content');
  if (!root) return;
  const start = '在小说阅读器读本章';
  const end = '在小说阅读器中沉浸阅读';
  const elements = Array.from(root.querySelectorAll('*'));
  for (const el of elements.reverse()) {
    const text = (el.textContent || '').replace(/\s+/g, ' ').trim();
    if (!text.startsWith(start) || !text.endsWith(end)) continue;
    const childContainsWholePromo = Array.from(el.children).some((child) => {
      const childText = (child.textContent || '').replace(/\s+/g, ' ').trim();
      return childText.startsWith(start) && childText.endsWith(end);
    });
    if (!childContainsWholePromo) el.remove();
  }
}

/** Standardize source-bearing diagrams before the generic extractor cleans classes. */
function normalizeMermaidSources(doc: Document): void {
  doc.querySelectorAll('code.language-mermaid, code.lang-mermaid').forEach((code) => {
    code.setAttribute('data-lang', 'mermaid');
    code.classList.add('language-mermaid');
  });

  const candidates = doc.querySelectorAll(
    'pre.mermaid, div.mermaid, [data-original-code], [data-mermaid], [data-graph]',
  );
  candidates.forEach((element) => {
    const attributeSource =
      element.getAttribute('data-original-code') ||
      element.getAttribute('data-mermaid') ||
      element.getAttribute('data-graph') ||
      '';
    const text = attributeSource || (element.querySelector('svg') ? '' : sourceText(element));
    if (!looksLikeMermaid(text)) return;

    const pre = doc.createElement('pre');
    const code = doc.createElement('code');
    code.className = 'language-mermaid';
    code.setAttribute('data-lang', 'mermaid');
    code.textContent = text.replace(/\u00a0/g, ' ').trim();
    pre.appendChild(code);
    element.replaceWith(pre);
  });
}

function sourceText(element: Element): string {
  const clone = element.cloneNode(true) as Element;
  clone.querySelectorAll('br').forEach((br) => {
    br.replaceWith(clone.ownerDocument.createTextNode('\n'));
  });
  return clone.textContent || '';
}

function hasRenderedMermaid(doc: Document): boolean {
  return Boolean(doc.querySelector('.mermaid svg, svg[aria-roledescription="flowchart-v2"]'));
}

function htmlHasMermaidSource(html: string): boolean {
  if (!html) return false;
  const doc = new DOMParser().parseFromString(html, 'text/html');
  return Array.from(doc.querySelectorAll('pre,code')).some((element) => {
    const language = `${element.getAttribute('data-lang') || ''} ${element.className || ''}`;
    return /(?:^|\s|-)mermaid(?:\s|$)/i.test(language) || looksLikeMermaid(element.textContent || '');
  });
}

function htmlTextLength(html: string): number {
  if (!html) return 0;
  const doc = new DOMParser().parseFromString(html, 'text/html');
  doc.querySelectorAll('script,style,noscript').forEach((el) => el.remove());
  return (doc.body.textContent || '').replace(/\s+/g, ' ').trim().length;
}

function cleanTitle(title: string): string {
  return title.replace(/\s+/g, ' ').trim() || 'Clipped page';
}
