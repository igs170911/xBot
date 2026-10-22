import { chromium } from '@playwright/test';

async function capture(mode, baseURL) {
  const outputDir = `/workspace/docs/ui/${mode}`;
  const browser = await chromium.launch();
  const context = await browser.newContext({
    viewport: { width: 1280, height: 800 },
    baseURL,
  });
  const page = await context.newPage();

  try {
    await page.goto('/');
    await page.waitForTimeout(2000);

    await page.screenshot({ path: `${outputDir}/01-main-chat.png` });
    console.log(`✓ Captured ${mode}/01-main-chat.png`);

    const moreButton = page.locator('button:has-text("更多")').first();
    if (await moreButton.isVisible()) {
      await moreButton.click();
      await page.waitForTimeout(500);
    }
    await page.screenshot({ path: `${outputDir}/02-sidebar-more-open.png` });
    console.log(`✓ Captured ${mode}/02-sidebar-more-open.png`);

    await page.goto('/bots/b1/threads/t1');
    await page.waitForTimeout(1000);
    await page.screenshot({ path: `${outputDir}/03-worksteps-in-progress.png` });
    console.log(`✓ Captured ${mode}/03-worksteps-in-progress.png`);

    await page.screenshot({ path: `${outputDir}/04-worksteps-done.png` });
    console.log(`✓ Captured ${mode}/04-worksteps-done.png`);

    console.log(`\n✅ All ${mode} screenshots captured`);
  } finally {
    await browser.close();
  }
}

const [,, mode, baseURL] = process.argv;
capture(mode, baseURL).catch(err => {
  console.error('Failed:', err);
  process.exit(1);
});
