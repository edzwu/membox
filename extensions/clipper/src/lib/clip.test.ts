// @vitest-environment happy-dom

import { afterEach, describe, expect, it } from 'vitest';
import {
  clipCurrentDocument,
  isMarkdownURL,
  rawMarkdownText,
  rawMarkdownTitle,
} from './clip';

const RAW_URL =
  'https://raw.githubusercontent.com/earendil-works/pi/refs/heads/main/packages/agent/docs/harness-v2.md';

function installRawPage(markdown: string, url = RAW_URL) {
  (window as unknown as { happyDOM?: { setURL(url: string): void } }).happyDOM?.setURL(url);
  Object.defineProperty(document, 'contentType', { value: 'text/plain', configurable: true });
  document.body.innerHTML = '';
  const pre = document.createElement('pre');
  pre.textContent = markdown;
  document.body.appendChild(pre);
}

afterEach(() => {
  Object.defineProperty(document, 'contentType', { value: 'text/html', configurable: true });
  document.body.innerHTML = '';
});

describe('rawMarkdownText', () => {
  it('returns the source of a text/plain single-pre page', () => {
    installRawPage('# Title\n\nbody text\n');
    expect(rawMarkdownText()).toBe('# Title\n\nbody text\n');
  });

  it('returns null for ordinary HTML pages', () => {
    Object.defineProperty(document, 'contentType', { value: 'text/html', configurable: true });
    document.body.innerHTML = '<pre>looks like code</pre>';
    expect(rawMarkdownText()).toBeNull();
  });

  it('returns null when the body is not exactly one pre', () => {
    installRawPage('text');
    const div = document.createElement('div');
    div.textContent = 'extra';
    document.body.appendChild(div);
    expect(rawMarkdownText()).toBeNull();
  });

  it('returns null for an empty page', () => {
    installRawPage('   \n  ');
    expect(rawMarkdownText()).toBeNull();
  });
});

describe('isMarkdownURL', () => {
  it('matches Markdown file extensions', () => {
    expect(isMarkdownURL('https://example.com/docs/guide.md')).toBe(true);
    expect(isMarkdownURL('https://example.com/docs/guide.markdown')).toBe(true);
    expect(isMarkdownURL('https://example.com/docs/GUIDE.MD?x=1')).toBe(true);
  });

  it('rejects other paths', () => {
    expect(isMarkdownURL('https://example.com/docs/guide.html')).toBe(false);
    expect(isMarkdownURL('https://example.com/docs/')).toBe(false);
    expect(isMarkdownURL('not a url')).toBe(false);
  });
});

describe('rawMarkdownTitle', () => {
  it('prefers the first ATX heading', () => {
    expect(rawMarkdownTitle('intro\n\n# Durable AgentHarness design\n\ntext', RAW_URL)).toBe(
      'Durable AgentHarness design',
    );
  });

  it('falls back to the filename stem', () => {
    expect(rawMarkdownTitle('no heading here', RAW_URL)).toBe('harness-v2');
  });

  it('falls back to a generic title', () => {
    expect(rawMarkdownTitle('no heading', 'not a url')).toBe('Clipped page');
  });
});

describe('clipCurrentDocument on a raw Markdown page', () => {
  it('persists the Markdown source verbatim instead of fencing it', async () => {
    const raw = [
      '# Durable AgentHarness design',
      '',
      'Some prose about the design.',
      '',
      '```mermaid',
      'flowchart TD',
      '  App --> Harness',
      '```',
      '',
      'More prose.',
    ].join('\n');
    installRawPage(raw);

    const payload = await clipCurrentDocument();

    expect(payload.clipMode).toBe('page');
    expect(payload.title).toBe('Durable AgentHarness design');
    expect(payload.sourceUrl).toBe(RAW_URL);
    // The body is the raw Markdown itself: the mermaid example stays a live
    // fence, and nothing wraps the page in an extra code block.
    expect(payload.body).toContain(`${raw}`);
    expect(payload.body).not.toContain('````');
    expect(payload.bodyLength).toBe(raw.length);
  });
});
