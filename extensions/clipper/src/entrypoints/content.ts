import { clipCurrentDocument, clipSelection, readSelection } from '../lib/clip';
import { SelectionCard } from '../lib/selection-card';

export default defineContentScript({
  matches: ['http://*/*', 'https://*/*'],
  runAt: 'document_idle',
  main() {
    const card = new SelectionCard({
      onSave: async ({ excerptText, excerptHTML, note }) => {
        const payload = clipSelection({ excerptText, excerptHTML, note });
        const response = (await browser.runtime.sendMessage({
          type: 'membox.ingest-payload',
          payload,
          open: false,
        })) as { ok: true; result: { id: string } } | { ok: false; error: string };

        if (!response?.ok) {
          throw new Error(response?.error || 'Save failed');
        }
        card.setSaved(response.result.id.slice(0, 8) + '…');
      },
    });

    // Full-page clip still used by the popup.
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
      return undefined;
    });

    let hideTimer: number | null = null;

    const scheduleOpenFromSelection = () => {
      if (hideTimer !== null) {
        window.clearTimeout(hideTimer);
        hideTimer = null;
      }
      // Wait a tick so mouseup selection settles.
      hideTimer = window.setTimeout(() => {
        hideTimer = null;
        maybeOpenCard();
      }, 10);
    };

    const maybeOpenCard = () => {
      const selection = readSelection();
      if (!selection) return;
      // Ignore selections inside our own UI (shouldn't happen with closed shadow, but safe).
      const anchor = window.getSelection()?.anchorNode ?? null;
      if (card.containsNode(anchor)) return;
      card.show(selection);
    };

    document.addEventListener('mouseup', (event) => {
      if (card.containsNode(event.target as Node)) return;
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

    // Click outside dismisses; click inside shadow is retargeted to host.
    document.addEventListener(
      'mousedown',
      (event) => {
        if (!card.isOpen()) return;
        if (card.containsNode(event.target as Node)) return;
        // Allow starting a new selection without the card eating the gesture.
        card.hide();
      },
      true,
    );

    window.addEventListener(
      'scroll',
      () => {
        if (card.isOpen()) card.hide();
      },
      true,
    );
  },
});
