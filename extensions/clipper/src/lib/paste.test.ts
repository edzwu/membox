import { describe, expect, it } from 'vitest';
import {
  composePageMarkdown,
  leadFromMarkdown,
  outlineFromMarkdown,
  titleFromMarkdown,
} from './paste';

describe('titleFromMarkdown', () => {
  it('returns the first H1 only', () => {
    expect(titleFromMarkdown('intro\n# Real Title\n## section')).toBe('Real Title');
  });

  it('returns empty when the text has no H1', () => {
    expect(titleFromMarkdown('just prose\n## only H2')).toBe('');
    expect(titleFromMarkdown('')).toBe('');
  });
});

describe('outlineFromMarkdown', () => {
  it('collects ATX headings and skips code fences', () => {
    const text = '# A\n\n```\n# not a heading\n```\n\n### Deep\nparagraph';
    expect(outlineFromMarkdown(text)).toEqual([
      { level: 1, text: 'A' },
      { level: 3, text: 'Deep' },
    ]);
  });
});

describe('leadFromMarkdown', () => {
  it('returns the first prose paragraph, skipping structure', () => {
    const text = '# Title\n\n- bullet\n\n> quote\n\nThe real lead sentence.\n\nmore';
    expect(leadFromMarkdown(text)).toBe('The real lead sentence.');
  });

  it('caps at maxRunes and returns empty for structure-only text', () => {
    expect(leadFromMarkdown('a'.repeat(600))).toHaveLength(480);
    expect(leadFromMarkdown('# only\n- lists\n')).toBe('');
  });
});

describe('composePageMarkdown', () => {
  it('prepends an H1 and appends source provenance', () => {
    const out = composePageMarkdown('My Page', 'Body text.', 'https://a.com/x');
    expect(out).toBe('# My Page\n\nBody text.\n\n---\nSource: https://a.com/x\n');
  });

  it('does not duplicate an H1 the body already has', () => {
    const out = composePageMarkdown('My Page', '# My Page\n\nBody.', 'https://a.com/x');
    expect(out.startsWith('# My Page\n\nBody.')).toBe(true);
    expect(out.match(/^# /gm)).toHaveLength(1);
  });

  it('omits the source line when there is none', () => {
    expect(composePageMarkdown('T', 'Body.', '')).toBe('# T\n\nBody.\n');
  });
});
