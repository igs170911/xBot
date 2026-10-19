import { expect, test } from './fixtures'
import { MockServer } from './mock'

// 縮小的側欄：「⋯」選單要蓋在頁面上面，不能被右邊的頁面蓋住（2026-10-19 使用者回報：縮小後選單被切掉）。
test('縮小側欄的「⋯」選單整個看得到、點得到', async ({ page }, info) => {
  test.skip(info.project.name === 'mobile', '手機沒有縮小的側欄')
  await page.setViewportSize({ width: 1280, height: 800 })
  const m = new MockServer(page)
  m.addBot({ name: '小秘', title: '幕僚長', color: 'blue', shape: 'cursor' })
  await m.install()
  await page.goto('/office')
  const nav = page.getByRole('navigation', { name: '側欄' })
  await nav.getByRole('button', { name: '縮小側欄' }).click()
  await nav.getByRole('button', { name: '更多' }).click()
  const item = page.getByRole('menuitem', { name: '組織架構' })
  await expect(item).toBeVisible()
  // 選單項目的右半邊（在側欄外面）最上層要是選單本身
  const onTop = await item.evaluate((el) => {
    const r = el.getBoundingClientRect()
    const hit = document.elementFromPoint(r.right - 12, r.top + r.height / 2)
    return !!hit && el.contains(hit)
  })
  expect(onTop).toBe(true)
  await item.click()
  await expect(page).toHaveURL(/\/org$/)
  await nav.getByRole('button', { name: '展開側欄' }).click()
})
