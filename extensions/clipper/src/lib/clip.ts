import type { ClipPayload } from './types';
import { extractCurrentArticle } from './article-extractor';
import { htmlToMarkdown } from './markdown';
import { currentSourceURL, normalizeSourceURL } from './url';

function yamlQuote(value: string): string {
  return `"${value.replace(/\\/g, '\\\\').replace(/"/g, '\\"').replace(/\n/g, ' ')}"`;
}

function buildMarkdown(
  title: string,
  sourceUrl: string,
  body: string,
  extraFrontMatter: Record<string, string> = {},
): string {
  const lines = [
    '---',
    `title: ${yamlQuote(title)}`,
    `source_url: ${yamlQuote(sourceUrl)}`,
    `clipped_at: ${yamlQuote(new Date().toISOString())}`,
    'clipper: membox-clipper',
  ];
  const version = runtimeClipperVersion();
  if (version) lines.push(`clipper_version: ${yamlQuote(version)}`);
  for (const [key, value] of Object.entries(extraFrontMatter)) {
    if (value) lines.push(`${key}: ${yamlQuote(value)}`);
  }
  lines.push('---', '');

  const trimmed = body.trim();
  lines.push(trimmed || `# ${markdownHeading(title)}\n\nSource: ${sourceUrl}`, '');
  return lines.join('\n');
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
  if (!excerptText) throw new Error('Selection is empty');

  let excerptMarkdown = '';
  try {
    const html = args.excerptHTML?.trim()
      ? args.excerptHTML
      : plainTextHtml(excerptText);
    excerptMarkdown = htmlToMarkdown(html);
  } catch {
    excerptMarkdown = excerptText;
  }
  if (!excerptMarkdown) excerptMarkdown = excerptText;

  const note = args.note.trim();
  const cleanExcerpt = excerptText.replace(/\s+/g, ' ').trim();
  const titleBase = cleanExcerpt.slice(0, 10).trim() || pageTitle;
  const title = `${titleBase}${cleanExcerpt.length > 10 ? '…' : ''} — note`;
  const body = [
    `> ${excerptMarkdown.replace(/\n/g, '\n> ')}`,
    '',
    note || '_No note._',
    '',
    `Source: [${pageTitle.replace(/\]/g, '')}](${sourceUrl})`,
  ].join('\n');

  return {
    title,
    sourceUrl,
    clipMode: 'selection',
    excerptRaw: excerptText.replace(/\u00a0/g, ' ').trim(),
    body: buildMarkdown(title, sourceUrl, body, {
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
  const selection = window.getSelection();
  if (!selection || selection.isCollapsed || selection.rangeCount === 0) return null;

  const range = selection.getRangeAt(0);
  const excerptText = selection.toString().replace(/\u00a0/g, ' ').trim();
  if (!excerptText) return null;

  const holder = document.createElement('div');
  holder.appendChild(range.cloneContents());
  const rect = range.getBoundingClientRect();
  if (!rect || (rect.width === 0 && rect.height === 0)) return null;
  return { excerptText, excerptHTML: holder.innerHTML, rect };
}

/** Runs inside the extension content script (isolated world + full DOM). */
export async function clipCurrentDocument(): Promise<ClipPayload> {
  // Read this at clip time, not when the content script was initialized. SPA
  // navigations can keep the same content script alive while changing the
  // document URL; a captured URL would attach the new article to the old one.
  const sourceUrl = currentSourceURL();
  const article = await extractCurrentArticle();
  const title = article.title || (document.title || 'Clipped page').trim();
  const converted = htmlToMarkdown(article.html);
  if (!converted) throw new Error('Page has no extractable text');

  // Defuddle standardizes body headings to h2+, so the persisted document has
  // one unambiguous h1 owned by this serialization boundary.
  const markdownBody = `# ${markdownHeading(title)}\n\n${converted}`;
  const extraFrontMatter: Record<string, string> = { clip_mode: 'page' };
  if (article.author) extraFrontMatter.author = article.author;
  if (article.published) extraFrontMatter.published = article.published;

  return {
    title,
    sourceUrl,
    clipMode: 'page',
    body: buildMarkdown(title, sourceUrl, markdownBody, extraFrontMatter),
    bodyLength: markdownBody.length,
  };
}

function runtimeClipperVersion(): string {
  try {
    return browser.runtime.getManifest().version || '';
  } catch {
    return ''; // Unit tests and non-extension consumers have no runtime API.
  }
}

function plainTextHtml(text: string): string {
  return `<p>${escapeHtml(text).replace(/\n\n+/g, '</p><p>').replace(/\n/g, '<br>')}</p>`;
}

function escapeHtml(value: string): string {
  return value.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
}

function markdownHeading(value: string): string {
  return value.replace(/([\\`*_[\]<>#])/g, '\\$1').replace(/\r?\n/g, ' ');
}
