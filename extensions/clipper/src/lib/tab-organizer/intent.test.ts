import { describe, expect, it } from 'vitest';
import { inferCurrentIntent } from './intent';
import type { TabInfo } from './tabs';

function tab(id: number, hostname: string, overrides: Partial<TabInfo> = {}): TabInfo {
  return {
    id,
    title: `Page ${id}`,
    url: `https://${hostname}/${id}`,
    path: `/${id}`,
    active: false,
    pinned: false,
    discarded: false,
    windowId: 1,
    groupId: -1,
    hostname,
    ...overrides,
  };
}

describe('current intent inference', () => {
  it('recognizes a concentrated research host', async () => {
    const intent = await inferCurrentIntent([
      tab(1, 'github.com'),
      tab(2, 'github.com'),
      tab(3, 'github.com'),
      tab(4, 'example.com'),
    ]);

    expect(intent).toMatchObject({
      text: 'Researching github.com across 3 tabs',
      source: 'inferred',
    });
  });

  it('falls back to the active tab title', async () => {
    const intent = await inferCurrentIntent([
      tab(1, 'example.com', { active: true, title: 'A useful article' }),
    ]);
    expect(intent.text).toBe('Reading A useful article');
  });

  it('handles an empty browser session', async () => {
    expect((await inferCurrentIntent([])).text).toBe('Idle — no open tabs');
  });
});
