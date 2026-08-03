import { fetchClipsBySource, ingestClip } from '../lib/membox-client';
import { loadSettings } from '../lib/settings';
import type { ClipPayload, IngestResult, SourceClip } from '../lib/types';

type IngestResponse = { ok: true; result: IngestResult } | { ok: false; error: string };

export default defineBackground(() => {
  browser.runtime.onMessage.addListener((message) => {
    if (message?.type === 'membox.ingest-active-tab') {
      return ingestActiveTab();
    }
    if (message?.type === 'membox.ingest-payload') {
      return ingestPayload(message.payload as ClipPayload, {
        open: message.open !== false,
      });
    }
    if (message?.type === 'membox.clips-for-url') {
      return clipsForUrl(String(message.url || ''));
    }
    return undefined;
  });
});

async function clipsForUrl(
  sourceUrl: string,
): Promise<{ ok: true; clips: SourceClip[] } | { ok: false; error: string }> {
  try {
    if (!sourceUrl) return { ok: false, error: 'missing url' };
    const settings = await loadSettings();
    const clips = await fetchClipsBySource(settings, sourceUrl);
    return { ok: true, clips };
  } catch (err) {
    return { ok: false, error: err instanceof Error ? err.message : String(err) };
  }
}

async function ingestPayload(
  payload: ClipPayload,
  opts: { open: boolean },
): Promise<IngestResponse> {
  try {
    if (!payload?.body?.trim()) {
      return { ok: false, error: 'Empty clip payload' };
    }
    const settings = await loadSettings();
    const result = await ingestClip(settings, payload);
    const shouldOpen = opts.open && settings.autoOpen && result.view_url;
    if (shouldOpen) {
      await browser.tabs.create({ url: result.view_url });
    }
    return { ok: true, result };
  } catch (err) {
    return { ok: false, error: err instanceof Error ? err.message : String(err) };
  }
}

async function ingestActiveTab(): Promise<IngestResponse> {
  try {
    const [tab] = await browser.tabs.query({ active: true, currentWindow: true });
    if (!tab?.id) {
      return { ok: false, error: 'No active tab' };
    }
    const url = tab.url || '';
    if (
      !url ||
      url.startsWith('chrome://') ||
      url.startsWith('chrome-extension://') ||
      url.startsWith('about:') ||
      url.startsWith('edge://') ||
      url.startsWith('moz-extension://') ||
      url.startsWith('devtools://')
    ) {
      return { ok: false, error: 'This page cannot be clipped' };
    }

    const clip = await clipTab(tab.id);
    return ingestPayload(clip, { open: true });
  } catch (err) {
    return { ok: false, error: err instanceof Error ? err.message : String(err) };
  }
}

type ClipResponse =
  | { ok: true; payload: ClipPayload }
  | { ok: false; error: string };

async function clipTab(tabId: number): Promise<ClipPayload> {
  let response = await requestClip(tabId);
  if (!response) {
    await injectContentScript(tabId);
    response = await requestClip(tabId);
  }
  if (!response) {
    throw new Error('Content script did not respond (try reloading the page)');
  }
  if (!response.ok) {
    throw new Error(response.error || 'Clip failed inside the page');
  }
  if (!response.payload?.body?.trim()) {
    throw new Error('Extracted Markdown was empty');
  }
  return response.payload;
}

async function requestClip(tabId: number): Promise<ClipResponse | null> {
  try {
    return (await browser.tabs.sendMessage(tabId, { type: 'membox.clip' })) as ClipResponse;
  } catch {
    return null;
  }
}

async function injectContentScript(tabId: number): Promise<void> {
  await browser.scripting.executeScript({
    target: { tabId },
    files: ['/content-scripts/content.js'],
  });
}
