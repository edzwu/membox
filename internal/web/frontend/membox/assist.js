/* Selection assist: ask a question or rewrite the selected span via
   deepseek-v4-flash (Companion → Pi).
   - Edit Apply → replace source Markdown + SyncDocument (version/blob)
   - Ask Apply  → pin answer as a floating page note on the selection */

import { applyNote, findAnnot, setNoteOnPassage } from '../js/annotations/model.js';
import { onAnnotToolbarHide, registerAnnotAction } from '../js/annotations/toolbar.js';
import { loadDocument } from '../js/document.js';
import { state } from '../js/state.js';
import { showToast } from '../js/ui/feedback.js';
import { streamAssist, applyAssistEdit } from './api.js';
import { applyConversionDisplayLabelIfNeeded } from './document.js';
import { session } from './session.js';
import { emitRender } from './events.js';

const ASSIST_ICON =
  '<svg viewBox="0 0 24 24" width="16" height="16" aria-hidden="true">' +
  '<path fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" ' +
  'd="M12 3l1.2 3.6L17 8l-3.8 1.4L12 13l-1.2-3.6L7 8l3.8-1.4L12 3z"/>' +
  '<path fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" ' +
  'd="M18 14l.7 2 2 .7-2 .7-.7 2-.7-2-2-.7 2-.7.7-2zM6 15l.5 1.4L8 17l-1.5.6L6 19l-.5-1.4L4 17l1.5-.6L6 15z"/>' +
  '</svg>';

const ICON_ASK =
  '<svg viewBox="0 0 24 24" width="15" height="15" aria-hidden="true">' +
  '<path fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" ' +
  'd="M8 10h.01M12 10h.01M16 10h.01M21 12a8 8 0 1 1-3.2-6.4L21 6v6z"/>' +
  '</svg>';

const ICON_EDIT =
  '<svg viewBox="0 0 24 24" width="15" height="15" aria-hidden="true">' +
  '<path fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" ' +
  'd="M12 20h9M16.5 3.5a2.1 2.1 0 0 1 3 3L7 19l-4 1 1-4L16.5 3.5z"/>' +
  '</svg>';

const ICON_RUN =
  '<svg viewBox="0 0 24 24" width="15" height="15" aria-hidden="true">' +
  '<path fill="currentColor" d="M3.4 4.2 21 12 3.4 19.8l2.1-6.3L15 12l-9.5-1.5L3.4 4.2z"/>' +
  '</svg>';

const ICON_RUN_BUSY =
  '<svg viewBox="0 0 24 24" width="15" height="15" aria-hidden="true" class="annot-assist-spin">' +
  '<circle cx="12" cy="12" r="8" fill="none" stroke="currentColor" stroke-width="1.8" stroke-dasharray="36 20" stroke-linecap="round"/>' +
  '</svg>';

const ICON_SAVE_NOTE =
  '<svg viewBox="0 0 24 24" width="15" height="15" aria-hidden="true">' +
  '<path fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" ' +
  'd="M7 4h10a1 1 0 0 1 1 1v15l-6-3.5L6 20V5a1 1 0 0 1 1-1z"/>' +
  '<path fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" d="M12 8v5M9.5 10.5h5"/>' +
  '</svg>';

const ICON_APPLY_EDIT =
  '<svg viewBox="0 0 24 24" width="15" height="15" aria-hidden="true">' +
  '<path fill="none" stroke="currentColor" stroke-width="1.9" stroke-linecap="round" stroke-linejoin="round" d="M5 12.5 10 17.5 19 7"/>' +
  '</svg>';

let abortController = null;
let busy = false;

function escapeHTML(value) {
  return String(value)
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;');
}

