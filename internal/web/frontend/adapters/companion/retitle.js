/* AI 重命名 (tag icon, top of the file rail): sends the same lightweight
   context as the polish wand — heading outline + per-section samples, plus
   the lead paragraph and current title — to DeepSeek, and lets the user
   edit/apply the suggested title through the normal rename flow. */

import { state } from '../../js/state.js';
import { extractHeadings } from '../../js/markdown/structure.js';
import { parseFrontmatter } from '../../js/markdown/frontmatter.js';
import { escapeHtml } from '../../js/utils.js';
import { showToast } from '../../js/ui/feedback.js';
import { session } from './session.js';
import { onRender } from './events.js';
import { postSuggestTitle } from './api.js';
import { renameCurrentDocument } from './document.js';
import { addFileAction } from './file-actions.js';

const TAG_ICON = '<svg xmlns="http://www.w3.org/2000/svg" width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">'
  + '<path d="M4 17l3.5-9L11 17"/><path d="M5.5 13h4"/>'
  + '<path d="M10.6 20.3l.5-2.2 7.3-7.3 1.7 1.7-7.3 7.3z"/><path d="M16.6 9.4l1.7 1.7"/></svg>';

const LEAD_SAMPLE_RUNES = 300;

let button = null;
let overlay = null;
let running = false;

function available() {
  return session.connected && Boolean(session.documentID);
}

function renderButton() {
  if (!button) return;
  button.hidden = !available();
  button.classList.toggle('is-running', running);
  button.disabled = running;
}

/* Lead = prose before the first heading, frontmatter and markup stripped. */
function leadSample(markdown) {
  const { body } = parseFrontmatter(markdown);
  const lines = body.split('\n');
  const parts = [];
  let runes = 0;
  let inCodeBlock = false;
  for (const line of lines) {
    if (/^(#{1,6})\s/.test(line)) break; // lead ends at the first heading
    if (/^\s*(```|~~~)/.test(line)) {
      inCodeBlock = !inCodeBlock;
      continue;
    }
    if (inCodeBlock) continue;
    let text = line.trim();
    if (!text) continue;
    if (/^!\[/.test(text) || /^\|/.test(text)) continue;
    text = text.replace(/^>\s*/, '').replace(/^[-*+]\s+/, '').trim();
    if (!text) continue;
    parts.push(text);
    runes += Array.from(text).length;
    if (runes >= LEAD_SAMPLE_RUNES) break;
  }
  return Array.from(parts.join(' ').replace(/\s+/g, ' ').trim()).slice(0, LEAD_SAMPLE_RUNES).join('');
}

async function startRetitle() {
  if (running || !available()) return;
  const source = state.currentMarkdown || '';
  if (!source.trim()) return;
  running = true;
  renderButton();
  showToast('正在构思标题… (DeepSeek)');
  try {
    const headings = extractHeadings(source);
    const result = await postSuggestTitle({
      current: state.docTitle || '',
      lead: leadSample(source),
      headings: headings.map((h) => ({ level: h.level, text: h.text, sample: h.sample })),
    });
    const suggestion = String(result.title || '').trim();
    if (!suggestion) throw new Error('模型返回了空标题');
    if (suggestion === (state.docTitle || '').trim()) {
      showToast('当前标题已经很好');
      return;
    }
    openConfirm(suggestion);
  } catch (err) {
    console.error('membox: title suggestion failed', err);
    showToast(err?.message || '标题建议失败');
  } finally {
    running = false;
    renderButton();
  }
}

function openConfirm(suggestion) {
  closeConfirm();
  const current = state.docTitle || '';
  overlay = document.createElement('div');
  overlay.className = 'retitle-overlay';
  overlay.setAttribute('role', 'dialog');
  overlay.setAttribute('aria-label', '建议标题');
  overlay.innerHTML =
    '<div class="retitle-panel">' +
    '<div class="retitle-head">建议标题</div>' +
    `<div class="retitle-current">${escapeHtml(current)}</div>` +
    '<div class="retitle-arrow" aria-hidden="true">↓</div>' +
    `<input class="retitle-input" type="text" value="${escapeHtml(suggestion)}" maxlength="120">` +
    '<div class="retitle-actions">' +
    '<button type="button" class="polish-btn retitle-apply">重命名</button>' +
    '<button type="button" class="polish-btn retitle-cancel">取消</button>' +
    '</div></div>';
  const input = overlay.querySelector('.retitle-input');
  const apply = () => {
    const next = input.value.trim();
    if (!next || next === current) {
      closeConfirm();
      return;
    }
    closeConfirm();
    const titleElement = document.getElementById('doc-title');
    if (!titleElement) {
      showToast('标题栏不可用');
      return;
    }
    void renameCurrentDocument(titleElement, current, next);
  };
  overlay.querySelector('.retitle-apply').addEventListener('click', apply);
  overlay.querySelector('.retitle-cancel').addEventListener('click', closeConfirm);
  overlay.addEventListener('click', (event) => {
    if (event.target === overlay) closeConfirm();
  });
  input.addEventListener('keydown', (event) => {
    if (event.key === 'Enter') apply();
    if (event.key === 'Escape') closeConfirm();
  });
  document.body.appendChild(overlay);
  input.focus();
  input.select();
}

function closeConfirm() {
  overlay?.remove();
  overlay = null;
}

export function initRetitle() {
  button = document.createElement('button');
  button.type = 'button';
  button.className = 'file-action retitle-action';
  button.dataset.action = 'retitle';
  button.title = 'Suggest a better title (DeepSeek reads outline & lead only)';
  button.setAttribute('aria-label', button.title);
  button.innerHTML = TAG_ICON;
  button.hidden = true;
  button.addEventListener('click', () => void startRetitle());
  // Top of the rail, above summarize and the polish wand.
  addFileAction(button, { beforeAction: 'summarize' });
  onRender(renderButton);
  document.addEventListener('keydown', (event) => {
    if (event.key === 'Escape' && overlay) closeConfirm();
  });
  renderButton();
}
