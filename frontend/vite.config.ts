import { defineConfig } from 'vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';
import { devAPI } from './dev-api.ts';

const apiPort = Number(process.env.TIKI_DEV_API_PORT || 8081);

export default defineConfig({
  plugins: [svelte(), devAPI(apiPort)],
  server: {
    port: 5173,
    strictPort: true,
    proxy: { '/api': process.env.TIKI_API_URL || `http://127.0.0.1:${apiPort}` },
    watch: { ignored: ['**/.dev/**'] },
  },
  preview: { port: 5173, strictPort: true },
});
