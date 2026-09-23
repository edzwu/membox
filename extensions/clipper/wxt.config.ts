import { defineConfig } from 'wxt';

export default defineConfig({
  srcDir: 'src',
  outDir: 'dist',
  // Keep Vite defaults; a postbuild step rewrites extension-page asset hrefs to
  // extension-root-relative paths (see scripts/fix-popup-paths.mjs).
  manifest: ({ browser }) => ({
    name: 'membox clipper',
    description: 'Save pages to membox and organize open tabs',
    // WXT derives manifest.version from package.json, our single version source.
    permissions: [
      'activeTab',
      'storage',
      'scripting',
      'tabs',
      // Stash enrichment: visitCount / lastVisitTime per stashed URL.
      'history',
      ...(browser === 'firefox' ? [] : ['sidePanel']),
    ],
    // 127.0.0.1 = membox bridge. http(s)://*/ = clip any normal webpage
    // (content script + optional programmatic inject after user gesture).
    host_permissions: [
      'http://127.0.0.1/*',
      'http://localhost/*',
      'http://*/*',
      'https://*/*',
    ],
    action: {
      default_title: 'Save to membox',
    },
  }),
});
