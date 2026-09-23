import { fuzzyMatch, parseQuery } from './filter';
import { importanceOf, type StashedTab } from './stash';

/**
 * Filtering and ranking for the stash page. Reuses the side panel's query
 * syntax (bare fuzzy terms + host:/title:/url:/is:pinned); the open-tab-only
 * flags (win:, is:active, is:discarded) parse but simply do not apply to
 * stashed rows.
 */

export type StashSortMode = 'visited' | 'importance';

export type RankedStashedTab = {
  tab: StashedTab;
  titleIndices: number[];
  hostIndices: number[];
};

function mergeIndices(...groups: number[][]): number[] {
  return [...new Set(groups.flat())].sort((a, b) => a - b);
}

/** "Visit time" ranking key: prefer the real browser-history visit, fall
 * back to when the user stashed it. */
export function visitedAt(tab: StashedTab): number {
  return Math.max(tab.lastVisitedAt, tab.lastSavedAt);
}

export function filterStashedTabs(
  tabs: StashedTab[],
  rawQuery: string,
  sort: StashSortMode,
): RankedStashedTab[] {
  const filter = parseQuery(rawQuery);
  const ranked: RankedStashedTab[] = [];

  for (const tab of tabs) {
    if (filter.host && !tab.hostname.toLowerCase().includes(filter.host)) continue;
    if (filter.title && !tab.title.toLowerCase().includes(filter.title)) continue;
    if (filter.url && !tab.url.toLowerCase().includes(filter.url)) continue;
    if (filter.isPinned && !tab.pinned) continue;

    let titleIndices: number[] = [];
    let hostIndices: number[] = [];
    let ok = true;

    for (const term of filter.terms) {
      const inTitle = fuzzyMatch(tab.title, term);
      const inHost = fuzzyMatch(tab.hostname, term);
      const inUrl = fuzzyMatch(tab.url, term);
      if (!inTitle && !inHost && !inUrl) {
        ok = false;
        break;
      }
      if (inTitle) titleIndices = mergeIndices(titleIndices, inTitle.indices);
      if (inHost) hostIndices = mergeIndices(hostIndices, inHost.indices);
    }
    if (!ok) continue;

    ranked.push({ tab, titleIndices, hostIndices });
  }

  ranked.sort((a, b) => {
    if (sort === 'importance') {
      const diff = importanceOf(b.tab) - importanceOf(a.tab);
      if (diff !== 0) return diff;
    }
    // 'visited' primary, and importance's tie-breaker: most recent first.
    const byTime = visitedAt(b.tab) - visitedAt(a.tab);
    if (byTime !== 0) return byTime;
    return a.tab.url.localeCompare(b.tab.url);
  });

  return ranked;
}
