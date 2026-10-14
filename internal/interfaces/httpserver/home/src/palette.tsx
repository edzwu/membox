import { useCallback, useEffect, useRef, useState } from 'react';
import { DEMO_DOCUMENTS, formatSize, searchDocuments, type RemoteDocument } from './api';

interface Props {
  open: boolean;
  onClose: () => void;
}

// Cmd+K palette: filename search over remote documents. Debounced; Escape or
// backdrop click closes. Backend is a stub until M4 — demo rows are shown
// when the API answers empty or is unreachable.
export function SearchPalette({ open, onClose }: Props) {
  const [query, setQuery] = useState('');
  const [results, setResults] = useState<RemoteDocument[]>([]);
  const [usingDemo, setUsingDemo] = useState(false);
  const [active, setActive] = useState(0);
  const inputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    if (!open) return;
    setQuery('');
    setActive(0);
    inputRef.current?.focus();
  }, [open]);

  useEffect(() => {
    if (!open) return;
    const controller = new AbortController();
    const timer = setTimeout(async () => {
      try {
        const docs = await searchDocuments(query, controller.signal);
        setResults(docs.length > 0 ? docs : DEMO_DOCUMENTS);
        setUsingDemo(docs.length === 0);
      } catch {
        if (!controller.signal.aborted) {
          setResults([]); // 401/网络错误时不展示 demo 数据，避免误导
        }
      }
      setActive(0);
    }, 150);
    return () => {
      controller.abort();
      clearTimeout(timer);
    };
  }, [query, open]);

  const onKeyDown = useCallback(
    (event: React.KeyboardEvent) => {
      if (event.key === 'ArrowDown') {
        event.preventDefault();
        setActive((i) => Math.min(i + 1, results.length - 1));
      } else if (event.key === 'ArrowUp') {
        event.preventDefault();
        setActive((i) => Math.max(i - 1, 0));
      } else if (event.key === 'Enter' && results[active]) {
        // M4 will serve content at /v1/documents/{id}/content.
        window.open(`/v1/documents/${results[active].id}/content`, '_blank');
        onClose();
      }
    },
    [results, active, onClose],
  );

  if (!open) return null;
  return (
    <div className="palette-backdrop" onClick={onClose}>
      <div className="palette" onClick={(e) => e.stopPropagation()} onKeyDown={onKeyDown}>
        <input
          ref={inputRef}
          className="palette-input"
          placeholder="搜索文件名…"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
        />
        <ul className="palette-list">
          {results.map((doc, i) => (
            <li
              key={doc.id}
              className={i === active ? 'palette-row active' : 'palette-row'}
              onMouseEnter={() => setActive(i)}
            >
              <span className="palette-title">{doc.title}</span>
              <span className="palette-meta">{formatSize(doc.size_bytes)}</span>
            </li>
          ))}
          {results.length === 0 && <li className="palette-empty">没有匹配的文档</li>}
        </ul>
        <div className="palette-footer">
          {usingDemo && <span className="palette-demo">demo 数据（文档 API 待 M4）</span>}
          <span>↑↓ 选择 · Enter 打开 · Esc 关闭</span>
        </div>
      </div>
    </div>
  );
}
