/* Bottom-left agentic search over the membox Markdown catalog.
   Uses Companion → Pi Agent (local model) with membox_search_documents /
   membox_read_document tools. Lives in the membox adapter, not Miru core. */

import { showToast } from '../../js/ui/feedback.js';
import { session } from '../session.js';
import { replaceDocumentID, loadFromMembox } from '../document.js';
import { closeOtherModals, registerModal } from '../modals.js';
import { postSync } from '../api.js';
import {
  abortAgentRun,
  createAgentSession,
  fetchAgentStatus,
  getAgentSession,
  promptAgent,
  subscribeAgentEvents,
} from './client.js';

const SESSION_STORAGE_KEY = 'membox-agent-search-session';
const HISTORY_STORAGE_KEY = 'membox-agent-search-history';
const HISTORY_LIMIT = 50;
/** Local model for search — pinned so a stale cloud default (e.g. xai) can't silently time out. */
const SEARCH_MODEL = { provider: 'ollama', id: 'qwen3:14b', name: 'qwen3:14b' };

const SEARCH_ICON =
  '<svg viewBox="0 0 24 24" width="15" height="15" aria-hidden="true">' +
  '<circle cx="11" cy="11" r="6.25" fill="none" stroke="currentColor" stroke-width="1.7"/>' +
  '<path d="M16.2 16.2 20 20" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round"/>' +
  '</svg>';

const SEND_ICON =
  '<svg viewBox="0 0 24 24" width="15" height="15" aria-hidden="true">' +
  '<path fill="currentColor" d="M3.4 4.2 21 12 3.4 19.8l2.1-6.3L15 12l-9.5-1.5L3.4 4.2z"/>' +
  '</svg>';

const STOP_ICON =
  '<svg viewBox="0 0 24 24" width="14" height="14" aria-hidden="true">' +
  '<rect x="6" y="6" width="12" height="12" rx="2" fill="currentColor"/>' +
  '</svg>';

const CLOSE_ICON =
  '<svg viewBox="0 0 24 24" width="14" height="14" aria-hidden="true">' +
  '<path fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" d="M6 6l12 12M18 6 6 18"/>' +
  '</svg>';

const HISTORY_ICON =
  '<svg viewBox="0 0 24 24" width="14" height="14" aria-hidden="true">' +
  '<path d="M12 5a7 7 0 1 1-6.4 4.1" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round"/>' +
  '<path d="M4.6 4.4v4.6h4.6" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"/>' +
  '<path d="M12 9v3.4l2.4 1.4" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"/>' +
  '</svg>';

/** @type {HTMLElement | null} */
let panel = null;
/** @type {HTMLElement | null} */
let transcriptEl = null;
/** @type {HTMLTextAreaElement | null} */
let inputEl = null;
/** @type {HTMLButtonElement | null} */
let sendBtn = null;
/** @type {HTMLButtonElement | null} */
let historyBtn = null;
/** @type {HTMLElement | null} */
let statusEl = null;

let open = false;
let busy = false;
let historyOpen = false;
let sessionID = '';
let lastEventID = '';
let runID = '';
/** @type {{ close(): void } | null} */
let subscription = null;
let rafPending = 0;
let savingHistory = false;
let lastSavedRunID = '';

/** @type {{ kind: string, text: string, running?: boolean, tool?: string, status?: string, args?: any }[]} */
let lines = [];

function escapeHTML(value) {
  return String(value)
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;');
}

function loadStoredSessionID() {
  try {
    return sessionStorage.getItem(SESSION_STORAGE_KEY) || '';
  } catch {
    return '';
  }
}

function storeSessionID(id) {
  sessionID = id || '';
  try {
    if (sessionID) sessionStorage.setItem(SESSION_STORAGE_KEY, sessionID);
    else sessionStorage.removeItem(SESSION_STORAGE_KEY);
  } catch {
    // ignore
  }
}

function buildSearchPrompt(query) {
  return [
    'You are doing an agentic search over the membox Markdown catalog (all indexed documents).',
    'Workflow:',
    '1. Identify the rarest literal keyword(s) in the query — names, code identifiers, product terms (e.g. ChatService).',
    '2. Call membox_grep_documents with EACH rare keyword alone (one call per keyword, no extra words).',
    '3. Call membox_search_documents for the conceptual part (e.g. agent, 协作, harness). Multi-word queries are AND-ed.',
    '4. Intersect/cross-reference the hits, then membox_read_document the most promising ones when snippets are not enough.',
    '5. If a search returns 0 hits, retry with fewer or different terms before concluding nothing matches.',
    '',
    'Reply with:',
    '- A ranked list: title · membox://doc/<uuid> · one-line why it matches (quote the matched keyword context)',
    '- A short synthesis only if the question needs it',
    '',
    'Rules: do not invent documents; cite only UUIDs from tool results; never report a document you did not see in a tool result.',
    '',
    `Query: ${query}`,
  ].join('\n');
}

