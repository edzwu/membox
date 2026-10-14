import { useCallback, useEffect, useRef, useState } from 'react';
import { DocumentsPanel } from './documents';
import { Login } from './Login';
import { SearchPalette } from './palette';

interface Message {
  role: 'user' | 'assistant';
  text: string;
}

type SessionState = 'loading' | 'anonymous' | 'signed-in';

// Home shell: a chat surface for the future agent, plus global shortcuts.
// The chat box is local-only for now — the agent backend is a later milestone.
export default function App() {
  const [session, setSession] = useState<SessionState>('loading');
  const [messages, setMessages] = useState<Message[]>([]);
  const [draft, setDraft] = useState('');
  const [paletteOpen, setPaletteOpen] = useState(false);
  const [docsOpen, setDocsOpen] = useState(false);
  const inputRef = useRef<HTMLTextAreaElement>(null);
  const scrollRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    fetch('/v1/me')
      .then((r) => setSession(r.ok ? 'signed-in' : 'anonymous'))
      .catch(() => setSession('anonymous'));
  }, []);

  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 'k') {
        event.preventDefault();
        setPaletteOpen((open) => !open);
      } else if (event.key === 'Escape') {
        setPaletteOpen(false);
      }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, []);

  useEffect(() => {
    scrollRef.current?.scrollTo({ top: scrollRef.current.scrollHeight });
  }, [messages]);

  const send = useCallback(() => {
    const text = draft.trim();
    if (!text) return;
    setDraft('');
    setMessages((prev) => [
      ...prev,
      { role: 'user', text },
      { role: 'assistant', text: 'Agent 后端尚未接入（规划中的里程碑）。这条回复是本地占位。' },
    ]);
  }, [draft]);

  if (session === 'loading') return <div className="shell" />;
  if (session === 'anonymous') {
    return (
      <div className="shell">
        <Login onSignedIn={() => setSession('signed-in')} />
      </div>
    );
  }

  return (
    <div className="shell">
      <header className="topbar">
        <span className="brand">membox</span>
        <div className="topbar-actions">
          <button className="search-trigger" onClick={() => setDocsOpen(true)}>
            文档库
          </button>
          <button className="search-trigger" onClick={() => setPaletteOpen(true)}>
            搜索文档 <kbd>⌘K</kbd>
          </button>
          <button
            className="search-trigger"
            onClick={() =>
              fetch('/v1/auth/logout', { method: 'POST' }).then(() => location.reload())
            }
          >
            退出
          </button>
        </div>
      </header>

      <div className="messages" ref={scrollRef}>
        {messages.length === 0 && (
          <div className="welcome">
            <h1>membox remote</h1>
            <p>你的远端文档节点。按 <kbd>⌘K</kbd> 搜索 PDF，或在下方和 agent 对话。</p>
          </div>
        )}
        {messages.map((message, i) => (
          <div key={i} className={`message ${message.role}`}>
            {message.text}
          </div>
        ))}
      </div>

      <div className="composer">
        <textarea
          ref={inputRef}
          value={draft}
          placeholder="问点什么… (Enter 发送，Shift+Enter 换行)"
          rows={1}
          onChange={(e) => setDraft(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter' && !e.shiftKey) {
              e.preventDefault();
              send();
            }
          }}
        />
      </div>

      <footer className="hints">
        <span><kbd>⌘K</kbd> 搜索</span>
        <span><kbd>Enter</kbd> 发送</span>
        <span><kbd>Esc</kbd> 关闭</span>
      </footer>

      <SearchPalette open={paletteOpen} onClose={() => setPaletteOpen(false)} />
      {docsOpen && <DocumentsPanel onClose={() => setDocsOpen(false)} />}
    </div>
  );
}
