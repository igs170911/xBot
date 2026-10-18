import { expect, test, type Page } from '@playwright/test'

// W01：截圖給人和 design/N02、N03、Sidebar、Main 畫板並排比對（存在 e2e-results/screens，不進 git）。
const shot = (name: string) => `e2e-results/screens/${name}.png`

async function ready(page: Page) {
  await page.evaluate(() => document.fonts.ready)
  // 停掉眨眼等動畫，截圖才穩定
  await page.addStyleTag({ content: '*{animation:none!important;transition:none!important}' })
  await page.evaluate(() => document.querySelectorAll('svg').forEach((s) => (s as SVGSVGElement).pauseAnimations?.()))
}

test.beforeEach(async ({ page }, info) => {
  if (info.project.name === 'desktop') await page.setViewportSize({ width: 1440, height: 960 })
})

test('字型自己 host：粉圓載入成功，沒有連到 Google', async ({ page }) => {
  const external: string[] = []
  page.on('request', (r) => {
    if (!r.url().startsWith('http://127.0.0.1')) external.push(r.url())
  })
  await page.goto('/dev/components')
  await ready(page)
  expect(await page.evaluate(() => document.fonts.check('16px Huninn', '待我處理'))).toBe(true)
  expect(external).toEqual([])
})

test('/dev/components 截圖', async ({ page }, info) => {
  await page.goto('/dev/components')
  await expect(page.getByRole('heading', { level: 1, name: '元件（Grok 風格 · Midnight）' })).toBeVisible()
  await expect(page.getByTestId('avatar-matrix').locator('[data-shape]')).toHaveCount(70)
  await ready(page)
  await page.getByTestId('focus-demo').focus()
  await page.screenshot({ path: shot(`${info.project.name}-dev-components`), fullPage: true })
  for (const id of ['n03', 'n02', 'n01']) {
    await page.getByTestId(`board-${id}`).screenshot({ path: shot(`${info.project.name}-${id}`) })
  }
})

test('版型截圖：側欄＋待我處理', async ({ page }, info) => {
  await page.goto('/')
  await expect(page.getByRole('heading', { level: 1, name: '早安，有 5 件事等你' })).toBeVisible()
  await expect(page.getByTestId('health')).toContainText('服務正常')
  await ready(page)
  await page.screenshot({ path: shot(`${info.project.name}-shell`), fullPage: info.project.name === 'mobile' })

  const nav = page.getByRole('navigation', { name: '側欄' })
  if (info.project.name === 'desktop') {
    await expect(nav).toBeVisible()
    await expect(page.getByRole('button', { name: '打開側欄' })).toBeHidden()
    const box = await nav.boundingBox()
    expect(box?.width).toBe(300)
    return
  }

  // 手機：側欄變抽屜、沒有橫向捲動、可點的東西 ≥ 44
  await expect(nav).toBeHidden()
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
  const opener = page.getByRole('button', { name: '打開側欄' })
  await expect(opener).toBeVisible()
  await opener.click()
  const drawer = page.getByRole('dialog', { name: '側欄' })
  await expect(drawer).toBeVisible()
  await expect(drawer.getByRole('button', { name: '關閉側欄' })).toBeFocused()
  await ready(page)
  await page.screenshot({ path: shot('mobile-shell-drawer') })

  const small: string[] = []
  for (const el of await drawer.locator('a, button, input').all()) {
    const b = await el.boundingBox()
    if (b && (b.height < 44 || (b.width < 44 && (await el.getAttribute('aria-label'))))) {
      small.push(`${(await el.getAttribute('aria-label')) ?? (await el.innerText()).slice(0, 12)} ${b.width}×${b.height}`)
    }
  }
  // 群組標題（收合鈕）用 touch-target 撐大可點範圍，量不到 ::before，所以排除
  expect(small.filter((s) => !/已釘選|群組對話|營運部|後勤|工程/.test(s))).toEqual([])

  await page.keyboard.press('Escape')
  await expect(drawer).toBeHidden()
  await expect(opener).toBeFocused()
})
