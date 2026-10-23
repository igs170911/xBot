#!/usr/bin/env node
/**
 * Capture before/after UI screenshots at 1280x800
 * Usage: node scripts/capture-ui-screenshots.mjs <before|after> <http://localhost:8780>
 */

import { chromium } from '@playwright/test';
import { mkdir, writeFile } from 'fs/promises';
import { dirname } from 'path';

const [,, mode, baseURL] = process.argv;

if (!mode || !baseURL) {
  console.error('Usage: node scripts/capture-ui-screenshots.mjs <before|after> <http://localhost:8780>');
  process.exit(1);
}

const outputDir = `/workspace/docs/ui/${mode}`;

async function capture() {
  const browser = await chromium.launch();
  const context = await browser.newContext({
    viewport: { width: 1280, height: 800 },
    baseURL,
  });
  const page = await context.newPage();

  try {
    // Navigate to app
    await page.goto('/');
    await page.waitForTimeout(2000);

    // 01: Main chat view
    await page.screenshot({ path: `${outputDir}/01-main-chat.png`, fullPage: false });
    console.log(`✓ Captured ${mode}/01-main-chat.png`);

    // 02: Sidebar with 更多 menu open
    const moreButton = page.locator('button:has-text("更多")').first();
    if (await moreButton.isVisible()) {
      await moreButton.click();
      await page.waitForTimeout(500);
    }
    await page.screenshot({ path: `${outputDir}/02-sidebar-more-open.png`, fullPage: false });
    console.log(`✓ Captured ${mode}/02-sidebar-more-open.png`);

    // 03: WorkSteps in progress (simulate by checking for existing or creating test data)
    // This requires actual running work - for now capture what's visible
    await page.goto('/bots/b1/threads/t1');
    await page.waitForTimeout(1000);
    await page.screenshot({ path: `${outputDir}/03-worksteps-in-progress.png`, fullPage: false });
    console.log(`✓ Captured ${mode}/03-worksteps-in-progress.png`);

    // 04: WorkSteps done
    await page.screenshot({ path: `${outputDir}/04-worksteps-done.png`, fullPage: false });
    console.log(`✓ Captured ${mode}/04-worksteps-done.png`);

    console.log(`\n✅ All ${mode} screenshots captured to ${outputDir}/`);

  } finally {
    await browser.close();
  }
}

capture().catch(err => {
  console.error('Screenshot capture failed:', err);
  process.exit(1);
});
