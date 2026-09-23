import { listOpenTabs } from './tabs';
import type { TabInfo } from './tabs';

/**
 * Tab stash — OneTab-style persistent record of "tabs worth keeping".
 *
 * Unlike the side panel (a live view of *open* tabs), the stash is an
 * accumulating database in browser.storage.local: pressing "Stash all tabs"
 * snapshots every open http(s) tab; re-stashing the same URL bumps its
 * saveCount and lastSavedAt instead of duplicating the row, so the store
 * reads like history with frequency signals for importance ranking.
 *
 * Enrichment: at stash time each URL is looked up in browser.history for
 * visitCount / lastVisitTime (best effort — the history permission is
 * requested in the manifest, but the stash must still work without it).
 */

export type StashedTab = {
  url: string; // primary key (exact URL)
  title: string;
  hostname: string;
  favIconUrl?: string;
  pinned: boolean;
  firstSavedAt: number;
  lastSavedAt: number;
  saveCount: number;
  visitCount: number; // browser.history visit count, 0 when unknown
  lastVisitedAt: number; // browser.history last visit; falls back to lastSavedAt
};

/** One stash-batch entry before merging — a snapshot of an open tab. */
export type StashEntry = {
  url: string;
  title: string;
  hostname: string;
  favIconUrl?: string;
  pinned: boolean;
  visitCount?: number;
  lastVisitedAt?: number;
};

export type StashStats = {
  total: number; // rows in the store after merging
  added: number; // brand-new URLs
  refreshed: number; // already-known URLs whose counts were bumped
};

// storage.local is ~10 MB; a row is ~300 bytes, so 5k rows stays well under
// quota while covering years of accumulation. Evict least-recently-saved.
export const MAX_STASHED_TABS = 5000;

const STORAGE_KEY = 'membox.tabs.stash';

/** Convert a live open tab into a stash entry; returns null for pages that
 * cannot be usefully re-opened later (chrome://, extension pages, …). */
