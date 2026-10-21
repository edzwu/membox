import { useCallback, useEffect, useRef, useState } from 'react';
import { formatSize, searchDocuments, type RemoteDocument } from './api';

interface Props {
  open: boolean;
  onClose: () => void;
  onOpen: (doc: RemoteDocument) => void;
}

// Cmd+K palette: filename search over remote documents. Debounced; Enter or
// click opens the document in the in-page viewer; Escape closes.
export function SearchPalette({ open, onClose, onOpen }: Props) {
  const [query, setQuery] = useState('');
  const [results, setResults] = useState<RemoteDocument[]>([]);
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
        setResults(await searchDocuments(query, controller.signal));
      } catch {
        if (!controller.signal.aborted) setResults([]);
      }
      setActive(0);
    }, 150);
    return () => {
      controller.abort();
      clearTimeout(timer);
    };
  }, [query, open]);

  const pick = useCallback(
    (doc: RemoteDocument) => {
      onOpen(doc);
      onClose();
    },
    [onOpen, onClose],
  );

  const onKeyDown = useCallback(
    (event: React.KeyboardEvent) => {
      if (event.key === 'ArrowDown') {
        event.preventDefault();
        setActive((i) => Math.min(i + 1, results.length - 1));
      } else if (event.key === 'ArrowUp') {
        event.preventDefault();
        setActive((i) => Math.max(i - 1, 0));
      } else if (event.key === 'Enter' && results[active]) {
        pick(results[active]);
      }
    },
    [results, active, pick],
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
              onClick={() => pick(doc)}
            >
              <span className="palette-title">
                {doc.title}
                <span className="palette-uuid">{doc.id}</span>
              </span>
              <span className="palette-meta">{formatSize(doc.size_bytes)}</span>
            </li>
          ))}
          {results.length === 0 && (
            <li className="palette-empty">没有匹配的文档 — 可在文档库首页上传</li>
          )}
        </ul>
        <div className="palette-footer">
          <span>↑↓ 选择 · Enter 打开 · Esc 关闭</span>
        </div>
      </div>
    </div>
  );
}
