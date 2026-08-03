/* Miru — snippet mode: when a paste is code rather than Markdown, render it
   as a ray-style framed card (editable filename, language picker, highlighted
   preview) instead of mangling it through the Markdown pipeline. Detection
   lives here so every entry point (paste, drop, bundle restore) benefits. */

import { elements } from '../dom.js';
import { state } from '../state.js';
import { copyText } from '../ui/feedback.js';

const LANGUAGE_EXTENSIONS = {
  javascript: 'js', typescript: 'ts', python: 'py', java: 'java', c: 'c',
  cpp: 'cpp', csharp: 'cs', go: 'go', rust: 'rs', ruby: 'rb', php: 'php',
  swift: 'swift', kotlin: 'kt', sql: 'sql', bash: 'sh', shell: 'sh',
  yaml: 'yml', json: 'json', xml: 'xml', html: 'html', css: 'css',
  scss: 'scss', lua: 'lua', r: 'r', scala: 'scala', haskell: 'hs',
  elixir: 'ex', dockerfile: 'dockerfile', diff: 'diff', perl: 'pl',
  vim: 'vim', ini: 'ini', toml: 'toml', graphql: 'graphql', plaintext: 'txt',
};

// Strong Markdown structures. Deliberately no bold/italic: `char **argv`
// and snake_case would false-positive code into the Markdown path.
const MARKDOWN_SIGNALS = [
  /^#{1,6}\s/m,                    // headings
  /^\s{0,3}[-*+]\s+\S/m,           // bullet lists
  /^\s{0,3}\d+\.\s+\S/m,           // ordered lists
  /^\s{0,3}>\s?\S/m,               // blockquotes
  /\[[^\]]+\]\([^)]+\)/,           // links
  /^\s*\|.*\|\s*$/m,               // tables
  /^\s*(-{3,}|\*{3,}|_{3,})\s*$/m, // hr / frontmatter fence
  /```|~~~/,                       // code fences
];

export function looksLikeMarkdown(text) {
  return MARKDOWN_SIGNALS.some((p) => p.test(text));
}

// hljs auto-detection with a relevance floor: plain prose stays Markdown,
// recognizable code becomes a snippet.
export function detectSnippetLanguage(text) {
  if (typeof window.hljs !== 'object') return null;
  let result;
  try {
    result = window.hljs.highlightAuto(text.slice(0, 8000));
  } catch (err) {
    return null;
  }
  if (!result || !result.language) return null;
  if (result.language === 'plaintext' || result.language === 'markdown') return null;
  if ((result.relevance || 0) < 6) return null;
  return result.language;
}

function extensionFor(lang) {
  return LANGUAGE_EXTENSIONS[lang] || String(lang).replace(/[^a-z0-9]+/gi, '') || 'txt';
}

// Module state for the currently rendered snippet (the card owns the rest).
let current = null;

function buildLangOptions(select, detected) {
  const auto = document.createElement('option');
  auto.value = 'auto';
  auto.textContent = 'auto';
  select.appendChild(auto);
  const langs = typeof window.hljs === 'object' && window.hljs.listLanguages
    ? window.hljs.listLanguages().slice().sort()
    : [detected];
  langs.forEach((lang) => {
    const option = document.createElement('option');
    option.value = lang;
    option.textContent = lang;
    select.appendChild(option);
  });
  select.value = detected;
}

// Render the ray-style card into the (already emptied) article and wire the
// titlebar. Returns the display filename.
export function renderSnippet(code, lang) {
  const filename = state.droppedFilename && /\.[a-z0-9]+$/i.test(state.droppedFilename)
    ? state.droppedFilename
    : `snippet.${extensionFor(lang)}`;
  current = { code, lang, filename };

  const frame = document.createElement('div');
  frame.className = 'snippet-frame';
  const card = document.createElement('div');
  card.className = 'snippet-card';

  const bar = document.createElement('div');
  bar.className = 'snippet-titlebar';
  const star = document.createElement('span');
  star.className = 'snippet-star';
  star.textContent = '✶';
  const name = document.createElement('span');
  name.className = 'snippet-filename';
  name.contentEditable = 'true';
  name.spellcheck = false;
  name.setAttribute('role', 'textbox');
  name.setAttribute('aria-label', 'Snippet filename');
  name.textContent = filename;
  const picker = document.createElement('span');
  picker.className = 'snippet-langpicker';
  const tag = document.createElement('span');
  tag.className = 'snippet-langtag';
  tag.textContent = lang;
  const select = document.createElement('select');
  select.className = 'snippet-langselect';
  select.setAttribute('aria-label', 'Language');
  buildLangOptions(select, lang);
  picker.append(tag, select);
  const copyBtn = document.createElement('button');
  copyBtn.type = 'button';
  copyBtn.className = 'snippet-copy';
  copyBtn.textContent = 'Copy';
  bar.append(star, name, picker, copyBtn);

  const pre = document.createElement('pre');
  pre.className = 'snippet-code';
  const codeEl = document.createElement('code');
  codeEl.className = 'hljs';
  pre.appendChild(codeEl);
  card.append(bar, pre);
  frame.appendChild(card);
  elements.article.appendChild(frame);

  function highlight() {
    try {
      codeEl.innerHTML = window.hljs.highlight(current.code, { language: current.lang }).value;
    } catch (err) {
      codeEl.textContent = current.code;
    }
    // A trailing newline needs a filler so the pre keeps its height.
    if (current.code.endsWith('\n') || current.code === '') codeEl.innerHTML += '​';
    tag.textContent = current.lang;
    select.value = current.lang;
  }

  select.addEventListener('change', () => {
    current.lang = select.value === 'auto'
      ? (detectSnippetLanguage(current.code) || 'plaintext')
      : select.value;
    // Changing languages preserves the filename base and swaps its extension.
    const text = name.textContent.trim();
    const dot = text.lastIndexOf('.');
    const base = dot > 0 ? text.slice(0, dot) : text;
    name.textContent = (base || 'snippet') + '.' + extensionFor(current.lang);
    current.filename = name.textContent;
    state.docTitle = base || 'snippet';
    highlight();
  });

  name.addEventListener('keydown', (e) => {
    if (e.key === 'Enter') {
      e.preventDefault();
      name.blur();
    } else if (e.key === 'Escape') {
      name.textContent = current.filename;
      name.blur();
    }
  });
  name.addEventListener('blur', () => {
    const clean = name.textContent.replace(/\s+/g, ' ').trim() || current.filename;
    name.textContent = clean;
    current.filename = clean;
    const dot = clean.lastIndexOf('.');
    state.docTitle = (dot > 0 ? clean.slice(0, dot) : clean) || 'snippet';
  });

  copyBtn.addEventListener('click', () => copyText(current.code, copyBtn));

  highlight();
  return { filename };
}
