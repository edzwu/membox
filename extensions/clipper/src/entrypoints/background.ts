import { fetchClipsBySource, ingestClip, IngestConflictError } from '../lib/membox-client';
import { loadSettings } from '../lib/settings';
import { getNotesEnabled } from '../lib/float-notes';
import type { ClipPayload, IngestConflict, IngestResult, SourceClip } from '../lib/types';

type IngestResponse =
  | { ok: true; result: IngestResult }
  | { ok: false; error: string; conflict?: IngestConflict };

async function syncBadge() {
  try {
    const enabled = await getNotesEnabled();
    if (enabled) {
      await browser.action.setBadgeText({ text: 'on' });
      await browser.action.setBadgeBackgroundColor({ color: '#1b365d' });
      await browser.action.setBadgeTextColor?.({ color: '#ffffff' });
    } else {
      await browser.action.setBadgeText({ text: '' });
    }
  } catch {
    /* badge APIs unavailable */
  }
}

export default defineBackground(() => {
  void syncBadge();
  browser.storage.onChanged.addListener((_changes, area) => {
    if (area === 'local') void syncBadge();
  });

  browser.runtime.onMessage.addListener((message, sender) => {
    if (message?.type === 'membox.ingest-active-tab') {
      return ingestActiveTab({ overwrite: message.overwrite === true });
    }
    if (message?.type === 'membox.ingest-payload') {
      return ingestPayload(message.payload as ClipPayload, {
        open: message.open !== false,
        tabId: sender?.tab?.id,
        overwrite: message.overwrite === true,
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
  opts: { open: boolean; tabId?: number; overwrite?: boolean },
): Promise<IngestResponse> {
  try {
    if (!payload?.body?.trim()) {
      return { ok: false, error: 'Empty clip payload' };
    }
    const settings = await loadSettings();
    // A selection note belongs to its page: make sure the page itself is in
    // membox first, so the annotation has a document to be projected onto.
    if (payload.clipMode === 'selection' && payload.sourceUrl && opts.tabId) {
      await ensurePageClip(settings, opts.tabId, payload.sourceUrl);
    }
    const result = await ingestClip(settings, payload, { overwrite: opts.overwrite });
    const shouldOpen = opts.open && settings.autoOpen && result.view_url;
    if (shouldOpen) {
      await browser.tabs.create({ url: result.view_url });
    }
    return { ok: true, result };
  } catch (err) {
    if (err instanceof IngestConflictError) {
      return { ok: false, error: err.message, conflict: err.conflict };
    }
    return { ok: false, error: err instanceof Error ? err.message : String(err) };
  }
}

/**
 * Make sure the current page is saved to membox as a page clip. Best-effort:
 * if clipping the page fails, the selection note is still saved on its own.
 */
async function ensurePageClip(
  settings: Awaited<ReturnType<typeof loadSettings>>,
  tabId: number,
  sourceUrl: string,
): Promise<void> {
  try {
    const clips = await fetchClipsBySource(settings, sourceUrl, 'all');
    if (clips.some((c) => c.clip_mode === 'page')) return; // already saved
    const clip = await clipTab(tabId); // full-page clip (clip_mode: 'page')
    await ingestClip(settings, clip);
  } catch {
    /* best effort — the note itself is saved regardless */
  }
}

async function ingestActiveTab(opts: { overwrite?: boolean } = {}): Promise<IngestResponse> {
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
    return ingestPayload(clip, { open: true, overwrite: opts.overwrite });
  } catch (err) {
    if (err instanceof IngestConflictError) {
      return { ok: false, error: err.message, conflict: err.conflict };
    }
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
