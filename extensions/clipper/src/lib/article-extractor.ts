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
  /**
   * Use this container's HTML directly as the clip content, bypassing
   * Defuddle. For non-article pages (listing/search SPAs) where readability
   * heuristics destroy the structure or pick the wrong block.
   */
  directSelector?: string;
  /**
   * Read innerText from these live containers (computed-style aware, so
   * CSS-hidden anti-scraping decoys never enter the text), skipping
   * `remove`-matched chrome. Falls back to generic extraction when a
   * selector matches nothing.
   */
  liveText?: { selectors: string[]; remove: string };
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
  // Anti-scraping SPAs poison the HTML text with CSS-hidden decoys. Read the
  // live page's innerText instead: it is computed-style aware, so the decoys
  // never enter the text.
  if (profile.liveText) {
    const live = extractLiveTextCandidate(profile.liveText);
    if (live) return live;
  }
  const renderedMermaid = hasRenderedMermaid(document);
  const candidates: ExtractedArticle[] = [];
  let liveError: unknown = null;
  try {
    candidates.push(...extractCandidates(snapshotDocument(document), pageUrl, 'live'));
  } catch (error) {
    liveError = error;
  }

  const live = longestCandidate(candidates.filter((candidate) => candidate.source === 'live'));
  if (
    renderedMermaid ||
    profile.preserveHidden ||
    !live ||
    live.textLength < MIN_ARTICLE_CHARS
  ) {
    const rawDocuments = await fetchRawDocuments(pageUrl, Boolean(profile.preserveHidden));
    for (const raw of rawDocuments) {
      try {
        candidates.push(...extractCandidates(raw.doc, raw.url, 'raw'));
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
        candidates.push(...extractCandidates(snapshotDocument(frameDoc), frameUrl, 'iframe'));
      } catch {
        // Continue with the remaining same-origin viewers.
      }
    }
    best = selectBestCandidate(candidates, renderedMermaid);
  }

  if (!best.html.trim()) throw new Error('Page has no extractable text');
  return best;
}

/**
 * Build generic and framework-aware extraction candidates for one document.
 * Explicit site profiles remain authoritative compatibility adapters; pages
 * without one compare generic Defuddle against a detected framework scope.
 */
export function extractCandidates(
  doc: Document,
  url: string,
  source: CandidateSource,
): ExtractedArticle[] {
  const explicit = siteProfile(url);
  let profiles = hasProfile(explicit) ? [explicit] : [{}, ...frameworkProfiles(doc)];
  // A direct selector is a fast path for non-article pages. When its
  // container is absent (stale selector, unexpected page state), generic
  // extraction must still run instead of failing the whole clip.
  if (explicit.directSelector && !doc.querySelector(explicit.directSelector)) {
    profiles = [{}, ...profiles];
  }
  const candidates: ExtractedArticle[] = [];
  const seen = new Set<string>();

  for (const profile of profiles) {
    const key = `${profile.contentSelector || ''} ${profile.directSelector || ''} ${Boolean(profile.preserveHidden)}`;
    if (seen.has(key)) continue;
    seen.add(key);
    try {
      candidates.push(extractCandidate(doc.cloneNode(true) as Document, url, source, profile));
    } catch {
      // A framework selector is only another candidate. If it becomes stale,
      // generic Defuddle must still be able to extract the page.
    }
  }
  if (!candidates.length) throw new Error('Page has no extractable candidates');
  return candidates;
}

