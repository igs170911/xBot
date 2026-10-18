import { defineConfig, devices } from '@playwright/test'
import { E2E_NOW } from './e2e/clock'

// e2e 對著 make build 出來的執行檔跑（make e2e 會先建置）。
const port = Number(process.env.XBOT_E2E_PORT ?? 8799)

export default defineConfig({
  testDir: './e2e',
  outputDir: './e2e-results',
  reporter: [['list']],
  // 假服務靠 page.route 攔 /api；service worker 會讓攔截失準，所以預設擋掉（pwa.spec 另外打開）
  use: { baseURL: `http://127.0.0.1:${port}`, trace: 'retain-on-failure', serviceWorkers: 'block' },
  projects: [
    { name: 'desktop', use: { ...devices['Desktop Chrome'] } },
    { name: 'mobile', use: { ...devices['Pixel 7'] } },
    // W31：手機畫面（L01～L21）另外用 iPhone 尺寸（390×844，和 L 畫板一樣）再跑一次；用 Chromium 模擬
    { name: 'iphone', testMatch: /phone\.spec\.ts/, use: { ...devices['iPhone 14'], viewport: { width: 390, height: 844 }, browserName: 'chromium' } },
  ],
  webServer: {
    command: `../bin/xbot serve`,
    url: `http://127.0.0.1:${port}/healthz`,
    // 定時任務的時鐘固定（見 e2e/clock.ts）；XBOT_FAKE_NOW 只有 XBOT_DEV=1 時服務才理。
    env: { XBOT_PORT: String(port), XBOT_DATA_DIR: './e2e-results/data', XBOT_DEV: '1', XBOT_FAKE_NOW: E2E_NOW },
    reuseExistingServer: false,
    timeout: 30_000,
  },
})
