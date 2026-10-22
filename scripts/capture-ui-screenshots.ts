#!/usr/bin/env ts-node
// 截取真實的 UI 截圖：1280x800，對著已seeded 資料的執行中伺服器
// 用法：先啟動伺服器 (make dev)，再執行 npx ts-node scripts/capture-ui-screenshots.ts <output-dir>

import { chromium, type Page } from '@playwright/test'
import * as path from 'path'
import * as fs from 'fs'

const WIDTH = 1280
const HEIGHT = 800

async function captureScreenshots(baseURL: string, outputDir: string) {
  const browser = await chromium.launch({ headless: true })
  const context = await browser.newContext({
    viewport: { width: WIDTH, height: HEIGHT },
    serviceWorkers: 'block',
  })
  const page = await context.newPage()

  // 確保輸出目錄存在
  fs.mkdirSync(outputDir, { recursive: true })

  console.log(`🎬 Capturing UI screenshots at ${WIDTH}x${HEIGHT}...`)

  try {
    // 等待服務就緒
    await page.goto(baseURL, { waitUntil: 'networkidle', timeout: 30000 })
    console.log('✓ Connected to server')

    // 1. 主對話畫面
    console.log('📸 Capturing: 01-main-chat.png')
    await page.goto(`${baseURL}/chats`)
    await page.waitForTimeout(1000) // 等待資料載入
    await page.screenshot({ path: path.join(outputDir, '01-main-chat.png'), fullPage: false })

    // 2. 側欄「更多」展開
    console.log('📸 Capturing: 02-sidebar-more-open.png')
    const moreButton = page.locator('[aria-label*="更多"], button:has-text("更多"), [data-testid="sidebar-more"]').first()
    if (await moreButton.isVisible({ timeout: 5000 }).catch(() => false)) {
      await moreButton.click()
      await page.waitForTimeout(500)
    } else {
      console.warn('⚠️  Could not find "更多" button, capturing current state')
    }
    await page.screenshot({ path: path.join(outputDir, '02-sidebar-more-open.png'), fullPage: false })

    // 3. WorkSteps 進行中
    console.log('📸 Capturing: 03-worksteps-in-progress.png')
    // 嘗試找到正在執行的工作或任務
    const workstepsRunning = page.locator('[data-testid*="worksteps"], [aria-label*="工作步驟"], .worksteps-running').first()
    if (await workstepsRunning.isVisible({ timeout: 5000 }).catch(() => false)) {
      await workstepsRunning.click({ timeout: 1000 }).catch(() => {})
      await page.waitForTimeout(500)
    } else {
      // 若沒有正在執行的，進入任一對話試圖觸發
      const chatItem = page.locator('[role="link"][href*="/threads/"], [data-testid*="chat-item"]').first()
      if (await chatItem.isVisible({ timeout: 3000 }).catch(() => false)) {
        await chatItem.click()
        await page.waitForTimeout(1000)
      }
      console.warn('⚠️  Could not find active WorkSteps, capturing current state')
    }
    await page.screenshot({ path: path.join(outputDir, '03-worksteps-in-progress.png'), fullPage: false })

    // 4. WorkSteps 完成
    console.log('📸 Capturing: 04-worksteps-done.png')
    const workstepsDone = page.locator('[data-testid*="worksteps-done"], [aria-label*="已完成"], .worksteps-done').first()
    if (await workstepsDone.isVisible({ timeout: 5000 }).catch(() => false)) {
      await workstepsDone.click({ timeout: 1000 }).catch(() => {})
      await page.waitForTimeout(500)
    } else {
      console.warn('⚠️  Could not find completed WorkSteps, capturing current state')
    }
    await page.screenshot({ path: path.join(outputDir, '04-worksteps-done.png'), fullPage: false })

    console.log(`✅ All screenshots saved to ${outputDir}`)
  } catch (err) {
    console.error('❌ Error capturing screenshots:', err)
    throw err
  } finally {
    await browser.close()
  }
}

async function main() {
  const outputDir = process.argv[2]
  if (!outputDir) {
    console.error('Usage: ts-node scripts/capture-ui-screenshots.ts <output-dir>')
    process.exit(1)
  }

  const baseURL = process.env.XBOT_URL || 'http://127.0.0.1:8782'
  await captureScreenshots(baseURL, outputDir)
}

main().catch((err) => {
  console.error(err)
  process.exit(1)
})
