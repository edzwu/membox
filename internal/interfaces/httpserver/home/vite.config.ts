import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

// Built assets are embedded by the Go server (../static via go:embed) and
// served under /, so asset URLs must be relative.
export default defineConfig({
  plugins: [react()],
  base: './',
  build: {
    outDir: '../static',
    emptyOutDir: true,
  },
  server: {
    port: 5174,
    proxy: {
      '/v1': 'http://127.0.0.1:8081',
    },
  },
});
