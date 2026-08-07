import { defineConfig } from 'wxt';

export default defineConfig({
  srcDir: 'src',
  outDir: 'dist',
  // Keep Vite defaults; a postbuild step rewrites popup.html asset hrefs to
  // extension-root-relative paths (see scripts/fix-popup-paths.mjs).
  manifest: {
    name: 'membox clipper',
    description: 'Save the current page to membox and open it in Miru',
    version: '0.1.7',
    permissions: ['activeTab', 'storage', 'scripting', 'tabs'],
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
  },
});