function renderAssistantHTML(text) {
  const source = String(text || '');
  // Prefer membox://doc links even before markdown-it linkify.
  const withLinks = source.replace(
    /membox:\/\/doc\/([0-9a-fA-F-]{8,})/g,
    '[$1](membox://doc/$1)',
  );
  if (typeof window.markdownit === 'function' && typeof window.DOMPurify === 'function') {
    const md = window.markdownit({ html: false, linkify: true, breaks: true });
    const defaultLinkOpen = md.renderer.rules.link_open || ((tokens, idx, options, _env, self) =>
      self.renderToken(tokens, idx, options));
    md.renderer.rules.link_open = (tokens, idx, options, env, self) => {
      const token = tokens[idx];
      const hrefIdx = token.attrIndex('href');
      if (hrefIdx >= 0) {
        const href = token.attrs[hrefIdx][1] || '';
        const match = href.match(/^membox:\/\/doc\/([0-9a-fA-F-]{8,})$/i);
        if (match) {
          token.attrs[hrefIdx][1] = `/?id=${encodeURIComponent(match[1])}`;
          token.attrSet('data-membox-id', match[1]);
          token.attrSet('class', 'membox-agent-doc-link');
        } else {
          token.attrSet('target', '_blank');
          token.attrSet('rel', 'noopener noreferrer');
        }
      }
      return defaultLinkOpen(tokens, idx, options, env, self);
    };
    const dirty = md.render(withLinks);
    return window.DOMPurify.sanitize(dirty, {
      USE_PROFILES: { html: true },
      ADD_ATTR: ['target', 'rel', 'data-membox-id', 'class'],
    });
  }
  return `<p>${escapeHTML(source).replace(/\n/g, '<br>')}</p>`;
}

function setStatus(text, isError = false) {
  if (!statusEl) return;
  statusEl.textContent = text || '';
  statusEl.classList.toggle('is-error', Boolean(isError && text));
  statusEl.hidden = !text;
}

function updateSendButton() {
  if (!sendBtn) return;
  sendBtn.disabled = busy && !runID ? true : false;
  sendBtn.innerHTML = busy && runID ? STOP_ICON : SEND_ICON;
  sendBtn.title = busy && runID ? 'Stop' : 'Search (⌘↵)';
  sendBtn.setAttribute('aria-label', busy && runID ? 'Stop' : 'Search');
}

function scheduleTranscriptPaint() {
  if (rafPending) return;
  rafPending = requestAnimationFrame(() => {
    rafPending = 0;
    paintTranscript();
  });
}

function paintTranscript() {
  if (!transcriptEl || historyOpen) return;
  const stick = transcriptEl.scrollHeight - transcriptEl.scrollTop - transcriptEl.clientHeight < 48;
  if (!lines.length) {
    transcriptEl.innerHTML =
      '<p class="membox-agent-empty">Search every Markdown note with the local agent. ' +
      'It will call search/read tools and cite <code>membox://doc/…</code> hits.</p>';
    return;
  }
  const html = lines.map((line) => {
    if (line.kind === 'user') {
      return `<div class="membox-agent-line is-user"><div class="membox-agent-bubble">${escapeHTML(line.text)}</div></div>`;
    }
    if (line.kind === 'tool') {
      const label = line.status ? `${line.tool || 'tool'} · ${line.status}` : `${line.tool || 'tool'}…`;
      return `<div class="membox-agent-line is-tool"><span class="membox-agent-tool">${escapeHTML(label)}</span></div>`;
    }
    if (line.kind === 'error') {
      return `<div class="membox-agent-line is-error">${escapeHTML(line.text)}</div>`;
    }
    if (line.kind === 'system') {
      return `<div class="membox-agent-line is-system">${escapeHTML(line.text)}</div>`;
    }
    // assistant
    const body = renderAssistantHTML(line.text);
    const cursor = line.running ? '<span class="membox-agent-cursor" aria-hidden="true"></span>' : '';
    return `<div class="membox-agent-line is-assistant"><div class="membox-agent-md">${body}${cursor}</div></div>`;
  }).join('');
  transcriptEl.innerHTML = html;
  if (stick) transcriptEl.scrollTop = transcriptEl.scrollHeight;
}

