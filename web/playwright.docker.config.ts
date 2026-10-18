import { defineConfig, devices } from '@playwright/test'
import { mkdtempSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

// make e2e-docker：真的開 Bot 電腦（xbot-box 映像＋Hermes），模型是測試裡的假 OpenAI 相容服務。
// 資料目錄放在 /tmp 的短路徑：product.sock 的完整路徑不能超過 108 字元。
const port = Number(process.env.XBOT_E2E_DOCKER_PORT ?? 8797)
const dataDir = process.env.XBOT_E2E_DATA_DIR ?? mkdtempSync(join(tmpdir(), 'xbe2e-'))
process.env.XBOT_E2E_DATA_DIR = dataDir // 設定檔會被每個 worker 再載入一次，沿用同一個目錄

export default defineConfig({
  testDir: './e2e-docker',
  outputDir: './e2e-results/docker',
  reporter: [['list']],
  timeout: 300_000,
  workers: 1,
  use: { baseURL: `http://127.0.0.1:${port}`, trace: 'retain-on-failure', ...devices['Desktop Chrome'], viewport: { width: 1440, height: 960 } },
  webServer: {
    command: `../bin/xbot serve`,
    url: `http://127.0.0.1:${port}/healthz`,
    env: { XBOT_PORT: String(port), XBOT_DATA_DIR: dataDir, XBOT_BOX_IMAGE: process.env.BOX_IMAGE ?? 'xbot-box:dev' },
    reuseExistingServer: false,
    timeout: 30_000,
  },
})
