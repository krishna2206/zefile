import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

export default defineConfig({
  plugins: [react(), tailwindcss()],
  build: {
    // Output lands where the Go package that embeds it lives, so the
    // binary needs no build step to copy files around.
    outDir: '../internal/web/dist',
    emptyOutDir: true,
  },
  server: {
    // During development the API runs separately, so the dev server forwards
    // to it rather than the app knowing two base URLs.
    proxy: {
      '/api': 'http://localhost:8080',
      '/healthz': 'http://localhost:8080',
    },
  },
})
