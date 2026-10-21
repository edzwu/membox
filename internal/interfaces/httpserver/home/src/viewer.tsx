import { useEffect, useState } from 'react';
import { contentUrl, isPdf, type RemoteDocument } from './api';

interface Props {
  doc: RemoteDocument;
}

export function DocumentViewer({ doc }: Props) {
  return (
    <main className="reader" aria-label={`阅读 ${doc.title}`}>
      {isPdf(doc) ? <PdfView url={contentUrl(doc)} title={doc.title} /> : <TextView url={contentUrl(doc)} />}
    </main>
  );
}

/**
 * Let the browser's native PDF viewer do the heavy lifting. It streams large
 * PDFs, keeps memory outside React, and is considerably more reliable than
 * rasterising every page into canvases in the application tab.
 */
function PdfView({ url, title }: { url: string; title: string }) {
  const [loaded, setLoaded] = useState(false);

  return (
    <div className="pdf-native-view">
      {!loaded && <div className="viewer-message pdf-loading"><span className="spinner dark" />正在打开 PDF… 大文件可能需要一点时间</div>}
      <iframe
        className={`pdf-frame${loaded ? ' is-loaded' : ''}`}
        src={url}
        title={`阅读 ${title}`}
        onLoad={() => setLoaded(true)}
      />
      <div className="pdf-fallback">
        <p>浏览器没有内置 PDF 阅读器？</p>
        <a className="primary-button" href={url} target="_blank" rel="noreferrer">打开原文件</a>
      </div>
    </div>
  );
}

function TextView({ url }: { url: string }) {
  const [text, setText] = useState<string | null>(null);
  const [error, setError] = useState('');

  useEffect(() => {
    const controller = new AbortController();
    fetch(url, { signal: controller.signal })
      .then(async (response) => {
        if (!response.ok) throw new Error(`HTTP ${response.status}`);
        setText(await response.text());
      })
      .catch((reason: unknown) => {
        if (!controller.signal.aborted) setError(reason instanceof Error ? reason.message : String(reason));
      });
    return () => controller.abort();
  }, [url]);

  if (error) return <div className="viewer-message viewer-error">加载失败：{error}</div>;
  if (text === null) return <div className="viewer-message"><span className="spinner dark" />加载中…</div>;
  return <pre className="viewer-text">{text}</pre>;
}
