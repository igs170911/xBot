import { test as base } from '@playwright/test'
import { basename } from 'node:path'
import { scanA11y } from './axe'

// e2e 共用的 test：每次 page.screenshot（各規格拍來和畫板比對的畫面）也順便跑一次無障礙檢查（v1.0）。
// 有 serious／critical 時記為失敗但讓測試繼續跑完（expect.soft）。A11Y_SKIP=1 可以暫時關掉。
export * from '@playwright/test'

export const test = base.extend({
  page: async ({ page }, run, info) => {
    if (!process.env.A11Y_SKIP) {
      const shot = page.screenshot.bind(page)
      let n = 0
      page.screenshot = async (opts) => {
        const buf = await shot(opts)
        n += 1
        const name = opts?.path ? basename(opts.path).replace(/\.png$/, '') : `${info.titlePath.slice(1).join('-')}-${n}`
        await scanA11y(page, info, `shot-${name}`, { wait: false, soft: true })
        return buf
      }
    }
    await run(page)
  },
})
