/**
 * Clipboard helpers for the popup's Copy / Paste buttons.
 *
 * Copy: compose the clipped page into a self-contained Markdown document
 * for the clipboard (title H1 + body + source provenance) — nothing is
 * sent to membox.
 *
 * Paste: clipboard text → membox ingest. Title resolution order:
 *   1. the text's own first Markdown H1 ("自带的 title");
 *   2. the bridge's /api/suggest-title model call (lead + outline);
 *   3. empty — the server falls back to its default.
 */

/** First ATX H1 in the text, mirroring the server's titleFromBody. */
export function titleFromMarkdown(text: string): string {
  for (const line of text.split('\n')) {
    const trimmed = line.trim();
    if (trimmed.startsWith('# ')) {
      const title = trimmed.slice(2).trim();
      if (title) return title;
    }
  }
  return '';
}

export type OutlineHeading = { level: number; text: string };

/** Read clipboard text in an extension page. Chrome's async clipboard API
 * is flaky in popups ("Read permission denied" despite the clipboardRead
 * permission and a user gesture) — fall back to a paste into a scratch
 * textarea, which the permission does cover. */
export async function readClipboardText(): Promise<string> {
  try {
    return await navigator.clipboard.readText();
  } catch {
    /* fall through to execCommand */
  }
  const scratch = document.createElement('textarea');
  scratch.style.position = 'fixed';
  scratch.style.opacity = '0';
  document.body.appendChild(scratch);
  scratch.focus();
  try {
    if (document.execCommand('paste')) return scratch.value;
    throw new Error('clipboard read denied — is the clipboardRead permission granted?');
  } finally {
    scratch.remove();
  }
}

/** Write clipboard text with the same execCommand fallback. */
export async function writeClipboardText(text: string): Promise<void> {
  try {
    await navigator.clipboard.writeText(text);
    return;
  } catch {
    /* fall through to execCommand */
  }
  const scratch = document.createElement('textarea');
  scratch.value = text;
  scratch.style.position = 'fixed';
  scratch.style.opacity = '0';
  document.body.appendChild(scratch);
  scratch.focus();
  scratch.select();
  try {
    if (!document.execCommand('copy')) {
      throw new Error('clipboard write denied');
    }
  } finally {
    scratch.remove();
  }
}

/** Headings of the pasted Markdown (fence-aware, capped) for the
 * suggest-title prompt — better outlines give better titles. */
export function outlineFromMarkdown(text: string, max = 50): OutlineHeading[] {
  const headings: OutlineHeading[] = [];
  let inFence = false;
  for (const line of text.split('\n')) {
    const trimmed = line.trim();
    if (trimmed.startsWith('```') || trimmed.startsWith('~~~')) {
      inFence = !inFence;
      continue;
    }
    if (inFence) continue;
    const match = /^(#{1,6})\s+(.+)$/.exec(trimmed);
    if (match) {
      headings.push({ level: match[1]!.length, text: match[2]!.trim() });
      if (headings.length >= max) break;
    }
  }
  return headings;
}

/** First paragraph of real prose (no headings / fences / list markers),
 * rune-capped for the suggest-title lead. */
export function leadFromMarkdown(text: string, maxRunes = 480): string {
  let inFence = false;
  for (const line of text.split('\n')) {
    const trimmed = line.trim();
    if (trimmed.startsWith('```') || trimmed.startsWith('~~~')) {
      inFence = !inFence;
      continue;
    }
    if (inFence || !trimmed) continue;
    if (/^(#{1,6}\s|[-*+]\s|\d+\.\s|>|---|\|)/.test(trimmed)) continue;
    return [...trimmed].slice(0, maxRunes).join('');
  }
  return '';
}

/** Self-contained Markdown for the clipboard: H1 title (unless the body
 * already opens with it), the body, and a source line for provenance. */
export function composePageMarkdown(title: string, body: string, sourceUrl: string): string {
  const parts: string[] = [];
  const trimmedBody = body.trim();
  if (title && !titleFromMarkdown(trimmedBody)) {
    parts.push(`# ${title}`);
  }
  parts.push(trimmedBody);
  if (sourceUrl) {
    parts.push(`---\nSource: ${sourceUrl}`);
  }
  return parts.join('\n\n') + '\n';
}
