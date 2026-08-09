// @vitest-environment node

import MarkdownIt from 'markdown-it';
import { describe, expect, it } from 'vitest';
import {
  cjkFriendlyEmphasis,
  configureTechnicalMarkdown,
} from '../../../../internal/web/frontend/miru/js/markdown/cjk-emphasis.js';
import { preprocessMath } from '../../../../internal/web/frontend/miru/js/markdown/math-preprocess.js';

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
