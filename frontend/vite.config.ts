import { defineConfig } from 'vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';

export default defineConfig({
  plugins: [svelte()],
  server: {
    port: 5173,
    strictPort: true,
    proxy: { '/api': process.env.TIKI_API_URL || 'http://127.0.0.1:8080' },
  },
  preview: { port: 5173, strictPort: true },
});
