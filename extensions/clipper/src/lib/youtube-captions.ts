/**
 * Browser-side YouTube caption extraction (ported from ~/repo/echo extension).
 *
 * Uses the live page session (cookies + ytInitialPlayerResponse / InnerTube /
 * timedtext) so we avoid yt-dlp API timeouts that hit server-side downloads.
 */

export type CaptionChunk = {
  start_sec: number;
  end_sec: number;
  text: string;
};

export type YouTubeCaptionsResult = {
  videoId: string;
  title: string;
  url: string;
  lang: string;
  source: 'page' | 'innertube' | 'texttracks' | 'timedtext' | 'browser';
  playlistId?: string;
  playlistIndex?: number;
  segments: CaptionChunk[];
};

function yamlQuote(value: string): string {
  return `"${value.replace(/\\/g, '\\\\').replace(/"/g, '\\"').replace(/\n/g, ' ')}"`;
}

function formatTimestamp(seconds: number): string {
  const total = Math.max(0, Math.floor(seconds || 0));
  const h = Math.floor(total / 3600);
  const m = Math.floor((total % 3600) / 60);
  const s = total % 60;
  return `[${String(h).padStart(2, '0')}:${String(m).padStart(2, '0')}:${String(s).padStart(2, '0')}]`;
}

/** echo-bp / membox caption Markdown: frontmatter + timed lines. */
export function captionsToMarkdown(captions: YouTubeCaptionsResult): string {
  const title = (captions.title || captions.videoId).trim();
  const lines = [
    '---',
    'kind: youtube-transcript',
    'managed_by: membox-clipper',
    `video_id: ${yamlQuote(captions.videoId)}`,
    `title: ${yamlQuote(title)}`,
    `source_url: ${yamlQuote(captions.url)}`,
    `lang: ${yamlQuote(captions.lang || 'en')}`,
    `source: ${yamlQuote(captions.source || 'browser')}`,
    `segment_count: ${captions.segments.length}`,
    '---',
    '',
    `# ${title}`,
    '',
  ];
  for (const seg of captions.segments) {
    const text = (seg.text || '').replace(/\s+/g, ' ').trim();
    if (!text) continue;
    lines.push(`${formatTimestamp(seg.start_sec)} ${text}`);
  }
  lines.push('');
  return lines.join('\n');
}

function decodeHtmlEntities(text: string): string {
  const el = document.createElement('textarea');
  el.innerHTML = text;
  return el.value;
}

function extractYtInitialPlayerResponse(): any | null {
  try {
    const scripts = Array.from(document.querySelectorAll('script'));
    for (const script of scripts) {
      const text = script.textContent || '';
      const marker = 'ytInitialPlayerResponse';
      const idx = text.indexOf(marker);
      if (idx < 0) continue;
      const eq = text.indexOf('=', idx);
      if (eq < 0) continue;
      let i = eq + 1;
      while (i < text.length && /\s/.test(text[i]!)) i += 1;
      if (text[i] !== '{') continue;
      let depth = 0;
      let end = -1;
      for (let j = i; j < text.length; j++) {
        const ch = text[j];
        if (ch === '{') depth += 1;
        else if (ch === '}') {
          depth -= 1;
          if (depth === 0) {
            end = j + 1;
            break;
          }
        }
      }
      if (end < 0) continue;
      try {
        return JSON.parse(text.slice(i, end));
      } catch {
        /* try next script */
      }
    }
  } catch {
    /* ignore */
  }
  return null;
}

function getCaptionTracksFromPage(): any[] {
  try {
    const ytd = (window as any).ytInitialPlayerResponse;
    const tracks = ytd?.captions?.playerCaptionsTracklistRenderer?.captionTracks;
    if (tracks?.length) return tracks;
  } catch {
    /* isolated world may not see window binding */
  }
  try {
    const data = extractYtInitialPlayerResponse();
    const tracks = data?.captions?.playerCaptionsTracklistRenderer?.captionTracks;
    if (tracks?.length) return tracks;
  } catch {
    /* ignore */
  }
  return [];
}

async function waitForCaptionTracks(maxAttempts = 20): Promise<any[]> {
  for (let i = 0; i < maxAttempts; i++) {
    const tracks = getCaptionTracksFromPage();
    if (tracks.length > 0) return tracks;
    await new Promise((r) => setTimeout(r, 400));
  }
  return [];
}

