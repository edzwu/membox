import { clipCurrentDocument } from '../lib/clip';

export default defineContentScript({
  matches: ['http://*/*', 'https://*/*'],
  runAt: 'document_idle',
  main() {
    browser.runtime.onMessage.addListener((message) => {
      if (message?.type !== 'membox.clip') {
        return undefined;
      }
      try {
        const payload = clipCurrentDocument();
        return Promise.resolve({ ok: true as const, payload });
      } catch (err) {
        return Promise.resolve({
          ok: false as const,
          error: err instanceof Error ? err.message : String(err),
        });
      }
    });
  },
});
