import { chromium } from '@playwright/test';
import { fileURLToPath } from 'url';
import { dirname, join } from 'path';

const __filename = fileURLToPath(import.meta.url);
const __dirname = dirname(__filename);

async function takeScreenshots() {
  const browser = await chromium.launch();
  
  // Desktop light mode
  const desktopLight = await browser.newPage({
    viewport: { width: 1920, height: 1080 },
    colorScheme: 'light',
  });
  await desktopLight.goto('http://localhost:5181');
  await desktopLight.waitForTimeout(2000);
  await desktopLight.screenshot({ path: join(__dirname, '../docs/screenshots/before/desktop-light.png'), fullPage: true });
  await desktopLight.close();

  // Desktop dark mode
  const desktopDark = await browser.newPage({
    viewport: { width: 1920, height: 1080 },
    colorScheme: 'dark',
  });
  await desktopDark.goto('http://localhost:5181');
  await desktopDark.waitForTimeout(2000);
  await desktopDark.screenshot({ path: join(__dirname, '../docs/screenshots/before/desktop-dark.png'), fullPage: true });
  await desktopDark.close();

  // Mobile light mode
  const mobileLight = await browser.newPage({
    viewport: { width: 375, height: 667 },
    colorScheme: 'light',
  });
  await mobileLight.goto('http://localhost:5181');
  await mobileLight.waitForTimeout(2000);
  await mobileLight.screenshot({ path: join(__dirname, '../docs/screenshots/before/mobile-light.png'), fullPage: true });
  await mobileLight.close();

  // Mobile dark mode
  const mobileDark = await browser.newPage({
    viewport: { width: 375, height: 667 },
    colorScheme: 'dark',
  });
  await mobileDark.goto('http://localhost:5181');
  await mobileDark.waitForTimeout(2000);
  await mobileDark.screenshot({ path: join(__dirname, '../docs/screenshots/before/mobile-dark.png'), fullPage: true });
  await mobileDark.close();

  await browser.close();
  console.log('Screenshots captured successfully!');
}

takeScreenshots().catch(console.error);
