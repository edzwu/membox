export type TabInfo = {
  id: number;
  title: string;
  url: string;
  path: string;
  favIconUrl?: string;
  active: boolean;
  pinned: boolean;
  discarded: boolean;
  windowId: number;
  groupId: number;
  hostname: string;
};

function hostnameOf(url: string): string {
  if (!url) return '(empty)';
  try {
    const parsed = new URL(url);
    if (parsed.protocol === 'chrome:' || parsed.protocol === 'chrome-extension:') {
      return `${parsed.protocol}//${parsed.host || 'page'}`;
    }
    if (parsed.protocol === 'about:' || parsed.protocol === 'edge:') {
      return parsed.href.split('?')[0] || parsed.protocol;
    }
    return parsed.hostname || parsed.protocol;
  } catch {
    return '(invalid)';
  }
}

function pathOf(url: string): string {
  if (!url) return '';
  try {
    const parsed = new URL(url);
    const path = `${parsed.pathname || '/'}${parsed.search || ''}`;
    if (path.length <= 48) return path;
    return `${path.slice(0, 45)}...`;
  } catch {
    return url.slice(0, 48);
  }
}

export function normalizeTab(tab: Browser.tabs.Tab): TabInfo | null {
  if (tab.id == null) return null;
  const url = tab.url ?? tab.pendingUrl ?? '';
  return {
    id: tab.id,
    title: tab.title?.trim() || '(untitled)',
    url,
    path: pathOf(url),
    favIconUrl: tab.favIconUrl,
    active: Boolean(tab.active),
    pinned: Boolean(tab.pinned),
    discarded: Boolean(tab.discarded),
    windowId: tab.windowId,
    groupId: tab.groupId ?? -1,
    hostname: hostnameOf(url),
  };
}

export async function listOpenTabs(): Promise<TabInfo[]> {
  const tabs = await browser.tabs.query({});
  return tabs
    .map(normalizeTab)
    .filter((tab): tab is TabInfo => tab != null)
    .sort((a, b) => a.windowId - b.windowId || a.id - b.id);
}

export async function getCurrentWindowId(): Promise<number | undefined> {
  try {
    const win = await browser.windows.getCurrent();
    return win.id;
  } catch {
    return undefined;
  }
}

export async function focusTab(tabId: number): Promise<void> {
  const tab = await browser.tabs.get(tabId);
  if (tab.windowId != null) {
    await browser.windows.update(tab.windowId, { focused: true });
  }
  await browser.tabs.update(tabId, { active: true });
}