function appendUser(text) {
  lines.push({ kind: 'user', text });
  scheduleTranscriptPaint();
}

function appendSystem(text) {
  lines.push({ kind: 'system', text });
  scheduleTranscriptPaint();
}

function appendError(text) {
  lines.push({ kind: 'error', text });
  scheduleTranscriptPaint();
}

function appendToolStart(tool, args) {
  lines.push({ kind: 'tool', tool: tool || 'tool', status: '', args: args || null });
  scheduleTranscriptPaint();
}

function appendToolDone(tool, status) {
  const name = tool || 'tool';
  for (let i = lines.length - 1; i >= 0; i--) {
    if (lines[i].kind === 'tool' && lines[i].tool === name && !lines[i].status) {
      lines[i].status = status || 'done';
      scheduleTranscriptPaint();
      return;
    }
  }
  lines.push({ kind: 'tool', tool: name, status: status || 'done' });
  scheduleTranscriptPaint();
}

function appendAssistantDelta(text) {
  if (!text) return;
  const last = lines[lines.length - 1];
  if (last && last.kind === 'assistant' && last.running) {
    last.text += text;
  } else {
    lines.push({ kind: 'assistant', text, running: true });
  }
  scheduleTranscriptPaint();
}

function finishAssistant() {
  const last = lines[lines.length - 1];
  if (last && last.kind === 'assistant') last.running = false;
  scheduleTranscriptPaint();
}

/** Rewrite membox://doc/<uuid> references so the saved note navigates inside
    Miru via the existing ?id= wiki-link handling. Paren form first so bare
    URIs left over are only the unlinked ones. */
function linkifyMemboxRefs(text) {
  return String(text || '')
    .replace(/\]\(\s*membox:\/\/doc\/([0-9a-fA-F-]{8,})\s*\)/g, '](?id=$1)')
    .replace(/membox:\/\/doc\/([0-9a-fA-F-]{8,})/g, (_m, id) => `[doc ${id.slice(0, 8)}](?id=${id})`);
}

function loadHistoryEntries() {
  try {
    const value = JSON.parse(localStorage.getItem(HISTORY_STORAGE_KEY));
    return Array.isArray(value) ? value : [];
  } catch {
    return [];
  }
}

function pushHistoryEntry(entry) {
  const list = loadHistoryEntries().filter((item) => item.runID !== entry.runID);
  list.unshift(entry);
  try {
    localStorage.setItem(HISTORY_STORAGE_KEY, JSON.stringify(list.slice(0, HISTORY_LIMIT)));
  } catch {
    // storage full or unavailable — the membox document is still saved
  }
}

function firstLine(text) {
  return String(text || '').split('\n').map((s) => s.trim()).find(Boolean) || '';
}

function buildHistoryMarkdown({ query, answer, tools, settledRunID }) {
  const iso = new Date().toISOString();
  const heading = firstLine(query).slice(0, 80) || 'Untitled search';
  const multiLine = query.trim().includes('\n');
  const toolSection = tools.length
    ? [
        '',
        '---',
        '',
        '**Tools**',
        '',
        ...tools.map((t) => {
          const arg = t.args?.query || t.args?.pattern || t.args?.id || '';
          const argText = arg ? ` \`${String(arg).replace(/`/g, "'")}\`` : '';
          return `- \`${t.tool}\`${argText} · ${t.status || 'done'}`;
        }),
        '',
      ].join('\n')
    : '';
  return [
    '---',
    'kind: "agent-search"',
    `model: "${SEARCH_MODEL.provider}/${SEARCH_MODEL.id}"`,
    `agent_session: "${sessionID}"`,
    `agent_run: "${settledRunID}"`,
    `created_at: "${iso}"`,
    '---',
    '',
    `# Search: ${heading}`,
    '',
    multiLine ? `> ${query.trim().split('\n').join('\n> ')}\n` : '',
    linkifyMemboxRefs(answer).trim(),
    toolSection,
  ].join('\n');
}

