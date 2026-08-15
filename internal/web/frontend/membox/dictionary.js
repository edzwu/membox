/* Dictionary lookup on single-word selections in Miru.
   Uses the same Free Dictionary source as ~/repo/lookup. Saving attaches a
   real floating annotation note on the selected word (same path as the
   toolbar's note button) so it persists through the normal *-note.md
   reconcile pipeline — not as a separate related document. */

import { applyNote, findAnnot, setNoteOnPassage } from '../js/annotations/model.js';
import { onAnnotToolbarHide, registerAnnotAction } from '../js/annotations/toolbar.js';
import { showToast } from '../js/ui/feedback.js';
import { fetchLookup } from './api.js';

const DICT_ICON =
  '<svg viewBox="0 0 24 24" width="16" height="16" aria-hidden="true">' +
  '<path fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" ' +
  'd="M4 5.5A2.5 2.5 0 0 1 6.5 3H20v16H6.5A2.5 2.5 0 0 0 4 21.5V5.5z"/>' +
  '<path fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" d="M8 7h8M8 11h8M8 15h5"/>' +
  '</svg>';

// Bookmark-plus: pin the lookup as a floating page note.
const SAVE_NOTE_ICON =
  '<svg viewBox="0 0 24 24" width="15" height="15" aria-hidden="true">' +
  '<path fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" ' +
  'd="M7 4h10a1 1 0 0 1 1 1v15l-6-3.5L6 20V5a1 1 0 0 1 1-1z"/>' +
  '<path fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" d="M12 8v5M9.5 10.5h5"/>' +
  '</svg>';

const COPY_ICON =
  '<svg viewBox="0 0 24 24" width="15" height="15" aria-hidden="true">' +
  '<rect x="8" y="8" width="12" height="12" rx="2" fill="none" stroke="currentColor" stroke-width="1.7"/>' +
  '<path fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" ' +
  'd="M16 8V6a2 2 0 0 0-2-2H6a2 2 0 0 0-2 2v8a2 2 0 0 0 2 2h2"/>' +
  '</svg>';

