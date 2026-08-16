/* Selection assist: ask about a selection via deepseek-v4-flash (Companion → Pi).
   Minimal panel: model status · reply · single input row · save Q&A note. */

import { applyNote, findAnnot, setNoteOnPassage } from '../js/annotations/model.js';
import { onAnnotToolbarHide, registerAnnotAction } from '../js/annotations/toolbar.js';
import { state } from '../js/state.js';
import { showToast } from '../js/ui/feedback.js';
import { streamAssist } from './api.js';
import { session } from './session.js';

const MODEL_LABEL = 'deepseek-v4-flash';

const ASSIST_ICON =
  '<svg viewBox="0 0 24 24" width="16" height="16" aria-hidden="true">' +
  '<path fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" ' +
  'd="M12 3l1.2 3.6L17 8l-3.8 1.4L12 13l-1.2-3.6L7 8l3.8-1.4L12 3z"/>' +
  '<path fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" ' +
  'd="M18 14l.7 2 2 .7-2 .7-.7 2-.7-2-2-.7 2-.7.7-2z"/>' +
  '</svg>';

const ICON_SEND =
  '<svg viewBox="0 0 24 24" width="15" height="15" aria-hidden="true">' +
  '<path fill="currentColor" d="M3.4 4.2 21 12 3.4 19.8l2.1-6.3L15 12l-9.5-1.5L3.4 4.2z"/>' +
  '</svg>';

const ICON_SPIN =
  '<svg viewBox="0 0 24 24" width="15" height="15" aria-hidden="true" class="annot-assist-spin">' +
  '<circle cx="12" cy="12" r="8" fill="none" stroke="currentColor" stroke-width="1.8" stroke-dasharray="36 20" stroke-linecap="round"/>' +
  '</svg>';

const ICON_CLOSE =
  '<svg viewBox="0 0 24 24" width="14" height="14" aria-hidden="true">' +
  '<path fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" d="M6 6l12 12M18 6 6 18"/>' +
  '</svg>';

const ICON_BOOKMARK =
  '<svg viewBox="0 0 24 24" width="15" height="15" aria-hidden="true">' +
  '<path fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" ' +
  'd="M7 4h10a1 1 0 0 1 1 1v15l-6-3.5L6 20V5a1 1 0 0 1 1-1z"/>' +
  '</svg>';

let abortController = null;
let busy = false;
/** @type {'unknown' | 'ok' | 'bad'} */
let modelStatus = 'unknown';

function escapeHTML(value) {
  return String(value)
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;');
}

/** Grow the composer textarea up to ~6 lines. */
function autosizeInput(el) {
  if (!el) return;
  el.style.height = 'auto';
  const max = 6 * 20; // ~6 lines at 20px line-height
  const next = Math.min(Math.max(el.scrollHeight, 20), max);
  el.style.height = `${next}px`;
}

function contextAround(markdown, selection, radius = 280) {
  if (!markdown || !selection) return { prefix: '', suffix: '' };
  const idx = markdown.indexOf(selection);
  if (idx < 0) return { prefix: '', suffix: '' };
  return {
    prefix: markdown.slice(Math.max(0, idx - radius), idx),
    suffix: markdown.slice(idx + selection.length, Math.min(markdown.length, idx + selection.length + radius)),
  };
}

function hasNoteAnchor(ctx) {
  if (ctx.annotEl && ctx.annotEl.isConnected) return true;
  if (!ctx.range) return false;
  try {
    const root = ctx.range.commonAncestorContainer;
    return Boolean(root && root.isConnected !== false);
  } catch {
    return false;
  }
}

function noteBodyFromAnswer(instruction, answer) {
  const q = String(instruction || '').trim();
  const a = String(answer || '').trim();
  if (!a) return '';
  if (!q) return a;
  return `**Q:** ${q}\n\n${a}\n`;
}

async function probeModelStatus() {
  if (!session.connected) {
    modelStatus = 'bad';
    return modelStatus;
  }
  try {
    const res = await fetch('/api/status', { cache: 'no-store' });
    modelStatus = res.ok ? 'ok' : 'bad';
  } catch {
    modelStatus = 'bad';
  }
  return modelStatus;
}

function statusTitle(status) {
  if (status === 'ok') return `${MODEL_LABEL} ready`;
  if (status === 'bad') return 'Model unavailable';
  return 'Checking…';
}

