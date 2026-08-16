import type { IngestResult } from './types';

const JOB_KEY = 'membox.activeJob';

export type JobStatus = 'running' | 'done' | 'error';

export type ActiveJob = {
  id: string;
  kind: 'youtube' | 'page';
  status: JobStatus;
  label: string;
  /** 0–100, or null for indeterminate */
  pct: number | null;
  url?: string;
  title?: string;
  result?: IngestResult;
  error?: string;
  updatedAt: number;
};

export async function getActiveJob(): Promise<ActiveJob | null> {
  try {
    const stored = await browser.storage.session.get(JOB_KEY);
    const job = stored[JOB_KEY] as ActiveJob | undefined;
    return job ?? null;
  } catch {
    // session storage unavailable in some test contexts
    return null;
  }
}

export async function setActiveJob(job: ActiveJob): Promise<void> {
  job = { ...job, updatedAt: Date.now() };
  try {
    await browser.storage.session.set({ [JOB_KEY]: job });
  } catch {
    /* ignore */
  }
  // Notify open popup(s), if any.
  void browser.runtime.sendMessage({ type: 'membox.progress', job }).catch(() => {});
  // Badge: show activity while a long job runs.
  try {
    if (job.status === 'running') {
      await browser.action.setBadgeText({ text: '…' });
      await browser.action.setBadgeBackgroundColor({ color: '#1b365d' });
    } else if (job.status === 'error') {
      await browser.action.setBadgeText({ text: '!' });
      await browser.action.setBadgeBackgroundColor({ color: '#9b2c2c' });
    } else if (job.status === 'done') {
      await browser.action.setBadgeText({ text: '✓' });
      await browser.action.setBadgeBackgroundColor({ color: '#2f6b3a' });
      // Clear checkmark after a moment so "on" notes badge can return.
      setTimeout(() => {
        void browser.action.setBadgeText({ text: '' });
      }, 4000);
    }
  } catch {
    /* badge APIs optional */
  }
}

export async function clearActiveJob(): Promise<void> {
  try {
    await browser.storage.session.remove(JOB_KEY);
  } catch {
    /* ignore */
  }
}

export function newJobId(): string {
  return `${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 8)}`;
}
