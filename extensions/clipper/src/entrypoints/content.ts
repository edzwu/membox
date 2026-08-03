import { clipCurrentDocument, clipSelection, readSelection } from '../lib/clip';
import { FloatNotesLayer } from '../lib/float-notes';
import { SelectionCard } from '../lib/selection-card';
import { normalizeSourceURL } from '../lib/url';

export default defineContentScript({
  matches: ['http://*/*', 'https://*/*'],
  runAt: 'document_idle',
  main() {
    const floats = new FloatNotesLayer();
    void floats.init();

    const card = new SelectionCard({
      onSave: async ({ excerptText, excerptHTML, note, rect }) => {
        const payload = clipSelection({ excerptText, excerptHTML, note });
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

        await floats.addNote({
          id: response.result.id,
          excerpt: excerptText,
          note: note.trim(),
          top: rect.top + window.scrollY,
          left: rect.left + window.scrollX,
          createdAt: new Date().toISOString(),
        });

        card.setSaved(response.result.id.slice(0, 8) + '…');
      },
    });

    browser.runtime.onMessage.addListener((message) => {
      if (message?.type === 'membox.clip') {
        try {
          const payload = clipCurrentDocument();
          return Promise.resolve({ ok: true as const, payload });
        } catch (err) {
          return Promise.resolve({
            ok: false as const,
            error: err instanceof Error ? err.message : String(err),
          });
        }
      }

      if (message?.type === 'membox.floats.toggle') {
        return floats.toggleCollapsed().then(() => ({
          ok: true as const,
          ...floats.getStatus(),
        }));
      }

      if (message?.type === 'membox.floats.set') {
        const collapsed = Boolean(message.collapsed);
        return floats.setCollapsed(collapsed).then(() => ({
          ok: true as const,
          ...floats.getStatus(),
        }));
      }

      if (message?.type === 'membox.floats.status') {
        return (async () => {
          const url = normalizeSourceURL(location.href) || location.href;
          let clips: Array<{ id: string; excerpt?: string; note?: string; title?: string }> = [];
          try {
            const res = (await browser.runtime.sendMessage({
              type: 'membox.clips-for-url',
              url,
            })) as {
              ok: boolean;
              clips?: Array<{ id: string; excerpt?: string; note?: string; title?: string }>;
            };
            if (res?.ok && Array.isArray(res.clips)) clips = res.clips;
          } catch {
            clips = [];
          }
          // Prune stale local pins (the old "7 stacked") against membox truth.
          const status = await floats.reconcileWithMembox(clips);
          return { ok: true as const, ...status, savedCount: clips.length };
        })();
      }

      if (message?.type === 'membox.floats.restore') {
        return (async () => {
          const url = normalizeSourceURL(location.href) || location.href;
          let clips: Array<{ id: string; excerpt?: string; note?: string; title?: string }> = [];
          try {
            const res = (await browser.runtime.sendMessage({
              type: 'membox.clips-for-url',
              url,
            })) as {
              ok: boolean;
              clips?: Array<{ id: string; excerpt?: string; note?: string; title?: string }>;
            };
            if (res?.ok && Array.isArray(res.clips)) clips = res.clips;
          } catch {
            clips = [];
          }
          await floats.reconcileWithMembox(clips);
          let restored = await floats.restoreHidden();
          if (clips.length) {
            restored += await floats.hydrateFromMembox(clips);
          }
          return {
            ok: true as const,
            restored,
            ...floats.getStatus(clips.length),
          };
        })();
      }

      if (message?.type === 'membox.floats.clear') {
        return floats.clearPage().then(() => ({
          ok: true as const,
          count: 0,
          hiddenCount: 0,
          total: 0,
          collapsed: true,
        }));
      }

      return undefined;
    });

    let hideTimer: number | null = null;

    const scheduleOpenFromSelection = () => {
      if (hideTimer !== null) {
        window.clearTimeout(hideTimer);
        hideTimer = null;
      }
      hideTimer = window.setTimeout(() => {
        hideTimer = null;
        maybeOpenCard();
      }, 10);
    };

    const maybeOpenCard = () => {
      const selection = readSelection();
      if (!selection) return;
      const anchor = window.getSelection()?.anchorNode ?? null;
      if (card.containsNode(anchor) || floats.containsNode(anchor)) return;
      card.show(selection);
    };

    document.addEventListener('mouseup', (event) => {
      if (card.containsNode(event.target as Node)) return;
      if (floats.containsNode(event.target as Node)) return;
      scheduleOpenFromSelection();
    });

    document.addEventListener('keyup', (event) => {
      if (event.key === 'Shift' || event.key.startsWith('Arrow')) {
        scheduleOpenFromSelection();
      }
    });

    document.addEventListener(
      'keydown',
      (event) => {
        if (event.key === 'Escape' && card.isOpen()) {
          card.hide();
        }
      },
      true,
    );

    document.addEventListener(
      'mousedown',
      (event) => {
        if (!card.isOpen()) return;
        if (card.containsNode(event.target as Node)) return;
        card.hide();
      },
      true,
    );

    // Composer dismisses on scroll; pinned floats stay (document-absolute).
    window.addEventListener(
      'scroll',
      () => {
        if (card.isOpen()) card.hide();
      },
      true,
    );
  },
});