function renderPanel({
  status = '',
  result = '',
  canSave = false,
  running = false,
  model = modelStatus,
}) {
  const showResult = Boolean(result) || running;
  const err = status && /fail|error|empty|unavail/i.test(status);

  return (
    '<div class="annot-assist-panel" role="dialog" aria-label="Ask selection">' +
      '<div class="annot-assist-head">' +
        `<span class="annot-assist-dot annot-assist-dot-${model}" title="${escapeHTML(statusTitle(model))}"></span>` +
        `<span class="annot-assist-model-name" title="${escapeHTML(statusTitle(model))}">${escapeHTML(MODEL_LABEL)}</span>` +
        '<span class="annot-assist-spacer"></span>' +
        `<button type="button" class="annot-assist-close" data-assist-close aria-label="Close">${ICON_CLOSE}</button>` +
      '</div>' +
      (showResult
        ? `<div class="annot-assist-result${running ? ' is-streaming' : ''}">` +
            (running && !result ? '<span class="annot-assist-placeholder">…</span>' : '') +
          '</div>'
        : '') +
      (status ? `<p class="annot-assist-status${err ? ' is-error' : ''}">${escapeHTML(status)}</p>` : '') +
      '<div class="annot-assist-composer">' +
        '<textarea class="annot-assist-input" rows="1" placeholder="Ask…" autocomplete="off" ' +
          'aria-label="Question (⌘↵ to send)"></textarea>' +
        '<div class="annot-assist-actions">' +
          (canSave
            ? '<button type="button" class="annot-assist-save" title="Save note" aria-label="Save note">' +
                `${ICON_BOOKMARK}</button>`
            : '') +
          `<button type="button" class="annot-assist-run"${running ? ' disabled' : ''} title="Send (⌘↵)" aria-label="Send">` +
            `${running ? ICON_SPIN : ICON_SEND}</button>` +
        '</div>' +
      '</div>' +
    '</div>'
  );
}

function setResultHTML(root, text) {
  let el = root.querySelector('.annot-assist-result');
  if (!text) {
    if (el) el.remove();
    return;
  }
  if (!el) {
    const head = root.querySelector('.annot-assist-head');
    el = document.createElement('div');
    el.className = 'annot-assist-result has-content';
    if (head && head.nextSibling) root.insertBefore(el, head.nextSibling);
    else root.appendChild(el);
  }
  el.classList.add('has-content');
  el.classList.remove('is-streaming');
  el.textContent = text;
}

