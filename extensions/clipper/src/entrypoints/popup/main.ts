import { fetchBridgeStatus } from '../../lib/membox-client';
import { loadSettings, saveSettings } from '../../lib/settings';
import { getNotesEnabled, setNotesEnabled } from '../../lib/float-notes';
import type { ActiveJob } from '../../lib/jobs';
import type { BridgeSettings } from '../../lib/types';

const homeView = document.getElementById('homeView') as HTMLElement;
const settingsView = document.getElementById('settingsView') as HTMLElement;
const statusEl = document.getElementById('status') as HTMLDivElement;
const baseUrlEl = document.getElementById('baseUrl') as HTMLInputElement;
const tokenEl = document.getElementById('token') as HTMLInputElement;
const autoOpenEl = document.getElementById('autoOpen') as HTMLInputElement;
const messageEl = document.getElementById('message') as HTMLParagraphElement;
const saveSettingsBtn = document.getElementById('saveSettings') as HTMLButtonElement;
const clipBtn = document.getElementById('clip') as HTMLButtonElement;
const notesToggleBtn = document.getElementById('notesToggle') as HTMLButtonElement;
const openSettingsBtn = document.getElementById('openSettings') as HTMLButtonElement;
const closeSettingsBtn = document.getElementById('closeSettings') as HTMLButtonElement;
const progressEl = document.getElementById('progress') as HTMLDivElement;
const progressBar = document.getElementById('progressBar') as HTMLDivElement;
const progressLabel = document.getElementById('progressLabel') as HTMLDivElement;

let pollTimer: ReturnType<typeof setInterval> | null = null;

function showHome() {
  homeView.hidden = false;
  settingsView.hidden = true;
}

function showSettings() {
  homeView.hidden = true;
  settingsView.hidden = false;
}

openSettingsBtn.addEventListener('click', showSettings);
closeSettingsBtn.addEventListener('click', showHome);

async function renderEnableState(enabled: boolean) {
  notesToggleBtn.classList.toggle('is-on', enabled);
  notesToggleBtn.setAttribute('aria-checked', enabled ? 'true' : 'false');
  notesToggleBtn.title = enabled ? 'Notes on — select text to annotate' : 'Notes off';
}

notesToggleBtn.addEventListener('click', async () => {
  const next = !(await getNotesEnabled());
  await setNotesEnabled(next);
  renderEnableState(next);
  const tabId = await activeTabId();
  if (tabId) {
    try {
      await browser.tabs.sendMessage(tabId, { type: 'membox.set-enabled', enabled: next });
    } catch {
      showMessage(next ? 'Reload the page to enable notes' : 'Notes disabled', 'ok');
    }
  }
});

function showMessage(text: string, kind: 'ok' | 'error' | '' = '') {
  messageEl.hidden = !text;
  messageEl.textContent = text;
  messageEl.classList.toggle('is-error', kind === 'error');
  messageEl.classList.toggle('is-ok', kind === 'ok');
}

function showProgress(label: string, pct: number | null) {
  progressEl.hidden = false;
  progressLabel.textContent = label;
  if (pct == null || pct < 0) {
    progressBar.classList.add('indeterminate');
    progressBar.style.width = '40%';
  } else {
    progressBar.classList.remove('indeterminate');
    progressBar.style.width = `${Math.max(0, Math.min(100, pct))}%`;
  }
}

function hideProgress() {
  progressEl.hidden = true;
  progressBar.classList.remove('indeterminate');
  progressBar.style.width = '0%';
  progressLabel.textContent = '';
}

function applyJob(job: ActiveJob | null | undefined) {
  if (!job) {
    hideProgress();
    clipBtn.disabled = false;
    return;
  }
  if (job.status === 'running') {
    clipBtn.disabled = true;
    showProgress(job.label || 'Working…', job.pct);
    showMessage(job.title ? `Working on: ${job.title}` : '', '');
    return;
  }
  clipBtn.disabled = false;
  if (job.status === 'done' && job.result) {
    showProgress('Done', 100);
    const short = String(job.result.id || '').slice(0, 8);
    const titleBit = job.result.title || job.title ? ` · ${job.result.title || job.title}` : '';
    const reused = job.result.reused;
    showMessage(`${reused ? 'Opened' : 'Saved'} ${short}…${titleBit}`, 'ok');
    return;
  }
  if (job.status === 'error') {
    hideProgress();
    showMessage(job.error || 'Failed', 'error');
  }
}

function stopPolling() {
  if (pollTimer) {
    clearInterval(pollTimer);
    pollTimer = null;
  }
}

function startPolling() {
  stopPolling();
  pollTimer = setInterval(() => {
    void refreshJob();
  }, 600);
}

async function refreshJob() {
  try {
    const res = await browser.runtime.sendMessage({ type: 'membox.job-status' });
    const job = res?.job as ActiveJob | null;
    applyJob(job);
    if (job && job.status !== 'running') {
      stopPolling();
      // Clear terminal job after user has seen it (next open starts fresh).
      if (job.status === 'done') {
        setTimeout(() => {
          hideProgress();
          void browser.runtime.sendMessage({ type: 'membox.job-clear' });
        }, 2500);
      }
    }
  } catch {
    /* ignore */
  }
}

