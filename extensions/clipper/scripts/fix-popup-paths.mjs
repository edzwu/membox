#!/usr/bin/env node
// WXT/Vite emit absolute "/chunks/…" and "/assets/…" URLs in extension HTML.
// Chrome extension pages should load them relative to the extension root;
// absolute paths can leave a popup or side panel without JS and styles.
import fs from 'node:fs';
import path from 'node:path';

const roots = [
  path.resolve('dist/chrome-mv3'),
  path.resolve('dist/chrome-mv3-production'),
  path.resolve('dist/firefox-mv2'),
  path.resolve('dist/firefox-mv2-production'),
  path.resolve('dist/firefox-mv3'),
  path.resolve('dist/firefox-mv3-production'),
  path.resolve('.output/chrome-mv3'),
  path.resolve('.output/firefox-mv2'),
];
const pages = ['popup.html', 'sidepanel.html'];

let fixed = 0;
const found = [];
for (const root of roots) {
  for (const pageName of pages) {
    const page = path.join(root, pageName);
    if (!fs.existsSync(page)) continue;
    found.push(page);
    const before = fs.readFileSync(page, 'utf8');
    let after = before
      .replace(/(?:src|href)="\/(?:\.\/)?(chunks|assets)\//g, (match, dir) =>
        match.replace(`/${dir}/`, `./${dir}/`).replace(`/./${dir}/`, `./${dir}/`),
      )
      // Also collapse accidental ../../../chunks after base:'./' experiments.
      .replace(/(?:src|href)="(?:\.\.\/)+(chunks|assets)\//g, (match, dir) =>
        match.replace(/(?:\.\.\/)+(chunks|assets)\//, `./${dir}/`),
      );
    // Normalize any remaining absolute root refs for known dirs.
    after = after
      .replaceAll('src="/chunks/', 'src="./chunks/')
      .replaceAll('href="/assets/', 'href="./assets/')
      .replaceAll('src="../../../chunks/', 'src="./chunks/')
      .replaceAll('href="../../../assets/', 'href="./assets/');
    if (after !== before) {
      fs.writeFileSync(page, after);
      fixed++;
      console.log(`fixed ${path.relative(process.cwd(), page)}`);
    } else {
      console.log(`ok     ${path.relative(process.cwd(), page)}`);
    }
  }
}

if (fixed === 0) {
  // Still print final script and stylesheet tags for sanity.
  for (const page of found) {
    const lines = fs.readFileSync(page, 'utf8')
      .split('\n')
      .filter((line) => line.includes('chunks/') || line.includes('assets/'));
    for (const line of lines) console.log(' ', line.trim());
  }
}
