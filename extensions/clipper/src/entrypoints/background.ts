import {
  fetchClipsBySource,
  ingestClip,
  IngestConflictError,
  summarizeVideo,
} from '../lib/membox-client';
import { loadSettings } from '../lib/settings';
import { getNotesEnabled } from '../lib/float-notes';
import { isYouTubeVideoURL, normalizeSourceURL, youTubeVideoID } from '../lib/url';
import { captionsToMarkdown, type YouTubeCaptionsResult } from '../lib/youtube-captions';
import {
  clearActiveJob,
  getActiveJob,
  newJobId,
  setActiveJob,
  type ActiveJob,
} from '../lib/jobs';
import type { ClipPayload, IngestConflict, IngestResult, SourceClip } from '../lib/types';

type IngestResponse =
  | { ok: true; result: IngestResult }
  | { ok: true; started: true; jobId: string }
  | { ok: false; error: string; conflict?: IngestConflict; job?: ActiveJob };

async function syncBadge() {
  try {
    const job = await getActiveJob();
    if (job?.status === 'running') {
      await browser.action.setBadgeText({ text: '…' });
      await browser.action.setBadgeBackgroundColor({ color: '#1b365d' });
      return;
    }
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

/** Keep the MV3 service worker alive during long summarize jobs. */
let keepAliveTimer: ReturnType<typeof setInterval> | null = null;
function keepAliveStart() {
  if (keepAliveTimer) return;
  keepAliveTimer = setInterval(() => {
    void browser.storage.session.get('membox.activeJob');
  }, 20_000);
}
function keepAliveStop() {
  if (keepAliveTimer) {
    clearInterval(keepAliveTimer);
    keepAliveTimer = null;
  }
}

export default defineBackground(() => {
  void syncBadge();
  browser.storage.onChanged.addListener((_changes, area) => {
    if (area === 'local' || area === 'session') void syncBadge();
  });

  browser.runtime.onMessage.addListener((message, sender) => {
    if (message?.type === 'membox.ingest-active-tab') {
      return startActiveTabJob({ overwrite: message.overwrite === true });
    }
    if (message?.type === 'membox.job-status') {
      return getActiveJob().then((job) => ({ ok: true as const, job }));
    }
    if (message?.type === 'membox.job-clear') {
      return clearActiveJob().then(() => ({ ok: true as const }));
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
    if (payload.clipMode === 'selection' && payload.sourceUrl && opts.tabId) {
      await ensurePageClip(settings, opts.tabId, payload.sourceUrl);
    }
    const result = await ingestClip(settings, payload, { overwrite: opts.overwrite });
    if (payload.bodyLength != null) result.body_length = payload.bodyLength;
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

async function ensurePageClip(
  settings: Awaited<ReturnType<typeof loadSettings>>,
  tabId: number,
  sourceUrl: string,
): Promise<void> {
  try {
    const clips = await fetchClipsBySource(settings, sourceUrl, 'all');
    if (clips.some((c) => c.clip_mode === 'page')) return;
    const clip = await clipTab(tabId);
    await ingestClip(settings, clip);
  } catch {
    /* best effort */
  }
}

/**
 * Start save/summarize as a background job that outlives the popup.
 * Popup closes no longer cancel the work — progress lives in session storage.
 */
async function startActiveTabJob(opts: { overwrite?: boolean } = {}): Promise<IngestResponse> {
  const running = await getActiveJob();
  if (running?.status === 'running') {
    return { ok: false, error: 'A save is already running', job: running };
  }

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

  const youtube = isYouTubeVideoURL(url);
  // Fast page clips stay synchronous (seconds). YouTube summarize is long —
  // run detached so closing the popup cannot abort it.
  if (!youtube) {
    try {
      await updateJobProgress({
        id: newJobId(),
        kind: 'page',
        status: 'running',
        label: 'Extracting page…',
        pct: 30,
        url,
      });
      const clip = await clipTab(tab.id);
      await updateJobProgress({ label: 'Saving…', pct: 70 });
      const result = await ingestPayload(clip, { open: true, overwrite: opts.overwrite });
      if (!result.ok) {
        await finishJobError(result.error || 'Save failed');
        return result;
      }
      if ('result' in result && result.result) {
        await finishJobDone(result.result);
      }
      return result;
    } catch (err) {
      if (err instanceof IngestConflictError) {
        await clearActiveJob();
        return { ok: false, error: err.message, conflict: err.conflict };
      }
      const msg = err instanceof Error ? err.message : String(err);
      await finishJobError(msg);
      return { ok: false, error: msg };
    }
  }

  const jobId = newJobId();
  await updateJobProgress({
    id: jobId,
    kind: 'youtube',
    status: 'running',
    label: 'Starting…',
    pct: 5,
    url,
  });

  // Detached: do not await inside the message handler's returned promise chain
  // beyond this point — popup can close immediately after receiving started.
  void runYouTubeJob(jobId, url, {
    open: true,
    force: opts.overwrite === true,
    tabId: tab.id,
  });

  return { ok: true, started: true, jobId };
}

async function updateJobProgress(patch: Partial<ActiveJob> & { id?: string; kind?: ActiveJob['kind'] }) {
  const cur = (await getActiveJob()) || {
    id: patch.id || newJobId(),
    kind: patch.kind || 'youtube',
    status: 'running' as const,
    label: 'Working…',
    pct: null as number | null,
    updatedAt: Date.now(),
  };
  await setActiveJob({
    ...cur,
    ...patch,
    status: patch.status || cur.status || 'running',
    label: patch.label ?? cur.label,
    pct: patch.pct !== undefined ? patch.pct : cur.pct,
  });
}

async function finishJobDone(result: IngestResult) {
  keepAliveStop();
  const cur = await getActiveJob();
  await setActiveJob({
    id: cur?.id || newJobId(),
    kind: cur?.kind || 'youtube',
    status: 'done',
    label: 'Done',
    pct: 100,
    url: cur?.url,
    title: result.title,
    result,
    updatedAt: Date.now(),
  });
}

async function finishJobError(error: string) {
  keepAliveStop();
  const cur = await getActiveJob();
  await setActiveJob({
    id: cur?.id || newJobId(),
    kind: cur?.kind || 'youtube',
    status: 'error',
    label: 'Failed',
    pct: null,
    url: cur?.url,
    error,
    updatedAt: Date.now(),
  });
}

async function runYouTubeJob(
  jobId: string,
  url: string,
  opts: { open: boolean; force?: boolean; tabId: number },
) {
  keepAliveStart();
  try {
    const settings = await loadSettings();
    const videoId = youTubeVideoID(url);

    if (!opts.force) {
      await updateJobProgress({ id: jobId, label: 'Checking library…', pct: 12 });
      try {
        const existing = await summarizeVideo(settings, {
          url,
          videoId: videoId || undefined,
          lookupOnly: true,
        });
        if (existing.reused && existing.view_url) {
          await updateJobProgress({ label: 'Opening…', pct: 90, title: existing.title });
          if (opts.open && settings.autoOpen) {
            await browser.tabs.create({ url: existing.view_url });
          }
          await finishJobDone(existing);
          return;
        }
      } catch {
        /* continue */
      }
    }

    if (!videoId) {
      await finishJobError('Not a YouTube video URL');
      return;
    }

    await updateJobProgress({ label: 'Fetching captions…', pct: 28 });
    const captions = await fetchYouTubeCaptions(opts.tabId, videoId);
    const transcriptMarkdown = captionsToMarkdown(captions);
    await updateJobProgress({
      label: 'Summarizing with deepseek…',
      pct: 48,
      title: captions.title,
    });

    const pulse = setInterval(() => {
      void updateJobProgress({ label: 'Summarizing with deepseek…', pct: null });
    }, 5000);

    let result: IngestResult;
    try {
      result = await summarizeVideo(settings, {
        url,
        force: opts.force === true,
        videoId: captions.videoId || videoId,
        title: captions.title,
        lang: captions.lang,
        source: 'browser',
        playlistId: captions.playlistId,
        playlistIndex: captions.playlistIndex,
        transcriptMarkdown,
      });
    } finally {
      clearInterval(pulse);
    }

    await updateJobProgress({ label: 'Opening…', pct: 92, title: result.title || captions.title });
    if (opts.open && settings.autoOpen && result.view_url) {
      await browser.tabs.create({ url: result.view_url });
    }
    await finishJobDone(result);
  } catch (err) {
    await finishJobError(err instanceof Error ? err.message : String(err));
  } finally {
    keepAliveStop();
  }
}

type CaptionsResponse =
  | { ok: true; captions: YouTubeCaptionsResult }
  | { ok: false; error: string };

async function fetchYouTubeCaptions(tabId: number, videoId: string): Promise<YouTubeCaptionsResult> {
  let response = await requestYouTubeCaptions(tabId, videoId);
  if (!response) {
    await injectContentScript(tabId);
    response = await requestYouTubeCaptions(tabId, videoId);
  }
  if (!response) {
    throw new Error('Content script did not respond for captions (try reloading the page)');
  }
  if (!response.ok) {
    throw new Error(response.error || 'Caption extraction failed');
  }
  if (!response.captions?.segments?.length) {
    throw new Error('No caption segments extracted');
  }
  return response.captions;
}

async function requestYouTubeCaptions(
  tabId: number,
  videoId: string,
): Promise<CaptionsResponse | null> {
  try {
    return (await browser.tabs.sendMessage(tabId, {
      type: 'membox.youtube-captions',
      videoId,
    })) as CaptionsResponse;
  } catch {
    return null;
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

  const tab = await browser.tabs.get(tabId);
  const expected = normalizeSourceURL(tab.url || '');
  const actual = normalizeSourceURL(response.payload.sourceUrl || '');
  if (expected && actual !== expected) {
    throw new Error(
      `Page changed while clipping; reload the page and try again (expected ${expected}, got ${actual || 'no source URL'})`,
    );
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
