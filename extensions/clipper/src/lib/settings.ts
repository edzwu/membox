import { DEFAULT_SETTINGS, type BridgeSettings } from './types';

const KEY = 'membox.bridge';

export async function loadSettings(): Promise<BridgeSettings> {
  const stored = await browser.storage.local.get(KEY);
  const value = stored[KEY] as Partial<BridgeSettings> | undefined;
  return {
    baseUrl: (value?.baseUrl || DEFAULT_SETTINGS.baseUrl).replace(/\/$/, ''),
    token: value?.token || '',
    autoOpen: value?.autoOpen ?? DEFAULT_SETTINGS.autoOpen,
  };
}

export async function saveSettings(settings: BridgeSettings): Promise<void> {
  await browser.storage.local.set({
    [KEY]: {
      baseUrl: settings.baseUrl.replace(/\/$/, ''),
      token: settings.token.trim(),
      autoOpen: settings.autoOpen,
    },
  });
}
