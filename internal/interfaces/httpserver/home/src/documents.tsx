import { useCallback, useEffect, useRef, useState } from 'react';
import {
  deleteDocument,
  formatDate,
  formatSize,
  isPdf,
  renameDocument,
  replaceContent,
  searchDocuments,
  uploadDocument,
  type RemoteDocument,
  type UploadProgress,
} from './api';

interface Props {
  onOpen: (doc: RemoteDocument) => void;
  onLogout: () => void;
}

type Transfer = {
  fileName: string;
  percent: number;
  mode: 'upload' | 'replace';
};

const MAX_UPLOAD_BYTES = 200 * 1024 * 1024;

function supportedFile(file: File): boolean {
  return file.type === 'application/pdf' || /\.(pdf|md|markdown)$/i.test(file.name);
}

function fileError(file: File): string {
  if (!supportedFile(file)) return '只支持 PDF 或 Markdown 文件';
  if (file.size > MAX_UPLOAD_BYTES) return '文件不能超过 200 MB';
  return '';
}

export function DocumentsWorkspace({ onOpen, onLogout }: Props) {
  const [docs, setDocs] = useState<RemoteDocument[]>([]);
  const [query, setQuery] = useState('');
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [transfer, setTransfer] = useState<Transfer | null>(null);
  const [dragOver, setDragOver] = useState(false);
  const [renameTarget, setRenameTarget] = useState<RemoteDocument | null>(null);
  const [renameValue, setRenameValue] = useState('');
  const [deleteTarget, setDeleteTarget] = useState<RemoteDocument | null>(null);
  const [actionBusy, setActionBusy] = useState(false);
  const fileInput = useRef<HTMLInputElement>(null);
  const replaceInput = useRef<HTMLInputElement>(null);
  const replaceTarget = useRef<RemoteDocument | null>(null);

  const refresh = useCallback(async () => {
    try {
      setError('');
      setDocs(await searchDocuments());
    } catch (reason) {
      if (reason instanceof DOMException && reason.name === 'AbortError') return;
      setError(reason instanceof Error ? reason.message : String(reason));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  const onProgress: UploadProgress = useCallback((loaded, total) => {
    setTransfer((current) => current ? { ...current, percent: total ? Math.round((loaded / total) * 100) : 0 } : current);
  }, []);

  const upload = useCallback(async (file: File) => {
    const validationError = fileError(file);
    if (validationError) {
      setError(validationError);
      return;
    }
    setError('');
    setTransfer({ fileName: file.name, percent: 0, mode: 'upload' });
    try {
      await uploadDocument(file, onProgress);
      await refresh();
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : String(reason));
    } finally {
      setTransfer(null);
    }
  }, [onProgress, refresh]);

  const startReplace = useCallback((doc: RemoteDocument) => {
    replaceTarget.current = doc;
    replaceInput.current?.click();
  }, []);

  const replace = useCallback(async (file: File, doc: RemoteDocument) => {
    const validationError = fileError(file);
    if (validationError) {
      setError(validationError);
      return;
    }
    setError('');
    setTransfer({ fileName: file.name, percent: 0, mode: 'replace' });
    try {
      await replaceContent(doc, file, onProgress);
      await refresh();
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : String(reason));
    } finally {
      setTransfer(null);
    }
  }, [onProgress, refresh]);

  const submitRename = async (event: React.FormEvent) => {
    event.preventDefault();
    const target = renameTarget;
    const title = renameValue.trim();
    if (!target || !title || title === target.title) {
      setRenameTarget(null);
      return;
    }
    setActionBusy(true);
    setError('');
    try {
      await renameDocument(target, title);
      setRenameTarget(null);
      await refresh();
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : String(reason));
    } finally {
      setActionBusy(false);
    }
  };

  const remove = async () => {
    if (!deleteTarget) return;
    setActionBusy(true);
    setError('');
    try {
      await deleteDocument(deleteTarget);
      setDeleteTarget(null);
      await refresh();
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : String(reason));
    } finally {
      setActionBusy(false);
    }
  };

  const visibleDocs = docs
    .filter((doc) => doc.title.toLocaleLowerCase().includes(query.trim().toLocaleLowerCase()))
    .sort((a, b) => new Date(b.updated_at).getTime() - new Date(a.updated_at).getTime());

  const acceptFiles = (files: FileList | File[]) => {
    const file = files[0];
    if (file && !transfer) void upload(file);
  };

  return (
    <div className="app-shell">
      <header className="app-header">
        <a className="brand" href="/">membox<span className="brand-dot">.</span></a>
        <div className="header-actions">
          <span className="connection-status"><span className="status-dot" />已连接</span>
          <button className="text-button" onClick={onLogout}>退出登录</button>
        </div>
      </header>

      <main className="workspace">
        <section className="workspace-intro">
          <div>
            <p className="eyebrow">DOCUMENTS</p>
            <h1>你的文档库</h1>
            <p className="intro-copy">上传、整理并阅读你的 PDF 文件。</p>
          </div>
          <button className="primary-button" disabled={Boolean(transfer)} onClick={() => fileInput.current?.click()}>
            <span className="button-plus">+</span> 上传文件
          </button>
        </section>

        <input
          ref={fileInput}
          type="file"
          accept=".pdf,.md,.markdown,application/pdf,text/markdown"
          hidden
          onChange={(event) => {
            const file = event.target.files?.[0];
            event.target.value = '';
            if (file) void upload(file);
          }}
        />
        <input
          ref={replaceInput}
          type="file"
          accept=".pdf,.md,.markdown,application/pdf,text/markdown"
          hidden
          onChange={(event) => {
            const file = event.target.files?.[0];
            const target = replaceTarget.current;
            event.target.value = '';
            if (file && target) void replace(file, target);
          }}
        />

        <section
          className={`upload-dropzone${dragOver ? ' is-dragging' : ''}${transfer ? ' is-uploading' : ''}`}
          onDragOver={(event) => { event.preventDefault(); if (!transfer) setDragOver(true); }}
          onDragLeave={() => setDragOver(false)}
          onDrop={(event) => {
            event.preventDefault();
            setDragOver(false);
            acceptFiles(event.dataTransfer.files);
          }}
        >
          {transfer ? (
            <div className="transfer-content">
              <div className="transfer-icon"><span className="spinner" /></div>
              <div className="transfer-details">
                <div className="transfer-line"><strong>{transfer.mode === 'replace' ? '正在替换' : '正在上传'}</strong><span>{transfer.percent}%</span></div>
                <div className="progress-track"><div className="progress-value" style={{ width: `${transfer.percent}%` }} /></div>
                <span className="transfer-name">{transfer.fileName}</span>
              </div>
            </div>
          ) : (
            <>
              <div className="drop-icon">↑</div>
              <div><strong>拖拽文件到这里</strong><span>，或 <button className="inline-button" onClick={() => fileInput.current?.click()}>选择文件</button></span></div>
              <small>支持 PDF、Markdown，单个文件最大 200 MB</small>
            </>
          )}
        </section>

        {error && <div className="error-banner" role="alert"><span>!</span>{error}<button onClick={() => setError('')} aria-label="关闭提示">×</button></div>}

        <section className="documents-section">
          <div className="section-toolbar">
            <div><h2>全部文档</h2><span className="count-badge">{docs.length}</span></div>
            <label className="search-box">
              <span aria-hidden="true">⌕</span>
              <input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="搜索文档" aria-label="搜索文档" />
              {query && <button onClick={() => setQuery('')} aria-label="清除搜索">×</button>}
            </label>
          </div>

          {loading ? (
            <div className="empty-state"><span className="spinner dark" /><p>正在加载文档…</p></div>
          ) : visibleDocs.length === 0 ? (
            <div className="empty-state">
              <div className="empty-icon">▱</div>
              <strong>{query ? '没有找到匹配的文档' : '文档库还是空的'}</strong>
              <p>{query ? '换个关键词试试。' : '上传第一个 PDF，开始建立你的知识库。'}</p>
            </div>
          ) : (
            <div className="document-list">
              {visibleDocs.map((doc) => (
                <article className="document-card" key={doc.id}>
                  <button className="document-main" onClick={() => onOpen(doc)} title={`打开 ${doc.title}`}>
                    <span className={`file-icon${isPdf(doc) ? ' pdf' : ' markdown'}`}>{isPdf(doc) ? 'PDF' : 'MD'}</span>
                    <span className="document-info"><strong>{doc.title}</strong><span>{formatSize(doc.size_bytes)} · 更新于 {formatDate(doc.updated_at)}</span></span>
                  </button>
                  <div className="document-actions">
                    <button onClick={() => onOpen(doc)}>阅读</button>
                    <button onClick={() => { setRenameTarget(doc); setRenameValue(doc.title); }}>重命名</button>
                    <button onClick={() => startReplace(doc)}>替换</button>
                    <button className="danger-button" onClick={() => setDeleteTarget(doc)}>删除</button>
                  </div>
                </article>
              ))}
            </div>
          )}
        </section>
      </main>

      <footer className="app-footer"><span>membox</span><span>你的文档，简单地放在这里。</span></footer>

      {renameTarget && (
        <div className="modal-backdrop" onClick={() => setRenameTarget(null)}>
          <form className="modal" onSubmit={submitRename} onClick={(event) => event.stopPropagation()}>
            <h2>重命名文档</h2><p>修改文档的显示名称，不会改变文件内容。</p>
            <label className="modal-label">文档名称<input autoFocus value={renameValue} onChange={(event) => setRenameValue(event.target.value)} /></label>
            <div className="modal-actions"><button type="button" className="secondary-button" onClick={() => setRenameTarget(null)}>取消</button><button className="primary-button" disabled={!renameValue.trim() || actionBusy}>{actionBusy ? '保存中…' : '保存'}</button></div>
          </form>
        </div>
      )}

      {deleteTarget && (
        <div className="modal-backdrop" onClick={() => setDeleteTarget(null)}>
          <div className="modal" onClick={(event) => event.stopPropagation()}>
            <div className="modal-warning">!</div><h2>删除这个文档？</h2><p><strong>{deleteTarget.title}</strong> 将从文档库中移除。此操作无法从这里撤销。</p>
            <div className="modal-actions"><button className="secondary-button" onClick={() => setDeleteTarget(null)}>取消</button><button className="danger-solid-button" disabled={actionBusy} onClick={() => void remove()}>{actionBusy ? '删除中…' : '确认删除'}</button></div>
          </div>
        </div>
      )}
    </div>
  );
}