/** Parse one fully isolated document. Exported for focused fixture tests. */
export function extractCandidate(
  doc: Document,
  url: string,
  source: CandidateSource,
  profile = siteProfile(url),
): ExtractedArticle {
  profile.prepare?.(doc);
  normalizeMermaidSources(doc);

  if (profile.directSelector) {
    return extractDirectCandidate(doc, url, source, profile.directSelector);
  }

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
 * Non-article pages (listing/search result SPAs) have no article body for
 * Defuddle to find; the content container itself is the clip.
 */
function extractDirectCandidate(
  doc: Document,
  url: string,
  source: CandidateSource,
  selector: string,
): ExtractedArticle {
  const root = doc.querySelector(selector);
  if (!root) throw new Error(`content container not found: ${selector}`);
  absolutizeResourceLinks(root, url);
  const html = root.outerHTML || '';
  if (!html.trim()) throw new Error('Page has no extractable text');
  return {
    source,
    title: cleanTitle(doc.title || 'Clipped page'),
    html,
    textLength: htmlTextLength(html),
    wordCount: 0,
    hasMermaidSource: htmlHasMermaidSource(html),
    author: '',
    published: '',
  };
}

/** Resolve relative href/src against the page URL (Defuddle does this for the
 * article path; the direct path must do it itself). */
function absolutizeResourceLinks(root: Element, pageUrl: string): void {
  root.querySelectorAll('a[href], img[src], source[src], source[srcset]').forEach((el) => {
    for (const attr of ['href', 'src', 'srcset']) {
      const value = el.getAttribute(attr);
      if (!value || /^(?:[a-z][a-z0-9+.-]*:|#)/i.test(value)) continue;
      try {
        el.setAttribute(attr, new URL(value, pageUrl).href);
      } catch {
        // Keep the original attribute on malformed input.
      }
    }
  });
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
  const live = longestCandidate(candidates.filter((candidate) => candidate.source === 'live'));
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

function longestCandidate<T extends ArticleCandidate>(candidates: T[]): T | undefined {
  return candidates.reduce<T | undefined>(
    (best, candidate) => (!best || candidate.textLength > best.textLength ? candidate : best),
    undefined,
  );
}

function hasProfile(profile: SiteProfile): boolean {
  return Boolean(profile.contentSelector || profile.preserveHidden || profile.prepare);
}

function frameworkProfiles(doc: Document): SiteProfile[] {
  const generator =
    doc.querySelector('meta[name="generator"]')?.getAttribute('content')?.toLowerCase() || '';
  const looksLikeVuePress =
    generator.includes('vuepress') ||
    Boolean(
      doc.querySelector(
        '#app[data-server-rendered] .theme-container, .theme-default-content, .theme-vdoing-content.content__default',
      ),
    );
  if (!looksLikeVuePress) return [];

  // Ordered from the narrowest article scope to older/common VuePress themes.
  for (const selector of [
    '#main-content .content__default',
    '#main-content .theme-default-content',
    '.theme-default-content',
    'main .content__default',
  ]) {
    if (doc.querySelector(selector)) return [{ contentSelector: selector }];
  }
  return [];
}

/** Exported for focused fixture tests. */
export function siteProfile(url: string): SiteProfile {
  let host = '';
  let pathname = '';
  try {
    const parsed = new URL(url);
    host = parsed.hostname;
    pathname = parsed.pathname;
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
  // BOSS直聘职位列表是卡片列表而非文章：Defuddle 会把筛选侧栏当成正文，
  // 并清理掉卡片里的短文本（经验/学历/公司）。直接取职位列表容器。
  if (host === 'www.zhipin.com' && pathname === '/web/geek/jobs') {
    return {
      directSelector: '.job-list-container',
      prepare: prepareZhipinJobList,
    };
  }
  // 职位详情页的正文被反爬水印污染（隐藏诱饵文本注入），走 live innerText。
  if (host === 'www.zhipin.com' && pathname.startsWith('/job_detail/')) {
    return {
      liveText: {
        selectors: ['.job-banner .info-primary', '.job-detail'],
        remove: [
          '.tag-all', // hidden "show all tags" dropdown duplicating the visible tags
          '.zp-hide-salary', // salary rendered as SVG paths (anti-scraping)
          '.job-op', // resume/application CTAs
          '.detail-section-operate', // wechat share / report links
          '.zp-more-info-layer-wrapper', // "login to view full content" overlay
          '.job-detail-guide-immediate-login',
          '.security-box', // "BOSS 安全提示" boilerplate
          '.prop-item', // personalized competitiveness analysis
          '.more-job-section', // "更多职位" recommendations
          '.job-search-scan',
        ].join(','),
      },
    };
  }
  return {};
}

/** Site adapter: drop the login CTA and decorative company logos, keep the
 * structured job cards. */
function prepareZhipinJobList(doc: Document): void {
  doc.querySelectorAll('.zp-job-list-login-card, .boss-logo').forEach((el) => el.remove());
}

/**
 * Read a live container's visible text. innerText is computed-style aware, so
 * anti-scraping decoys hidden via CSS never enter the output. `remove`
 * chrome and the remaining hiding vectors innerText misses outside Chrome
 * (visibility:hidden / font-size:0) are detached first and restored after, so
 * the page is never left mutated. Exported for focused fixture tests.
 */
export function extractLiveTextCandidate(
  spec: { selectors: string[]; remove: string },
  doc: Document = document,
): ExtractedArticle | null {
  const parts: string[] = [];
  for (const selector of spec.selectors) {
    const root = doc.querySelector(selector);
    if (!root) return null; // stale selector → generic extraction
    const text = readVisibleText(root as HTMLElement, spec.remove, doc.defaultView);
    if (text) parts.push(text);
  }
  const text = parts.join('\n\n');
  if (text.length < MIN_ARTICLE_CHARS) return null;
  const html = text
    .split('\n')
    .filter((line) => line.trim())
    .map(
      (line) =>
        `<p>${line.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')}</p>`,
    )
    .join('');
  return {
    source: 'live',
    title: cleanTitle(doc.title || 'Clipped page'),
    html,
    textLength: text.length,
    wordCount: 0,
    hasMermaidSource: false,
    author: '',
    published: '',
  };
}

function readVisibleText(
  root: HTMLElement,
  junkSelector: string,
  view: (Window & typeof globalThis) | null,
): string {
  const detached: { el: Element; parent: Node; next: Node | null }[] = [];
  const detach = (el: Element) => {
    if (!el.parentNode) return;
    detached.push({ el, parent: el.parentNode, next: el.nextSibling });
    el.remove();
  };
  root.querySelectorAll(junkSelector).forEach(detach);
  if (view?.getComputedStyle) {
    root.querySelectorAll('*').forEach((el) => {
      const style = view.getComputedStyle(el);
      if (style.visibility === 'hidden' || style.fontSize === '0px') detach(el);
    });
  }
  try {
    return root.innerText.trim();
  } finally {
    for (const { el, parent, next } of detached.reverse()) parent.insertBefore(el, next);
  }
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
