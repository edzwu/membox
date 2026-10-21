// The remote document API is deliberately kept in one small client so UI
// components don't need to know about HTTP status codes or CAS headers.

export interface RemoteDocument {
  id: string;
  revision: number;
  title: string;
  source_url?: string;
  size_bytes: number;
  mime_type?: string;
  updated_at: string;
}

interface DocumentListResponse {
  documents: RemoteDocument[];
  next_page_token?: string;
}

export type UploadProgress = (loaded: number, total: number) => void;

async function apiError(response: Response, fallback: string): Promise<Error> {
  const body = (await response.json().catch(() => ({}))) as { error?: string };
  return new Error(body.error ?? `${fallback}：HTTP ${response.status}`);
}

export async function getDocument(id: string, signal?: AbortSignal): Promise<RemoteDocument> {
  const response = await fetch(`/v1/documents/${encodeURIComponent(id)}`, { signal });
  if (!response.ok) throw await apiError(response, '加载文档失败');
  return (await response.json()) as RemoteDocument;
}

export async function searchDocuments(query = '', signal?: AbortSignal): Promise<RemoteDocument[]> {
  const documents: RemoteDocument[] = [];
  let pageToken = '';
  do {
    const params = new URLSearchParams({ q: query, limit: '100' });
    if (pageToken) params.set('page_token', pageToken);
    const response = await fetch(`/v1/documents?${params}`, { signal });
    if (!response.ok) throw await apiError(response, '加载文档失败');
    const body = (await response.json()) as DocumentListResponse;
    documents.push(...(body.documents ?? []));
    pageToken = body.next_page_token ?? '';
  } while (pageToken);
  return documents;
}

/** XHR is used only for uploads because fetch still has no upload progress API. */
function uploadRequest<T>(url: string, method: 'POST' | 'PUT', form: FormData, onProgress?: UploadProgress, headers?: Record<string, string>): Promise<T> {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    xhr.open(method, url);
    xhr.withCredentials = true;
    for (const [name, value] of Object.entries(headers ?? {})) xhr.setRequestHeader(name, value);
    xhr.upload.addEventListener('progress', (event) => {
      if (event.lengthComputable) onProgress?.(event.loaded, event.total);
    });
    xhr.addEventListener('error', () => reject(new Error('网络错误，请检查连接后重试')));
    xhr.addEventListener('abort', () => reject(new Error('上传已取消')));
    xhr.addEventListener('load', () => {
      let body: unknown = {};
      try {
        body = xhr.responseText ? JSON.parse(xhr.responseText) : {};
      } catch {
        // The status is still useful when a proxy returns a non-JSON error.
      }
      if (xhr.status < 200 || xhr.status >= 300) {
        reject(new Error((body as { error?: string }).error ?? `请求失败：HTTP ${xhr.status}`));
        return;
      }
      resolve(body as T);
    });
    xhr.send(form);
  });
}

export function uploadDocument(file: File, onProgress?: UploadProgress): Promise<RemoteDocument> {
  const form = new FormData();
  form.append('file', file, file.name);
  return uploadRequest<RemoteDocument>('/v1/documents', 'POST', form, onProgress);
}

export async function renameDocument(doc: RemoteDocument, title: string): Promise<RemoteDocument> {
  const response = await fetch(`/v1/documents/${doc.id}`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json', 'If-Match': `"rev-${doc.revision}"` },
    body: JSON.stringify({ title, source_url: doc.source_url ?? '' }),
  });
  if (!response.ok) throw await apiError(response, '重命名失败');
  return (await response.json()) as RemoteDocument;
}

export function replaceContent(doc: RemoteDocument, file: File, onProgress?: UploadProgress): Promise<RemoteDocument> {
  const form = new FormData();
  form.append('file', file, file.name);
  return uploadRequest<RemoteDocument>(
    `/v1/documents/${doc.id}/content`,
    'PUT',
    form,
    onProgress,
    { 'If-Match': `"rev-${doc.revision}"` },
  );
}

export async function deleteDocument(doc: RemoteDocument): Promise<void> {
  const response = await fetch(`/v1/documents/${doc.id}`, {
    method: 'DELETE',
    headers: { 'If-Match': `"rev-${doc.revision}"` },
  });
  if (!response.ok) throw await apiError(response, '删除失败');
}

export function contentUrl(doc: RemoteDocument): string {
  return `/v1/documents/${doc.id}/content`;
}

export function isPdf(doc: RemoteDocument): boolean {
  // MIME is authoritative after a content replacement; the title may still
  // carry the original extension.
  return doc.mime_type ? doc.mime_type === 'application/pdf' : /\.pdf$/i.test(doc.title);
}

export function formatSize(bytes: number): string {
  if (!bytes) return '—';
  if (bytes >= 1 << 30) return `${(bytes / (1 << 30)).toFixed(1)} GB`;
  if (bytes >= 1 << 20) return `${(bytes / (1 << 20)).toFixed(1)} MB`;
  if (bytes >= 1 << 10) return `${(bytes / (1 << 10)).toFixed(0)} KB`;
  return `${bytes} B`;
}

export function formatDate(value: string): string {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return '未知时间';
  return new Intl.DateTimeFormat('zh-CN', { year: 'numeric', month: 'short', day: 'numeric' }).format(date);
}
