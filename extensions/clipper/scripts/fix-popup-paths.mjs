#!/usr/bin/env node
// WXT/Vite emit absolute "/chunks/…" and "/assets/…" URLs in popup.html.
// Chrome extension pages should load them as "./chunks/…" relative to the
// extension root; absolute paths can leave the popup stuck on static HTML
// ("checking…", no CSS) when the JS module fails to resolve.
import fs from 'node:fs';
import path from 'node:path';

const roots = [
  path.resolve('dist/chrome-mv3'),
  path.resolve('dist/chrome-mv3-production'),
  path.resolve('.output/chrome-mv3'),
];

let fixed = 0;
for (const root of roots) {
  const popup = path.join(root, 'popup.html');
  if (!fs.existsSync(popup)) continue;
  const before = fs.readFileSync(popup, 'utf8');
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
    fs.writeFileSync(popup, after);
    fixed++;
    console.log(`fixed ${path.relative(process.cwd(), popup)}`);
  } else {
    console.log(`ok     ${path.relative(process.cwd(), popup)}`);
  }
}
if (fixed === 0) {
  // Still print final script tags for sanity.
  for (const root of roots) {
    const popup = path.join(root, 'popup.html');
    if (!fs.existsSync(popup)) continue;
    const lines = fs.readFileSync(popup, 'utf8').split('\n').filter((l) => l.includes('chunks/') || l.includes('assets/'));
    for (const line of lines) console.log(' ', line.trim());
  }
}
