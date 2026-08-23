/* Miru — render ```mermaid fenced blocks as diagrams. */

import { elements } from '../dom.js';

let mermaidLoading = null;
let mermaidReady = false;
let renderSeq = 0;

function themeName() {
  const theme = document.documentElement.getAttribute('data-theme');
  return theme === 'dark' ? 'dark' : 'default';
}

function loadMermaid() {
  if (mermaidReady && window.mermaid) return Promise.resolve(window.mermaid);
  if (mermaidLoading) return mermaidLoading;

  mermaidLoading = new Promise((resolve, reject) => {
    if (window.mermaid) {
      mermaidReady = true;
      resolve(window.mermaid);
      return;
    }
    const script = document.createElement('script');
    script.src = 'vendor/mermaid.min.js';
    script.async = true;
    script.onload = () => {
      if (!window.mermaid) {
        reject(new Error('mermaid loaded without global'));
        return;
      }
      mermaidReady = true;
      resolve(window.mermaid);
    };
    script.onerror = () => reject(new Error('failed to load mermaid'));
    document.head.appendChild(script);
  }).finally(() => {
    mermaidLoading = null;
  });

  return mermaidLoading;
}

function isMermaidBlock(block) {
  for (const cls of block.classList) {
    if (cls === 'language-mermaid' || cls === 'lang-mermaid') return true;
  }
  const text = (block.textContent || '').trimStart();
  return /^(flowchart|graph|sequenceDiagram|classDiagram|stateDiagram|erDiagram|journey|gantt|pie|gitGraph|mindmap|timeline)\b/.test(
    text,
  );
}

export function collectMermaidBlocks() {
  return Array.from(elements.article.querySelectorAll('pre code')).filter(isMermaidBlock);
}

// LLMs often emit flowchart labels like `A[accept()]` or `A -->|accept()| B`
// without quoting. Mermaid's parser treats bare `()` inside [] / || as shape
// tokens and fails. Quote those labels conservatively before render.
//
// Only touches unquoted rectangle node text and edge labels. Leaves already
// quoted text, and non-rectangle shapes like A((c)), A[(db)], A([s]), alone.
export function sanitizeMermaidSource(source) {
  if (!source) return source;

  // Rectangle nodes: Foo[text with (parens)] → Foo["text with (parens)"]
  // Negative look after '[' skips shapes that start with ", ', (, [, /, \
  let out = source.replace(
    /\b([A-Za-z_][\w-]*)\[(?![["'(\s\/\\])([^\]\n]*)\]/g,
    (match, id, text) => {
      if (!mermaidLabelNeedsQuotes(text)) return match;
      return `${id}["${escapeMermaidQuotedLabel(text)}"]`;
    },
  );

  // Edge labels: -->|text with ()| → -->|"text with ()"|
  out = out.replace(
    /(\|)(?!")([^|\n]*[()][^|\n]*)(\|)/g,
    (match, open, text, close) => {
      if (isAlreadyQuoted(text)) return match;
      return `${open}"${escapeMermaidQuotedLabel(text)}"${close}`;
    },
  );

  // subgraph id [Title with ()] → subgraph id ["Title with ()"]
  out = out.replace(
    /\bsubgraph(\s+\S+)(\s+)\[(?!")([^\]\n]*)\]/g,
    (match, idPart, spaces, title) => {
      if (!mermaidLabelNeedsQuotes(title)) return match;
      return `subgraph${idPart}${spaces}["${escapeMermaidQuotedLabel(title)}"]`;
    },
  );

  return out;
}

function isAlreadyQuoted(text) {
  const t = text.trim();
  return t.length >= 2 && t.startsWith('"') && t.endsWith('"');
}

function mermaidLabelNeedsQuotes(text) {
  if (!text || isAlreadyQuoted(text)) return false;
  // Parentheses are the common LLM footgun. Also quote other tokens the
  // flowchart parser is known to misread inside bare labels.
  return /[(){}|;]/.test(text);
}

function escapeMermaidQuotedLabel(text) {
  // Mermaid uses #quot; inside quoted labels for literal double quotes.
  return text.replace(/"/g, '#quot;');
}

/** Replace mermaid code fences with rendered diagrams. Safe to call repeatedly. */
export async function renderMermaid() {
  const blocks = collectMermaidBlocks();
  if (!blocks.length) return;

  let mermaid;
  try {
    mermaid = await loadMermaid();
  } catch (err) {
    console.error('mermaid unavailable:', err);
    return;
  }

  const seq = ++renderSeq;
  try {
    mermaid.initialize({
      startOnLoad: false,
      securityLevel: 'strict',
      theme: themeName(),
      fontFamily: 'inherit',
    });
  } catch (err) {
    console.error('mermaid init failed:', err);
  }

  for (let i = 0; i < blocks.length; i++) {
    if (seq !== renderSeq) return; // superseded by a newer render pass
    const code = blocks[i];
    const pre = code.closest('pre');
    if (!pre || pre.dataset.mermaidRendered === '1') continue;

    const source = sanitizeMermaidSource((code.textContent || '').trim());
    if (!source) continue;

    const host = document.createElement('div');
    host.className = 'mermaid-diagram';
    host.setAttribute('role', 'img');
    host.setAttribute('aria-label', 'Mermaid diagram');

    const id = `miru-mermaid-${seq}-${i}-${Math.random().toString(36).slice(2, 8)}`;
    try {
      // mermaid v10: render(id, text) → { svg, bindFunctions }
      const result = await mermaid.render(id, source);
      host.innerHTML = result.svg || '';
      if (typeof result.bindFunctions === 'function') {
        result.bindFunctions(host);
      }
      pre.replaceWith(host);
    } catch (err) {
      console.error('mermaid render failed:', err);
      // Leave the original fence visible; mark so we don't loop forever.
      pre.dataset.mermaidRendered = 'error';
      const note = document.createElement('div');
      note.className = 'mermaid-error';
      note.textContent = 'Mermaid diagram failed to render.';
      pre.insertAdjacentElement('afterend', note);
    }
  }
}

// Re-theme diagrams when the reader toggles light/dark.
export function onMermaidThemeChange() {
  // Force re-render by restoring sources is hard once replaced; full document
  // re-render already runs processArticle on load. For live theme toggles,
  // re-init is enough for the next document open. No-op here is fine.
}