/** Persist the just-settled Q&A turn as a membox Markdown document. */
async function saveSearchHistory(settledRunID) {
  if (!settledRunID || savingHistory || lastSavedRunID === settledRunID) return;
  if (!session.connected) return;
  const userIndex = lines.map((l) => l.kind).lastIndexOf('user');
  if (userIndex < 0) return;
  const turn = lines.slice(userIndex + 1);
  const answer = turn
    .filter((l) => l.kind === 'assistant')
    .map((l) => l.text)
    .join('\n\n')
    .trim();
  if (answer.length < 10) return;
  const query = lines[userIndex].text.trim();
  const tools = turn.filter((l) => l.kind === 'tool');
  savingHistory = true;
  try {
    const title = `Search: ${firstLine(query).slice(0, 60) || 'untitled'}`;
    const body = buildHistoryMarkdown({ query, answer, tools, settledRunID });
    const result = await postSync({ id: '', title, body });
    lastSavedRunID = settledRunID;
    pushHistoryEntry({ id: result.id, query: firstLine(query), ts: Date.now(), runID: settledRunID });
    setStatus(`Saved · ${String(result.id).slice(0, 8)}`);
    appendSystem(`Saved to membox · ${String(result.id).slice(0, 8)}`);
  } catch (err) {
    console.warn('membox agent search: history save failed', err);
    setStatus(`History save failed — ${err?.message || 'unknown error'}`, true);
  } finally {
    savingHistory = false;
  }
}

function formatHistoryWhen(ts) {
  try {
    return new Date(ts).toLocaleString(undefined, {
      month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit',
    });
  } catch {
    return '';
  }
}

function renderHistoryList() {
  if (!transcriptEl) return;
  const entries = loadHistoryEntries();
  if (!entries.length) {
    transcriptEl.innerHTML =
      '<p class="membox-agent-empty">No saved searches yet. Completed answers are stored as membox notes automatically.</p>';
    return;
  }
  transcriptEl.innerHTML = entries.map((entry) => (
    `<button type="button" class="membox-agent-history-item" data-history-id="${escapeHTML(entry.id)}">` +
    `<span class="membox-agent-history-query">${escapeHTML(entry.query || 'Untitled search')}</span>` +
    `<span class="membox-agent-history-when">${escapeHTML(formatHistoryWhen(entry.ts))}</span>` +
    '</button>'
  )).join('');
}

function toggleHistoryView() {
  historyOpen = !historyOpen;
  historyBtn?.classList.toggle('is-open', historyOpen);
  historyBtn?.setAttribute('aria-pressed', String(historyOpen));
  if (historyOpen) renderHistoryList();
  else paintTranscript();
}

function stopSubscription() {
  if (subscription) {
    subscription.close();
    subscription = null;
  }
}

function eventPayload(event) {
  const raw = event?.payload;
  if (!raw) return {};
  if (typeof raw === 'string') {
    try { return JSON.parse(raw); } catch { return {}; }
  }
  return raw;
}

/** After a settled run with no streamed text, surface the worker's error
    (e.g. model timeout) instead of staying silent. */
async function surfaceSettledError() {
  const lastAssistant = [...lines].reverse().find((l) => l.kind === 'assistant');
  const streamed = lastAssistant && lastAssistant.text && lastAssistant.text.trim();
  if (streamed) return;
  try {
    const snap = await getAgentSession(sessionID);
    const messages = Array.isArray(snap?.messages) ? snap.messages : [];
    for (let i = messages.length - 1; i >= 0; i--) {
      const msg = messages[i];
      if (!msg || msg.role !== 'assistant') continue;
      const reason = msg.errorMessage || (msg.stopReason === 'error' ? 'Model request failed' : '');
      if (reason) {
        appendError(`${reason} · model ${msg.provider || ''}/${msg.model || ''}`);
        setStatus(reason, true);
        return;
      }
      const text = (msg.content || [])
        .filter((c) => c?.type === 'text')
        .map((c) => c.text || '')
        .join('');
      if (text.trim()) {
        appendAssistantDelta(text);
        finishAssistant();
        return;
      }
    }
    if (!streamed) setStatus('No response from model', true);
  } catch {
    // keep quiet — SSE settle already cleared the busy state
  }
}

