// @vitest-environment node

import MarkdownIt from 'markdown-it';
import { describe, expect, it } from 'vitest';
import {
  cjkFriendlyEmphasis,
  configureTechnicalMarkdown,
} from '../../../../internal/web/frontend/miru/js/markdown/cjk-emphasis.js';
import { preprocessMath } from '../../../../internal/web/frontend/miru/js/markdown/math-preprocess.js';
import { preprocessHtml } from '../../../../internal/web/frontend/miru/js/markdown/html-preprocess.js';

describe('Miru CJK emphasis plugin', () => {
  it('renders strong emphasis next to CJK text without patching markdown-it', () => {
    const md = new MarkdownIt().use(cjkFriendlyEmphasis);
    expect(md.renderInline('这是**有状态（stateful）**的服务')).toBe(
      '这是<strong>有状态（stateful）</strong>的服务',
    );
  });

  it('keeps underscore intraword behavior unchanged', () => {
    const md = new MarkdownIt().use(cjkFriendlyEmphasis);
    expect(md.renderInline('foo_bar_baz')).toBe('foo_bar_baz');
  });

  it('keeps technical identifiers when typographer is enabled', () => {
    const md = configureTechnicalMarkdown(new MarkdownIt({ typographer: true }));
    expect(md.renderInline('(c) -- +-')).toBe('(c) -- +-');
  });
});

describe('Miru HTML preprocess tables', () => {
  it('rewrites body-only HTML tables to GFM pipe tables', () => {
    const source = [
      'Intro',
      '',
      '<table><tbody><tr><td>L1 cache reference</td><td>0.5 ns</td></tr>',
      '<tr><td>Send packet CA-&gt;Netherlands-&gt;CA</td><td>150,000,000 ns</td><td>150 ms</td></tr>',
      '</tbody></table>',
      '',
      'Where',
    ].join('\n');

    const out = preprocessHtml(source);
    expect(out).not.toContain('<table');
    expect(out).toContain('| L1 cache reference | 0.5 ns |  |');
    expect(out).toContain('CA->Netherlands->CA');
    expect(out).toContain('| --- | --- | --- |');

    const md = configureTechnicalMarkdown(new MarkdownIt({ html: false }));
    const html = md.render(out);
    expect(html).toContain('<table>');
    expect(html).toContain('L1 cache reference');
    expect(html).toContain('150,000,000 ns');
  });

  it('keeps heading-row tables and simple lists', () => {
    const source = `
<table><tr><th>Name</th><th>Value</th></tr><tr><td>A</td><td>1</td></tr></table>
<ul><li>1 ns = 10<sup>-9</sup> seconds</li><li>1 ms = 10<sup>-3</sup> seconds</li></ul>
`;
    const out = preprocessHtml(source);
    expect(out).toContain('| Name | Value |');
    expect(out).toContain('| A | 1 |');
    expect(out).toContain('- 1 ns = 10^-9 seconds');
    expect(out).toContain('- 1 ms = 10^-3 seconds');
  });
});

describe('Miru explicit math canonicalization', () => {
  it('does not guess that prose parentheses or brackets are math', () => {
    const source = 'By design (refer to [SEP-2575](url)), arrays[x_1] stay prose.';
    expect(preprocessMath(source)).toBe(source);
  });

  it('canonicalizes explicit inline and display dollar delimiters', () => {
    expect(preprocessMath('Value $x_1 + y^2$ here.')).toBe(
      'Value \\(x_1 + y^2\\) here.',
    );
    expect(preprocessMath('$$\nx_1 + y^2\n$$')).toBe(
      '\\[\nx_1 + y^2\n\\]',
    );
  });

  it('leaves fenced and inline code byte-for-byte intact', () => {
    const source = '```md\n$not_math$\n```\n`$also_code$` and $x$';
    expect(preprocessMath(source)).toBe(
      '```md\n$not_math$\n```\n`$also_code$` and \\(x\\)',
    );
  });
});
