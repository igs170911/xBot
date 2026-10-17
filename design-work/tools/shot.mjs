// usage: node shot.mjs <outDir> [--noshot] [--port=N] ALL | File.dc.html[:w:h] ...   (w,h default from canvas.json)
import { chromium } from '/home/daniel/Projects/LazyBoyHermes/web/node_modules/playwright/index.mjs';
import fs from 'fs';
const PROJ = '/tmp/claude-1000/-home-daniel-Projects/119fe167-bf85-4b35-9f2a-d88d25ba20e9/scratchpad/xbot-design/project';
const canvas = JSON.parse(fs.readFileSync(PROJ + '/canvas.json', 'utf8'));
let [,, outDir, ...items] = process.argv;
const noshot = items.includes('--noshot');
const portArg = items.find(x => x.startsWith('--port='));
const PORT = portArg ? portArg.slice(7) : '8932';
items = items.filter(x => !x.startsWith('--'));
if (items[0] === 'ALL') items = Object.keys(canvas.boards);
fs.mkdirSync(outDir, { recursive: true });
const browser = await chromium.launch();
const out = [];
for (const it of items) {
  let [file, w, h] = it.split(':');
  const b = canvas.boards[file] || {};
  w = +(w || b.w || 1440); h = +(h || b.h || 900);
  const page = await browser.newPage({ viewport: { width: w, height: h } });
  const errs = [];
  page.on('console', m => { const t = m.text(); if (m.type() === 'error' && !/^Error: <\w+> attribute/.test(t)) errs.push(t.slice(0, 240)); });
  page.on('pageerror', e => errs.push('PAGEERR ' + String(e).slice(0, 240)));
  page.on('requestfailed', r => errs.push('REQFAIL ' + r.url()));
  await page.goto(`http://127.0.0.1:${PORT}/` + file, { waitUntil: 'networkidle' }).catch(e => errs.push('GOTO ' + e));
  await page.waitForTimeout(1200);
  const info = await page.evaluate(() => {
    const H = Math.max(document.documentElement.scrollHeight, document.body.scrollHeight);
    const W = Math.max(document.documentElement.scrollWidth, document.body.scrollWidth);
    const txt = document.body.innerText || '';
    const leftovers = (txt.match(/\{\{[^}]*\}\}/g) || []).slice(0, 5);
    const brokenImgs = [...document.images].filter(i => !i.complete || i.naturalWidth === 0).map(i => i.src).slice(0, 5);
    const fonts = [...new Set([...document.fonts].filter(f => f.status === 'loaded').map(f => f.family))];
    return { H, W, leftovers, brokenImgs, fonts };
  });
  if (!noshot) await page.screenshot({ path: `${outDir}/${file.replace('.dc.html', '')}.png`, fullPage: true });
  out.push({ file, frame: [w, h], ...info, errs: [...new Set(errs)].slice(0, 6) });
  await page.close();
}
await browser.close();
console.log(JSON.stringify(out));
