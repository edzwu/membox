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
    throw new Error(text || `ingest failed (${response.status})`);
  }
  return JSON.parse(text) as IngestResult;
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
