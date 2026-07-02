import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

// The web UI compiles to internal/web/static, which the Go server embeds with
// go:embed (ADR-0009). Two pages ship as separate entries so the search page
// stays light and never pulls in the graph's cytoscape bundle.
export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    // "@/..." -> src/... (shadcn convention). Derived from import.meta.url so the
    // config needs no node typings.
    alias: { '@': new URL('./src', import.meta.url).pathname },
  },
  build: {
    outDir: '../static',
    emptyOutDir: true,
    rollupOptions: {
      input: {
        index: 'index.html',
        graph: 'graph.html',
      },
    },
  },
})
