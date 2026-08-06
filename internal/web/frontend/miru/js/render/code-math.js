/* Miru — post-process rendered content: highlight.js syntax highlighting +
   per-block language tag, KaTeX math rendering, and the "Copy" button on
   fenced code blocks. */

import { elements } from '../dom.js';
import { copyText } from '../ui/feedback.js';

function detectLanguage(block) {
  if (typeof window.hljs !== 'object') return 'text';

  // Explicit markdown class, e.g. language-cpp.
  for (const cls of block.classList) {
    if (cls.startsWith('language-')) {
      const lang = cls.replace('language-', '');
      if (window.hljs.getLanguage(lang)) return lang;
    }
  }

  // Auto-detect for reasonably short blocks to keep performance snappy.
  const text = block.textContent || '';
  if (text.length > 0 && text.length < 6000) {
    const result = window.hljs.highlightAuto(text);
    return result.language || 'text';
  }

  return 'text';
}

function addLanguageTag(pre, language) {
  if (!pre || pre.querySelector('.code-lang')) return;

  const tag = document.createElement('span');
  tag.className = 'code-lang';
  tag.textContent = language;
  pre.appendChild(tag);
}

function isMermaidFence(block) {
  for (const cls of block.classList) {
    if (cls === 'language-mermaid' || cls === 'lang-mermaid') return true;
  }
  const text = (block.textContent || '').trimStart();
  return /^(flowchart|graph|sequenceDiagram|classDiagram|stateDiagram|erDiagram|journey|gantt|pie|gitGraph|mindmap|timeline)\b/.test(
    text,
  );
}

export function highlightCode() {
  if (typeof window.hljs !== 'object') return;
  const codeBlocks = elements.article.querySelectorAll('pre code');
  codeBlocks.forEach((block) => {
    // Skip if already highlighted by hljs.
    if (block.classList.contains('hljs')) return;
    // Mermaid fences are rendered as diagrams — do not syntax-highlight them.
    if (isMermaidFence(block)) {
      addLanguageTag(block.parentElement, 'mermaid');
      return;
    }

    const language = detectLanguage(block);
    addLanguageTag(block.parentElement, language);

    try {
      window.hljs.highlightElement(block);
    } catch (err) {
      console.error('highlight.js failed:', err);
    }
  });
}

export function renderMath() {
  if (typeof window.katex !== 'object' || typeof window.katex.renderToString !== 'function') return;

  // Render \\(...\\) and \\[...\\] blocks preserved by markdown-it-texmath.
  const mathBlocks = elements.article.querySelectorAll('eq, eqn');
  mathBlocks.forEach((el) => {
    const isDisplay = el.tagName.toLowerCase() === 'eqn';
    const latex = el.textContent.trim();
    try {
      const html = window.katex.renderToString(latex, {
        throwOnError: false,
        displayMode: isDisplay,
      });
      const wrapper = document.createElement(isDisplay ? 'div' : 'span');
      wrapper.innerHTML = html;
      el.replaceWith(wrapper);
    } catch (err) {
      console.error('KaTeX render failed:', err);
    }
  });

  // Also handle any remaining $$...$$, $...$, \\[...\\] or \\(...\\) in the text.
  if (typeof window.renderMathInElement === 'function') {
    try {
      window.renderMathInElement(elements.article, {
        delimiters: [
          { left: '$$', right: '$$', display: true },
          { left: '$', right: '$', display: false },
          { left: '\\[', right: '\\]', display: true },
          { left: '\\(', right: '\\)', display: false },
        ],
        throwOnError: false,
        ignoredTags: ['script', 'noscript', 'style', 'textarea', 'pre', 'code', 'svg', 'math', 'annotation'],
      });
    } catch (err) {
      console.error('KaTeX auto-render failed:', err);
    }
  }
}

export function addCopyButtons() {
  const pres = elements.article.querySelectorAll('pre');
  pres.forEach((pre) => {
    if (pre.querySelector('.code-copy')) return;

    const button = document.createElement('button');
    button.type = 'button';
    button.className = 'code-copy';
    button.textContent = 'Copy';
    pre.appendChild(button);

    button.addEventListener('click', () => {
      const code = pre.querySelector('code');
      const text = code ? code.textContent : pre.textContent;
      copyText(text.trim(), button);
    });
  });
}
