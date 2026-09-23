import type { TabInfo } from './tabs';

/**
 * Current Intent — mtabs' explicit, first-class representation of
 * "what the user is currently trying to do".
 *
 * Conceptual split:
 *   observed context = tabs / URLs / titles / activity (see utils/tabs.ts)
 *   current intent   = this module — a small persisted state object that
 *                      future agent features consume as a core input.
 *
 * Semantics:
 * - `source: 'inferred'`  — written by inference; may be refreshed freely
 *                           as the observed context changes.
 * - `source: 'confirmed'` — written by an explicit user edit; inference
 *                           must NEVER silently overwrite it. It stays
 *                           stable until the user edits/resets it (or a
 *                           future session lifecycle mechanism exists).
 */

export type CurrentIntent = {
  text: string;
  source: 'inferred' | 'confirmed';
  updatedAt: number;
};

const STORAGE_KEY = 'membox.tabs.intent';

/** Read the persisted intent. This is the clean entry point for future
 * agent logic (e.g. from the background worker) to consume the intent. */
export async function getCurrentIntent(): Promise<CurrentIntent | null> {
  const data = await browser.storage.local.get(STORAGE_KEY);
  return (data[STORAGE_KEY] as CurrentIntent | undefined) ?? null;
}

export async function saveCurrentIntent(intent: CurrentIntent): Promise<void> {
  await browser.storage.local.set({ [STORAGE_KEY]: intent });
}

/** Explicit reset. After clearing, the next inference pass writes a fresh
 * inferred intent. */
export async function clearCurrentIntent(): Promise<void> {
  await browser.storage.local.remove(STORAGE_KEY);
}

/**
 * PLACEHOLDER inference — rule-based stand-in for a future local agent/LLM.
 *
 * Contract: observe the current tab context, return a short one-line guess
 * with `source: 'inferred'`. Callers are responsible for never overwriting
 * a confirmed intent with the result. The UI and state flow must not depend
 * on any specific LLM provider — swap this implementation later.
 */
export async function inferCurrentIntent(tabs: TabInfo[]): Promise<CurrentIntent> {
  return {
    text: guessIntentText(tabs),
    source: 'inferred',
    updatedAt: Date.now(),
  };
}

function guessIntentText(tabs: TabInfo[]): string {
  if (tabs.length === 0) return 'Idle — no open tabs';

  // Dominant site: several tabs on one host looks like focused research.
  const hostCounts = new Map<string, number>();
  for (const tab of tabs) {
    hostCounts.set(tab.hostname, (hostCounts.get(tab.hostname) ?? 0) + 1);
  }
  const [topHost, topCount] = [...hostCounts.entries()].sort((a, b) => b[1] - a[1])[0]!;
  if (topCount >= 3) return `Researching ${topHost} across ${topCount} tabs`;

  const active = tabs.find((tab) => tab.active);
  // Full title — the intent card wraps and displays text completely.
  if (active) return `Reading ${active.title}`;

  return `Browsing ${tabs.length} tabs`;
}
