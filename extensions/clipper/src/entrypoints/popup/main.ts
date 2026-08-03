import { fetchBridgeStatus } from '../../lib/membox-client';
import { loadSettings, saveSettings } from '../../lib/settings';
import { getNotesEnabled, setNotesEnabled } from '../../lib/float-notes';
import type { BridgeSettings } from '../../lib/types';

const statusEl = document.getElementById('status') as HTMLDivElement;
const baseUrlEl = document.getElementById('baseUrl') as HTMLInputElement;
const tokenEl = document.getElementById('token') as HTMLInputElement;
const autoOpenEl = document.getElementById('autoOpen') as HTMLInputElement;
const messageEl = document.getElementById('message') as HTMLPreElement;
const saveSettingsBtn = document.getElementById('saveSettings') as HTMLButtonElement;
const clipBtn = document.getElementById('clip') as HTMLButtonElement;
const floatToggleBtn = document.getElementById('floatToggle') as HTMLButtonElement;
const floatRestoreBtn = document.getElementById('floatRestore') as HTMLButtonElement;
const floatCountEl = document.getElementById('floatCount') as HTMLSpanElement;
const notesToggleBtn = document.getElementById('notesToggle') as HTMLButtonElement;
const enableHintEl = document.getElementById('enableHint') as HTMLSpanElement;

async function renderEnableState(enabled: boolean) {
  notesToggleBtn.textContent = enabled ? 'Disable' : 'Enable';
  notesToggleBtn.classList.toggle('is-on', enabled);
  enableHintEl.textContent = enabled
    ? 'On — select text to take notes on this kind of page'
    : 'Off — selection composer and note cards are disabled';
}

notesToggleBtn.addEventListener('click', async () => {
  const next = !(await getNotesEnabled());
  await setNotesEnabled(next);
  renderEnableState(next);
  // Apply live to the current tab (no reload needed).
  const tabId = await activeTabId();
  if (tabId) {
    try {
      await browser.tabs.sendMessage(tabId, { type: 'membox.set-enabled', enabled: next });
    } catch {
      showMessage(next ? 'Enabled — reload the page to take effect' : 'Disabled');
    }
  }
  await refreshFloatStatus();
});

function showMessage(text: string) {
  messageEl.hidden = !text;
  messageEl.textContent = text;
}

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
  statusEl.textContent = 'checking…';
  statusEl.className = 'status status-unknown';
  const status = await fetchBridgeStatus(settings);
  if (status.connected) {
    statusEl.textContent = status.auth_required ? 'connected · auth' : 'connected';
    statusEl.className = 'status status-ok';
  } else {
    statusEl.textContent = 'offline';
    statusEl.className = 'status status-bad';
    if (status.error) {
      showMessage(`Cannot reach membox.\nRun: mm serve --port 8787\n${status.error}`);
    }
  }
}

saveSettingsBtn.addEventListener('click', async () => {
  const settings = readForm();
  await saveSettings(settings);
  showMessage('Settings saved.');
  await refreshStatus();
});

clipBtn.addEventListener('click', async () => {
  clipBtn.disabled = true;
  showMessage('Clipping…');
  try {
    await saveSettings(readForm());
    const response = await browser.runtime.sendMessage({ type: 'membox.ingest-active-tab' });
    if (!response?.ok) {
      showMessage(response?.error || 'Clip failed');
      return;
    }
    const { id, path, view_url: viewUrl } = response.result;
    showMessage(`Saved ${id.slice(0, 8)}…\n${path}\n${viewUrl}`);
    await refreshStatus();
  } catch (err) {
    showMessage(err instanceof Error ? err.message : String(err));
  } finally {
    clipBtn.disabled = false;
  }
});

type FloatStatusResponse = {
  ok: boolean;
  collapsed?: boolean;
  count?: number;
  hiddenCount?: number;
  total?: number;
  savedCount?: number;
  restored?: number;
  error?: string;
};

async function activeTabId(): Promise<number | null> {
  const [tab] = await browser.tabs.query({ active: true, currentWindow: true });
  return tab?.id ?? null;
}

