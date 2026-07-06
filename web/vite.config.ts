import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

const buildId = (process.env.VITE_BUILD_ID || '').replace(/[^a-zA-Z0-9_.-]/g, '')
const namePattern = buildId ? `assets/[name]-${buildId}-[hash]` : 'assets/[name]-[hash]'

export default defineConfig({
  plugins: [react()],
  build: {
    rollupOptions: {
      output: {
        entryFileNames: `${namePattern}.js`,
        chunkFileNames: `${namePattern}.js`,
        assetFileNames: `${namePattern}.[ext]`,
      },
    },
  },
  server: {
    proxy: {
      '/api': 'http://127.0.0.1:18980',
      '/health': 'http://127.0.0.1:18980',
    },
  },
})
