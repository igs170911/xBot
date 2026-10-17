// usage: node ln-click.mjs <out.png> <File> <w> <h> "text1" "text2" ...  (clicks buttons by visible text in order)
import { chromium } from '/home/daniel/Projects/LazyBoyHermes/web/node_modules/playwright/index.mjs';
const [,, out, file, w, h, ...clicks] = process.argv;
const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: +w, height: +h } });
const errs = [];
page.on('pageerror', e => errs.push(String(e).slice(0, 200)));
await page.goto('http://127.0.0.1:8932/' + file, { waitUntil: 'networkidle' });
await page.waitForTimeout(1000);
for (const t of clicks) { await page.getByRole('tab', { name: t, exact: true }).or(page.getByRole('button', { name: t, exact: true })).or(page.getByRole('switch', { name: t })).or(page.getByRole('option', { name: t })).or(page.getByRole('radio', { name: t })).or(page.getByRole('link', { name: t, exact: true })).or(page.getByRole('checkbox', { name: t })).first().click({ force: true }); await page.waitForTimeout(400); }
const H = await page.evaluate(() => document.documentElement.scrollHeight);
await page.screenshot({ path: out, fullPage: true });
console.log(JSON.stringify({ H, errs }));
await browser.close();
