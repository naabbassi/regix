import preact from '@preact/preset-vite';
import { defineConfig } from 'vite';

// The build lands where the Go server embeds it. In development, `npm run dev` serves the UI
// and forwards API calls to a Regix server on :8080.
export default defineConfig({
  plugins: [preact()],
  build: { outDir: '../internal/web/dist', emptyOutDir: true },
  server: { proxy: { '/api': 'http://localhost:8080' } },
});