export function entryFromTab(tab: TabInfo): StashEntry | null {
  if (!/^https?:\/\//.test(tab.url)) return null;
  return {
    url: tab.url,
    title: tab.title,
    hostname: tab.hostname,
    favIconUrl: tab.favIconUrl,
    pinned: tab.pinned,
  };
}

/**
 * Merge one stash batch into the store (pure). Keyed by exact URL:
 * known URLs accumulate saveCount / lastSavedAt and keep the *best* history
 * signals; new URLs are appended. Over-cap stores drop the
 * least-recently-saved rows.
 */
export function mergeStashEntries(
  existing: StashedTab[],
  incoming: StashEntry[],
  now: number,
): StashedTab[] {
  const byUrl = new Map(existing.map((tab) => [tab.url, tab]));

  // Collapse duplicates within the batch first: saveCount measures how many
  // *times* the user stashed a page, so N identical open tabs must count as
  // one stash, not N — otherwise duplicate tabs inflate importance.
  const batch = new Map<string, StashEntry>();
  for (const entry of incoming) {
    const prev = batch.get(entry.url);
    batch.set(entry.url, prev
      ? {
          ...entry,
          title: entry.title || prev.title,
          favIconUrl: entry.favIconUrl ?? prev.favIconUrl,
          pinned: entry.pinned || prev.pinned,
          visitCount: Math.max(prev.visitCount ?? 0, entry.visitCount ?? 0),
          lastVisitedAt: Math.max(prev.lastVisitedAt ?? 0, entry.lastVisitedAt ?? 0),
        }
      : entry);
  }

  for (const entry of batch.values()) {
    const prev = byUrl.get(entry.url);
    if (prev) {
      byUrl.set(entry.url, {
        ...prev,
        title: entry.title || prev.title,
        hostname: entry.hostname || prev.hostname,
        favIconUrl: entry.favIconUrl ?? prev.favIconUrl,
        pinned: entry.pinned,
        lastSavedAt: now,
        saveCount: prev.saveCount + 1,
        visitCount: Math.max(prev.visitCount, entry.visitCount ?? 0),
        lastVisitedAt: Math.max(prev.lastVisitedAt, entry.lastVisitedAt ?? 0),
      });
    } else {
      byUrl.set(entry.url, {
        url: entry.url,
        title: entry.title.trim() || '(untitled)',
        hostname: entry.hostname,
        favIconUrl: entry.favIconUrl,
        pinned: entry.pinned,
        firstSavedAt: now,
        lastSavedAt: now,
        saveCount: 1,
        visitCount: entry.visitCount ?? 0,
        lastVisitedAt: entry.lastVisitedAt ?? now,
      });
    }
  }

  let tabs = [...byUrl.values()];
  if (tabs.length > MAX_STASHED_TABS) {
    tabs = tabs
      .sort((a, b) => b.lastSavedAt - a.lastSavedAt)
      .slice(0, MAX_STASHED_TABS);
  }
  return tabs;
}

/**
 * Importance heuristic for the stash page's sort toggle (pure). Deliberately
 * simple and explainable: how often the user stashed the page dominates,
 * browser visit count is a strong but capped signal, pinned pages were
 * explicitly kept around. Recency is *not* here — that is the other sort
 * mode, mixing it in would blur the contrast between the two rankings.
 */
export function importanceOf(tab: StashedTab): number {
  return tab.saveCount * 5 + Math.min(tab.visitCount, 50) + (tab.pinned ? 10 : 0);
}

/* ── storage + browser glue ──────────────────────────────────────── */

export async function loadStash(): Promise<StashedTab[]> {
  const data = await browser.storage.local.get(STORAGE_KEY);
  const stash = data[STORAGE_KEY] as { tabs?: StashedTab[] } | undefined;
  return stash?.tabs ?? [];
}

async function saveStash(tabs: StashedTab[]): Promise<void> {
  await browser.storage.local.set({ [STORAGE_KEY]: { version: 1, tabs } });
}

/** Snapshot every open http(s) tab into the stash, enriched with
 * browser.history visit stats. Tabs are left open — this records, OneTab's
 * close-on-save is intentionally not the default. */
export async function stashOpenTabs(): Promise<StashStats> {
  const open = await listOpenTabs();
  const entries = (
    await Promise.all(
      open.map(async (tab) => {
        const entry = entryFromTab(tab);
        if (!entry) return null;
        return { ...entry, ...(await visitStats(tab.url)) };
      }),
    )
  ).filter((entry): entry is StashEntry & { visitCount?: number } => entry != null);

  const existing = await loadStash();
  const before = new Set(existing.map((tab) => tab.url));
  const merged = mergeStashEntries(existing, entries, Date.now());
  await saveStash(merged);

  const added = merged.filter((tab) => !before.has(tab.url)).length;
  const distinctUrls = new Set(entries.map((entry) => entry.url));
  const refreshed = [...distinctUrls].filter((url) => before.has(url)).length;
  return { total: merged.length, added, refreshed };
}

export async function removeStashedTab(url: string): Promise<StashedTab[]> {
  const tabs = (await loadStash()).filter((tab) => tab.url !== url);
  await saveStash(tabs);
  return tabs;
}

export async function clearStash(): Promise<void> {
  await browser.storage.local.remove(STORAGE_KEY);
}

/** Subscribe to stash writes (any extension page). Returns an unsubscribe. */
export function onStashChanged(listener: (tabs: StashedTab[]) => void): () => void {
  const handler = (
    changes: Record<string, unknown>,
    area: string,
  ): void => {
    if (area !== 'local' || !(STORAGE_KEY in changes)) return;
    const next = (changes as Record<string, { newValue?: { tabs?: StashedTab[] } }>)[
      STORAGE_KEY
    ]?.newValue;
    listener(next?.tabs ?? []);
  };
  browser.storage.onChanged.addListener(handler);
  return () => browser.storage.onChanged.removeListener(handler);
}

/** Best-effort history enrichment for one URL. An exact URL match in the
 * search result carries visitCount / lastVisitTime; anything failing
 * (missing permission, Firefox quirks) degrades to "unknown". */
async function visitStats(
  url: string,
): Promise<{ visitCount?: number; lastVisitedAt?: number }> {
  try {
    const items = await browser.history.search({ text: url, startTime: 0, maxResults: 50 });
    const exact = items.find((item) => item.url === url);
    if (!exact) return {};
    return {
      visitCount: exact.visitCount ?? 0,
      lastVisitedAt: exact.lastVisitTime ?? 0,
    };
  } catch {
    return {};
  }
}
