import type {
  BridgeSettings,
  BridgeStatus,
  ClipPayload,
  IngestConflict,
  IngestResult,
  SourceClip,
} from './types';
import { normalizeSourceURL } from './url';

export class IngestConflictError extends Error {
  readonly conflict: IngestConflict;
  constructor(conflict: IngestConflict) {
    super(conflict.message);
    this.name = 'IngestConflictError';
    this.conflict = conflict;
  }
}

function headers(settings: BridgeSettings, json = false): HeadersInit {
  const h: Record<string, string> = {};
  if (json) h['Content-Type'] = 'application/json';
  if (settings.token) h['X-Membox-Token'] = settings.token;
  return h;
}

export async function fetchBridgeStatus(settings: BridgeSettings): Promise<BridgeStatus> {
  const base = (settings.baseUrl || '').replace(/\/$/, '');
  if (!base) {
    return { connected: false, error: 'Server URL is empty' };
  }
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), 4000);
  try {
    const response = await fetch(`${base}/api/bridge/status`, {
      method: 'GET',
      headers: headers(settings),
      signal: controller.signal,
    });
    if (!response.ok) {
      return { connected: false, error: `HTTP ${response.status}` };
    }
    const data = (await response.json()) as BridgeStatus;
    return { connected: !!data.connected, auth_required: data.auth_required };
  } catch (err) {
    const message = err instanceof Error ? err.message : String(err);
    if (err instanceof DOMException && err.name === 'AbortError') {
      return { connected: false, error: 'timed out after 4s — is membox running on that URL?' };
    }
    return { connected: false, error: message };
  } finally {
    clearTimeout(timer);
  }
}

export async function ingestClip(
  settings: BridgeSettings,
  clip: ClipPayload,
  opts: { overwrite?: boolean } = {},
): Promise<IngestResult> {
  const base = settings.baseUrl.replace(/\/$/, '');
  const response = await fetch(`${base}/api/ingest`, {
    method: 'POST',
    headers: headers(settings, true),
    body: JSON.stringify({
      title: clip.title,
      body: clip.body,
      source_url: normalizeSourceURL(clip.sourceUrl) || clip.sourceUrl,
      clip_mode: clip.clipMode || undefined,
      excerpt_raw: clip.excerptRaw || undefined,
      overwrite: opts.overwrite === true ? true : undefined,
    }),
  });
  const text = await response.text();
  if (response.status === 409) {
    let parsed: {
      error?: { code?: string; message?: string };
      existing?: IngestResult;
    } = {};
    try {
      parsed = JSON.parse(text) as typeof parsed;
    } catch {
      /* plain text */
    }
    if (parsed.error?.code === 'clip_exists' && parsed.existing?.id) {
      throw new IngestConflictError({
        code: 'clip_exists',
        message: parsed.error.message || 'A clip for this URL already exists',
        existing: {
          ...parsed.existing,
          // Server uses view_url in JSON via encoding of ViewURL field — check both.
          view_url:
            parsed.existing.view_url ||
            (parsed.existing as { ViewURL?: string }).ViewURL ||
            '',
        },
      });
    }
    throw new Error(text || 'conflict');
  }
  if (!response.ok) {
    if (response.status === 401) {
      throw new Error(
        'unauthorized: bridge token mismatch.\n' +
          '1. Open ~/.membox/bridge.json\n' +
          '2. Copy "token" into the extension popup\n' +
          '3. Save settings and try again\n' +
          '(TUI must be running so the bridge is up)',
      );
    }
    throw new Error(extractErrorMessage(text, response.status));
  }
  return JSON.parse(text) as IngestResult;
}

/** Prefer a structured error message over dumping raw JSON into the UI. */
function extractErrorMessage(text: string, status: number): string {
  try {
    const parsed = JSON.parse(text) as {
      error?: { message?: string; code?: string };
    };
    if (parsed?.error?.message) return parsed.error.message;
  } catch {
    /* not JSON — use raw text */
  }
  const trimmed = text.trim();
  return trimmed || `request failed (${status})`;
}