// Mirror backend normalizeLookupWord: English headword, letters + ' - .
const WORD_RE = /^[A-Za-z]+(?:['\u2019-][A-Za-z]+)*$/;

let lookupController = null;
let saving = false;

function isLookupWord(text) {
  const value = String(text || '').trim();
  if (!value || value.length > 40) return false;
  if (/\s/.test(value)) return false;
  return WORD_RE.test(value.replace(/^[^A-Za-z]+|[^A-Za-z]+$/g, ''));
}

function normalizeWord(text) {
  return String(text || '')
    .trim()
    .replace(/^[^A-Za-z]+|[^A-Za-z]+$/g, '')
    .toLowerCase();
}

function escapeHTML(value) {
  return String(value)
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;');
}

function phoneticLabel(result) {
  const texts = [];
  const seen = new Set();
  for (const item of result.phonetics || []) {
    const text = String(item.text || '').trim();
    if (!text || seen.has(text)) continue;
    seen.add(text);
    texts.push(text);
  }
  return texts.join(' · ');
}

function renderMeanings(result) {
  if (!result.meanings || !result.meanings.length) {
    return '<p class="annot-dict-empty">No definitions.</p>';
  }
  return result.meanings.map((meaning) => {
    const pos = escapeHTML(meaning.part_of_speech || 'meaning');
    const defs = (meaning.definitions || []).map((def) => {
      let html = `<li><span class="annot-dict-def">${escapeHTML(def.definition || '')}</span>`;
      if (def.example) {
        html += `<div class="annot-dict-example">“${escapeHTML(def.example)}”</div>`;
      }
      if (def.synonyms && def.synonyms.length) {
        html += `<div class="annot-dict-meta"><span>syn</span> ${escapeHTML(def.synonyms.join(', '))}</div>`;
      }
      if (def.antonyms && def.antonyms.length) {
        html += `<div class="annot-dict-meta"><span>ant</span> ${escapeHTML(def.antonyms.join(', '))}</div>`;
      }
      html += '</li>';
      return html;
    }).join('');
    return `<section class="annot-dict-meaning"><h4>${pos}</h4><ol>${defs}</ol></section>`;
  }).join('');
}

function renderPanel({ state, word, result, error, canSave }) {
  if (state === 'loading') {
    return (
      '<div class="annot-dict-panel" role="dialog" aria-label="Dictionary">' +
        `<header class="annot-dict-head"><strong>${escapeHTML(word)}</strong></header>` +
        '<p class="annot-dict-status">Looking up…</p>' +
      '</div>'
    );
  }
  if (state === 'error') {
    return (
      '<div class="annot-dict-panel" role="dialog" aria-label="Dictionary">' +
        `<header class="annot-dict-head"><strong>${escapeHTML(word)}</strong></header>` +
        `<p class="annot-dict-status is-error">${escapeHTML(error || 'Not found')}</p>` +
      '</div>'
    );
  }
  const phonetic = phoneticLabel(result);
  const saveTitle = canSave ? 'Save as note on this word' : 'Selection lost — select the word again';
  return (
    '<div class="annot-dict-panel" role="dialog" aria-label="Dictionary">' +
      '<header class="annot-dict-head">' +
        `<strong>${escapeHTML(result.word || word)}</strong>` +
        (phonetic ? `<span class="annot-dict-phonetic">${escapeHTML(phonetic)}</span>` : '') +
      '</header>' +
      `<div class="annot-dict-body">${renderMeanings(result)}</div>` +
      '<footer class="annot-dict-foot">' +
        '<span class="annot-dict-source">gdict · offline</span>' +
        '<div class="annot-dict-actions">' +
          '<button type="button" class="annot-dict-copy" title="Copy definition" aria-label="Copy definition">' +
            COPY_ICON +
          '</button>' +
          `<button type="button" class="annot-dict-save" ${canSave ? '' : 'disabled'} ` +
            `title="${saveTitle}" aria-label="${saveTitle}">` +
            SAVE_NOTE_ICON +
          '</button>' +
        '</div>' +
      '</footer>' +
    '</div>'
  );
}

// Compact rail-friendly body: keeps the full definition set as Markdown so
// the floating card renders it, and the long-note preview clamps as usual.
function noteBodyFromResult(result) {
  const markdown = String(result.markdown || '').trim();
  if (markdown) return markdown;
  const word = String(result.word || '').trim();
  return word ? `# ${word}\n` : '';
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

async function openDictionary(ctx) {
  const word = normalizeWord(ctx.text);
  if (!word) return;

  if (lookupController) lookupController.abort();
  lookupController = new AbortController();
  const { signal } = lookupController;

  ctx.toolbar.classList.add('is-dict');
  ctx.toolbar.innerHTML = renderPanel({ state: 'loading', word });
  ctx.position(ctx.range || ctx.annotEl);

  try {
    const result = await fetchLookup(word, { signal });
    if (signal.aborted || ctx.toolbar.hidden) return;
    const canSave = hasNoteAnchor(ctx);
    ctx.toolbar.innerHTML = renderPanel({ state: 'ready', word, result, canSave });
    ctx.position(ctx.range || ctx.annotEl);

    const copyBtn = ctx.toolbar.querySelector('.annot-dict-copy');
    if (copyBtn) {
      copyBtn.addEventListener('click', () => {
        void copyDictionaryNote(copyBtn, result);
      });
    }
    const saveBtn = ctx.toolbar.querySelector('.annot-dict-save');
    if (saveBtn && canSave) {
      saveBtn.addEventListener('click', () => {
        saveDictionaryNote(ctx, result);
      });
    }
  } catch (err) {
    if (signal.aborted || err.name === 'AbortError' || ctx.toolbar.hidden) return;
    ctx.toolbar.innerHTML = renderPanel({
      state: 'error',
      word,
      error: err.message || 'Lookup failed',
    });
    ctx.position(ctx.range || ctx.annotEl);
  }
}

async function copyDictionaryNote(button, result) {
  const text = noteBodyFromResult(result);
  if (!text) {
    showToast('Nothing to copy');
    return;
  }
  try {
    await navigator.clipboard.writeText(text);
    button.classList.add('is-copied');
    button.title = 'Copied';
    button.setAttribute('aria-label', 'Copied');
    showToast('Definition copied');
    window.setTimeout(() => {
      if (!button.isConnected) return;
      button.classList.remove('is-copied');
      button.title = 'Copy definition';
      button.setAttribute('aria-label', 'Copy definition');
    }, 1200);
  } catch (err) {
    console.error('membox: copy dictionary note failed', err);
    showToast(`Could not copy: ${err.message || err}`);
  }
}

function saveDictionaryNote(ctx, result) {
  if (saving) return;
  const noteText = noteBodyFromResult(result);
  if (!noteText) {
    showToast('Nothing to save');
    return;
  }
  if (!hasNoteAnchor(ctx)) {
    showToast('Selection lost — select the word again');
    return;
  }

  saving = true;
  const saveBtn = ctx.toolbar.querySelector('.annot-dict-save');
  if (saveBtn) {
    saveBtn.disabled = true;
    saveBtn.classList.add('is-busy');
    saveBtn.title = 'Saving…';
    saveBtn.setAttribute('aria-label', 'Saving…');
  }

  try {
    // Same path as the toolbar note button: wrap the word, float a card in
    // the margin rail, mark annotations dirty → reconcile to *-note.md.
    if (ctx.annotEl && ctx.annotEl.isConnected) {
      const entry = findAnnot(ctx.annotEl.dataset.annotId);
      if (!entry) throw new Error('annotation missing');
      setNoteOnPassage(entry, ctx.annotEl, noteText);
    } else {
      applyNote(ctx.range, noteText);
    }
    showToast(`Note on “${result.word || 'word'}”`);
    ctx.finish();
  } catch (err) {
    console.error('membox: saving dictionary note failed', err);
    showToast(`Could not save note: ${err.message || err}`);
    if (saveBtn) {
      saveBtn.disabled = false;
      saveBtn.classList.remove('is-busy');
      saveBtn.title = 'Save as note on this word';
      saveBtn.setAttribute('aria-label', 'Save as note on this word');
    }
  } finally {
    saving = false;
  }
}

export function initDictionary() {
  onAnnotToolbarHide(() => {
    if (lookupController) {
      lookupController.abort();
      lookupController = null;
    }
  });
  registerAnnotAction({
    id: 'dictionary',
    icon: DICT_ICON,
    title: 'Dictionary',
    when: ({ text }) => isLookupWord(text),
    run: (ctx) => {
      void openDictionary(ctx);
    },
  });
}
