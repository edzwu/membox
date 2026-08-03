export type BridgeSettings = {
  baseUrl: string;
  token: string;
  autoOpen: boolean;
};

export type BridgeStatus = {
  connected: boolean;
  auth_required?: boolean;
  error?: string;
};

export type ClipPayload = {
  title: string;
  sourceUrl: string;
  body: string;
  /** "selection" | "page" — stored in membox document_sources */
  clipMode?: 'selection' | 'page';
};

export type IngestResult = {
  id: string;
  path: string;
  created: boolean;
  view_url: string;
  linked?: string;
};

export type SourceClip = {
  id: string;
  title: string;
  excerpt?: string;
  note?: string;
  clip_mode?: string;
};

export const DEFAULT_SETTINGS: BridgeSettings = {
  baseUrl: 'http://127.0.0.1:8787',
  token: '',
  autoOpen: true,
};
