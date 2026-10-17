import { chromium } from '/home/daniel/Projects/LazyBoyHermes/web/node_modules/playwright/index.mjs';
const b = await chromium.launch(); const p = await b.newPage({ viewport: { width: 1600, height: 1200 } });
const errs = []; p.on('pageerror', e => errs.push(String(e))); p.on('console', m => { if (m.type() === 'error') errs.push(m.text()) });
await p.goto('file://' + process.argv[2]); await p.waitForTimeout(3500);
const out = await p.evaluate(() => [...document.querySelectorAll('span[data-state]')].map(s => {
  const svg = s.querySelector('svg'); const paths = [...svg.querySelectorAll('g[clip-path] path')];
  const anchor = svg.querySelector('g[clip-path] > g').getAttribute('transform');
  return { state: s.dataset.state, viewBox: svg.getAttribute('viewBox'), anchor, eye0: paths[0]?.getAttribute('d'), eye1: paths[1]?.getAttribute('d'), mouth: paths[2]?.getAttribute('d'), mouthW: paths[2]?.getAttribute('stroke-width') };
}));
await p.screenshot({ path: process.argv[3] });
console.log(JSON.stringify({ errs, out }));
await b.close();