function contextAround(markdown, selection, radius = 280) {
  if (!markdown || !selection) return { prefix: '', suffix: '' };
  const idx = markdown.indexOf(selection);
  if (idx < 0) return { prefix: '', suffix: '' };
  const start = Math.max(0, idx - radius);
  const end = Math.min(markdown.length, idx + selection.length + radius);
  return {
    prefix: markdown.slice(start, idx),
    suffix: markdown.slice(idx + selection.length, end),
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

function renderPanel({
  mode = 'ask',
  status = '',
  result = '',
  canApply = false,
  running = false,
}) {
  const askActive = mode === 'ask' ? ' is-active' : '';
  const editActive = mode === 'edit' ? ' is-active' : '';
  const applyTitle = mode === 'ask'
    ? 'Save answer as a floating note'
    : 'Apply rewrite to document';
  const applyIcon = mode === 'ask' ? ICON_SAVE_NOTE : ICON_APPLY_EDIT;
  const applyAria = mode === 'ask' ? 'Save note' : 'Apply edit';
  return (
    '<div class="annot-assist-panel" role="dialog" aria-label="Selection assist">' +
      '<div class="annot-assist-modes" role="tablist">' +
        `<button type="button" class="annot-assist-mode${askActive}" data-assist-mode="ask" role="tab" ` +
          `aria-selected="${mode === 'ask'}" title="Ask" aria-label="Ask">${ICON_ASK}</button>` +
        `<button type="button" class="annot-assist-mode${editActive}" data-assist-mode="edit" role="tab" ` +
          `aria-selected="${mode === 'edit'}" title="Edit" aria-label="Edit">${ICON_EDIT}</button>` +
      '</div>' +
      '<textarea class="annot-assist-input" rows="2" ' +
        `placeholder="${mode === 'edit' ? 'How should this passage change?' : 'Ask about this passage…'}" ` +
        'aria-label="Assist instruction"></textarea>' +
      '<div class="annot-assist-actions">' +
        `<button type="button" class="annot-assist-run"${running ? ' disabled' : ''} ` +
          `title="${running ? 'Running…' : 'Run (⌘Enter)'}" aria-label="${running ? 'Running' : 'Run'}">` +
          `${running ? ICON_RUN_BUSY : ICON_RUN}</button>` +
        `<button type="button" class="annot-assist-apply"${canApply ? '' : ' disabled'} ` +
          `title="${applyTitle}" aria-label="${applyAria}">${applyIcon}</button>` +
      '</div>' +
      (status ? `<p class="annot-assist-status">${escapeHTML(status)}</p>` : '') +
      `<div class="annot-assist-result"${result ? '' : ' hidden'}></div>` +
    '</div>'
  );
}

function setResultHTML(root, text) {
  const el = root.querySelector('.annot-assist-result');
  if (!el) return;
  if (!text) {
    el.hidden = true;
    el.textContent = '';
    return;
  }
  el.hidden = false;
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

  let mode = 'ask';
  let replacement = '';
  let answer = '';
  let lastInstruction = '';
  const markdown = state.currentMarkdown || '';
  const { prefix, suffix } = contextAround(markdown, selection);

  ctx.toolbar.classList.add('is-assist');
  ctx.toolbar.innerHTML = renderPanel({ mode });
  const input = ctx.toolbar.querySelector('.annot-assist-input');
  if (input) {
    input.value = '';
    input.focus();
  }
  ctx.position(ctx.range || ctx.annotEl);

  const canApplyNow = () => {
    if (mode === 'edit') return Boolean(replacement);
    return Boolean(answer) && hasNoteAnchor(ctx);
  };

  const paint = (opts) => {
    const field = ctx.toolbar.querySelector('.annot-assist-input');
    const value = field?.value || '';
    const selStart = field?.selectionStart;
    const selEnd = field?.selectionEnd;
    ctx.toolbar.innerHTML = renderPanel({
      mode,
      status: opts.status || '',
      result: opts.result || '',
      canApply: opts.canApply !== undefined ? Boolean(opts.canApply) : canApplyNow(),
      running: Boolean(opts.running),
    });
    const nextInput = ctx.toolbar.querySelector('.annot-assist-input');
    if (nextInput) {
      nextInput.value = value;
      if (typeof selStart === 'number') {
        try { nextInput.setSelectionRange(selStart, selEnd); } catch { /* ignore */ }
      }
    }
    if (opts.result) setResultHTML(ctx.toolbar, opts.result);
    bind();
    ctx.position(ctx.range || ctx.annotEl);
  };

  const bind = () => {
    ctx.toolbar.querySelectorAll('[data-assist-mode]').forEach((btn) => {
      btn.addEventListener('click', () => {
        mode = btn.dataset.assistMode === 'edit' ? 'edit' : 'ask';
        // Keep last outputs; Apply target changes with mode.
        paint({
          result: mode === 'edit' ? (replacement || answer) : (answer || replacement),
          canApply: canApplyNow(),
          status: '',
        });
        ctx.toolbar.querySelector('.annot-assist-input')?.focus();
      });
    });
    ctx.toolbar.querySelector('.annot-assist-run')?.addEventListener('click', () => { void run(); });
    ctx.toolbar.querySelector('.annot-assist-apply')?.addEventListener('click', () => { void apply(); });
    ctx.toolbar.querySelector('.annot-assist-input')?.addEventListener('keydown', (e) => {
      if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) {
        e.preventDefault();
        void run();
      }
      if (e.key === 'Escape') ctx.hide();
    });
  };

  const run = async () => {
    if (busy) return;
    const field = ctx.toolbar.querySelector('.annot-assist-input');
    const instruction = (field?.value || '').trim();
    if (!instruction) {
      showToast(mode === 'edit' ? 'Describe the edit' : 'Ask a question');
      field?.focus();
      return;
    }
    lastInstruction = instruction;
    busy = true;
    replacement = '';
    answer = '';
    if (abortController) abortController.abort();
    abortController = new AbortController();
    paint({
      running: true,
      status: mode === 'edit' ? 'Editing with deepseek-v4-flash…' : 'Asking deepseek-v4-flash…',
      result: '',
      canApply: false,
    });

    let assembled = '';
    try {
      await streamAssist(session.documentID, {
        mode,
        instruction,
        selection,
        prefix,
        suffix,
        title: state.docTitle || '',
      }, (event) => {
        if (event.type === 'delta' && event.text) {
          assembled += event.text;
          setResultHTML(ctx.toolbar, assembled);
          const applyBtn = ctx.toolbar.querySelector('.annot-assist-apply');
          if (applyBtn) applyBtn.disabled = true;
          ctx.position(ctx.range || ctx.annotEl);
        }
        if (event.type === 'done') {
          if (event.mode === 'edit' && event.replacement) {
            replacement = event.replacement;
            assembled = event.replacement;
          } else {
            answer = (event.text || assembled || '').trim();
            assembled = answer;
          }
        }
        if (event.type === 'error') {
          throw new Error(event.error || 'Assist failed');
        }
      }, abortController.signal);

      paint({
        running: false,
        status: mode === 'edit'
          ? 'Review the rewrite, then Apply'
          : 'Done — Save note to pin on this selection',
        result: assembled,
        canApply: canApplyNow(),
      });
    } catch (err) {
      if (err && err.name === 'AbortError') {
        busy = false;
        return;
      }
      console.error('membox: assist failed', err);
      const msg = (err && err.message) || 'Assist failed';
      paint({
        running: false,
        status: msg,
        result: assembled,
        canApply: false,
      });
      showToast(msg);
    } finally {
      busy = false;
    }
  };

  const applyEdit = async () => {
    if (!replacement) return;
    busy = true;
    paint({ running: true, status: 'Applying edit…', result: replacement, canApply: false });
    try {
      const result = await applyAssistEdit(session.documentID, {
        selection,
        replacement,
        prefix,
        suffix,
        body: state.currentMarkdown || '',
      });
      const nextBody = result.body || '';
      if (!nextBody) throw new Error('empty body after apply');
      state.currentMarkdown = nextBody;
      session.loadedMarkdown = nextBody;
      loadDocument(nextBody);
      applyConversionDisplayLabelIfNeeded(
        state.droppedFilename || result.filename || '',
        state.docTitle || '',
      );
      session.annotationsDirty = false;
      session.annotationsMutated = false;
      emitRender();
      showToast(`Applied edit (${lastInstruction.slice(0, 40) || 'assist'})`);
      ctx.finish();
    } catch (err) {
      console.error('membox: assist apply failed', err);
      paint({
        running: false,
        status: err.message || 'Apply failed',
        result: replacement,
        canApply: true,
      });
      showToast(err.message || 'Apply failed');
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
      showToast('Selection lost — select the passage again');
      return;
    }
    try {
      // Same path as toolbar note / dictionary save: floating rail card → *-note.md.
      if (ctx.annotEl && ctx.annotEl.isConnected) {
        const entry = findAnnot(ctx.annotEl.dataset.annotId);
        if (!entry) throw new Error('annotation missing');
        setNoteOnPassage(entry, ctx.annotEl, noteText);
      } else {
        applyNote(ctx.range, noteText);
      }
      showToast('Answer saved as note');
      ctx.finish();
    } catch (err) {
      console.error('membox: assist note failed', err);
      showToast(err.message || 'Could not save note');
    }
  };

  const apply = async () => {
    if (busy) return;
    if (mode === 'edit') {
      await applyEdit();
      return;
    }
    applyNoteAnswer();
  };

  bind();
}

export function initAssist() {
  onAnnotToolbarHide(() => {
    // Only cancel when the panel is actually dismissed while idle. Hiding
    // because the page selection collapsed (focus moved into the prompt)
    // used to abort deepseek mid-flight and surface "empty output".
    if (busy) return;
    if (abortController) {
      abortController.abort();
      abortController = null;
    }
  });
  registerAnnotAction({
    id: 'assist',
    icon: ASSIST_ICON,
    title: 'Ask / Edit selection',
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
