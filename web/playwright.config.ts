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
    // 真的 Safari 核心（WebKit）跑手機畫面：Arch 等沒有官方支援的系統用 scripts/test-webkit.sh 在 Playwright 的容器裡跑
    ...(process.env.XBOT_E2E_WEBKIT ? [{ name: 'iphone-webkit', testMatch: /phone\.spec\.ts/, use: { ...devices['iPhone 14'], viewport: { width: 390, height: 844 } } }] : []),
  ],
  webServer: {
    command: `../bin/xbot serve`,
    url: `http://127.0.0.1:${port}/healthz`,
    // 定時任務的時鐘固定（見 e2e/clock.ts）；XBOT_FAKE_NOW 只有 XBOT_DEV=1 時服務才理。
    // XBOT_IMPORT_PATHS=off：不去找這台電腦上真的 LazyBoy、OpenMausBot 舊資料（V10）。
    // XBOT_LOCAL_NOAUTH=1：e2e 測試在本機執行，不需要登入碼（和 make dev 一致）。
    env: { XBOT_PORT: String(port), XBOT_DATA_DIR: './e2e-results/data', XBOT_DEV: '1', XBOT_FAKE_NOW: E2E_NOW, XBOT_IMPORT_PATHS: 'off', XBOT_LOCAL_NOAUTH: '1' },
    reuseExistingServer: false,
    timeout: 30_000,
  },
})