async function refreshFloatStatus() {
  floatToggleBtn.disabled = true;
  floatRestoreBtn.disabled = true;
  floatRestoreBtn.hidden = true;
  floatCountEl.textContent = '—';
  const enabled = await getNotesEnabled();
  await renderEnableState(enabled);
  if (!enabled) {
    floatCountEl.textContent = 'notes disabled — click Enable';
    floatToggleBtn.hidden = true;
    return;
  }
  try {
    const tabId = await activeTabId();
    if (!tabId) {
      floatCountEl.textContent = 'no tab';
      return;
    }
    const response = (await browser.tabs.sendMessage(tabId, {
      type: 'membox.floats.status',
    })) as FloatStatusResponse | undefined;
    if (!response?.ok) {
      if (response?.error === 'native-annotations') {
        // Miru reader page: notes are rendered by Miru itself.
        floatCountEl.textContent = 'rendered as Miru annotations';
        floatToggleBtn.hidden = true;
        return;
      }
      if (response?.error === 'notes-disabled') {
        // Tab was loaded before enabling; needs a refresh to pick up.
        floatCountEl.textContent = 'reload this tab to activate';
        floatToggleBtn.hidden = true;
        return;
      }
      floatCountEl.textContent = '0 on page';
      floatToggleBtn.textContent = 'Expand';
      floatToggleBtn.hidden = false;
      return;
    }
    floatToggleBtn.hidden = false;
    const count = response.count ?? 0;
    const hidden = response.hiddenCount ?? 0;
    const saved = response.savedCount ?? 0;
    const collapsed = response.collapsed !== false;
    // saved = membox selection notes for this URL (source of truth).
    // pinned/stacked = local overlay cache (reconciled to membox on status).
    const parts: string[] = [`${saved} in membox`];
    if (count > 0) parts.push(`${count} ${collapsed ? 'pinned' : 'open'}`);
    if (hidden > 0) parts.push(`${hidden} hidden`);
    if (count === 0 && hidden === 0) parts.push('none pinned');
    floatCountEl.textContent = parts.join(' · ');
    floatToggleBtn.textContent = collapsed ? 'Expand' : 'Collapse';
    floatToggleBtn.disabled = count === 0;
    // Restore when anything is hidden locally OR membox has notes not pinned.
    const canRestore = hidden > 0 || saved > count + hidden;
    floatRestoreBtn.hidden = !canRestore;
    floatRestoreBtn.disabled = !canRestore;
    floatRestoreBtn.textContent = 'Restore';
  } catch {
    floatCountEl.textContent = 'reload page';
    floatToggleBtn.textContent = 'Expand';
    floatToggleBtn.disabled = true;
  }
}

floatToggleBtn.addEventListener('click', async () => {
  floatToggleBtn.disabled = true;
  try {
    const tabId = await activeTabId();
    if (!tabId) return;
    const response = (await browser.tabs.sendMessage(tabId, {
      type: 'membox.floats.toggle',
    })) as FloatStatusResponse;
    if (!response?.ok) {
      showMessage(response?.error || 'Toggle failed — reload the page');
      return;
    }
  } catch (err) {
    showMessage(err instanceof Error ? err.message : String(err));
  } finally {
    await refreshFloatStatus();
  }
});

floatRestoreBtn.addEventListener('click', async () => {
  floatRestoreBtn.disabled = true;
  try {
    const tabId = await activeTabId();
    if (!tabId) return;
    const response = (await browser.tabs.sendMessage(tabId, {
      type: 'membox.floats.restore',
    })) as FloatStatusResponse;
    if (!response?.ok) {
      showMessage(response?.error || 'Restore failed — reload the page');
      return;
    }
    const n = response.restored ?? 0;
    showMessage(n ? `Restored ${n} pin${n === 1 ? '' : 's'} on this page` : 'Nothing to restore');
  } catch (err) {
    showMessage(err instanceof Error ? err.message : String(err));
  } finally {
    await refreshFloatStatus();
  }
});

loadSettings().then(async (settings) => {
  fillForm(settings);
  await refreshStatus();
  await refreshFloatStatus();
});

