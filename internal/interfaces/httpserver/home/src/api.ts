// API client for the membox remote node. The document API lands in M4
// (docs/membox-remote-stage1-plan.md); until then the server answers with an
// empty list and the UI falls back to demo data so the shell stays usable.
export interface RemoteDocument {
  id: string;
  title: string;
  source_url?: string;
  size_bytes: number;
  updated_at: string;
}

interface DocumentListResponse {
  documents: RemoteDocument[];
  next_page_token?: string;
}

export async function searchDocuments(query: string, signal?: AbortSignal): Promise<RemoteDocument[]> {
  const params = new URLSearchParams({ q: query, limit: '50' });
  const response = await fetch(`/v1/documents?${params}`, { signal });
  if (!response.ok) throw new Error(`documents: HTTP ${response.status}`);
  const body = (await response.json()) as DocumentListResponse;
  return body.documents ?? [];
}

// Demo rows shown only while the document API is still a stub (M4).
export const DEMO_DOCUMENTS: RemoteDocument[] = [
  { id: 'demo-1', title: 'attention-is-all-you-need.pdf', size_bytes: 2_145_288, updated_at: '2026-10-10T08:00:00Z' },
  { id: 'demo-2', title: 'ddia-notes.pdf', size_bytes: 812_340, updated_at: '2026-10-08T12:30:00Z' },
  { id: 'demo-3', title: 'pi-durable-architecture.pdf', size_bytes: 1_024_000, updated_at: '2026-10-05T02:02:00Z' },
];

export function formatSize(bytes: number): string {
  if (bytes >= 1 << 20) return `${(bytes / (1 << 20)).toFixed(1)} MB`;
  if (bytes >= 1 << 10) return `${(bytes / (1 << 10)).toFixed(0)} KB`;
  return `${bytes} B`;
}
