/* Miru — render literal `==highlight==` and `<u>…</u>` markdown syntax as
   display-only emphasis (not tracked as editable session marks). Skips code
   and math regions. */

import { elements } from '../dom.js';

function annotateTextNode(textNode) {
  const text = textNode.nodeValue;
  const pattern = /==([^=\n]+)==|<u>([^<]+)<\/u>/g;
  const frag = document.createDocumentFragment();
  let last = 0;
  let m;
  pattern.lastIndex = 0;
  while ((m = pattern.exec(text)) !== null) {
    if (m.index > last) frag.appendChild(document.createTextNode(text.slice(last, m.index)));
    if (m[1] !== undefined) {
      const mark = document.createElement('mark');
      mark.className = 'annot-hl';
      mark.textContent = m[1];
      frag.appendChild(mark);
    } else {
      const u = document.createElement('u');
      u.textContent = m[2];
      frag.appendChild(u);
    }
    last = m.index + m[0].length;
  }
  if (last === 0) return;
  if (last < text.length) frag.appendChild(document.createTextNode(text.slice(last)));
  textNode.parentNode.replaceChild(frag, textNode);
}

export function renderAnnotationSyntax() {
  const walker = document.createTreeWalker(elements.article, NodeFilter.SHOW_TEXT);
  const textNodes = [];
  let node;
  while ((node = walker.nextNode())) {
    const parent = node.parentElement;
    if (!parent) continue;
    if (parent.closest('pre, code, script, style, eq, eqn')) continue;
    if (/==[^=\n]+==|<u>[^<]+<\/u>/.test(node.nodeValue)) textNodes.push(node);
  }
  textNodes.forEach(annotateTextNode);
}
