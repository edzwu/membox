import type { BridgeSettings, BridgeStatus, ClipPayload, IngestResult, SourceClip } from './types';
import { normalizeSourceURL } from './url';

function headers(settings: BridgeSettings, json = false): HeadersInit {
  const h: Record<string, string> = {};
  if (json) h['Content-Type'] = 'application/json';
  if (settings.token) h['X-Membox-Token'] = settings.token;
  return h;
}

export async function fetchBridgeStatus(settings: BridgeSettings): Promise<BridgeStatus> {
  const base = settings.baseUrl.replace(/\/$/, '');
  try {
    const response = await fetch(`${base}/api/bridge/status`, {
      method: 'GET',
      headers: headers(settings),
    });
    if (!response.ok) {
      return { connected: false, error: `HTTP ${response.status}` };
    }
    const data = (await response.json()) as BridgeStatus;
    return { connected: !!data.connected, auth_required: data.auth_required };
  } catch (err) {
    return {
      connected: false,
      error: err instanceof Error ? err.message : String(err),
    };
  }
}

export async function ingestClip(
  settings: BridgeSettings,
  clip: ClipPayload,
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
    }),
  });
  const text = await response.text();
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
