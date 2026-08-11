// @vitest-environment happy-dom

import { describe, expect, it } from 'vitest';
import {
  extractCandidate,
  extractCandidates,
  selectBestCandidate,
  type ArticleCandidate,
} from './article-extractor';
import { htmlToMarkdown } from './markdown';

function page(html: string): Document {
  return new DOMParser().parseFromString(html, 'text/html');
}

function candidate(
  source: ArticleCandidate['source'],
  textLength: number,
  hasMermaidSource = false,
): ArticleCandidate {
  return {
    source,
    title: 'Title',
    html: '<p>body</p>',
    textLength,
    wordCount: textLength,
    hasMermaidSource,
  };
}

describe('article candidate selection', () => {
  it('keeps the live DOM for near ties', () => {
    const live = candidate('live', 950);
    const raw = candidate('raw', 1000);
    expect(selectBestCandidate([live, raw], false)).toBe(live);
  });

  it('uses materially more complete extracted content', () => {
    const live = candidate('live', 120);
    const raw = candidate('raw', 1200);
    expect(selectBestCandidate([live, raw], false)).toBe(raw);
  });

  it('recovers source Mermaid only from a sufficiently complete candidate', () => {
    const live = candidate('live', 1000);
    const completeRaw = candidate('raw', 900, true);
    const shellRaw = candidate('raw', 100, true);
    expect(selectBestCandidate([live, completeRaw], true)).toBe(completeRaw);
    expect(selectBestCandidate([live, shellRaw], true)).toBe(live);
  });
});

