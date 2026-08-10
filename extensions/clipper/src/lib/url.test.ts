import { describe, expect, it } from 'vitest';
import { currentSourceURL, normalizeSourceURL } from './url';

describe('source URLs', () => {
  it('reads the URL supplied at clip time and removes only the fragment', () => {
    expect(
      currentSourceURL('https://cppguide.cn/pages/gopracticeguides01/#%E6%A8%A1%E5%9D%97%E5%92%8C%E5%8C%85'),
    ).toBe('https://cppguide.cn/pages/gopracticeguides01');
  });

  it('does not collapse a page path to its origin', () => {
    expect(normalizeSourceURL('https://cppguide.cn/pages/gopracticeguides01/')).toBe(
      'https://cppguide.cn/pages/gopracticeguides01',
    );
  });
});
