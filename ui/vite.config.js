import { fileURLToPath } from 'node:url'
import { defineConfig } from 'vite'
import { svelte } from '@sveltejs/vite-plugin-svelte'
import tailwindcss from '@tailwindcss/vite'

// The SPA. `index.html` is the entry, so Vite emits it too and rewrites the
// asset URLs into ui/dist — that whole directory feeds `//go:embed all:ui/dist`
// in main.go, and the Go server serves it at /static/ (hence `base`, which is a
// URL prefix, not a directory). The file names are fixed (no content hash) so a
// rebuild overwrites them and the committed .gitkeep survives; the server sends
// `Cache-Control: no-cache` for them.
export default defineConfig({
  base: '/static/',
  plugins: [tailwindcss(), svelte()],
  build: {
    outDir: 'dist',
    // Keep .gitkeep in dist/: go:embed fails on an empty directory, so a fresh
    // clone must still compile before the first `npm run build`.
    emptyOutDir: false,
    rollupOptions: {
      input: fileURLToPath(new URL('./index.html', import.meta.url)),
      output: {
        entryFileNames: 'app.js',
        assetFileNames: 'app.[ext]',
      },
    },
  },
})