function openAssist(ctx) {
  const selection = (ctx.text || '').trim();
  if (!selection) {
    showToast('Select some text first');
    return;
  }
  if (!session.connected || !session.documentID) {
    showToast('Open a membox document to use assist');
    return;
  }

  let answer = '';
  let lastInstruction = '';
  const markdown = state.currentMarkdown || '';
  const { prefix, suffix } = contextAround(markdown, selection);

  ctx.toolbar.classList.add('is-assist');
  void probeModelStatus().then((s) => {
    const dot = ctx.toolbar.querySelector('.annot-assist-dot');
    if (dot) {
      dot.className = `annot-assist-dot annot-assist-dot-${s}`;
      dot.title = statusTitle(s);
    }
  });

  ctx.toolbar.innerHTML = renderPanel({ model: modelStatus });
  const input = ctx.toolbar.querySelector('.annot-assist-input');
  if (input) {
    input.value = '';
    autosizeInput(input);
    input.focus();
  }
  ctx.position(ctx.range || ctx.annotEl);

  const canSaveNow = () => Boolean(answer) && hasNoteAnchor(ctx);

  const paint = (opts) => {
    const field = ctx.toolbar.querySelector('.annot-assist-input');
    const value = field?.value || '';
    ctx.toolbar.innerHTML = renderPanel({
      status: opts.status || '',
      result: opts.result || '',
      canSave: opts.canSave !== undefined ? Boolean(opts.canSave) : canSaveNow(),
      running: Boolean(opts.running),
      model: modelStatus,
    });
    const nextInput = ctx.toolbar.querySelector('.annot-assist-input');
    if (nextInput) {
      nextInput.value = value;
      autosizeInput(nextInput);
    }
    if (opts.result) setResultHTML(ctx.toolbar, opts.result);
    bind();
    ctx.position(ctx.range || ctx.annotEl);
  };

  const bind = () => {
    ctx.toolbar.querySelector('[data-assist-close]')?.addEventListener('click', () => ctx.hide());
    ctx.toolbar.querySelector('.annot-assist-run')?.addEventListener('click', () => { void run(); });
    ctx.toolbar.querySelector('.annot-assist-save')?.addEventListener('click', () => { applyNoteAnswer(); });
    const field = ctx.toolbar.querySelector('.annot-assist-input');
    field?.addEventListener('input', () => {
      autosizeInput(field);
      ctx.position(ctx.range || ctx.annotEl);
    });
    field?.addEventListener('keydown', (e) => {
      // Enter alone: newline / IME candidate confirm. Only ⌘/Ctrl+Enter sends.
      if (e.key === 'Enter' && (e.metaKey || e.ctrlKey) && !e.isComposing && e.keyCode !== 229) {
        e.preventDefault();
        void run();
        return;
      }
      if (e.key === 'Escape' && !e.isComposing) ctx.hide();
    });
  };

  const ensureResultEl = () => {
    let el = ctx.toolbar.querySelector('.annot-assist-result');
    if (!el) {
      const head = ctx.toolbar.querySelector('.annot-assist-head');
      el = document.createElement('div');
      el.className = 'annot-assist-result is-streaming';
      el.innerHTML = '<span class="annot-assist-placeholder">…</span>';
      const panel = ctx.toolbar.querySelector('.annot-assist-panel') || ctx.toolbar;
      if (head?.nextSibling) panel.insertBefore(el, head.nextSibling);
      else panel.appendChild(el);
    }
    return el;
  };

  const run = async () => {
    if (busy) return;
    const field = ctx.toolbar.querySelector('.annot-assist-input');
    const instruction = (field?.value || '').trim();
    if (!instruction) {
      showToast('Ask a question');
      field?.focus();
      return;
    }
    lastInstruction = instruction;
    busy = true;
    answer = '';
    if (abortController) abortController.abort();
    abortController = new AbortController();
    paint({ running: true, result: '', canSave: false });

    let assembled = '';
    try {
      await streamAssist(session.documentID, {
        mode: 'ask',
        instruction,
        selection,
        prefix,
        suffix,
        title: state.docTitle || '',
      }, (event) => {
        if (event.type === 'start') {
          modelStatus = 'ok';
          const dot = ctx.toolbar.querySelector('.annot-assist-dot');
          if (dot) {
            dot.className = 'annot-assist-dot annot-assist-dot-ok';
            dot.title = statusTitle('ok');
          }
        }
        if (event.type === 'delta' && event.text) {
          assembled += event.text;
          const el = ensureResultEl();
          el.classList.add('is-streaming', 'has-content');
          el.textContent = assembled;
          ctx.position(ctx.range || ctx.annotEl);
        }
        if (event.type === 'done') {
          answer = (event.text || assembled || '').trim();
          assembled = answer;
        }
        if (event.type === 'error') {
          modelStatus = 'bad';
          throw new Error(event.error || 'Assist failed');
        }
      }, abortController.signal);

      paint({
        running: false,
        result: assembled,
        canSave: canSaveNow(),
      });
    } catch (err) {
      if (err && err.name === 'AbortError') {
        busy = false;
        return;
      }
      console.error('membox: assist failed', err);
      modelStatus = 'bad';
      paint({
        running: false,
        status: (err && err.message) || 'Assist failed',
        result: assembled,
        canSave: false,
      });
      showToast((err && err.message) || 'Assist failed');
    } finally {
      busy = false;
    }
  };

  const applyNoteAnswer = () => {
    const noteText = noteBodyFromAnswer(lastInstruction, answer);
    if (!noteText) {
      showToast('Nothing to save');
      return;
    }
    if (!hasNoteAnchor(ctx)) {
      showToast('Selection lost — select again');
      return;
    }
    try {
      if (ctx.annotEl && ctx.annotEl.isConnected) {
        const entry = findAnnot(ctx.annotEl.dataset.annotId);
        if (!entry) throw new Error('annotation missing');
        setNoteOnPassage(entry, ctx.annotEl, noteText, { kind: 'qa' });
      } else {
        applyNote(ctx.range, noteText, { kind: 'qa' });
      }
      showToast('Saved as note');
      ctx.finish();
    } catch (err) {
      console.error('membox: assist note failed', err);
      showToast(err.message || 'Could not save note');
    }
  };

  bind();
}

export function initAssist() {
  onAnnotToolbarHide(() => {
    if (busy) return;
    if (abortController) {
      abortController.abort();
      abortController = null;
    }
  });
  registerAnnotAction({
    id: 'assist',
    icon: ASSIST_ICON,
    title: 'Ask about selection',
    when: ({ mode, text }) => {
      if (mode !== 'create') return false;
      const value = String(text || '').trim();
      if (value.length < 8) return false;
      if (/^[A-Za-z]+(?:['\u2019-][A-Za-z]+)*$/.test(value) && value.length <= 40) return false;
      return true;
    },
    run: (ctx) => openAssist(ctx),
  });
}
