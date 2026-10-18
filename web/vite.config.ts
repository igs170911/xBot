import { createHash } from 'node:crypto'
import { readFileSync } from 'node:fs'
import { defineConfig, type Plugin } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

// 開發時 API 交給 Go 服務（make dev 會同時啟動兩邊）。
const api = `http://127.0.0.1:${process.env.XBOT_PORT ?? '8782'}`

// PWA 的 app 殼（W31）：除了打包出來的入口 JS／CSS，還要先存這些（public/ 底下的檔案）。
const SHELL_FILES = ['/', '/manifest.webmanifest', '/icons/icon-192.png', '/icons/icon-512.png', '/icons/maskable-512.png', '/icons/apple-touch-icon.png', '/icons/badge-96.png', '/icons/favicon.svg']

/** 建置時把 pwa/sw.js 輸出成 dist/sw.js：填入這次的入口檔清單與版本（清單一變，版本就變，舊快取會被清掉）。 */
function xbotServiceWorker(): Plugin {
  return {
    name: 'xbot-service-worker',
    apply: 'build',
    generateBundle(_, bundle) {
      const entry = Object.values(bundle)
        .filter((f) => (f.type === 'chunk' && f.isEntry) || (f.type === 'asset' && f.fileName.endsWith('.css')))
        .map((f) => '/' + f.fileName)
      const precache = [...SHELL_FILES, ...entry.sort()]
      const version = createHash('sha256').update(precache.join('\n')).digest('hex').slice(0, 12)
      const src = readFileSync(new URL('./pwa/sw.js', import.meta.url), 'utf8')
        .replace('__XBOT_VERSION__', version)
        .replace('__XBOT_PRECACHE__', JSON.stringify(precache))
      this.emitFile({ type: 'asset', fileName: 'sw.js', source: src })
    },
  }
}

export default defineConfig({
  plugins: [react(), tailwindcss(), xbotServiceWorker()],
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
    // 機器很忙（make check 同時跑 Go 測試、多個 worker 一起掛整個 App）時，單一測試可能要好幾秒；
    // 要比 test-setup 裡 findBy／waitFor 的等待上限長，失敗時才看得到是哪一步等不到。
    testTimeout: 20_000,
  },
})
