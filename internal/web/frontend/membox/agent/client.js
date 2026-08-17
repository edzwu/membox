/* Companion Agent HTTP client for the Miru/membox web adapter.
   Browser same-origin + CSRF; EventSource for the SSE event stream. */

const CLIENT_STORAGE_KEY = 'membox-agent-client-id';
const CSRF_HEADER = 'X-Membox-CSRF';
const CLIENT_HEADER = 'X-Membox-Agent-Client';

function randomId(prefix) {
  if (globalThis.crypto?.randomUUID) return `${prefix}-${crypto.randomUUID()}`;
  return `${prefix}-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 10)}`;
}

export function getAgentClientID() {
  try {
    const existing = sessionStorage.getItem(CLIENT_STORAGE_KEY);
    if (existing) return existing;
    const id = randomId('web');
    sessionStorage.setItem(CLIENT_STORAGE_KEY, id);
    return id;
  } catch {
    return randomId('web');
  }
}

function agentHeaders(mutating = false, extra = {}) {
  const headers = {
    Accept: 'application/json',
    [CLIENT_HEADER]: getAgentClientID(),
    ...extra,
  };
  if (mutating) {
    headers[CSRF_HEADER] = '1';
    headers['Content-Type'] = 'application/json';
  }
  return headers;
}

async function parseError(response) {
  const text = (await response.text()).trim();
  if (!text) return `HTTP ${response.status}`;
  try {
    const body = JSON.parse(text);
    return body?.error?.message || body?.message || text;
  } catch {
    return text;
  }
}

async function agentFetch(path, { method = 'GET', body, signal, idempotencyKey } = {}) {
  const mutating = method !== 'GET' && method !== 'HEAD';
  const headers = agentHeaders(mutating);
  if (idempotencyKey) headers['Idempotency-Key'] = idempotencyKey;
  const response = await fetch(path, {
    method,
    headers,
    body: body === undefined ? undefined : JSON.stringify(body),
    cache: 'no-store',
    signal,
  });
  if (!response.ok) throw new Error(await parseError(response));
  if (response.status === 204) return null;
  return response.json();
}

export async function fetchAgentStatus(signal) {
  return agentFetch('/api/agent/status', { signal });
}

export async function createAgentSession({ title = 'Miru search', model, signal } = {}) {
  const body = { title };
  if (model?.provider && model?.id) body.model = { provider: model.provider, id: model.id, name: model.name };
  return agentFetch('/api/agent/sessions', {
    method: 'POST',
    body,
    signal,
    idempotencyKey: randomId('create'),
  });
}

export async function getAgentSession(sessionID, signal) {
  return agentFetch(`/api/agent/sessions/${encodeURIComponent(sessionID)}`, { signal });
}

export async function promptAgent(sessionID, text, { documentID = '', signal } = {}) {
  const body = { text };
  if (documentID) body.context = { document_id: documentID };
  return agentFetch(`/api/agent/sessions/${encodeURIComponent(sessionID)}/messages`, {
    method: 'POST',
    body,
    signal,
    idempotencyKey: randomId('prompt'),
  });
}

export async function abortAgentRun(sessionID, runID, signal) {
  return agentFetch(
    `/api/agent/sessions/${encodeURIComponent(sessionID)}/runs/${encodeURIComponent(runID)}/abort`,
    { method: 'POST', body: {}, signal },
  );
}

/** Subscribe to session SSE. Returns { close } and invokes onEvent/onError. */
export function subscribeAgentEvents(sessionID, after, { onEvent, onError } = {}) {
  let url = `/api/agent/sessions/${encodeURIComponent(sessionID)}/events`;
  if (after) url += `?after=${encodeURIComponent(after)}`;
  const source = new EventSource(url);
  let closed = false;

  const handlePayload = (raw) => {
    if (closed || !raw) return;
    try {
      const event = JSON.parse(raw);
      onEvent?.(event);
    } catch (err) {
      onError?.(err);
    }
  };

  source.addEventListener('agent', (ev) => handlePayload(ev.data));
  source.onmessage = (ev) => handlePayload(ev.data);
  source.onerror = () => {
    if (closed) return;
    // EventSource auto-reconnects; only surface a soft error when CLOSED.
    if (source.readyState === EventSource.CLOSED) {
      onError?.(new Error('Agent event stream closed'));
    }
  };

  return {
    close() {
      closed = true;
      source.close();
    },
  };
}
