/* membox backend HTTP client. Pure I/O: no DOM access, no adapter session
   state. Every helper throws an Error carrying the server's message on
   non-2xx responses, except where a caller legitimately handles status codes
   itself (the annotations GET uses 204 for "no sidecar"). */

async function request(path, { method = 'GET', body, keepalive = false, signal } = {}) {
  const options = { method, cache: 'no-store' };
  if (body !== undefined) {
    options.headers = { 'Content-Type': 'application/json' };
    options.body = JSON.stringify(body);
  }
  if (keepalive) options.keepalive = true;
  if (signal) options.signal = signal;
  const response = await fetch(path, options);
  if (!response.ok) throw new Error((await response.text()).trim() || `HTTP ${response.status}`);
  return response;
}

export async function streamTranslation(input, onEvent, { signal } = {}) {
  const response = await fetch('/api/translation/stream', {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      'X-Membox-Miru': '1',
    },
    body: JSON.stringify(input),
    cache: 'no-store',
    signal,
  });
  if (!response.ok) throw new Error((await response.text()).trim() || `HTTP ${response.status}`);
  if (!response.body) throw new Error('Translation stream is unavailable');

  const reader = response.body.getReader();
  const decoder = new TextDecoder();
  let buffer = '';
  const consume = (line) => {
    if (!line.trim()) return;
    const event = JSON.parse(line);
    if (event.type === 'error') throw new Error(event.text || 'Translation failed');
    onEvent(event);
  };
  while (true) {
    const { value, done } = await reader.read();
    buffer += decoder.decode(value || new Uint8Array(), { stream: !done });
    let newline = buffer.indexOf('\n');
    while (newline >= 0) {
      const line = buffer.slice(0, newline).replace(/\r$/, '');
      buffer = buffer.slice(newline + 1);
      consume(line);
      newline = buffer.indexOf('\n');
    }
    if (done) break;
  }
  consume(buffer);
}

export async function importPDF(file) {
  const form = new FormData();
  form.append('file', file, file.name || 'document.pdf');
  const response = await fetch('/api/pdfs/import', {
    method: 'POST',
    headers: { 'X-Membox-Miru': '1' },
    body: form,
    cache: 'no-store',
  });
  if (!response.ok) throw new Error((await response.text()).trim() || `HTTP ${response.status}`);
  return response.json();
}

export async function checkStatus() {
  try {
    const response = await fetch('/api/status', { cache: 'no-store' });
    return response.ok;
  } catch (err) {
    return false;
  }
}

// Filename/title travel in headers so the body stays raw Markdown.
export function decodeFilenameHeader(value) {
  if (!value) return '';
  try {
    return decodeURIComponent(value);
  } catch (err) {
    // Backward compatibility with older servers and literal `%` filenames.
    return value;
  }
}

export async function fetchDocument(id) {
  const response = await request(`/api/doc/${encodeURIComponent(id)}`);
  return {
    markdown: await response.text(),
    filename: decodeFilenameHeader(response.headers.get('X-Membox-Filename')),
    catalogTitle: decodeFilenameHeader(response.headers.get('X-Membox-Title')),
  };
}

// Raw response on purpose: 204 ("no stored sidecar") is a normal outcome the
// restore flow handles, not an error.
export function fetchAnnotationsRaw(id) {
  return fetch(`/api/doc/${encodeURIComponent(id)}/annotations`, { cache: 'no-store' });
}

export async function fetchAnnotationsRevision(id) {
  const response = await fetchAnnotationsRaw(id);
  if (response.status === 204 || !response.ok) return 0;
  return Number((await response.json()).revision) || 0;
}

export async function postAnnotations(id, sidecar, { keepalive = false, signal } = {}) {
  const response = await request(`/api/doc/${encodeURIComponent(id)}/annotations`, {
    method: 'POST',
    body: sidecar,
    keepalive,
    signal,
  });
  return response.json();
}

export async function postSync(payload) {
  const response = await request('/api/sync', { method: 'POST', body: payload });
  return response.json();
}

export async function renameDocument(id, filename, title) {
  const response = await request(`/api/doc/${encodeURIComponent(id)}/rename`, {
    method: 'POST',
    body: { filename, title },
  });
  return response.json();
}

export async function fetchRelated(id) {
  const response = await request(`/api/doc/${encodeURIComponent(id)}/related`);
  return response.json();
}

// body is either { target_id } (link existing) or { title, body } (create).
export async function postRelated(id, body) {
  const response = await request(`/api/doc/${encodeURIComponent(id)}/related`, {
    method: 'POST',
    body,
  });
  return response.json();
}

export async function searchCandidates({ purpose, documentID, query, signal }) {
  const params = new URLSearchParams({
    limit: '100',
    purpose: purpose === 'open' ? 'open' : 'related',
  });
  if (query) params.set('q', query);
  if (purpose === 'open' && documentID) params.set('focus', documentID);
  const endpoint = purpose === 'open'
    ? '/api/documents/candidates'
    : `/api/doc/${encodeURIComponent(documentID)}/related/candidates`;
  const response = await fetch(`${endpoint}?${params}`, { cache: 'no-store', signal });
  if (!response.ok) throw new Error((await response.text()).trim() || `HTTP ${response.status}`);
  return response.json();
}

export async function postReadStatus(id, status) {
  await request(`/api/doc/${encodeURIComponent(id)}/read-status`, {
    method: 'POST',
    body: { status },
    keepalive: true,
  });
}

// Fire-and-forget heartbeat; the caller attaches its own catch.
export function postPresence(params, gone) {
  return fetch(`/api/companion/presence?${params.toString()}`, {
    method: 'POST',
    headers: { 'X-Membox-Presence': '1' },
    keepalive: gone,
    cache: 'no-store',
  });
}
