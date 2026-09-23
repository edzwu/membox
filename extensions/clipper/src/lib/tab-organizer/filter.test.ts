import { describe, expect, it } from 'vitest';
import { filterTabs, fuzzyMatch, parseQuery } from './filter';
import type { TabInfo } from './tabs';

function tab(overrides: Partial<TabInfo> = {}): TabInfo {
  return {
    id: 1,
    title: 'Membox repository',
    url: 'https://github.com/example/membox/pulls',
    path: '/example/membox/pulls',
    active: false,
    pinned: false,
    discarded: false,
    windowId: 10,
    groupId: -1,
    hostname: 'github.com',
    ...overrides,
  };
}

describe('tab query parsing', () => {
  it('separates structured flags from fuzzy terms', () => {
    expect(parseQuery('review host:github is:pinned win:current')).toEqual({
      raw: 'review host:github is:pinned win:current',
      terms: ['review'],
      host: 'github',
      isPinned: true,
      win: 'current',
    });
  });

  it('keeps unsupported flags as search terms', () => {
    expect(parseQuery('kind:article')).toMatchObject({ terms: ['kind:article'] });
  });
});

describe('tab fuzzy matching', () => {
  it('matches subsequences and returns highlight indices', () => {
    expect(fuzzyMatch('Membox repository', 'mbr')).toMatchObject({ indices: [0, 3, 7] });
    expect(fuzzyMatch('Membox repository', 'xyz')).toBeNull();
  });
});

describe('tab filtering', () => {
  const tabs = [
    tab({ id: 1, pinned: true, active: true }),
    tab({
      id: 2,
      title: 'WXT side panel docs',
      url: 'https://wxt.dev/guide/essentials/entrypoints.html',
      path: '/guide/essentials/entrypoints.html',
      hostname: 'wxt.dev',
      windowId: 20,
    }),
  ];

  it('applies structured filters', () => {
    expect(filterTabs(tabs, 'host:github is:pinned', { currentWindowId: 10 }))
      .toHaveLength(1);
    expect(filterTabs(tabs, 'win:current', { currentWindowId: 20 })[0]?.tab.id).toBe(2);
  });

  it('ranks active tabs in the current window first when unfiltered', () => {
    expect(filterTabs(tabs, '', { currentWindowId: 10 })[0]?.tab.id).toBe(1);
  });

  it('searches title, host, and URL', () => {
    expect(filterTabs(tabs, 'panel')[0]?.tab.id).toBe(2);
    expect(filterTabs(tabs, 'wxt.dev')[0]?.tab.id).toBe(2);
  });
});
