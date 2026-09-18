import { uploadClipImage, type StoredClipImage } from './membox-client';
import type {
  BridgeSettings,
  ClipImageReplacement,
  ClipPayload,
} from './types';

const MAX_IMAGE_BYTES = 20 * 1024 * 1024;
const MAX_IMAGES_PER_CLIP = 100;
const DOWNLOAD_CONCURRENCY = 4;
const DOWNLOAD_TIMEOUT_MS = 20_000;

type LocalizerDependencies = {
  download?: (url: string) => Promise<Blob>;
  upload?: (settings: BridgeSettings, image: Blob) => Promise<StoredClipImage>;
  onLocalized?: (replacement: ClipImageReplacement) => Promise<void>;
};

export type LocalizedClip = {
  clip: ClipPayload;
  replacements: ClipImageReplacement[];
  localized: number;
  failed: number;
};

/** Build the fast first-pass document: readable text plus original image URLs. */
export function remoteClipImages(clip: ClipPayload): ClipPayload {
  let body = clip.body;
  for (const ref of clip.images || []) {
    body = replaceAll(body, ref.placeholder, ref.sourceUrl);
  }
  return { ...clip, body, images: undefined };
}

/**
 * Download extracted article images in extension context, upload them to the
 * local content-addressed store, and replace temporary Markdown placeholders.
 * Individual failures fall back to the original remote URL so image trouble
 * never prevents the article itself from being saved.
 */
export async function localizeClipImages(
  settings: BridgeSettings,
  clip: ClipPayload,
  dependencies: LocalizerDependencies = {},
): Promise<LocalizedClip> {
  const refs = clip.images || [];
  if (!refs.length) return { clip, replacements: [], localized: 0, failed: 0 };

  const download = dependencies.download || downloadImage;
  const upload = dependencies.upload || uploadClipImage;
  const onLocalized = dependencies.onLocalized;
  const selected = refs.slice(0, MAX_IMAGES_PER_CLIP);
  const placeholderURLs = new Map<string, string>();
  const replacements: ClipImageReplacement[] = [];
  let localized = 0;
  let failed = refs.length - selected.length;

  await mapConcurrent(selected, DOWNLOAD_CONCURRENCY, async (ref) => {
    try {
      const image = await download(ref.sourceUrl);
      const stored = await upload(settings, image);
      if (!stored.url) throw new Error('membox returned an empty image URL');
      const replacement = { sourceUrl: ref.sourceUrl, localUrl: stored.url };
      if (onLocalized) await onLocalized(replacement);
      placeholderURLs.set(ref.placeholder, stored.url);
      replacements.push(replacement);
      localized++;
    } catch {
      placeholderURLs.set(ref.placeholder, ref.sourceUrl);
      failed++;
    }
  });

  // Clips over the safety limit retain their original remote image URLs.
  for (const ref of refs.slice(MAX_IMAGES_PER_CLIP)) {
    placeholderURLs.set(ref.placeholder, ref.sourceUrl);
  }

  let body = clip.body;
  for (const ref of refs) {
    body = replaceAll(body, ref.placeholder, placeholderURLs.get(ref.placeholder) || ref.sourceUrl);
  }
  return {
    clip: { ...clip, body, images: undefined },
    replacements,
    localized,
    failed,
  };
}

async function downloadImage(url: string): Promise<Blob> {
  let lastError: unknown;
  // Anonymous/no-referrer first is essential for mmbiz.qpic.cn: a localhost
  // Referer returns its 140x140 anti-hotlink placeholder instead of the image.
  for (const credentials of ['omit', 'include'] as RequestCredentials[]) {
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), DOWNLOAD_TIMEOUT_MS);
    try {
      const response = await fetch(url, {
        credentials,
        cache: 'no-store',
        redirect: 'follow',
        referrerPolicy: 'no-referrer',
        signal: controller.signal,
      });
      if (!response.ok) throw new Error(`image download returned HTTP ${response.status}`);
      const mediaType =
        (response.headers.get('Content-Type') || '').split(';', 1)[0]?.trim() || '';
      if (mediaType && !mediaType.startsWith('image/') && mediaType !== 'application/octet-stream') {
        throw new Error(`image download returned ${mediaType}`);
      }
      return await limitedResponseBlob(response, mediaType || 'application/octet-stream');
    } catch (error) {
      lastError = error;
    } finally {
      clearTimeout(timer);
    }
  }
  throw lastError instanceof Error ? lastError : new Error('image download failed');
}

async function limitedResponseBlob(response: Response, mediaType: string): Promise<Blob> {
  const declared = Number(response.headers.get('Content-Length') || '0');
  if (Number.isFinite(declared) && declared > MAX_IMAGE_BYTES) {
    throw new Error(`image exceeds ${MAX_IMAGE_BYTES} bytes`);
  }
  if (!response.body) {
    const blob = await response.blob();
    if (blob.size > MAX_IMAGE_BYTES) throw new Error(`image exceeds ${MAX_IMAGE_BYTES} bytes`);
    return blob;
  }

  const reader = response.body.getReader();
  const chunks: Uint8Array[] = [];
  let size = 0;
  try {
    while (true) {
      const { done, value } = await reader.read();
      if (done) break;
      size += value.byteLength;
      if (size > MAX_IMAGE_BYTES) {
        await reader.cancel();
        throw new Error(`image exceeds ${MAX_IMAGE_BYTES} bytes`);
      }
      chunks.push(value);
    }
  } finally {
    reader.releaseLock();
  }

  const bytes = new Uint8Array(size);
  let offset = 0;
  for (const chunk of chunks) {
    bytes.set(chunk, offset);
    offset += chunk.byteLength;
  }
  return new Blob([bytes.buffer], { type: mediaType });
}

async function mapConcurrent<T>(
  items: T[],
  concurrency: number,
  visit: (item: T) => Promise<void>,
): Promise<void> {
  let cursor = 0;
  const worker = async () => {
    while (cursor < items.length) {
      const index = cursor++;
      await visit(items[index]!);
    }
  };
  await Promise.all(
    Array.from({ length: Math.min(concurrency, items.length) }, () => worker()),
  );
}

function replaceAll(value: string, search: string, replacement: string): string {
  return value.split(search).join(replacement);
}
