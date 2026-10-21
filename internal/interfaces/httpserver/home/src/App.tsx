import { useEffect, useState } from 'react';
import { getDocument, type RemoteDocument } from './api';
import { DocumentsWorkspace } from './documents';
import { Login } from './Login';
import { SearchPalette } from './palette';
import { DocumentViewer } from './viewer';

type SessionState = 'loading' | 'anonymous' | 'signed-in';

function readerDocumentID(): string | null {
  const match = window.location.pathname.match(/^\/reader\/([^/]+)\/?$/);
  return match ? decodeURIComponent(match[1]) : null;
}

function goToReader(id: string) {
  window.history.pushState({}, '', `/reader/${encodeURIComponent(id)}`);
}

function goToLibrary() {
  window.history.pushState({}, '', '/');
}

export default function App() {
  const [session, setSession] = useState<SessionState>('loading');
  const [paletteOpen, setPaletteOpen] = useState(false);
  const [viewing, setViewing] = useState<RemoteDocument | null>(null);
  const [routeError, setRouteError] = useState('');

  useEffect(() => {
    fetch('/v1/me')
      .then((response) => setSession(response.ok ? 'signed-in' : 'anonymous'))
      .catch(() => setSession('anonymous'));
  }, []);

  // The reader is a real URL, so refresh/back/forward all work. A direct link
  // loads document metadata first, then renders the same full-screen reader.
  useEffect(() => {
    if (session !== 'signed-in') return;
    const loadRoute = () => {
      const id = readerDocumentID();
      if (!id) {
        setViewing(null);
        setRouteError('');
        return;
      }
      setRouteError('');
      void getDocument(id)
        .then(setViewing)
        .catch((reason) => setRouteError(reason instanceof Error ? reason.message : String(reason)));
    };
    loadRoute();
    window.addEventListener('popstate', loadRoute);
    return () => window.removeEventListener('popstate', loadRoute);
  }, [session]);

  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 'k') {
        event.preventDefault();
        setPaletteOpen((open) => !open);
      } else if (event.key === 'Escape') {
        if (paletteOpen) setPaletteOpen(false);
        else if (viewing) closeReader();
      }
    };
    window.addEventListener('keydown', onKeyDown);
    return () => window.removeEventListener('keydown', onKeyDown);
  }, [paletteOpen, viewing]);

  if (session === 'loading') {
    return <div className="app-loading"><span className="spinner dark" /></div>;
  }
  if (session === 'anonymous') {
    return <div className="auth-shell"><Login onSignedIn={() => setSession('signed-in')} /></div>;
  }

  function openDocument(doc: RemoteDocument) {
    goToReader(doc.id);
    setViewing(doc);
    setRouteError('');
    setPaletteOpen(false);
  }

  function closeReader() {
    goToLibrary();
    setViewing(null);
    setRouteError('');
  }

  return (
    <>
      {!viewing && !routeError && (
        <DocumentsWorkspace
          onOpen={openDocument}
          onLogout={() => fetch('/v1/auth/logout', { method: 'POST' }).then(() => location.reload())}
        />
      )}
      {viewing && <DocumentViewer doc={viewing} />}
      {routeError && (
        <div className="reader-route-error">
          <strong>文档无法打开</strong><span>{routeError}</span><button className="secondary-button" onClick={closeReader}>返回文档库</button>
        </div>
      )}
      <SearchPalette open={paletteOpen} onClose={() => setPaletteOpen(false)} onOpen={openDocument} />
    </>
  );
}
