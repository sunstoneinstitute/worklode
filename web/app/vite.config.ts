/// <reference types="vitest/config" />
import { readFileSync } from 'node:fs'
import path from 'node:path'
import tailwindcss from '@tailwindcss/vite'
import react from '@vitejs/plugin-react'
import { defineConfig, loadEnv } from 'vite'

// The SPA's routes (WL-REQ-1305) as one regex, "$param" matching one segment
const spaRoute = new RegExp(
  `^(?:${(JSON.parse(readFileSync(path.resolve(import.meta.dirname, 'src/routes.json'), 'utf8')) as string[])
    .map((r) => r.replace(/\$\w+/g, '[^/]+'))
    .join('|')})/?$`,
)
// Vite's dev client and module graph
const viteOwned = /^\/(?:@|src\/|node_modules\/|__)/

function keepInVite(url: string): boolean {
  const p = url.split('?')[0]
  return viteOwned.test(p) || spaRoute.test(p)
}

// https://vite.dev/config/
export default defineConfig(({ mode }) => {
  // Includes VITE_* variables from the shell, e.g. `VITE_API_MOCKS=off pnpm dev`
  const env = loadEnv(mode, process.cwd(), 'VITE_')
  const mocksOff = env.VITE_API_MOCKS === 'off'

  return {
    plugins: [react(), tailwindcss()],
    resolve: {
      alias: {
        '@': path.resolve(import.meta.dirname, './src'),
      },
    },
    build: {
      outDir: '../../internal/webapp/dist',
      emptyOutDir: true,
      // The cockpit owns /assets/ on the legacy host; the SPA's files are /static/
      assetsDir: 'static',
      // No data: URIs: the server's CSP allows fonts and images from 'self' only
      assetsInlineLimit: 0,
    },
    // With the mock API off, every path except Vite's own and the SPA's routes
    // goes to the Go server, so links into the templ cockpit work too
    server: mocksOff
      ? {
          proxy: {
            '/': {
              target: env.VITE_API_TARGET || 'http://localhost:8080',
              bypass: (req) => (keepInVite(req.url ?? '/') ? req.url : undefined),
            },
          },
        }
      : undefined,
    test: {
      environment: 'jsdom',
      globals: true,
      setupFiles: ['./src/test/setup.ts'],
      include: ['src/**/*.{test,spec}.{ts,tsx}'],
    },
  }
})
