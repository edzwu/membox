import type { TabInfo } from './tabs';

export type QueryFilter = {
  raw: string;
  terms: string[];
  host?: string;
  title?: string;
  url?: string;
  win?: 'current' | number;
  isPinned?: boolean;
  isActive?: boolean;
  isDiscarded?: boolean;
};

export type RankedTab = {
  tab: TabInfo;
  score: number;
  titleIndices: number[];
  urlIndices: number[];
  hostIndices: number[];
};

export type FilterContext = {
  currentWindowId?: number;
};

function parseFlagValue(token: string): { key: string; value: string } | null {
  const idx = token.indexOf(':');
  if (idx <= 0) return null;
  return {
    key: token.slice(0, idx).toLowerCase(),
    value: token.slice(idx + 1),
  };
}

export function parseQuery(raw: string): QueryFilter {
  const filter: QueryFilter = {
    raw,
    terms: [],
  };

  const tokens = raw.trim().split(/\s+/).filter(Boolean);
  for (const token of tokens) {
    const flagged = parseFlagValue(token);
    if (!flagged || !flagged.value) {
      filter.terms.push(token);
      continue;
    }

    const { key, value } = flagged;
    switch (key) {
      case 'host':
      case 'h':
        filter.host = value.toLowerCase();
        break;
      case 'title':
      case 't':
        filter.title = value.toLowerCase();
        break;
      case 'url':
      case 'u':
        filter.url = value.toLowerCase();
        break;
      case 'win':
      case 'w':
        if (value === 'current' || value === '.') {
          filter.win = 'current';
        } else if (/^\d+$/.test(value)) {
          filter.win = Number(value);
        } else {
          filter.terms.push(token);
        }
        break;
      case 'is':
        if (value === 'pinned' || value === 'pin') filter.isPinned = true;
        else if (value === 'active') filter.isActive = true;
        else if (value === 'discarded' || value === 'sleep') filter.isDiscarded = true;
        else filter.terms.push(token);
        break;
      default:
        filter.terms.push(token);
    }
  }

  return filter;
}

/** fzf-like subsequence match; lower score is better. */
export function fuzzyMatch(
  text: string,
  pattern: string,
): { score: number; indices: number[] } | null {
  if (!pattern) return { score: 0, indices: [] };

  const hay = text.toLowerCase();
  const needle = pattern.toLowerCase();
  const indices: number[] = [];

  let pi = 0;
  let score = 0;
  let prev = -2;
  let first = -1;

  for (let i = 0; i < hay.length && pi < needle.length; i += 1) {
    if (hay[i] !== needle[pi]) continue;
    indices.push(i);
    if (first < 0) first = i;
    // Prefer contiguous runs and earlier matches.
    score += i - prev === 1 ? 1 : 4;
    score += first;
    prev = i;
    pi += 1;
  }

  if (pi < needle.length) return null;
  score += Math.max(0, hay.length - needle.length) * 0.01;
  return { score, indices };
}

function mergeIndices(...groups: number[][]): number[] {
  return [...new Set(groups.flat())].sort((a, b) => a - b);
}

function matchesStructured(tab: TabInfo, filter: QueryFilter, ctx: FilterContext): boolean {
  if (filter.host && !tab.hostname.toLowerCase().includes(filter.host)) return false;
  if (filter.title && !tab.title.toLowerCase().includes(filter.title)) return false;
  if (filter.url && !tab.url.toLowerCase().includes(filter.url)) return false;
  if (filter.isPinned && !tab.pinned) return false;
  if (filter.isActive && !tab.active) return false;
  if (filter.isDiscarded && !tab.discarded) return false;

  if (filter.win === 'current') {
    if (ctx.currentWindowId == null || tab.windowId !== ctx.currentWindowId) return false;
  } else if (typeof filter.win === 'number' && tab.windowId !== filter.win) {
    return false;
  }

  return true;
}

export function filterTabs(
  tabs: TabInfo[],
  rawQuery: string,
  ctx: FilterContext = {},
): RankedTab[] {
  const filter = parseQuery(rawQuery);
  const ranked: RankedTab[] = [];

  for (const tab of tabs) {
    if (!matchesStructured(tab, filter, ctx)) continue;

    let score = 0;
    let titleIndices: number[] = [];
    let urlIndices: number[] = [];
    let hostIndices: number[] = [];
    let ok = true;

    for (const term of filter.terms) {
      const inTitle = fuzzyMatch(tab.title, term);
      const inUrl = fuzzyMatch(tab.url, term);
      const inHost = fuzzyMatch(tab.hostname, term);

      if (!inTitle && !inUrl && !inHost) {
        ok = false;
        break;
      }

      // Prefer title hits over url/host.
      const best = [inTitle, inHost, inUrl]
        .filter((m): m is NonNullable<typeof m> => m != null)
        .sort((a, b) => a.score - b.score)[0]!;

      score += best.score;
      if (inTitle) titleIndices = mergeIndices(titleIndices, inTitle.indices);
      if (inUrl) urlIndices = mergeIndices(urlIndices, inUrl.indices);
      if (inHost) hostIndices = mergeIndices(hostIndices, inHost.indices);
    }

    if (!ok) continue;

    // Stable tie-breakers: active current window-ish, then pinned, then id.
    if (tab.active) score -= 50;
    if (tab.pinned) score -= 5;
    if (ctx.currentWindowId != null && tab.windowId === ctx.currentWindowId) score -= 20;

    ranked.push({ tab, score, titleIndices, urlIndices, hostIndices });
  }

  ranked.sort((a, b) => {
    if (a.score !== b.score) return a.score - b.score;
    if (a.tab.windowId !== b.tab.windowId) return a.tab.windowId - b.tab.windowId;
    return a.tab.id - b.tab.id;
  });

  return ranked;
}