function handleAgentEvent(event) {
  if (!event || (sessionID && event.session_id && event.session_id !== sessionID)) return;
  if (event.id) lastEventID = event.id;
  const payload = eventPayload(event);
  switch (event.type) {
    case 'assistant.delta': {
      appendAssistantDelta(payload.text || '');
      break;
    }
    case 'assistant.completed':
      finishAssistant();
      break;
    case 'run.started':
      runID = event.run_id || runID;
      busy = true;
      setStatus('Searching…');
      updateSendButton();
      break;
    case 'run.settled':
    case 'run.interrupted': {
      const settledRunID = event.run_id || runID;
      runID = '';
      busy = false;
      finishAssistant();
      setStatus(event.type === 'run.interrupted' ? 'Stopped' : '');
      updateSendButton();
      if (event.type === 'run.settled') {
        void surfaceSettledError().then(() => void saveSearchHistory(settledRunID));
      }
      break;
    }
    case 'tool.started':
      appendToolStart(payload.tool || 'tool', payload.args);
      setStatus(`Tool · ${payload.tool || 'running'}…`);
      break;
    case 'tool.completed':
      appendToolDone(payload.tool || 'tool', payload.status || 'done');
      break;
    case 'agent.error':
      appendError(payload.message || 'Agent error');
      setStatus(payload.message || 'Agent error', true);
      busy = false;
      runID = '';
      updateSendButton();
      break;
    case 'stream.reset':
      appendSystem('Stream reset — reconnecting');
      void ensureSubscription();
      break;
    default:
      break;
  }
}

async function ensureSubscription() {
  if (!sessionID) return;
  stopSubscription();
  subscription = subscribeAgentEvents(sessionID, lastEventID, {
    onEvent: handleAgentEvent,
    onError: (err) => {
      if (!open) return;
      console.warn('membox agent search sse', err);
    },
  });
}

async function ensureSession() {
  if (sessionID) {
    try {
      const snap = await getAgentSession(sessionID);
      lastEventID = snap?.last_event_id || lastEventID || '';
      await ensureSubscription();
      return sessionID;
    } catch {
      storeSessionID('');
      lastEventID = '';
    }
  }
  const stored = loadStoredSessionID();
  if (stored) {
    try {
      const snap = await getAgentSession(stored);
      // Stale session pinned to an unreachable model (e.g. xai timeouts):
      // drop it and start fresh with the pinned local model.
      const provider = snap?.session?.model?.provider;
      if (provider && provider !== SEARCH_MODEL.provider) {
        storeSessionID('');
        lastEventID = '';
      } else {
        storeSessionID(snap?.session?.id || stored);
        lastEventID = snap?.last_event_id || '';
        await ensureSubscription();
        return sessionID;
      }
    } catch {
      storeSessionID('');
    }
  }
  const created = await createAgentSession({ title: 'Miru search', model: SEARCH_MODEL });
  storeSessionID(created?.session?.id || '');
  lastEventID = created?.last_event_id || '';
  if (!sessionID) throw new Error('Agent session was not created');
  await ensureSubscription();
  return sessionID;
}

async function probeAgent() {
  const status = await fetchAgentStatus();
  if (!status?.enabled) throw new Error('Agent is disabled on this Companion');
  if (!status?.available) {
    const detail = status?.error || status?.state || 'unavailable';
    throw new Error(`Local agent unavailable (${detail})`);
  }
  return status;
}

function autosizeInput() {
  if (!inputEl) return;
  inputEl.style.height = 'auto';
  const max = 5 * 20;
  inputEl.style.height = `${Math.min(Math.max(inputEl.scrollHeight, 22), max)}px`;
}

async function runSearch() {
  const query = (inputEl?.value || '').trim();
  if (!query) return;
  if (!session.connected) {
    showToast('Connect to membox before searching');
    return;
  }
  if (busy && runID) {
    try {
      await abortAgentRun(sessionID, runID);
    } catch (err) {
      showToast(err?.message || 'Could not stop');
    }
    return;
  }
  if (busy) return;

  busy = true;
  historyOpen = false;
  historyBtn?.classList.remove('is-open');
  updateSendButton();
  setStatus('Starting local agent…');
  appendUser(query);
  inputEl.value = '';
  autosizeInput();

  try {
    await probeAgent();
    await ensureSession();
    // Give SSE a beat to attach before the prompt lands.
    await new Promise((r) => setTimeout(r, 40));
    const accepted = await promptAgent(sessionID, buildSearchPrompt(query), {
      documentID: session.documentID || '',
    });
    runID = accepted?.run_id || '';
    setStatus(runID ? 'Searching…' : 'Waiting…');
    updateSendButton();
  } catch (err) {
    console.error('membox agent search failed', err);
    busy = false;
    runID = '';
    const message = err?.message || 'Search failed';
    appendError(message);
    setStatus(message, true);
    updateSendButton();
    showToast(message);
  }
}

