import { Readability } from '@mozilla/readability';
import TurndownService from 'turndown';
import type { ClipPayload } from './types';

function yamlQuote(value: string): string {
  return `"${value.replace(/\\/g, '\\\\').replace(/"/g, '\\"').replace(/\n/g, ' ')}"`;
}

function buildMarkdown(title: string, sourceUrl: string, body: string): string {
  const clippedAt = new Date().toISOString();
  const lines = [
    '---',
    `title: ${yamlQuote(title)}`,
    `source_url: ${yamlQuote(sourceUrl)}`,
    `clipped_at: ${yamlQuote(clippedAt)}`,
    'clipper: membox-clipper',
    '---',
    '',
  ];
  const trimmed = body.trim();
  if (trimmed) {
    lines.push(trimmed, '');
  } else {
    lines.push(`# ${title}`, '', `Source: ${sourceUrl}`, '');
  }
  return lines.join('\n');
}

/** Runs inside the extension content script (isolated world + full DOM). */
export function clipCurrentDocument(): ClipPayload {
  const sourceUrl = location.href;
  const titleHint = (document.title || 'Clipped page').trim();

  // Readability mutates its document; always clone first.
  const documentClone = document.cloneNode(true) as Document;
  // Some sites ship without <body> in the clone edge case — guard it.
  if (!documentClone.body) {
    const body = documentClone.createElement('body');
    body.innerHTML = document.body?.innerHTML || titleHint;
    documentClone.documentElement.appendChild(body);
  }

  let article: { title?: string | null; content?: string | null } | null = null;
  try {
    article = new Readability(documentClone).parse();
  } catch {
    article = null;
  }

  const turndown = new TurndownService({
    headingStyle: 'atx',
    codeBlockStyle: 'fenced',
    bulletListMarker: '-',
  });
  // Keep links/images useful in Markdown notes.
  turndown.addRule('keepCode', {
    filter: ['pre'],
    replacement(_, node) {
      const el = node as HTMLElement;
      const text = el.textContent || '';
      return `\n\n\`\`\`\n${text.replace(/\n+$/, '')}\n\`\`\`\n\n`;
    },
  });

  const title = (article?.title || titleHint || 'Clipped page').trim();
  let html = article?.content || '';
  if (!html) {
    const root =
      document.querySelector('article, main, [role="main"], .post, .entry-content, .article-content') ||
      document.body;
    html = root ? (root as HTMLElement).innerHTML : '';
  }
  if (!html) {
    const text = document.body?.innerText?.trim() || '';
    if (!text) {
      throw new Error('Page has no extractable text');
    }
    html = `<p>${escapeHtml(text).replace(/\n\n+/g, '</p><p>').replace(/\n/g, '<br>')}</p>`;
  }

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
  // Avoid duplicating H1 if Readability already produced one.
  if (!/^#\s/m.test(markdownBody)) {
    markdownBody = `# ${title}\n\n${markdownBody}`;
  }

  return {
    title,
    sourceUrl,
    body: buildMarkdown(title, sourceUrl, markdownBody),
  };
}

function escapeHtml(value: string): string {
  return value
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;');
}
