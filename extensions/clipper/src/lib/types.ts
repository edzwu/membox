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
  /** Length of the extracted Markdown body (debug/diagnostic). */
  bodyLength?: number;
  /** "selection" | "page" — stored in membox document_sources */
  clipMode?: 'selection' | 'page';
  /** Raw selection text (pre-Markdown) used for annotation anchoring. */
  excerptRaw?: string;
};

export type IngestResult = {
  id: string;
  path: string;
  created: boolean;
  updated?: boolean;
  /** True when an existing YouTube summary was opened without re-running echo-bp. */
  reused?: boolean;
  view_url: string;
  body_length?: number;
  linked?: string;
  title?: string;
  filename?: string;
  video_id?: string;
  course_code?: string;
};

/** Returned when a page clip for the URL already exists and overwrite was not set. */
export type IngestConflict = {
  code: 'clip_exists';
  message: string;
  existing: IngestResult;
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