async function openDoc(id) {
  if (!id) return;
  try {
    replaceDocumentID(id);
    await loadFromMembox();
    // Keep the panel open so the user can keep browsing hits.
  } catch (err) {
    showToast(err?.message || 'Could not open document');
  }
}

function onTranscriptClick(event) {
  const historyItem = event.target?.closest?.('[data-history-id]');
  if (historyItem) {
    event.preventDefault();
    void openDoc(historyItem.getAttribute('data-history-id'));
    return;
  }
  const link = event.target?.closest?.('a[data-membox-id], a.membox-agent-doc-link');
  if (!link) return;
  const id = link.getAttribute('data-membox-id')
    || (link.getAttribute('href') || '').match(/[?&]id=([^&]+)/)?.[1]
    || '';
  if (!id) return;
  event.preventDefault();
  void openDoc(decodeURIComponent(id));
}

function openPanel() {
  if (!panel) return;
  closeOtherModals(modalHandle);
  open = true;
  panel.hidden = false;
  setTimeout(() => inputEl?.focus(), 0);
  if (session.connected) {
    void probeAgent()
      .then(() => setStatus(`Local model · ${SEARCH_MODEL.id}`))
      .catch((err) => setStatus(err?.message || 'Agent unavailable', true));
  } else {
    setStatus('Not connected to membox', true);
  }
}

function closePanel() {
  open = false;
  if (panel) panel.hidden = true;
  // Keep SSE alive while a run is in flight so results finish in the background.
  if (!busy) stopSubscription();
}

const modalHandle = {
  isOpen: () => open,
  close: closePanel,
};

function buildDOM() {
  panel = document.createElement('section');
  panel.id = 'membox-agent-search-panel';
  panel.className = 'membox-agent-search-panel';
  panel.hidden = true;
  panel.setAttribute('role', 'dialog');
  panel.setAttribute('aria-label', 'Agentic search');
  panel.innerHTML = `
    <header class="membox-agent-search-head">
      <div class="membox-agent-search-title">Search</div>
      <div class="membox-agent-search-actions">
        <button type="button" class="membox-agent-search-history" aria-label="Search history" aria-pressed="false" title="Search history">${HISTORY_ICON}</button>
        <button type="button" class="membox-agent-search-close" aria-label="Close">${CLOSE_ICON}</button>
      </div>
    </header>
    <p class="membox-agent-search-status" hidden></p>
    <div class="membox-agent-search-transcript" tabindex="0"></div>
    <footer class="membox-agent-search-composer">
      <textarea class="membox-agent-search-input" rows="1" placeholder="Ask across all Markdown… (⌘↵ to send)" autocomplete="off"></textarea>
      <button type="button" class="membox-agent-search-send" aria-label="Search" title="Search (⌘↵)">${SEND_ICON}</button>
    </footer>
  `;
  document.body.appendChild(panel);

  transcriptEl = panel.querySelector('.membox-agent-search-transcript');
  inputEl = panel.querySelector('.membox-agent-search-input');
  sendBtn = panel.querySelector('.membox-agent-search-send');
  statusEl = panel.querySelector('.membox-agent-search-status');
  historyBtn = panel.querySelector('.membox-agent-search-history');

  panel.querySelector('.membox-agent-search-close')?.addEventListener('click', closePanel);
  historyBtn?.addEventListener('click', toggleHistoryView);
  sendBtn.addEventListener('click', () => { void runSearch(); });
  transcriptEl.addEventListener('click', onTranscriptClick);
  inputEl.addEventListener('input', autosizeInput);
  inputEl.addEventListener('keydown', (event) => {
    // Enter inserts a newline; ⌘↵ / Ctrl+↵ sends.
    if (event.key !== 'Enter' || event.shiftKey || event.isComposing) return;
    if (event.metaKey || event.ctrlKey) {
      event.preventDefault();
      void runSearch();
    }
  });

  // Cmd/Ctrl+K toggles the search panel (dock magnifier removed).
  document.addEventListener('keydown', (event) => {
    if (event.repeat || event.isComposing || event.keyCode === 229) return;
    if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 'k') {
      event.preventDefault();
      if (open) closePanel();
      else openPanel();
    }
  });
  document.addEventListener('keydown', (event) => {
    if (event.key === 'Escape' && open) {
      event.stopPropagation();
      closePanel();
    }
  });

  registerModal(modalHandle);
  paintTranscript();
  updateSendButton();
}

export function initAgentSearch() {
  if (panel) return;
  buildDOM();
}
