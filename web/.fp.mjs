import { chromium, devices } from '@playwright/test'
const out = process.argv[2]
const b = await chromium.launch({ args: ['--touch-events=enabled'] })
const ctx = await b.newContext({ ...devices['iPhone 14'], viewport: { width: 390, height: 844 } })
const p = await ctx.newPage()
const cdp = await ctx.newCDPSession(p)
const drag = async (x, y0, y1) => {
  await cdp.send('Input.dispatchTouchEvent', { type: 'touchStart', touchPoints: [{ x, y: y0 }] })
  for (let i = 1; i <= 12; i++) { await cdp.send('Input.dispatchTouchEvent', { type: 'touchMove', touchPoints: [{ x, y: y0 + ((y1 - y0) * i) / 12 }] }); await new Promise((r) => setTimeout(r, 16)) }
  await cdp.send('Input.dispatchTouchEvent', { type: 'touchEnd', touchPoints: [] })
  await p.waitForTimeout(700)
}
const state = () => p.evaluate(() => ({ doc: document.scrollingElement.scrollTop, boxes: [...document.querySelectorAll('*')].filter((e) => /(auto|scroll)/.test(getComputedStyle(e).overflowY) && e.scrollHeight > e.clientHeight + 2).map((e) => e.className.toString().slice(0, 40) + ' ' + Math.round(e.scrollTop) + '/' + e.scrollHeight + '-' + e.clientHeight) }))
await p.goto('http://127.0.0.1:8780/bots/01a15489275427ee6b2ebde19ea96bc0/files'); await p.waitForTimeout(3000)
// 打開一個看起來比較長的檔案（.md 報告）
const names = await p.locator('main button, main a').evaluateAll((els) => els.map((e) => e.textContent.trim()).filter((t) => /\.(md|txt|csv|json)/.test(t)).slice(0, 10))
console.log('files:', names)
const target = p.locator('main').getByText(/\.md/).first()
if (await target.count()) { await target.click(); await p.waitForTimeout(2500) } else { const dir = p.locator('main').getByText(/reports|data/).first(); await dir.click(); await p.waitForTimeout(1500); await p.locator('main').getByText(/\.md|\.csv/).first().click(); await p.waitForTimeout(2500) }
console.log('before:', JSON.stringify(await state()))
await drag(195, 650, 350); console.log('swipe up:', JSON.stringify(await state()))
await drag(195, 350, 650); console.log('swipe down:', JSON.stringify(await state()))
await p.screenshot({ path: out + '/filepreview.png' })
await b.close()