function pickTrack(tracks: any[]): any | null {
  if (!tracks.length) return null;
  return (
    tracks.find((t) => t.languageCode === 'en' && t.kind !== 'asr') ||
    tracks.find((t) => t.languageCode?.startsWith?.('en') && t.kind !== 'asr') ||
    tracks.find((t) => t.kind === 'asr' && t.languageCode?.startsWith?.('en')) ||
    tracks.find((t) => t.kind === 'asr') ||
    tracks.find((t) => t.languageCode?.startsWith?.('zh')) ||
    tracks[0]
  );
}

function parseTranscriptXml(xml: string): CaptionChunk[] {
  const chunks: CaptionChunk[] = [];
  const parser = new DOMParser();
  const doc = parser.parseFromString(xml, 'text/xml');

  const textEls = doc.querySelectorAll('text');
  if (textEls.length > 0) {
    textEls.forEach((el) => {
      const start = parseFloat(el.getAttribute('start') || '0');
      const dur = parseFloat(el.getAttribute('dur') || '0');
      const text = decodeHtmlEntities(el.textContent || '').replace(/\s+/g, ' ').trim();
      if (text) chunks.push({ start_sec: start, end_sec: start + dur, text });
    });
    return chunks;
  }

  const pEls = doc.querySelectorAll('p');
  if (pEls.length > 0) {
    pEls.forEach((el) => {
      const startMs = parseInt(el.getAttribute('t') || '0', 10);
      const durMs = parseInt(el.getAttribute('d') || '0', 10);
      const start = startMs / 1000;
      const dur = durMs / 1000;
      const text = decodeHtmlEntities(el.textContent || '').replace(/\s+/g, ' ').trim();
      if (text) chunks.push({ start_sec: start, end_sec: start + dur, text });
    });
  }
  return chunks;
}

function parseJson3(json: any): CaptionChunk[] {
  const chunks: CaptionChunk[] = [];
  for (const ev of json?.events || []) {
    const start = (ev.tStartMs || 0) / 1000;
    const dur = (ev.dDurationMs || 0) / 1000;
    const text = (ev.segs || [])
      .map((s: any) => s.utf8 || '')
      .join('')
      .replace(/\s+/g, ' ')
      .trim();
    if (text) chunks.push({ start_sec: start, end_sec: start + dur, text });
  }
  return chunks;
}

async function fetchTrackChunks(track: any): Promise<CaptionChunk[]> {
  const baseUrl = String(track.baseUrl || '');
  if (!baseUrl) return [];
  const response = await fetch(baseUrl, { credentials: 'include' });
  if (!response.ok) throw new Error(`caption track HTTP ${response.status}`);
  const body = await response.text();
  if (!body.trim().startsWith('<')) {
    const jsonUrl = baseUrl + (baseUrl.includes('?') ? '&' : '?') + 'fmt=json3';
    const jsonResp = await fetch(jsonUrl, { credentials: 'include' });
    if (!jsonResp.ok) throw new Error(`caption json3 HTTP ${jsonResp.status}`);
    return parseJson3(await jsonResp.json());
  }
  return parseTranscriptXml(body);
}

async function fetchCaptionTracksViaInnerTube(videoId: string): Promise<any[]> {
  try {
    const html = document.documentElement.outerHTML;
    const apiKeyMatch = html.match(/"INNERTUBE_API_KEY":"([^"]+)"/);
    if (!apiKeyMatch) return [];
    const apiKey = apiKeyMatch[1];
    const response = await fetch(`https://www.youtube.com/youtubei/v1/player?key=${apiKey}`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      credentials: 'include',
      body: JSON.stringify({
        context: { client: { clientName: 'ANDROID', clientVersion: '20.10.38' } },
        videoId,
      }),
    });
    if (!response.ok) return [];
    const data = await response.json();
    return data?.captions?.playerCaptionsTracklistRenderer?.captionTracks || [];
  } catch {
    return [];
  }
}

