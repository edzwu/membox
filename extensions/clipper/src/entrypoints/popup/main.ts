import { fetchBridgeStatus } from '../../lib/membox-client';
import { loadSettings, saveSettings } from '../../lib/settings';
import type { BridgeSettings } from '../../lib/types';

const statusEl = document.getElementById('status') as HTMLDivElement;
const baseUrlEl = document.getElementById('baseUrl') as HTMLInputElement;
const tokenEl = document.getElementById('token') as HTMLInputElement;
const autoOpenEl = document.getElementById('autoOpen') as HTMLInputElement;
const messageEl = document.getElementById('message') as HTMLPreElement;
const saveSettingsBtn = document.getElementById('saveSettings') as HTMLButtonElement;
const clipBtn = document.getElementById('clip') as HTMLButtonElement;

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

loadSettings().then(async (settings) => {
  fillForm(settings);
  await refreshStatus();
});
