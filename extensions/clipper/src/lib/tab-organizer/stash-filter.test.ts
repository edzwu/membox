import { describe, expect, it } from 'vitest';
import { filterStashedTabs, visitedAt } from './stash-filter';
import type { StashedTab } from './stash';

function stashed(url: string, overrides: Partial<StashedTab> = {}): StashedTab {
  return {
    url,
    title: `Title for ${url}`,
    hostname: new URL(url).hostname,
    pinned: false,
    firstSavedAt: 1000,
    lastSavedAt: 1000,
    saveCount: 1,
    visitCount: 0,
    lastVisitedAt: 1000,
    ...overrides,
  };
}

const TABS: StashedTab[] = [
  stashed('https://github.com/a', {
    title: 'repo A',
    lastVisitedAt: 3000,
    saveCount: 1,
    visitCount: 2,
  }),
  stashed('https://news.ycombinator.com/b', {
    title: 'Hacker News thread',
    lastVisitedAt: 9000,
    saveCount: 1,
    visitCount: 1,
  }),
  stashed('https://github.com/c', {
    title: 'repo C',
    lastVisitedAt: 1000,
    saveCount: 4,
    visitCount: 40,
    pinned: true,
  }),
];

describe('filterStashedTabs sorting', () => {
  it('visited mode ranks by real visit time, newest first', () => {
    const ranked = filterStashedTabs(TABS, '', 'visited');
    expect(ranked.map((r) => r.tab.url)).toEqual([
      'https://news.ycombinator.com/b',
      'https://github.com/a',
      'https://github.com/c',
    ]);
  });

  it('importance mode ranks repeated + pinned + much-visited first', () => {
    const ranked = filterStashedTabs(TABS, '', 'importance');
    expect(ranked[0]!.tab.url).toBe('https://github.com/c');
  });

  it('visitedAt falls back to save time when history never saw the URL', () => {
    const tab = stashed('https://a.com/', { lastVisitedAt: 0, lastSavedAt: 7000 });
    expect(visitedAt(tab)).toBe(7000);
  });
});

describe('filterStashedTabs queries', () => {
  it('fuzzy-matches bare terms against title/host/url', () => {
    const ranked = filterStashedTabs(TABS, 'hacker', 'visited');
    expect(ranked.map((r) => r.tab.url)).toEqual(['https://news.ycombinator.com/b']);
    expect(ranked[0]!.titleIndices.length).toBeGreaterThan(0);
  });

  it('supports host: filters like the side panel', () => {
    const ranked = filterStashedTabs(TABS, 'host:github', 'visited');
    expect(ranked.map((r) => r.tab.url)).toEqual([
      'https://github.com/a',
      'https://github.com/c',
    ]);
  });

  it('supports is:pinned and combines it with terms', () => {
    expect(filterStashedTabs(TABS, 'is:pinned', 'visited').map((r) => r.tab.url)).toEqual([
      'https://github.com/c',
    ]);
    expect(filterStashedTabs(TABS, 'is:pinned news', 'visited')).toEqual([]);
  });
});
