import { describe, expect, it } from 'vitest';
import {
  canonicalYouTubeURL,
  currentSourceURL,
  isYouTubeVideoURL,
  normalizeSourceURL,
  youTubeVideoID,
} from './url';

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

  it('canonicalizes YouTube watch URLs to the stable video identity', () => {
    expect(
      youTubeVideoID('https://www.youtube.com/watch?v=dQw4w9WgXcQ&list=PLxx&index=3'),
    ).toBe('dQw4w9WgXcQ');
    expect(youTubeVideoID('https://youtu.be/dQw4w9WgXcQ?t=12')).toBe('dQw4w9WgXcQ');
    expect(youTubeVideoID('https://m.youtube.com/shorts/dQw4w9WgXcQ')).toBe('dQw4w9WgXcQ');
    expect(isYouTubeVideoURL('https://www.youtube.com/playlist?list=PLxx')).toBe(false);
    expect(normalizeSourceURL('https://youtu.be/dQw4w9WgXcQ?list=PLxx')).toBe(
      'https://www.youtube.com/watch?v=dQw4w9WgXcQ',
    );
    expect(canonicalYouTubeURL('https://www.youtube.com/embed/dQw4w9WgXcQ')).toBe(
      'https://www.youtube.com/watch?v=dQw4w9WgXcQ',
    );
  });
});