export async function fetchClipsBySource(
  settings: BridgeSettings,
  sourceUrl: string,
  mode: 'selection' | 'all' = 'selection',
): Promise<SourceClip[]> {
  const base = settings.baseUrl.replace(/\/$/, '');
  const normalized = normalizeSourceURL(sourceUrl) || sourceUrl;
  const url = `${base}/api/bridge/clips?source_url=${encodeURIComponent(normalized)}&mode=${mode}`;
  const response = await fetch(url, {
    method: 'GET',
    headers: headers(settings),
  });
  const text = await response.text();
  if (!response.ok) {
    if (response.status === 401) {
      throw new Error('unauthorized: bridge token mismatch');
    }
    throw new Error(text || `clips query failed (${response.status})`);
  }
  const data = JSON.parse(text) as { clips?: SourceClip[] };
  return Array.isArray(data.clips) ? data.clips : [];
}

export type VideoTranscriptSegment = {
  start_sec: number;
  end_sec?: number;
  text: string;
};

export type SummarizeVideoArgs = {
  url: string;
  courseCode?: string;
  lectureNo?: number;
  force?: boolean;
  /** Only check membox for an existing summary (no mmd/echo-bp). */
  lookupOnly?: boolean;
  title?: string;
  videoId?: string;
  lang?: string;
  source?: string;
  playlistId?: string;
  playlistTitle?: string;
  playlistIndex?: number;
  model?: string;
  /** Caption Markdown (preferred). Companion stores yt-<id>-transcript.md then summarizes from path. */
  transcriptMarkdown?: string;
  /** @deprecated prefer transcriptMarkdown */
  transcript?: VideoTranscriptSegment[];
};

/**
 * Ask the companion for a YouTube lecture summary via mmd → echo-bp.
 * Idempotent: an existing managed summary for the video_id is returned with
 * reused=true and no echo-bp work. force regenerates artifacts.
 *
 * Prefer passing browser-fetched `transcript` segments so echo-bp does not
 * need yt-dlp (which often times out against YouTube's API).
 */
export async function summarizeVideo(
  settings: BridgeSettings,
  args: SummarizeVideoArgs,
): Promise<IngestResult> {
  const base = settings.baseUrl.replace(/\/$/, '');
  const body: Record<string, unknown> = {
    url: normalizeSourceURL(args.url) || args.url,
  };
  if (args.courseCode) body.course_code = args.courseCode;
  if (args.lectureNo && args.lectureNo > 0) body.lecture_no = args.lectureNo;
  if (args.force) body.force = true;
  if (args.lookupOnly) body.lookup_only = true;
  if (args.title) body.title = args.title;
  if (args.videoId) body.video_id = args.videoId;
  if (args.lang) body.lang = args.lang;
  if (args.source) body.source = args.source;
  if (args.playlistId) body.playlist_id = args.playlistId;
  if (args.playlistTitle) body.playlist_title = args.playlistTitle;
  if (args.playlistIndex && args.playlistIndex > 0) body.playlist_index = args.playlistIndex;
  if (args.model) body.model = args.model;
  if (args.transcriptMarkdown?.trim()) body.transcript_markdown = args.transcriptMarkdown;
  else if (args.transcript?.length) body.transcript = args.transcript;

  const response = await fetch(`${base}/api/video/summary`, {
    method: 'POST',
    headers: headers(settings, true),
    body: JSON.stringify(body),
  });
  const text = await response.text();
  if (!response.ok) {
    if (response.status === 401) {
      throw new Error(
        'unauthorized: bridge token mismatch.\n' +
          '1. Open ~/.membox/bridge.json\n' +
          '2. Copy "token" into the extension popup\n' +
          '3. Save settings and try again\n' +
          '(TUI/companion must be running so the bridge is up)',
      );
    }
    throw new Error(extractErrorMessage(text, response.status));
  }
  const parsed = JSON.parse(text) as IngestResult & {
    view_url?: string;
    ViewURL?: string;
  };
  return {
    ...parsed,
    view_url: parsed.view_url || parsed.ViewURL || '',
  };
}
