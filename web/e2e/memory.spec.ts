import { expect, test, type Page } from './fixtures'
import { MockServer } from './mock'

// v1.0 效能：SSE 斷線重連、來回換頁不會累積記憶體或多開連線（結果記在 docs/perf.md）。
// 用 Chromium 的 CDP 量 JS heap（先強制 GC）；假服務的 EventSource 可以模擬斷線。

test.skip(({ browserName, isMobile }) => browserName !== 'chromium' || isMobile, '用 CDP 量，桌面 Chromium 跑一次就好')

async function heap(page: Page) {
  const cdp = await page.context().newCDPSession(page)
  await cdp.send('HeapProfiler.enable')
  for (let i = 0; i < 3; i++) await cdp.send('HeapProfiler.collectGarbage')
  const { usedSize } = await cdp.send('Runtime.getHeapUsage')
  await cdp.detach()
  return usedSize
}

test('SSE 斷線重連 15 次、來回換頁 30 次：只有一條連線，heap 不持續變大', async ({ page }, info) => {
  test.setTimeout(120_000)
  const api = new MockServer(page)
  const bot = api.addBot({ name: '小秘' })
  const th = api.addThread(bot.id, '比價')
  for (let i = 0; i < 40; i++) api.addMessage(th.id, { sender: i % 2 ? `bot:${bot.id}` : 'owner', content: `第 ${i} 則：` + '一些文字 '.repeat(20) })
  await api.install()
  await page.goto('/')
  await expect(page.getByRole('navigation', { name: '側欄' })).toBeVisible()
  const go = async (path: string) => {
    await page.evaluate((p) => {
      window.history.pushState({ d: 1 }, '', p)
      window.dispatchEvent(new PopStateEvent('popstate'))
    }, path)
  }
  const round = async () => {
    await go(`/bots/${bot.id}/threads/${th.id}`)
    await expect(page.getByText('第 39 則')).toBeVisible()
    await go('/')
    await expect(page.locator('main').first()).toBeVisible()
  }
  // 熱身：載入按需的頁面、建好快取
  for (let i = 0; i < 3; i++) await round()
  const before = await heap(page)
  for (let i = 0; i < 30; i++) {
    await round()
    if (i % 2 === 0) {
      await page.evaluate(() => (window as unknown as { __xbotDrop: () => void }).__xbotDrop())
      // 網頁 1 秒後自己重連
      await expect.poll(() => page.evaluate(() => (window as unknown as { __xbotOpenSources: () => number }).__xbotOpenSources()), { timeout: 5000 }).toBe(1)
      await api.emit({ kind: 'bot.updated', bot_id: bot.id, payload: { bot } })
    }
  }
  const after = await heap(page)
  const open = await page.evaluate(() => (window as unknown as { __xbotOpenSources: () => number }).__xbotOpenSources())
  const growth = after - before
  info.annotations.push({ type: 'heap', description: `before=${(before / 1048576).toFixed(1)}MB after=${(after / 1048576).toFixed(1)}MB growth=${(growth / 1024).toFixed(0)}KB` })
  console.log(`heap：${(before / 1048576).toFixed(2)} MB → ${(after / 1048576).toFixed(2)} MB（+${(growth / 1024).toFixed(0)} KB），開著的 SSE：${open}`)
  expect(open).toBe(1)
  // 30 輪換頁＋15 次重連後，GC 之後的 heap 增加要小於 2 MB（有累積的話會隨輪數線性長大）
  expect(growth).toBeLessThan(2 * 1048576)
})
