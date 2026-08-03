/** Normalize page URLs so WeChat share variants match the same article. */
export function normalizeSourceURL(raw: string): string {
  const trimmed = (raw || '').trim();
  if (!trimmed) return '';
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
