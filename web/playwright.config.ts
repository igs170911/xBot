import { defineConfig, devices } from '@playwright/test'
import { E2E_NOW } from './e2e/clock'

// e2e 對著 make build 出來的執行檔跑（make e2e 會先建置）。
const port = Number(process.env.XBOT_E2E_PORT ?? 8799)

export default defineConfig({
  testDir: './e2e',
  outputDir: './e2e-results',
  reporter: [['list']],
  use: { baseURL: `http://127.0.0.1:${port}`, trace: 'retain-on-failure' },
  projects: [
    { name: 'desktop', use: { ...devices['Desktop Chrome'] } },
    { name: 'mobile', use: { ...devices['Pixel 7'] } },
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
