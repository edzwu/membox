import { describe, expect, it } from 'vitest';
import { normalizeTab } from './tabs';

function browserTab(overrides: Partial<Browser.tabs.Tab> = {}): Browser.tabs.Tab {
  return {
    id: 7,
    index: 0,
    active: true,
    pinned: false,
    highlighted: false,
    selected: false,
    frozen: false,
    incognito: false,
    windowId: 2,
    discarded: false,
    autoDiscardable: true,
    groupId: -1,
    lastAccessed: Date.now(),
    ...overrides,
  };
}

describe('tab normalization', () => {
  it('extracts searchable metadata and truncates long paths', () => {
    const normalized = normalizeTab(browserTab({
      title: '  Example page  ',
      url: `https://example.com/${'a'.repeat(60)}?mode=read`,
    }));

    expect(normalized).toMatchObject({
      id: 7,
      title: 'Example page',
      hostname: 'example.com',
      active: true,
      windowId: 2,
    });
    expect(normalized?.path).toHaveLength(48);
    expect(normalized?.path.endsWith('...')).toBe(true);
  });

  it('uses pending URLs and safe labels for browser pages', () => {
    expect(normalizeTab(browserTab({ url: undefined, pendingUrl: 'chrome://settings/' })))
      .toMatchObject({ hostname: 'chrome://settings', title: '(untitled)' });
  });

  it('ignores tabs without an id', () => {
    expect(normalizeTab(browserTab({ id: undefined }))).toBeNull();
  });
});