// Live updates while popup stays open.
browser.runtime.onMessage.addListener((message) => {
  if (message?.type === 'membox.progress' && message.job) {
    applyJob(message.job as ActiveJob);
  } else if (message?.type === 'membox.progress') {
    showProgress(String(message.label || 'Working…'), typeof message.pct === 'number' ? message.pct : null);
  }
});

function readForm(): BridgeSettings {
  return {
    baseUrl: baseUrlEl.value.trim().replace(/\/$/, ''),
    token: tokenEl.value.trim(),
    autoOpen: autoOpenEl.checked,
  };
}

function fillForm(settings: BridgeSettings) {
  baseUrlEl.value = settings.baseUrl;
  tokenEl.value = settings.token;
  autoOpenEl.checked = settings.autoOpen;
}

async function refreshStatus() {
  const settings = readForm();
  statusEl.className = 'status-dot status-unknown';
  statusEl.title = 'checking…';
  try {
    const status = await fetchBridgeStatus(settings);
    if (status.connected) {
      const needsToken = status.auth_required && !settings.token;
      if (needsToken) {
        statusEl.className = 'status-dot status-bad';
        statusEl.title = 'Connected — token required';
      } else {
        statusEl.className = 'status-dot status-ok';
        statusEl.title = status.auth_required ? 'Connected (auth)' : 'Connected';
      }
    } else {
      statusEl.className = 'status-dot status-bad';
      statusEl.title = status.error || 'Offline';
    }
  } catch (err) {
    statusEl.className = 'status-dot status-bad';
    statusEl.title = err instanceof Error ? err.message : 'error';
  }
}

saveSettingsBtn.addEventListener('click', async () => {
  const settings = readForm();
  await saveSettings(settings);
  await refreshStatus();
  showHome();
  showMessage(settings.token ? 'Settings saved' : 'Saved — token still empty', settings.token ? 'ok' : 'error');
});

clipBtn.addEventListener('click', async () => {
  clipBtn.disabled = true;
  showMessage('');
  showProgress('Starting…', 5);
  try {
    const settings = readForm();
    if (!settings.token) {
      hideProgress();
      clipBtn.disabled = false;
      showMessage('Add bridge token in Settings ⚙', 'error');
      showSettings();
      return;
    }
    await saveSettings(settings);

    const response = await browser.runtime.sendMessage({ type: 'membox.ingest-active-tab' });

    // Detached YouTube job — popup may close; poll storage for progress.
    if (response?.ok && response?.started) {
      startPolling();
      await refreshJob();
      return;
    }

    if (!response?.ok && response?.conflict?.code === 'clip_exists') {
      const existing = response.conflict.existing;
      const shortId = (existing?.id || '').slice(0, 8);
      const title = existing?.title || 'existing note';
      hideProgress();
      const ok = window.confirm(
        `Already in membox.\n\n${title}\n${shortId}…\n\nOverwrite and keep the same id?`,
      );
      if (!ok) {
        if (existing?.view_url && settings.autoOpen) {
          await browser.tabs.create({ url: existing.view_url });
        }
        showMessage(`Kept ${shortId}…`, 'ok');
        clipBtn.disabled = false;
        return;
      }
      showProgress('Overwriting…', null);
      const again = await browser.runtime.sendMessage({
        type: 'membox.ingest-active-tab',
        overwrite: true,
      });
      if (again?.ok && again?.started) {
        startPolling();
        await refreshJob();
        return;
      }
      handleSyncResult(again);
      return;
    }

    handleSyncResult(response);
  } catch (err) {
    hideProgress();
    showMessage(err instanceof Error ? err.message : String(err), 'error');
    clipBtn.disabled = false;
  }
});

function handleSyncResult(response: {
  ok?: boolean;
  error?: string;
  result?: { id?: string; title?: string; reused?: boolean; created?: boolean; updated?: boolean };
} | undefined) {
  if (!response?.ok) {
    hideProgress();
    const err = response?.error || 'Save failed';
    showMessage(err, 'error');
    if (/token|unauthorized/i.test(err)) showSettings();
    clipBtn.disabled = false;
    return;
  }
  showProgress('Done', 100);
  const { id, created, updated, reused, title } = response.result || {};
  const action = reused ? 'Opened' : updated ? 'Updated' : created === false ? 'Saved' : 'Saved';
  const short = String(id || '').slice(0, 8);
  showMessage(`${action} ${short}…${title ? ` · ${title}` : ''}`, 'ok');
  clipBtn.disabled = false;
  void refreshStatus();
  setTimeout(() => hideProgress(), 800);
}

async function activeTabId(): Promise<number | null> {
  const [tab] = await browser.tabs.query({ active: true, currentWindow: true });
  return tab?.id ?? null;
}

loadSettings()
  .then(async (settings) => {
    fillForm(settings);
    if (!settings.token) showSettings();
    await renderEnableState(await getNotesEnabled());
    await refreshStatus();
    // Resume progress UI if a job is still running (popup was closed mid-job).
    await refreshJob();
    const res = await browser.runtime.sendMessage({ type: 'membox.job-status' });
    if (res?.job?.status === 'running') startPolling();
  })
  .catch((err) => {
    statusEl.className = 'status-dot status-bad';
    statusEl.title = 'error';
    showMessage(err instanceof Error ? err.message : String(err), 'error');
  });
