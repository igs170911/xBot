import { chromium } from '/home/daniel/Projects/LazyBoyHermes/web/node_modules/playwright/index.mjs';
const files = process.argv.slice(2);
const browser = await chromium.launch();
for (const f of files) {
  const page = await browser.newPage({ viewport: { width: 390, height: 844 } });
  await page.goto('http://127.0.0.1:8932/' + f, { waitUntil: 'networkidle' });
  await page.waitForTimeout(1000);
  const r = await page.evaluate(() => {
    const small = [], safe = [], overflow = [];
    for (const el of document.querySelectorAll('a,button,input,[role=switch],label')) {
      const b = el.getBoundingClientRect();
      if (!b.width) continue;
      if (el.closest('[aria-hidden="true"]')) continue;
      if (el.tagName === 'LABEL' && !el.querySelector('input[type=checkbox]')) continue;
      if (el.tagName === 'INPUT' && el.type === 'checkbox') continue;
      if (b.width < 44 || b.height < 44) small.push(`${el.tagName} ${Math.round(b.width)}x${Math.round(b.height)} "${(el.getAttribute('aria-label')||el.textContent||el.placeholder||'').trim().slice(0,20)}"`);
    }
    for (const el of document.querySelectorAll('body *')) {
      const b = el.getBoundingClientRect();
      if (!b.width || !b.height) continue;
      const leaf = el.children.length === 0 && (el.textContent.trim() || el.tagName === 'path');
      if (!leaf) continue;
      if (b.top < 47 || b.bottom > 810) safe.push(`${el.tagName} ${Math.round(b.top)}-${Math.round(b.bottom)} "${el.textContent.trim().slice(0,15)}"`);
      if (b.right > 390.5 || b.left < -0.5) overflow.push(`${el.tagName} ${Math.round(b.left)}-${Math.round(b.right)} "${el.textContent.trim().slice(0,15)}"`);
    }
    return { small, safe: safe.slice(0, 8), overflow: overflow.slice(0, 8) };
  });
  console.log(f, JSON.stringify(r, null, 1));
  await page.close();
}
await browser.close();
