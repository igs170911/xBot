// usage: node click.mjs File.dc.html w h "text1" "text2" ...  -> shots-e2/<File>-<i>.png after clicking button containing text
import { chromium } from '/home/daniel/Projects/LazyBoyHermes/web/node_modules/playwright/index.mjs';
const [,, file, w, h, ...texts] = process.argv;
const b = await chromium.launch();
const p = await b.newPage({ viewport: { width: +w, height: +h } });
const errs = [];
p.on('pageerror', e => errs.push(String(e)));
await p.goto('http://127.0.0.1:8932/' + file, { waitUntil: 'networkidle' });
await p.waitForTimeout(1000);
let i = 0;
for (const t of texts) {
  i++;
  await p.locator('button', { hasText: t }).first().click();
  await p.waitForTimeout(400);
  await p.screenshot({ path: `../shots-host/${file.replace('.dc.html', '')}-${i}.png` });
}
console.log(JSON.stringify({ errs }));
await b.close();
