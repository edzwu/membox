// @vitest-environment happy-dom

import { describe, expect, it } from 'vitest';
import { prepareClipImages, shouldLocalizeClipImages } from './image-assets';
import { htmlToMarkdown } from './markdown';

describe('shouldLocalizeClipImages', () => {
  it('enables binary localization only for WeChat Official Account articles', () => {
    expect(shouldLocalizeClipImages('https://mp.weixin.qq.com/s/D1HIxUJYhMQauPXFM9yxZA')).toBe(true);
    expect(shouldLocalizeClipImages('https://mp.weixin.qq.com/s?__biz=abc&mid=1')).toBe(true);
    expect(shouldLocalizeClipImages('https://mp.weixin.qq.com/mp/profile_ext?action=home')).toBe(false);
    expect(shouldLocalizeClipImages('https://example.com/s/article')).toBe(false);
    expect(shouldLocalizeClipImages('https://substack.com/post')).toBe(false);
  });
});

describe('prepareClipImages', () => {
  it('promotes a WeChat data-src image even when lazy loading left src empty', () => {
    const prepared = prepareClipImages(
      '<p><img data-src="https://mmbiz.qpic.cn/pic/640?wx_fmt=jpeg&amp;from=appmsg#imgIndex=2" alt="图" /></p>',
      'https://mp.weixin.qq.com/s/article',
    );

    expect(prepared.images).toEqual([
      {
        placeholder: 'https://membox.invalid/__clip-image/0',
        sourceUrl: 'https://mmbiz.qpic.cn/pic/640?wx_fmt=jpeg&from=appmsg',
      },
    ]);
    expect(htmlToMarkdown(prepared.html)).toBe(
      '![图](https://membox.invalid/__clip-image/0)',
    );
  });

  it('resolves relative sources, prefers lazy originals, and deduplicates URLs', () => {
    const prepared = prepareClipImages(
      [
        '<img src="data:image/gif;base64,AA" data-original="/images/full.png" alt="one">',
        '<img src="/images/thumb.png" data-src="/images/full.png#second" alt="two">',
      ].join(''),
      'https://example.com/posts/1',
    );

    expect(prepared.images).toHaveLength(1);
    expect(prepared.images[0]?.sourceUrl).toBe('https://example.com/images/full.png');
    const markdown = htmlToMarkdown(prepared.html);
    expect(markdown.match(/https:\/\/membox\.invalid\/__clip-image\/0/g)).toHaveLength(2);
  });

  it('uses the largest srcset candidate when an image has no direct source', () => {
    const prepared = prepareClipImages(
      '<img srcset="small.jpg 320w, large.jpg 1280w" alt="responsive">',
      'https://example.com/article/',
    );

    expect(prepared.images[0]?.sourceUrl).toBe('https://example.com/article/large.jpg');
  });

  it('leaves data and blob images for the serializer safety rules', () => {
    const html = '<img src="data:image/png;base64,AA"><img src="blob:https://example.com/id">';
    const prepared = prepareClipImages(html, 'https://example.com');
    expect(prepared.images).toEqual([]);
    expect(prepared.html).toContain('data:image/png');
  });
});
