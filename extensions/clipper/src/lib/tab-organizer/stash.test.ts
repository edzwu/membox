import { describe, expect, it } from 'vitest';
import {
  entryFromTab,
  importanceOf,
  MAX_STASHED_TABS,
  mergeStashEntries,
  type StashEntry,
  type StashedTab,
} from './stash';
import type { TabInfo } from './tabs';

function entry(url: string, overrides: Partial<StashEntry> = {}): StashEntry {
  return {
    url,
    title: `Title for ${url}`,
    hostname: new URL(url).hostname,
    pinned: false,
    ...overrides,
  };
}

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

describe('entryFromTab', () => {
  function tab(url: string): TabInfo {
    return {
      id: 1,
      title: 'Page',
      url,
      path: '/',
      active: false,
      pinned: false,
      discarded: false,
      windowId: 1,
      groupId: -1,
      hostname: 'example.com',
    };
  }

  it('keeps http(s) tabs and drops browser-internal pages', () => {
    expect(entryFromTab(tab('https://example.com/'))).toMatchObject({
      url: 'https://example.com/',
    });
    expect(entryFromTab(tab('http://example.com/a'))).not.toBeNull();
    expect(entryFromTab(tab('chrome://extensions'))).toBeNull();
    expect(entryFromTab(tab('about:blank'))).toBeNull();
    expect(entryFromTab(tab('chrome-extension://abc/popup.html'))).toBeNull();
  });
});

describe('mergeStashEntries', () => {
  it('appends new URLs with fresh timestamps', () => {
    const merged = mergeStashEntries([], [entry('https://a.com/')], 5000);
    expect(merged).toHaveLength(1);
    expect(merged[0]).toMatchObject({
      url: 'https://a.com/',
      saveCount: 1,
      firstSavedAt: 5000,
      lastSavedAt: 5000,
      lastVisitedAt: 5000,
    });
  });

  it('accumulates: re-stashing a URL bumps count instead of duplicating', () => {
    const first = mergeStashEntries([], [entry('https://a.com/')], 1000);
    const second = mergeStashEntries(first, [entry('https://a.com/', { title: 'New title' })], 2000);
    expect(second).toHaveLength(1);
    expect(second[0]).toMatchObject({
      title: 'New title',
      saveCount: 2,
      firstSavedAt: 1000, // original kept
      lastSavedAt: 2000,
    });
  });

  it('keeps the best history signals across merges', () => {
    const first = mergeStashEntries(
      [],
      [entry('https://a.com/', { visitCount: 30, lastVisitedAt: 9000 })],
      1000,
    );
    // A later stash with worse/no history data must not erase the peak.
    const second = mergeStashEntries(first, [entry('https://a.com/', { visitCount: 3 })], 5000);
    expect(second[0]).toMatchObject({ visitCount: 30, lastVisitedAt: 9000 });
  });

  it('treats missing history data as unknown, not zeroing', () => {
    const merged = mergeStashEntries([], [entry('https://a.com/')], 1000);
    expect(merged[0]).toMatchObject({ visitCount: 0, lastVisitedAt: 1000 });
  });

  it('evicts least-recently-saved rows beyond the cap', () => {
    const existing = Array.from({ length: MAX_STASHED_TABS }, (_, i) =>
      stashed(`https://old.com/${i}`, { lastSavedAt: i }),
    );
    const merged = mergeStashEntries(existing, [entry('https://new.com/')], 999_999);
    expect(merged).toHaveLength(MAX_STASHED_TABS);
    expect(merged.some((t) => t.url === 'https://new.com/')).toBe(true);
    expect(merged.some((t) => t.url === 'https://old.com/0')).toBe(false); // oldest evicted
    expect(merged.some((t) => t.url === 'https://old.com/1')).toBe(true);
  });
});

describe('importanceOf', () => {
  it('weights repeated stashing above casual visits', () => {
    const stashedTwice = stashed('https://a.com/', { saveCount: 2, visitCount: 3 }); // 13
    const visitedALot = stashed('https://b.com/', { saveCount: 1, visitCount: 7 }); // 12
    expect(importanceOf(stashedTwice)).toBeGreaterThan(importanceOf(visitedALot));
  });

  it('caps the visit-count contribution and rewards pinned', () => {
    const cap = stashed('https://a.com/', { visitCount: 10_000 });
    const pinned = stashed('https://b.com/', { pinned: true });
    expect(importanceOf(cap)).toBe(1 * 5 + 50);
    expect(importanceOf(pinned)).toBe(1 * 5 + 10);
  });
});
