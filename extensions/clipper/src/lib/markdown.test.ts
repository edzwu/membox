// @vitest-environment happy-dom

import { describe, expect, it } from 'vitest';
import { htmlToMarkdown, isLatexMathElement, looksLikeMermaid } from './markdown';

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

  it('recognizes lowercase MathML namespace elements used by Chromium', () => {
    const math = document.createElementNS('http://www.w3.org/1998/Math/MathML', 'math');
    math.setAttribute('data-latex', 'x_1');
    expect(math.nodeName).toBe('math');
    expect(isLatexMathElement(math)).toBe(true);
  });

  it('serializes standardized math with explicit delimiters', () => {
    const markdown = htmlToMarkdown(
      '<p>Inline <math data-latex="x_1 + y^2"></math>.</p>' +
        '<math display="block" data-latex="E = mc^2"></math>',
    );
    expect(markdown).toContain('$x_1 + y^2$');
    expect(markdown).toContain('$$\nE = mc^2\n$$');
  });

  it('collapses KaTeX MathML, TeX, and visual HTML to one inline formula', () => {
    const markdown = htmlToMarkdown(String.raw`
      <p>A retrieval problem consists of
        <span><span class="katex">
          <span class="katex-mathml"><math><semantics><mrow><mi>m</mi></mrow>
            <annotation encoding="application/x-tex">m</annotation>
          </semantics></math></span>
          <span class="katex-html" aria-hidden="true"><span>m</span></span>
        </span></span>
        queries and
        <span class="katex">
          <span class="katex-mathml"><math><semantics><mrow><mi>A</mi></mrow>
            <annotation encoding="application/x-tex">A \in \{0, 1\}^{m \times n}</annotation>
          </semantics></math></span>
          <span class="katex-html" aria-hidden="true">A VISIBLE DUPLICATE</span>
        </span>.
      </p>
    `);

    expect(markdown).toContain('consists of $m$ queries');
    expect(markdown).toContain(String.raw`$A \in \{0, 1\}^{m \times n}$`);
    expect(markdown).not.toContain('m m');
    expect(markdown).not.toContain('VISIBLE DUPLICATE');
  });

  it('preserves KaTeX display equations as explicit math blocks', () => {
    const markdown = htmlToMarkdown(String.raw`
      <p>The relationship is:</p>
      <div class="group/math-block"><span class="katex-display"><span class="katex">
        <span class="katex-mathml"><math display="block"><semantics><mrow></mrow>
          <annotation encoding="application/x-tex">B = U^T V</annotation>
        </semantics></math></span>
        <span class="katex-html" aria-hidden="true">B VISUAL DUPLICATE</span>
      </span></span></div>
    `);

    expect(markdown).toContain('$$\nB = U^T V\n$$');
    expect(markdown).not.toContain('VISUAL DUPLICATE');
  });

  it('extracts TeX annotations from standalone MathML', () => {
    const markdown = htmlToMarkdown(String.raw`<p>Value <math><semantics><mrow><mi>x</mi></mrow><annotation encoding="application/x-tex">x_1</annotation></semantics></math>.</p>`);
    expect(markdown).toContain('Value $x_1$.');
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

describe('looksLikeMermaid', () => {
  it('matches text that starts with a diagram keyword', () => {
    expect(looksLikeMermaid('flowchart TD\n  A --> B')).toBe(true);
    expect(looksLikeMermaid('  \n stateDiagram-v2\n  [*] --> Idle')).toBe(true);
  });

  it('rejects prose that mentions a keyword mid-line', () => {
    expect(looksLikeMermaid('# Notes\n\nUse flowchart TD for the diagram.')).toBe(false);
  });

  // Regression: a raw text page arrives as ONE giant <pre>. Its body may
  // contain mermaid examples on inner lines; a multiline ^ anchor used to
  // classify the whole document as a single mermaid block.
  it('rejects a large document that merely contains a mermaid example', () => {
    const document = [
      '# Durable AgentHarness design',
      '',
      'Some prose about the design.',
      '',
      '```mermaid',
      'flowchart TD',
      '  A --> B',
      '```',
      '',
      'More prose. '.repeat(200),
    ].join('\n');
    expect(looksLikeMermaid(document)).toBe(false);
  });
});
