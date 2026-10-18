import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

// 開發時 API 交給 Go 服務（make dev 會同時啟動兩邊）。
const api = `http://127.0.0.1:${process.env.XBOT_PORT ?? '8782'}`

export default defineConfig({
  plugins: [react(), tailwindcss()],
  server: {
    port: Number(process.env.XBOT_WEB_PORT ?? 5181),
    strictPort: true,
    proxy: {
      '/api': { target: api, changeOrigin: false, ws: true },
      '/healthz': { target: api, changeOrigin: false },
    },
  },
  build: { outDir: 'dist', emptyOutDir: true },
  test: {
    environment: 'jsdom',
    include: ['src/**/*.test.{ts,tsx}'],
    setupFiles: ['./src/test-setup.ts'],
  },
})
