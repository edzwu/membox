/* Miru — user feedback: toast messages, clipboard writes, button "Copied"
   flashes, and triggering blob downloads. No document/annotation state. */

import { elements } from '../dom.js';

let toastTimer = null;

export function showToast(message) {
  elements.toast.textContent = message;
  elements.toast.hidden = false;
  void elements.toast.offsetWidth;
  elements.toast.classList.add('visible');

  if (toastTimer) clearTimeout(toastTimer);
  toastTimer = setTimeout(() => {
    elements.toast.classList.remove('visible');
  }, 2200);
}

export function downloadBlob(blob, filename) {
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = filename;
  document.body.appendChild(a);
  a.click();
  a.remove();
  setTimeout(() => URL.revokeObjectURL(url), 5000);
}

export function flashButton(button) {
  button.classList.add('copied');
  setTimeout(() => button.classList.remove('copied'), 1500);
}

export function flashCopied(button, options = {}) {
  showToast('Copied to clipboard');
  if (options.preserveContent) {
    flashButton(button);
    return;
  }
  const original = button.textContent;
  button.textContent = 'Copied';
  button.classList.add('copied');
  setTimeout(() => {
    button.textContent = original;
    button.classList.remove('copied');
  }, 1500);
}

function fallbackCopy(text, onDone) {
  const textarea = document.createElement('textarea');
  textarea.value = text;
  textarea.setAttribute('readonly', '');
  textarea.style.position = 'fixed';
  textarea.style.left = '-9999px';
  document.body.appendChild(textarea);
  textarea.select();

  try {
    document.execCommand('copy');
    onDone();
  } catch (err) {
    showToast('Copy failed');
  } finally {
    document.body.removeChild(textarea);
  }
}

export function writeClipboard(text, onDone) {
  if (navigator.clipboard && navigator.clipboard.writeText) {
    navigator.clipboard.writeText(text).then(onDone).catch(() => fallbackCopy(text, onDone));
  } else {
    fallbackCopy(text, onDone);
  }
}

export function copyText(text, button) {
  writeClipboard(text, () => flashCopied(button));
}
