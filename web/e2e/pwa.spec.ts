import { expect, test } from './fixtures'

// W31 PWA（MB-1）：像 Lighthouse 的可安裝檢查——manifest 合格、service worker 註冊並控制頁面、
// Chrome 回報沒有安裝障礙、app 殼有存、/api 的回應不進快取；Android 分享目標由 SW 收下再帶到 /share。
// 對著真的服務跑（make e2e 建出來的執行檔，含 dist/sw.js）。只在桌面 Chromium 跑一次。

test.skip(({ isMobile }) => isMobile, '桌面 Chromium 跑一次就好')
test.use({ serviceWorkers: 'allow' })

test('manifest：名字、standalone、圖示（含 maskable）、分享目標', async ({ request }) => {
  const res = await request.get('/manifest.webmanifest')
  expect(res.ok()).toBe(true)
  expect(res.headers()['content-type']).toContain('application/manifest+json')
  const m = await res.json()
  expect(m).toMatchObject({ name: 'XBot', short_name: 'XBot', display: 'standalone', start_url: '/?source=pwa', scope: '/', theme_color: '#070707', background_color: '#070707' })
  const sizes = (m.icons as Array<{ sizes: string; purpose?: string; src: string }>).map((i) => `${i.sizes}:${i.purpose ?? 'any'}`)
  expect(sizes).toEqual(expect.arrayContaining(['192x192:any', '512x512:any', '512x512:maskable']))
  for (const i of m.icons) expect((await request.get(i.src)).ok(), i.src).toBe(true)
  expect(m.share_target).toMatchObject({ action: '/share', method: 'POST', enctype: 'multipart/form-data' })
  const sw = await request.get('/sw.js')
  expect(sw.ok()).toBe(true)
  expect(sw.headers()['cache-control']).toContain('no-cache')
  expect(await sw.text()).not.toContain('__XBOT_')
})

test('service worker 註冊並控制頁面、可安裝、/api 不進快取', async ({ page, context }) => {
  await page.goto('/')
  await expect(page.locator('link[rel="manifest"]')).toHaveAttribute('href', '/manifest.webmanifest')
  await expect(page.locator('meta[name="apple-mobile-web-app-capable"]')).toHaveAttribute('content', 'yes')
  await expect(page.locator('link[rel="apple-touch-icon"]')).toHaveCount(1)
  const scope = await page.evaluate(async () => (await navigator.serviceWorker.ready).scope)
  expect(scope).toMatch(/\/$/)
  await page.reload()
  await expect.poll(() => page.evaluate(() => !!navigator.serviceWorker.controller)).toBe(true)

  // Chrome 的安裝檢查（和 Lighthouse 的 installable 相同來源）
  const cdp = await context.newCDPSession(page)
  const { installabilityErrors } = (await cdp.send('Page.getInstallabilityErrors')) as { installabilityErrors: Array<{ errorId: string }> }
  expect(installabilityErrors.map((e) => e.errorId)).toEqual([])
  const manifest = (await cdp.send('Page.getAppManifest')) as { errors: unknown[]; url: string }
  expect(manifest.errors).toEqual([])

  // app 殼有存；任何快取裡都沒有 /api 的回應
  const cached = await page.evaluate(async () => {
    const out: string[] = []
    for (const k of await caches.keys()) for (const r of await (await caches.open(k)).keys()) out.push(new URL(r.url).pathname)
    return out
  })
  expect(cached).toEqual(expect.arrayContaining(['/', '/manifest.webmanifest', '/icons/icon-192.png']))
  expect(cached.some((p) => p.startsWith('/assets/'))).toBe(true)
  await page.evaluate(() => fetch('/api/v1/auth/status').then((r) => r.json()))
  const after = await page.evaluate(async () => {
    const out: string[] = []
    for (const k of await caches.keys()) for (const r of await (await caches.open(k)).keys()) out.push(new URL(r.url).pathname)
    return out
  })
  expect(after.filter((p) => p.startsWith('/api/'))).toEqual([])
})