describe('Defuddle article extraction', () => {
  it('removes Medium metadata without deleting prose beginning with “By”', () => {
    const doc = page(`<!doctype html><html><head>
      <title>Reliable APIs</title><meta property="og:site_name" content="Medium">
      </head><body><article class="meteredContent">
        <h1 data-testid="storyTitle">Reliable APIs</h1>
        <div data-testid="authorPhoto"><img alt="avatar"></div>
        <span data-testid="authorName">Ada</span>
        <span data-testid="storyReadTime">20 min read</span>
        <p>By design, this API does not maintain sessions and remains predictable under load.</p>
        <p>This second paragraph makes the article substantial enough for extraction.</p>
      </article></body></html>`);

    const result = extractCandidate(doc, 'https://medium.com/@ada/reliable-apis', 'live');
    expect(result.html).toContain('By design, this API');
    expect(result.html).not.toContain('20 min read');
    expect(result.html).not.toContain('authorPhoto');
  });

  it('does not remove a first heading merely because it contains the page title', () => {
    const doc = page(`<!doctype html><html><head><title>Go</title></head><body><article>
      <h1>Going deeper</h1>
      <p>${'A useful paragraph about the language and its runtime. '.repeat(8)}</p>
    </article></body></html>`);

    const result = extractCandidate(doc, 'https://example.com/go', 'live');
    expect(result.html).toContain('Going deeper');
    expect(result.html).not.toMatch(/<h1[\s>]/i);
  });

  it('compares generic and VuePress-scoped candidates without a host rule', () => {
    const doc = page(`<!doctype html><html><head>
      <title>第1章 Go项目如何组织</title>
      <meta name="generator" content="VuePress 1.9.5">
      </head><body><div id="app" data-server-rendered="true"><div class="theme-container">
      <main id="main-content">
        <aside><a href="/pages/other/01/">第 1 课：专栏导论</a></aside>
        <div class="theme-vdoing-content content__default">
          <h1>第1章 Go项目如何组织</h1>
          <p>${'正文内容应该被保留下来，而不是课程导航。 '.repeat(30)}</p>
        </div>
        <div class="article-list"><a href="/pages/other/03/">更多文章</a></div>
      </main></div></div>
    </body></html>`);

    const candidates = extractCandidates(doc, 'https://docs.example.com/chapter/one', 'live');
    const result = selectBestCandidate(candidates, false);
    expect(candidates).toHaveLength(2);
    expect(result.title).toBe('第1章 Go项目如何组织');
    expect(result.html).toContain('正文内容应该被保留下来');
    expect(result.html).not.toContain('/pages/other/01');
    expect(result.html).not.toContain('更多文章');
  });

  it('falls back to generic Defuddle when VuePress has no known content container', () => {
    const doc = page(`<!doctype html><html><head>
      <title>Custom VuePress Theme</title><meta name="generator" content="VuePress 2">
      </head><body><article><p>${'Generic article prose. '.repeat(30)}</p></article></body></html>`);

    const candidates = extractCandidates(doc, 'https://docs.example.com/custom', 'live');
    expect(candidates).toHaveLength(1);
    expect(candidates[0]?.html).toContain('Generic article prose');
  });

  it('extracts hidden WeChat content through a scoped site profile', () => {
    const doc = page(`<!doctype html><html><head><title>微信文章</title></head><body>
      <div id="js_content" style="visibility:hidden;opacity:0">
        <div><span>在小说阅读器读本章</span><span>在小说阅读器中沉浸阅读</span></div>
        <h1>第一节</h1>
        <p>${'这是文章的真实正文，用于验证隐藏正文不会被通用清理流程删除。'.repeat(20)}</p>
      </div></body></html>`);

    const result = extractCandidate(
      doc,
      'https://mp.weixin.qq.com/s/example',
      'raw',
    );
    expect(result.html).toContain('真实正文');
    expect(result.html).not.toContain('在小说阅读器读本章');
  });

  it('resolves relative links against the candidate URL', () => {
    const doc = page(`<!doctype html><html><head><title>Links</title></head><body><article>
      <p>${'Substantial article text. '.repeat(12)}</p>
      <a href="../guide">Guide</a><img src="./cover.png" alt="cover">
    </article></body></html>`);

    const result = extractCandidate(doc, 'https://example.com/posts/article', 'raw');
    expect(result.html).toContain('href="https://example.com/guide"');
    expect(result.html).toContain('src="https://example.com/posts/cover.png"');
  });

  it('keeps an unrendered Mermaid diagram at its original position', () => {
    const doc = page(`<!doctype html><html><head><title>Diagram</title></head><body><article>
      <p>Before diagram.</p>
      <pre class="mermaid">flowchart LR<br>A --&gt; B</pre>
      <p>After diagram.</p>
    </article></body></html>`);

    const result = extractCandidate(doc, 'https://example.com/diagram', 'raw');
    const markdown = htmlToMarkdown(result.html);
    expect(result.hasMermaidSource).toBe(true);
    expect(markdown.indexOf('Before diagram.')).toBeLessThan(markdown.indexOf('```mermaid'));
    expect(markdown.indexOf('```mermaid')).toBeLessThan(markdown.indexOf('After diagram.'));
    expect(markdown).toContain('A --> B');
  });

  // Regression: raw.githubusercontent.com serves Markdown as text/plain, so
  // the whole document lands in one <pre>. A mermaid example inside it must
  // not turn the entire page into one mermaid fence.
  it('does not fence a whole raw Markdown page as one mermaid diagram', () => {
    const raw = [
      '# Durable AgentHarness design',
      '',
      'Prose about durable runs. '.repeat(40),
      '',
      '```mermaid',
      'flowchart TD',
      '    App --> Harness',
      '```',
      '',
      'More prose. '.repeat(40),
    ].join('\n');
    const doc = page(`<!doctype html><html><head><title>raw</title></head><body><pre>${raw}</pre></body></html>`);

    const result = extractCandidate(doc, 'https://raw.githubusercontent.com/a/b/main/doc.md', 'raw');
    const markdown = htmlToMarkdown(result.html);
    expect(result.hasMermaidSource).toBe(false);
    // At this extractor layer a whole-page pre still becomes a plain code
    // fence (containing the literal inner ```mermaid example as text); the
    // bug wrapped it in a 4-backtick mermaid-language fence. clip.ts bypasses
    // this layer entirely for raw Markdown pages and saves the source verbatim.
    expect(markdown).not.toMatch(/^`{4,}mermaid/m);
    expect(markdown).toContain('Durable AgentHarness design');
  });
});
