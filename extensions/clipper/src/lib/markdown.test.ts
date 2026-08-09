// @vitest-environment happy-dom

import { describe, expect, it } from 'vitest';
import { htmlToMarkdown } from './markdown';

describe('htmlToMarkdown', () => {
  it('serializes GFM tables and removes executable content', () => {
    const markdown = htmlToMarkdown(`
      <table><tr><th>Name</th><th>Value</th></tr><tr><td>A</td><td>1</td></tr></table>
      <script>window.bad = true</script>
    `);

    expect(markdown).toContain('| Name | Value |');
    expect(markdown).toContain('| A | 1 |');
    expect(markdown).not.toContain('window.bad');
  });

  it('preserves br newlines and chooses a safe code fence', () => {
    const markdown = htmlToMarkdown(
      '<pre><code class="language-markdown">first<br>```nested```<br>last</code></pre>',
    );

    expect(markdown).toContain('first\n```nested```\nlast');
    expect(markdown).toMatch(/^````markdown/m);
    expect(markdown).toMatch(/^````$/m);
  });

  it('serializes standardized math with explicit delimiters', () => {
    const markdown = htmlToMarkdown(
      '<p>Inline <math data-latex="x_1 + y^2"></math>.</p>' +
        '<math display="block" data-latex="E = mc^2"></math>',
    );
    expect(markdown).toContain('$x_1 + y^2$');
    expect(markdown).toContain('$$\nE = mc^2\n$$');
  });

  it('removes heading permalink controls structurally', () => {
    const markdown = htmlToMarkdown(
      '<h2>Section <a class="headerlink" href="#section">¶</a></h2><p>Body</p>',
    );
    expect(markdown).toContain('## Section');
    expect(markdown).not.toContain('headerlink');
    expect(markdown).not.toContain('¶');
  });
});