test('離線時頁面還打得開（app 殼）', async ({ page, context }) => {
  await page.goto('/')
  await page.evaluate(async () => {
    await navigator.serviceWorker.ready
  })
  await page.reload()
  await expect.poll(() => page.evaluate(() => !!navigator.serviceWorker.controller)).toBe(true)
  await context.setOffline(true)
  await page.goto('/chats').catch(() => {})
  await expect(page.locator('#root')).toBeAttached()
  await expect(page.getByText(/網路已離線|連不到|XBot|對話|待我處理/).first()).toBeVisible()
  await context.setOffline(false)
})

test('Android 分享目標：SW 收下文字與網址，帶到 /share 選 Bot', async ({ page, request }) => {
  await request.post('/api/v1/bots.create', { data: { name: '小秘', title: '幕僚長' } })
  await page.goto('/')
  await page.evaluate(async () => {
    await navigator.serviceWorker.ready
  })
  await page.reload()
  await expect.poll(() => page.evaluate(() => !!navigator.serviceWorker.controller)).toBe(true)
  // 模擬系統的分享：對 /share 送 multipart POST（頁面導覽，交給 SW）
  await page.evaluate(() => {
    const f = document.createElement('form')
    f.method = 'POST'
    f.action = '/share'
    f.enctype = 'multipart/form-data'
    for (const [k, v] of [
      ['title', '好文章'],
      ['text', '幫我看重點'],
      ['url', 'https://example.com/a'],
    ]) {
      const i = document.createElement('input')
      i.name = k
      i.value = v
      f.appendChild(i)
    }
    document.body.appendChild(f)
    f.submit()
  })
  await expect(page).toHaveURL(/\/share\?received=1/)
  await expect(page.getByRole('textbox')).toHaveValue(/幫我看重點/)
  await expect(page.getByText('https://example.com/a').first()).toBeVisible()
  await expect(page.locator('[role="radio"][aria-checked="true"]')).toHaveCount(1)
})

test('沒有 service worker 時 POST /share 不會壞掉', async ({ request }) => {
  const res = await request.post('/share', { multipart: { text: 'hi' }, maxRedirects: 0 })
  expect(res.status()).toBe(303)
  expect(res.headers()['location']).toBe('/share?nosw=1')
})

test('連手機（A08／L09）：電腦顯示 QR，手機用配對碼登入後電腦顯示已連上；配對碼只能用一次', async ({ page, browser }, info) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.goto('/settings/computers')
  await page.getByRole('button', { name: '連手機' }).click()
  const dlg = page.getByRole('dialog', { name: '連手機' })
  await expect(dlg.getByRole('img', { name: '連手機的 QR' })).toBeVisible()
  await expect(dlg).toContainText('localhost') // 本機網址：提醒手機連不到
  const code = (await dlg.locator('.font-mono.text-ink').textContent())!.trim()
  expect(code).toMatch(/^[A-Z0-9]{4}-[A-Z0-9]{4}$/)
  await page.screenshot({ path: `e2e-results/screens/${info.project.name}-a08-pair-qr.png` })
  // 手機：另一個瀏覽器（沒有登入、看起來不是本機）打開 /pair#配對碼
  const phone = await browser.newContext({ baseURL: info.project.use.baseURL, extraHTTPHeaders: { 'X-Forwarded-For': '100.64.0.9' } })
  const p = await phone.newPage()
  const before = await (await p.request.get('/api/v1/auth/status')).json()
  expect(before.authenticated).toBe(false)
  await p.goto('/pair#' + code)
  await expect(p.getByRole('heading', { name: /已連上/ })).toBeVisible()
  const st = await (await p.request.get('/api/v1/auth/status')).json()
  expect(st.authenticated).toBe(true)
  await expect(dlg.getByText('手機已連上')).toBeVisible({ timeout: 10_000 })
  // 再用一次：不行
  const again = await p.request.post('/api/v1/auth/pair', { data: { code } })
  expect(again.status()).toBe(403)
  await phone.close()
})
