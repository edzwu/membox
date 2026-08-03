import { Readability } from '@mozilla/readability';
import TurndownService from 'turndown';
import type { ClipPayload } from './types';

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
  turndown.addRule('keepCode', {
    filter: ['pre'],
    replacement(_, node) {
      const el = node as HTMLElement;
      const text = el.textContent || '';
      return `\n\n\`\`\`\n${text.replace(/\n+$/, '')}\n\`\`\`\n\n`;
    },
  });
  return turndown;
}

/** Build a selection/excerpt note from the current DOM selection. */
export function clipSelection(args: {
  excerptText: string;
  excerptHTML: string;
  note: string;
}): ClipPayload {
  const sourceUrl = location.href;
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
  const titleBase = excerptText.replace(/\s+/g, ' ').slice(0, 48).trim() || pageTitle;
  const title = `${titleBase}${excerptText.length > 48 ? '…' : ''} — note`;

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

  const turndown = makeTurndown();

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

  // Drop Hugo/Sphinx/etc. heading permalink anchors before Markdown conversion
  // so saved notes don't contain "Title[Permalink](#id)".
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
  // ## Title[Permalink](#slug "Permalink")  →  ## Title
  return markdown.replace(
    /^(#{1,6}[ \t].+?)\s*\[Permalink\]\([^)]*\)/gim,
    '$1',
  );
}
