import { defineConfig } from 'vitest/config'
import { svelte } from '@sveltejs/vite-plugin-svelte'

export default defineConfig({
  plugins: [svelte()],
  // noVNC usa un await de nivel superior (ES2022): el destino por defecto de Vite (chrome87, safari14) no lo admite.
  // WebView2, WebKitGTK 4.1 y el WKWebView de macOS actuales sí.
  build: { target: 'es2022' },
  // Lo mismo para el pre-empaquetado de dependencias de `npm run dev` / `wails dev`.
  optimizeDeps: { esbuildOptions: { target: 'es2022' } },
  test: {
    environment: 'node',
    include: ['src/**/*.test.ts'],
  },
})
