/** True when the page is a local membox Miru reader view (?id=<doc>). */
export function isMemboxReaderUrl(href = location.href): boolean {
  try {
    const u = new URL(href);
    const host = u.hostname;
    const local = host === '127.0.0.1' || host === 'localhost';
    return local && Boolean(new URLSearchParams(u.search).get('id'));
  } catch {
    return false;
  }
}

/** Return the URL of the document currently shown in the content-script page. */
export function currentSourceURL(href = globalThis.location?.href || ''): string {
  return normalizeSourceURL(href) || href;
}

function youtubeHost(hostname: string): string {
  return hostname.toLowerCase().replace(/^(www|m)\./, '');
}

/** True when the URL is a single-video YouTube watch/shorts/embed/live page. */
export function isYouTubeVideoURL(raw: string): boolean {
  return Boolean(youTubeVideoID(raw));
}

/** Extract the YouTube video id from common watch/share URL forms. */
export function youTubeVideoID(raw: string): string {
  const trimmed = (raw || '').trim();
  if (!trimmed) return '';
  try {
    const u = new URL(trimmed);
    const host = youtubeHost(u.hostname);
    if (host === 'youtu.be') {
      const id = u.pathname.split('/').filter(Boolean)[0] || '';
      return sanitizeYouTubeID(id);
    }
    if (host === 'youtube.com' || host === 'youtube-nocookie.com') {
      if (u.pathname === '/watch' || u.pathname.startsWith('/watch')) {
        return sanitizeYouTubeID(u.searchParams.get('v') || '');
      }
      const m = u.pathname.match(/^\/(?:shorts|embed|live)\/([^/?#]+)/);
      if (m) return sanitizeYouTubeID(m[1] || '');
    }
  } catch {
    /* not a URL */
  }
  return '';
}

function sanitizeYouTubeID(id: string): string {
  const trimmed = (id || '').trim();
  if (!trimmed || /[/?&#]/.test(trimmed)) return '';
  return trimmed;
}

/** Canonical watch URL used as the source identity for a YouTube video. */
export function canonicalYouTubeURL(raw: string): string {
  const id = youTubeVideoID(raw);
  return id ? `https://www.youtube.com/watch?v=${id}` : '';
}

/** Normalize page URLs so WeChat share variants match the same article. */
export function normalizeSourceURL(raw: string): string {
  const trimmed = (raw || '').trim();
  if (!trimmed) return '';
  const yt = canonicalYouTubeURL(trimmed);
  if (yt) return yt;
  try {
    const u = new URL(trimmed);
    u.hash = '';
    u.hostname = u.hostname.toLowerCase();
    const host = u.hostname;
    const isWeixin =
      host.includes('weixin.qq.com') ||
      (host.includes('qq.com') && u.pathname.startsWith('/s'));
    if (isWeixin) {
      u.search = '';
    } else {
      const drop = [
        'from',
        'scene',
        'sessionid',
        'ascene',
        'devicetype',
        'version',
        'nettype',
        'abtest_cookie',
        'wx_header',
        'poc_token',
        'srcid',
        'sharer_shareinfo',
        'sharer_shareinfo_first',
        'mpshare',
        'clicktime',
        'enterid',
        'exportkey',
        'pass_ticket',
        'uin',
        'key',
        'wxtoken',
      ];
      for (const key of drop) u.searchParams.delete(key);
    }
    let path = u.pathname || '/';
    if (path.length > 1 && path.endsWith('/')) path = path.slice(0, -1);
    u.pathname = path;
    return u.toString();
  } catch {
    return trimmed.replace(/\/$/, '');
  }
}
