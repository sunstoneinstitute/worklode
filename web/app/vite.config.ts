/// <reference types="vitest/config" />
import path from 'node:path'
import tailwindcss from '@tailwindcss/vite'
import react from '@vitejs/plugin-react'
import { defineConfig, loadEnv } from 'vite'

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
      // No data: URIs: the server's CSP allows fonts and images from 'self' only
      assetsInlineLimit: 0,
    },
    // With the mock API off, /api goes to the Go server
    server: mocksOff
      ? { proxy: { '/api': env.VITE_API_TARGET || 'http://localhost:8080' } }
      : undefined,
    test: {
      environment: 'jsdom',
      globals: true,
      setupFiles: ['./src/test/setup.ts'],
      include: ['src/**/*.{test,spec}.{ts,tsx}'],
    },
  }
})
