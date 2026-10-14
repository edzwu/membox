import { useCallback, useEffect, useRef, useState } from 'react';

interface DocItem {
  id: string;
  revision: number;
  title: string;
  source_url?: string;
  updated_at: string;
}

// DocumentsPanel: browser-side CRUD over /v1/documents. Upload (drag or
// pick), list, open (inline content URL), delete (If-Match from the list's
// revision). PDFs and Markdown only (server enforces magic bytes).
export function DocumentsPanel({ onClose }: { onClose: () => void }) {
  const [docs, setDocs] = useState<DocItem[]>([]);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const [dragOver, setDragOver] = useState(false);
  const fileInput = useRef<HTMLInputElement>(null);

  const refresh = useCallback(async () => {
    try {
      const response = await fetch('/v1/documents?limit=100');
      if (!response.ok) throw new Error(`HTTP ${response.status}`);
      const body = await response.json();
      setDocs(body.documents ?? []);
    } catch (e) {
      setError(`加载失败：${e instanceof Error ? e.message : e}`);
    }
  }, []);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  const upload = useCallback(
    async (file: File) => {
      setBusy(true);
      setError('');
      try {
        const form = new FormData();
        form.append('file', file);
        const response = await fetch('/v1/documents', { method: 'POST', body: form });
        if (!response.ok) {
          const body = await response.json().catch(() => ({}));
          throw new Error(body.error ?? `HTTP ${response.status}`);
        }
        await refresh();
      } catch (e) {
        setError(`上传失败：${e instanceof Error ? e.message : e}`);
      } finally {
        setBusy(false);
      }
    },
    [refresh],
  );

  const remove = useCallback(
    async (doc: DocItem) => {
      if (!confirm(`删除 ${doc.title}？（保留历史版本，列表中不再可见）`)) return;
      const response = await fetch(`/v1/documents/${doc.id}`, {
        method: 'DELETE',
        headers: { 'If-Match': `"rev-${doc.revision}"` },
      });
      if (!response.ok) {
        setError(`删除失败：HTTP ${response.status}`);
        return;
      }
      await refresh();
    },
    [refresh],
  );

  return (
    <div className="palette-backdrop" onClick={onClose}>
      <div
        className={`docs-panel${dragOver ? ' drag-over' : ''}`}
        onClick={(e) => e.stopPropagation()}
        onDragOver={(e) => {
          e.preventDefault();
          setDragOver(true);
        }}
        onDragLeave={() => setDragOver(false)}
        onDrop={(e) => {
          e.preventDefault();
          setDragOver(false);
          const file = e.dataTransfer.files[0];
          if (file) void upload(file);
        }}
      >
        <div className="docs-header">
          <span>文档库</span>
          <div className="topbar-actions">
            <button
              className="search-trigger"
              disabled={busy}
              onClick={() => fileInput.current?.click()}
            >
              {busy ? '上传中…' : '上传 PDF / Markdown'}
            </button>
            <button className="search-trigger" onClick={onClose}>关闭</button>
          </div>
          <input
            ref={fileInput}
            type="file"
            accept=".pdf,.md,.markdown"
            hidden
            onChange={(e) => {
              const file = e.target.files?.[0];
              if (file) void upload(file);
              e.target.value = '';
            }}
          />
        </div>

        {error && <p className="login-error">{error}</p>}

        <ul className="docs-list">
          {docs.map((doc) => (
            <li key={doc.id} className="docs-row">
              <a
                className="docs-title"
                href={`/v1/documents/${doc.id}/content`}
                target="_blank"
                rel="noreferrer"
              >
                {doc.title}
              </a>
              <span className="palette-meta">rev-{doc.revision} · {new Date(doc.updated_at).toLocaleDateString()}</span>
              <button className="docs-delete" onClick={() => void remove(doc)}>删除</button>
            </li>
          ))}
          {docs.length === 0 && (
            <li className="palette-empty">还没有文档，拖拽文件到这里即可上传</li>
          )}
        </ul>
        <div className="palette-footer">
          <span>支持 PDF / Markdown，≤ 200MB，拖拽上传</span>
          <span>{docs.length} 篇</span>
        </div>
      </div>
    </div>
  );
}
