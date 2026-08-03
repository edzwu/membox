/* Miru — small, generic, DOM-light helpers with no app state of their own. */

export function prefersReducedMotion() {
  return window.matchMedia && window.matchMedia('(prefers-reduced-motion: reduce)').matches;
}

export function isEditableTarget(element) {
  if (!element) return false;
  const tag = element.tagName;
  return tag === 'INPUT' || tag === 'TEXTAREA' || element.isContentEditable;
}

export function slugify(text) {
  let s = text.trim().replace(/\s+/g, '-');
  // Keep letters, numbers, CJK, Hiragana, Katakana, Hangul, hyphen, underscore.
  s = s.replace(/[^a-zA-Z0-9\u4e00-\u9fa5\u3040-\u309f\u30a0-\u30ff\uac00-\ud7af_-]/g, '');
  s = s.replace(/-+/g, '-').replace(/^-|-$/g, '');
  return s || 'heading';
}

export function sanitizeFilename(name) {
  return name
    .replace(/\.[a-z0-9]+$/i, '')
    .replace(/[\/\\?%*:|"<>]/g, '-')
    .replace(/\s+/g, ' ')
    .trim()
    .replace(/^-+|-+$/g, '')
    || 'untitled';
}

export function escapeHtml(s) {
  return s
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#039;');
}
