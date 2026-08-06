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

    const source = (code.textContent || '').trim();
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
