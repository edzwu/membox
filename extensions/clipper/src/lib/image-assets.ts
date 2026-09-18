import type { ClipImageRef } from './types';

const PLACEHOLDER_PREFIX = 'https://membox.invalid/__clip-image/';
const LAZY_SOURCE_ATTRIBUTES = [
  'data-src',
  'data-original',
  'data-lazy-src',
  'data-original-src',
] as const;

export type PreparedClipHTML = {
  html: string;
  images: ClipImageRef[];
};

/** Only WeChat Official Account articles need binary localization. */
export function shouldLocalizeClipImages(sourceUrl: string): boolean {
  try {
    const url = new URL(sourceUrl);
    return (
      url.hostname.toLowerCase() === 'mp.weixin.qq.com' &&
      (url.pathname === '/s' || url.pathname.startsWith('/s/'))
    );
  } catch {
    return false;
  }
}

/**
 * Replace article image URLs with deterministic placeholders before Turndown.
 * The background worker later downloads each source and swaps the placeholder
 * for a stable local membox asset URL. Keeping this step in HTML avoids having
 * to parse Markdown image syntax after conversion.
 */
export function prepareClipImages(html: string, pageUrl: string): PreparedClipHTML {
  if (!html.trim()) return { html, images: [] };
  const doc = new DOMParser().parseFromString(
    `<div id="membox-image-root">${html}</div>`,
    'text/html',
  );
  const root = doc.getElementById('membox-image-root');
  if (!root) return { html, images: [] };

  const images: ClipImageRef[] = [];
  const placeholders = new Map<string, string>();
  root.querySelectorAll('img').forEach((img) => {
    const sourceUrl = imageSource(img, pageUrl);
    if (!sourceUrl) return;

    let placeholder = placeholders.get(sourceUrl);
    if (!placeholder) {
      placeholder = `${PLACEHOLDER_PREFIX}${images.length}`;
      placeholders.set(sourceUrl, placeholder);
      images.push({ placeholder, sourceUrl });
    }
    img.setAttribute('src', placeholder);
    img.removeAttribute('srcset');
    img.removeAttribute('data-srcset');
    for (const attribute of LAZY_SOURCE_ATTRIBUTES) img.removeAttribute(attribute);
  });

  return { html: root.innerHTML, images };
}

function imageSource(img: HTMLImageElement, pageUrl: string): string {
  // Lazy-loader attributes usually hold the original/full-resolution image,
  // while src may still be a transparent pixel or resized placeholder.
  for (const attribute of LAZY_SOURCE_ATTRIBUTES) {
    const resolved = resolveHTTPURL(img.getAttribute(attribute), pageUrl);
    if (resolved) return resolved;
  }

  const src = resolveHTTPURL(img.getAttribute('src'), pageUrl);
  if (src) return src;

  for (const value of [img.getAttribute('data-srcset'), img.getAttribute('srcset')]) {
    const resolved = bestSrcsetURL(value, pageUrl);
    if (resolved) return resolved;
  }

  const picture = img.closest('picture');
  if (picture) {
    for (const source of Array.from(picture.querySelectorAll('source'))) {
      for (const value of [source.getAttribute('data-srcset'), source.getAttribute('srcset')]) {
        const resolved = bestSrcsetURL(value, pageUrl);
        if (resolved) return resolved;
      }
    }
  }
  return '';
}

function bestSrcsetURL(value: string | null, pageUrl: string): string {
  if (!value) return '';
  // The final srcset candidate is conventionally the highest density/width.
  // Work backwards and accept the first valid HTTP(S) resource.
  const candidates = value.split(',').map((candidate) => candidate.trim().split(/\s+/, 1)[0]);
  for (let index = candidates.length - 1; index >= 0; index--) {
    const resolved = resolveHTTPURL(candidates[index], pageUrl);
    if (resolved) return resolved;
  }
  return '';
}

function resolveHTTPURL(raw: string | null | undefined, pageUrl: string): string {
  raw = raw?.trim();
  if (!raw || /^(?:data|blob|javascript):/i.test(raw)) return '';
  try {
    const url = new URL(raw, pageUrl);
    if (url.protocol !== 'http:' && url.protocol !== 'https:') return '';
    // Fragments are never sent to the image server and make identical WeChat
    // resources look distinct (#imgIndex=...).
    url.hash = '';
    return url.href;
  } catch {
    return '';
  }
}
