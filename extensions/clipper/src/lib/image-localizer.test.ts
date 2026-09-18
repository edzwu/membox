// @vitest-environment happy-dom

import { describe, expect, it, vi } from 'vitest';
import { localizeClipImages, remoteClipImages } from './image-localizer';
import type { BridgeSettings, ClipPayload } from './types';

const settings: BridgeSettings = {
  baseUrl: 'http://127.0.0.1:8787',
  token: 'token',
  autoOpen: true,
};

function clip(): ClipPayload {
  return {
    title: 'Article',
    sourceUrl: 'https://example.com/article',
    body: [
      '![one](https://membox.invalid/__clip-image/0)',
      '![two](https://membox.invalid/__clip-image/1)',
    ].join('\n'),
    images: [
      {
        placeholder: 'https://membox.invalid/__clip-image/0',
        sourceUrl: 'https://cdn.example.com/one.png',
      },
      {
        placeholder: 'https://membox.invalid/__clip-image/1',
        sourceUrl: 'https://cdn.example.com/two.png',
      },
    ],
  };
}

describe('localizeClipImages', () => {
  it('builds an immediate text document with remote image URLs', () => {
    const immediate = remoteClipImages(clip());
    expect(immediate.images).toBeUndefined();
    expect(immediate.body).toContain('https://cdn.example.com/one.png');
    expect(immediate.body).toContain('https://cdn.example.com/two.png');
    expect(immediate.body).not.toContain('membox.invalid');
  });

  it('uploads images and rewrites placeholders to stable local URLs', async () => {
    const download = vi.fn(async (url: string) => new Blob([url], { type: 'image/png' }));
    const upload = vi.fn(async (_settings: BridgeSettings, image: Blob) => ({
      url: `/api/note-assets/${image.size}.png`,
      filename: `${image.size}.png`,
      media_type: 'image/png',
      sha256: 'abc',
      size: image.size,
    }));

    const onLocalized = vi.fn(async () => undefined);
    const result = await localizeClipImages(settings, clip(), { download, upload, onLocalized });

    expect(result.localized).toBe(2);
    expect(result.failed).toBe(0);
    expect(result.clip.images).toBeUndefined();
    expect(result.replacements).toEqual([
      { sourceUrl: 'https://cdn.example.com/one.png', localUrl: '/api/note-assets/31.png' },
      { sourceUrl: 'https://cdn.example.com/two.png', localUrl: '/api/note-assets/31.png' },
    ]);
    expect(result.clip.body).toContain('/api/note-assets/31.png');
    expect(result.clip.body).toContain('/api/note-assets/31.png');
    expect(result.clip.body).not.toContain('membox.invalid');
    expect(download).toHaveBeenCalledTimes(2);
    expect(upload).toHaveBeenCalledTimes(2);
    expect(onLocalized).toHaveBeenCalledTimes(2);
  });

  it('keeps the original remote URL when one image cannot be localized', async () => {
    const download = vi.fn(async (url: string) => {
      if (url.endsWith('two.png')) throw new Error('blocked');
      return new Blob(['image'], { type: 'image/png' });
    });
    const upload = vi.fn(async () => ({
      url: '/api/note-assets/hash.png',
      filename: 'hash.png',
      media_type: 'image/png',
      sha256: 'hash',
      size: 5,
    }));

    const result = await localizeClipImages(settings, clip(), { download, upload });

    expect(result.localized).toBe(1);
    expect(result.failed).toBe(1);
    expect(result.replacements).toEqual([
      { sourceUrl: 'https://cdn.example.com/one.png', localUrl: '/api/note-assets/hash.png' },
    ]);
    expect(result.clip.body).toContain('/api/note-assets/hash.png');
    expect(result.clip.body).toContain('https://cdn.example.com/two.png');
    expect(result.clip.body).not.toContain('membox.invalid');
  });
});