async function extractFromTextTracks(): Promise<CaptionChunk[]> {
  const video = document.querySelector('video') as HTMLVideoElement | null;
  if (!video) return [];
  for (let attempt = 0; attempt < 10; attempt++) {
    for (const track of Array.from(video.textTracks)) {
      if (track.kind !== 'captions' && track.kind !== 'subtitles') continue;
      if (track.cues && track.cues.length > 0) {
        const chunks: CaptionChunk[] = [];
        for (const cue of Array.from(track.cues)) {
          const text = String((cue as any).text || '')
            .replace(/<[^>]+>/g, '')
            .replace(/\s+/g, ' ')
            .trim();
          if (text) {
            chunks.push({
              start_sec: cue.startTime,
              end_sec: cue.endTime,
              text,
            });
          }
        }
        if (chunks.length) return chunks;
      }
    }
    await new Promise((r) => setTimeout(r, 400));
  }
  return [];
}

function pageTitle(): string {
  const h1 = document.querySelector(
    'h1.ytd-watch-metadata yt-formatted-string, h1 yt-formatted-string, h1.title',
  );
  const fromDom = (h1?.textContent || '').trim();
  if (fromDom) return fromDom;
  return (document.title || '').replace(/\s*-\s*YouTube\s*$/i, '').trim();
}

function playlistMeta(href: string): { playlistId?: string; playlistIndex?: number } {
  try {
    const u = new URL(href);
    const playlistId = u.searchParams.get('list') || undefined;
    const raw = u.searchParams.get('index');
    const playlistIndex = raw ? parseInt(raw, 10) : undefined;
    return {
      playlistId,
      playlistIndex: Number.isFinite(playlistIndex) ? playlistIndex : undefined,
    };
  } catch {
    return {};
  }
}

/**
 * Extract captions for the current YouTube watch page. Must run in the page's
 * content-script world (has DOM + cookie jar).
 */
export async function extractYouTubeCaptions(
  videoId: string,
  pageUrl = location.href,
): Promise<YouTubeCaptionsResult> {
  if (!videoId) throw new Error('video_id is required');
  const title = pageTitle() || videoId;
  const url = pageUrl.startsWith('http')
    ? pageUrl
    : `https://www.youtube.com/watch?v=${videoId}`;
  const { playlistId, playlistIndex } = playlistMeta(url);

  // Strategy 1: page player response tracks
  try {
    const tracks = await waitForCaptionTracks(20);
    const track = pickTrack(tracks);
    if (track) {
      const segments = await fetchTrackChunks(track);
      if (segments.length) {
        return {
          videoId,
          title,
          url,
          lang: String(track.languageCode || 'en'),
          source: 'page',
          playlistId,
          playlistIndex,
          segments,
        };
      }
    }
  } catch {
    /* fall through */
  }

  // Strategy 2: InnerTube (youtube-transcript-api style)
  try {
    const tracks = await fetchCaptionTracksViaInnerTube(videoId);
    const track = pickTrack(tracks);
    if (track) {
      const segments = await fetchTrackChunks(track);
      if (segments.length) {
        return {
          videoId,
          title,
          url,
          lang: String(track.languageCode || 'en'),
          source: 'innertube',
          playlistId,
          playlistIndex,
          segments,
        };
      }
    }
  } catch {
    /* fall through */
  }

  // Strategy 3: HTML5 textTracks
  const textTrackSubs = await extractFromTextTracks();
  if (textTrackSubs.length) {
    return {
      videoId,
      title,
      url,
      lang: 'en',
      source: 'texttracks',
      playlistId,
      playlistIndex,
      segments: textTrackSubs,
    };
  }

  // Strategy 4: timedtext API
  for (const kind of ['', 'asr']) {
    try {
      for (const lang of ['en', 'en-US', 'zh-Hans', 'zh-CN', 'zh-Hant', 'zh-TW']) {
        const qs = new URLSearchParams({ v: videoId, lang });
        if (kind) qs.set('kind', kind);
        const resp = await fetch(`https://www.youtube.com/api/timedtext?${qs}`, {
          credentials: 'include',
        });
        if (!resp.ok) continue;
        const chunks = parseTranscriptXml(await resp.text());
        if (chunks.length) {
          return {
            videoId,
            title,
            url,
            lang,
            source: 'timedtext',
            playlistId,
            playlistIndex,
            segments: chunks,
          };
        }
      }
    } catch {
      /* try next */
    }
  }

  throw new Error('No captions available for this video (page/InnerTube/textTracks/timedtext all empty)');
}
