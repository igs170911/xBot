import { chromium, devices } from '@playwright/test'
const out = process.argv[2]
const BOT = '/bots/01a15163d7656bac5d6370e5fd39f4a1'
const ROOM = '/rooms/01a154862139e57beeaa0c6ec6e0c1e3'
const PAGES = ['/', '/chats', '/office', '/notifications', '/agenda', '/calendar', '/files', '/apps', '/apps/logins', '/teams', '/org', '/settings', '/settings/models', '/settings/advanced/limits',
  BOT, BOT + '/files', BOT + '/profile', ROOM, '/new/bot']
const b = await chromium.launch({ args: ['--touch-events=enabled'] })
const ctx = await b.newContext({ ...devices['iPhone 14'], viewport: { width: 390, height: 844 } })
const p = await ctx.newPage()
await p.addInitScript(() => {
  window.__shifts = []
  try { new PerformanceObserver((l) => { for (const e of l.getEntries()) window.__shifts.push({ v: +e.value.toFixed(3), t: Math.round(e.startTime), src: (e.sources || []).map((s) => s.node?.nodeName + '.' + (s.node?.className?.toString?.() || '').slice(0, 40)).slice(0, 3) }) }).observe({ type: 'layout-shift', buffered: true }) } catch {}
})
const cdp = await ctx.newCDPSession(p)
const snap = () => p.evaluate(() => {
  const all = [...document.querySelectorAll('*')].filter((e) => { const s = getComputedStyle(e); return /(auto|scroll)/.test(s.overflowY) && e.scrollHeight > e.clientHeight + 2 && e.clientHeight > 100 })
  const nav = document.querySelector('nav[aria-label="分頁"]')
  return {
    doc: document.scrollingElement.scrollTop, docH: document.scrollingElement.scrollHeight, vh: innerHeight,
    boxes: all.map((e) => ({ k: e.tagName + '.' + e.className.toString().slice(0, 30), top: Math.round(e.scrollTop), sh: e.scrollHeight, ch: e.clientHeight })),
    nav: nav ? Math.round(nav.getBoundingClientRect().top) : null,
  }
})
const swipe = async (dy) => {
  const y0 = dy < 0 ? 600 : 250, y1 = y0 + dy
  await cdp.send('Input.dispatchTouchEvent', { type: 'touchStart', touchPoints: [{ x: 195, y: y0 }] })
  for (let i = 1; i <= 12; i++) { await cdp.send('Input.dispatchTouchEvent', { type: 'touchMove', touchPoints: [{ x: 195, y: y0 + (dy * i) / 12 }] }); await new Promise((r) => setTimeout(r, 16)) }
  await cdp.send('Input.dispatchTouchEvent', { type: 'touchEnd', touchPoints: [] })
}
const report = {}
for (const path of PAGES) {
  const navTops = []
  await p.goto('http://127.0.0.1:8780' + path, { waitUntil: 'commit' })
  for (const ms of [50, 250, 600, 1200, 2500]) { await p.waitForTimeout(ms - (navTops.length ? [50, 250, 600, 1200, 2500][navTops.length - 1] : 0)); navTops.push(await p.evaluate(() => { const n = document.querySelector('nav[aria-label="分頁"]'); return n ? Math.round(n.getBoundingClientRect().top) : null })) }
  await p.waitForTimeout(500)
  const s0 = await snap()
  await swipe(-300) // 手指往上滑：內容往下捲
  await p.waitForTimeout(700)
  const s1 = await snap()
  await swipe(300)
  await p.waitForTimeout(700)
  const s2 = await snap()
  const shifts = await p.evaluate(() => window.__shifts.filter((x) => x.v > 0.01))
  const moved = (a, b) => b.doc !== a.doc || b.boxes.some((x, i) => a.boxes[i] && x.top !== a.boxes[i].top)
  const scrollable = s0.docH > s0.vh + 5 || s0.boxes.length > 0
  const issues = []
  if (new Set(navTops.filter((x) => x !== null)).size > 1) issues.push('NAV MOVED ' + navTops.join('→'))
  if (shifts.length) issues.push('SHIFTS ' + JSON.stringify(shifts).slice(0, 300))
  if (scrollable && !moved(s0, s1) && !moved(s1, s2)) issues.push('NO SCROLL docH=' + s0.docH + ' boxes=' + JSON.stringify(s0.boxes))
  if (s0.boxes.length && s0.docH > s0.vh + 5) issues.push('DOC ALSO SCROLLABLE docH=' + s0.docH + ' (rubber-band risk)')
  report[path.replace('01a15163d7656bac5d6370e5fd39f4a1', 'X').replace('01a154862139e57beeaa0c6ec6e0c1e3', 'R')] = { issues, before: s0.boxes.map((x) => x.k + ':' + x.top + '/' + x.sh), after: s1.boxes.map((x) => x.top), doc: [s0.doc, s1.doc, s2.doc], navTops }
  await p.screenshot({ path: `${out}/${path.replace(/[/:]/g, '_') || '_home'}.png` })
}
console.log(JSON.stringify(report, null, 1))
await b.close()
