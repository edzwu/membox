import { clipCurrentDocument, clipSelection, readSelection } from '../lib/clip';
import { FloatNotesLayer, getNotesEnabled } from '../lib/float-notes';
import { SelectionCard } from '../lib/selection-card';
import { currentSourceURL, isMemboxReaderUrl } from '../lib/url';

export default defineContentScript({
  matches: ['http://*/*', 'https://*/*'],
  runAt: 'document_idle',
  async main() {
    // Keep this initial URL for the floating-note layer, but never use it as
    // the source of a later clip: SPA navigation can change location.href
    // without reloading this content script.
    const pageUrl = currentSourceURL();
    const onMemboxReader = isMemboxReaderUrl();

    // Note-taking is opt-in. Nothing (no composer, no floating cards) runs
    // until the user explicitly enables it from the extension popup.
    let floats: FloatNotesLayer | null = null;
    let card: SelectionCard | null = null;
    let active = false;
    let cleanup: (() => void) | null = null;

    const nativeOnly = () => ({ ok: false as const, error: 'native-annotations' });
    const disabledResp = () => ({ ok: false as const, error: 'notes-disabled' });

    async function enableNotes() {
      if (active || onMemboxReader) return; // Miru renders native annotations
      active = true;

      floats = new FloatNotesLayer(pageUrl);
      await floats.init();

      card = new SelectionCard({
        onSave: async ({ excerptText, excerptHTML, note, rect }) => {
          const payload = clipSelection({
            excerptText,
            excerptHTML,
            note,
            sourceUrl: currentSourceURL(),
          });
          let response: { ok: true; result: { id: string } } | { ok: false; error: string };
          try {
            // After extension reload, old content scripts lose runtime.id.
            if (!browser.runtime?.id) {
              throw new Error('Extension context invalidated');
            }
            response = (await browser.runtime.sendMessage({
              type: 'membox.ingest-payload',
              payload,
              open: false,
            })) as { ok: true; result: { id: string } } | { ok: false; error: string };
          } catch (err) {
            const msg = err instanceof Error ? err.message : String(err);
            throw new Error(msg || 'Extension context invalidated');
          }

          if (!response?.ok) {
            throw new Error(response?.error || 'Save failed');
          }

          await floats?.addNote({
            id: response.result.id,
            excerpt: excerptText,
            note: note.trim(),
            top: rect.top + window.scrollY,
            left: rect.left + window.scrollX,
            createdAt: new Date().toISOString(),
          });

          card?.setSaved(response.result.id.slice(-5));
        },
      });

      const onMouseUp = (event: MouseEvent) => {
        if (card?.containsNode(event.target as Node)) return;
        if (floats?.containsNode(event.target as Node)) return;
        scheduleOpenFromSelection();
      };
      const onKeyUp = (event: KeyboardEvent) => {
        if (event.key === 'Shift' || event.key.startsWith('Arrow')) {
          scheduleOpenFromSelection();
        }
      };
      const onKeyDown = (event: KeyboardEvent) => {
        if (event.key === 'Escape' && card?.isOpen()) {
          card.hide();
        }
      };
      const onMouseDown = (event: MouseEvent) => {
        if (!card?.isOpen()) return;
        if (card.containsNode(event.target as Node)) return;
        card.hide();
      };
      const onScroll = () => {
        if (card?.isOpen()) card.hide();
      };

      document.addEventListener('mouseup', onMouseUp);
      document.addEventListener('keyup', onKeyUp);
      document.addEventListener('keydown', onKeyDown, true);
      document.addEventListener('mousedown', onMouseDown, true);
      window.addEventListener('scroll', onScroll, true);

      cleanup = () => {
        document.removeEventListener('mouseup', onMouseUp);
        document.removeEventListener('keyup', onKeyUp);
        document.removeEventListener('keydown', onKeyDown, true);
        document.removeEventListener('mousedown', onMouseDown, true);
        window.removeEventListener('scroll', onScroll, true);
        card?.hide();
        floats?.destroy();
      };
    }

    function disableNotes() {
      if (!active) return;
      active = false;
      cleanup?.();
      cleanup = null;
      card = null;
      floats = null;
    }

    let hideTimer: number | null = null;

    function scheduleOpenFromSelection() {
      if (hideTimer !== null) {
        window.clearTimeout(hideTimer);
        hideTimer = null;
      }
      hideTimer = window.setTimeout(() => {
        hideTimer = null;
        const selection = readSelection();
        if (!selection) return;
        const anchor = window.getSelection()?.anchorNode ?? null;
        if (card?.containsNode(anchor) || floats?.containsNode(anchor)) return;
        card?.show(selection);
      }, 10);
    }

    browser.runtime.onMessage.addListener((message) => {
      // Explicit user toggle from the popup — works live, no reload needed.
      if (message?.type === 'membox.set-enabled') {
        return (async () => {
          if (message.enabled) {
            await enableNotes();
          } else {
            disableNotes();
          }
          return { ok: true as const, active, onMemboxReader };
        })();
      }

      if (message?.type === 'membox.clip') {
        // Full-page clip is an explicit popup action — always available,
        // independent of the note-taking opt-in. Async: may re-fetch the
        // pristine HTML so mermaid sources survive client-side rendering.
        return clipCurrentDocument()
          .then((payload) => ({ ok: true as const, payload }))
          .catch((err: unknown) => ({
            ok: false as const,
            error: err instanceof Error ? err.message : String(err),
          }));
      }

      if (message?.type?.startsWith('membox.floats.')) {
        if (onMemboxReader) return nativeOnly();
        if (!active || !floats) return disabledResp();
        const layer = floats;

        if (message.type === 'membox.floats.toggle') {
          return layer.toggleCollapsed().then(() => ({
            ok: true as const,
            ...layer.getStatus(),
          }));
        }
        if (message.type === 'membox.floats.set') {
          const collapsed = Boolean(message.collapsed);
          return layer.setCollapsed(collapsed).then(() => ({
            ok: true as const,
            ...layer.getStatus(),
          }));
        }
        if (message.type === 'membox.floats.status') {
          return (async () => {
            let clips: Array<{ id: string; excerpt?: string; note?: string; title?: string }> = [];
            try {
              const res = (await browser.runtime.sendMessage({
                type: 'membox.clips-for-url',
                url: currentSourceURL(),
              })) as {
                ok: boolean;
                clips?: Array<{ id: string; excerpt?: string; note?: string; title?: string }>;
              };
              if (res?.ok && Array.isArray(res.clips)) clips = res.clips;
            } catch {
              clips = [];
            }
            // Prune stale local pins against membox truth.
            const status = await layer.reconcileWithMembox(clips);
            return { ok: true as const, ...status, savedCount: clips.length };
          })();
        }
        if (message.type === 'membox.floats.restore') {
          return (async () => {
            let clips: Array<{ id: string; excerpt?: string; note?: string; title?: string }> = [];
            try {
              const res = (await browser.runtime.sendMessage({
                type: 'membox.clips-for-url',
                url: currentSourceURL(),
              })) as {
                ok: boolean;
                clips?: Array<{ id: string; excerpt?: string; note?: string; title?: string }>;
              };
              if (res?.ok && Array.isArray(res.clips)) clips = res.clips;
            } catch {
              clips = [];
            }
            await layer.reconcileWithMembox(clips);
            let restored = await layer.restoreHidden();
            if (clips.length) {
              restored += await layer.hydrateFromMembox(clips);
            }
            return {
              ok: true as const,
              restored,
              ...layer.getStatus(clips.length),
            };
          })();
        }
        if (message.type === 'membox.floats.clear') {
          return layer.clearPage().then(() => ({
            ok: true as const,
            count: 0,
            hiddenCount: 0,
            total: 0,
            collapsed: true,
          }));
        }
      }

      return undefined;
    });

    if (await getNotesEnabled()) {
      await enableNotes();
    }
  },
});
