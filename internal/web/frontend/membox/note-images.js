/* Clipboard images for selection notes. Binary bytes are persisted in the
   private MEMBOX_HOME asset store; note Markdown contains only a stable
   same-origin /api/note-assets/<sha>.<ext> reference. */

import { registerAnnotImagePasteHandler } from '../js/annotations/toolbar.js';
import { importNoteImagePath, uploadNoteImage } from './api.js';
import { session } from './session.js';

function markdownAlt(source) {
  const raw = typeof source === 'string' ? source.split('/').pop() : source?.name;
  let value = String(raw || '').replace(/\.[^.]+$/, '').trim();
  if (!value || /^image$/i.test(value)) value = 'clipboard image';
  return value.replace(/[\[\]\\]/g, '\\$&').slice(0, 120);
}

export function initNoteImages() {
  registerAnnotImagePasteHandler(async (source) => {
    if (!session.connected) throw new Error('需要连接 membox 才能保存图片');
    const result = typeof source === 'string'
      ? await importNoteImagePath(source)
      : await uploadNoteImage(source);
    if (!result?.url) throw new Error('图片上传没有返回地址');
    return {
      markdown: `![${markdownAlt(source)}](${result.url})`,
      url: result.url,
    };
  });
}
