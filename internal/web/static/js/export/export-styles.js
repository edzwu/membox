/* Miru — build the combined stylesheet (embedded fonts + KaTeX CSS + app
   CSS) used by both the PNG export (inside an SVG <style>) and the
   interactive site ZIP export (inside index.html <style>). Fetched once and
   cached, since the text never changes between exports in the same session. */

import { EMBED_FONTS } from '../constants.js';

let fontCSSCache = null;
let exportStylesCache = null;

function toBase64(buf) {
  const bytes = new Uint8Array(buf);
  const chunks = [];
  for (let i = 0; i < bytes.length; i += 0x8000) {
    chunks.push(String.fromCharCode.apply(null, bytes.subarray(i, i + 0x8000)));
  }
  return btoa(chunks.join(''));
}

function getFontCSS() {
  if (fontCSSCache) return Promise.resolve(fontCSSCache);
  return Promise.all(EMBED_FONTS.map((f) => {
    return fetch(f.file)
      .then((r) => {
        if (!r.ok) throw new Error('font fetch failed: ' + f.file);
        return r.arrayBuffer();
      })
      .then((buf) => {
        return `@font-face{font-family:"${f.family}";font-style:${f.style};font-weight:${f.weight};` +
          `src:url(data:font/woff2;base64,${toBase64(buf)}) format("woff2");}`;
      });
  })).then((rules) => {
    fontCSSCache = rules.join('\n');
    return fontCSSCache;
  });
}

function fetchCSS(url) {
  return fetch(url)
    .then((r) => {
      if (!r.ok) throw new Error('css fetch failed: ' + url);
      return r.text();
    });
}

export function getExportStyles() {
  if (exportStylesCache) return Promise.resolve(exportStylesCache);
  return Promise.all([
    getFontCSS(),
    fetchCSS('vendor/katex.min.css'),
    fetchCSS('styles.css'),
  ]).then((parts) => {
    exportStylesCache = parts.join('\n');
    return exportStylesCache;
  });
}
