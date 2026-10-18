import { expect, test } from '@playwright/test'

test('首頁打得開，而且連得到服務', async ({ page }) => {
  await page.goto('/')
  await expect(page.getByRole('heading', { level: 1, name: /早安|待我處理/ })).toBeVisible()
  await expect(page.getByTestId('health')).toContainText('服務正常')
})
