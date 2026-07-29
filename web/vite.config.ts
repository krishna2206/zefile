import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

export default defineConfig({
  plugins: [react()],
  build: {
    // Output lands where the Go package that embeds it lives, so the
    // binary needs no build step to copy files around.
    outDir: '../internal/web/dist',
    // Left off deliberately: the output directory holds a committed
    // placeholder, without which //go:embed has nothing to match and a clone
    // that has never run the frontend build cannot compile at all. Vite would
    // delete it on every build. Stale output is removed by `make web` instead,
    // which can be selective about what it touches.
    emptyOutDir: false,
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
