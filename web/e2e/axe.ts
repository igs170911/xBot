import AxeBuilder from '@axe-core/playwright'
import { expect, type Page, type TestInfo } from '@playwright/test'
import { mkdirSync, writeFileSync } from 'node:fs'

// v1.0 無障礙檢查（PRD §8）：axe-core（WCAG 2.0～2.2 A／AA＋best-practice）＋手機的 44×44 可點範圍。
// serious／critical 算失敗；每次掃描的完整結果（含 moderate／minor）寫在 e2e-results/a11y/。
// a11y.spec.ts 直接呼叫；其他規格透過 fixtures.ts 在每次截圖時順便掃（截圖的地方就是畫面的各種狀態）。

const TAGS = ['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa', 'wcag22aa', 'best-practice']

type Violation = { id: string; impact?: string | null; help: string; nodes: { target: unknown[]; failureSummary?: string }[] }

export type ScanOptions = {
  /** 先等畫面穩定（字型、按需載入、讀取中）；截圖時已經穩定，不用等。 */
  wait?: boolean
  /** 用 expect.soft：記下問題但讓測試繼續跑（截圖時用）。 */
  soft?: boolean
}

export async function scanA11y(page: Page, info: TestInfo, name: string, opts: ScanOptions = {}) {
  if (opts.wait !== false) {
    await page.evaluate(() => document.fonts.ready)
    await expect(page.locator('[aria-busy="true"]')).toHaveCount(0, { timeout: 5000 }).catch(() => {})
    await page.waitForTimeout(200)
  }
  const res = await new AxeBuilder({ page }).withTags(TAGS).analyze()
  const all = res.violations as Violation[]
  if (info.project.use.isMobile || (page.viewportSize()?.width ?? 1000) < 640) all.push(...(await smallTargets(page)))
  const dir = 'e2e-results/a11y'
  mkdirSync(dir, { recursive: true })
  const file = `${dir}/${info.project.name}-${name.replace(/[^\w-]+/g, '_')}.json`
  writeFileSync(file, JSON.stringify(all.map((v) => ({ id: v.id, impact: v.impact, help: v.help, nodes: v.nodes.map((n) => ({ target: n.target, why: n.failureSummary })) })), null, 2))
  const bad = all.filter((v) => v.impact === 'serious' || v.impact === 'critical')
  const summary = bad.map((v) => `${v.id}（${v.impact}）：${v.help} → ${v.nodes.slice(0, 5).map((n) => JSON.stringify(n.target) + (n.failureSummary && v.id === 'touch-target-44' ? ' ' + n.failureSummary : '')).join('、')}${v.nodes.length > 5 ? ` 等 ${v.nodes.length} 處` : ''}`)
  if (process.env.A11Y_REPORT) return
  const check = opts.soft ? expect.soft : expect
  check(summary, `${name} 有無障礙問題（詳見 ${file}）`).toEqual([])
}

/**
 * 手機上可以點的東西至少 44×44（PRD §11、W31；WCAG 2.5.5 AAA，比 axe 的 target-size 24px 嚴）。
 * 用 ::before 撐大的（touch-target、index.css 的手機規則）也算；句子裡的連結、原生滑桿、只為了捲動而能聚焦的區塊不算。
 */
async function smallTargets(page: Page): Promise<Violation[]> {
  const bad = await page.evaluate(() => {
    const sel = 'a[href], button, input:not([type="hidden"]), select, textarea, summary, [role="button"], [role="tab"], [role="switch"], [role="checkbox"], [role="radio"], [role="menuitem"], [role="option"], [tabindex]:not([tabindex="-1"])'
    const out: { target: string; w: number; h: number }[] = []
    for (const el of document.querySelectorAll<HTMLElement>(sel)) {
      if (el.closest('[aria-hidden="true"], [inert]')) continue
      const r = el.getBoundingClientRect()
      // 只給螢幕閱讀器或鍵盤用的隱藏欄位（例：輸入到 Bot 電腦的隱藏輸入框）、畫面外的
      if (r.width <= 2 || r.height <= 2 || r.bottom < 0 || r.right < 0 || r.left > innerWidth) continue
      const cs = getComputedStyle(el)
      // 只給螢幕閱讀器的（sr-only：clip 起來），例：還沒聚焦的「跳到主要內容」
      if (cs.visibility === 'hidden' || cs.pointerEvents === 'none' || (cs.clip !== 'auto' && cs.clip !== '')) continue
      if (el instanceof HTMLInputElement && (el.type === 'range' || el.type === 'file')) continue
      // 句子裡的連結（WCAG 2.5.8 例外：inline）
      // 手機上回覆裡的連結、檔名是 role=link 的文字（iPhone 從 <a> 上滑不動），一樣是句子裡的連結
      if ((el.tagName === 'A' || el.getAttribute('role') === 'link') && cs.display === 'inline' && (el.parentElement?.textContent ?? '').trim().length > (el.textContent ?? '').trim().length) continue
      // 可以捲動、只為了鍵盤能捲而可聚焦的區塊（pre、表格）不是按鈕
      if (el.tagName === 'PRE' || (!el.matches('button, a, input, textarea, select') && (el.scrollWidth > el.clientWidth + 1 || el.scrollHeight > el.clientHeight + 1))) continue
      let w = r.width
      let h = r.height
      const before = getComputedStyle(el, '::before')
      // overflow 會把撐大的 ::before 裁掉
      if (before.content !== 'none' && before.position === 'absolute' && cs.overflowX === 'visible' && cs.overflowY === 'visible') {
        w = Math.max(w, parseFloat(before.width) || 0)
        h = Math.max(h, parseFloat(before.height) || 0)
      }
      // 包在 label 裡的欄位：點 label 也會聚焦
      const label = el.closest('label')
      if (label) {
        const lr = label.getBoundingClientRect()
        w = Math.max(w, lr.width)
        h = Math.max(h, lr.height)
      }
      if (w + 0.5 < 44 || h + 0.5 < 44) {
        const name = el.getAttribute('aria-label') || el.textContent?.trim().slice(0, 20) || el.getAttribute('placeholder') || ''
        out.push({ target: `${el.tagName.toLowerCase()}「${name}」`, w: Math.round(w), h: Math.round(h) })
      }
    }
    return out
  })
  if (!bad.length) return []
  return [{ id: 'touch-target-44', impact: 'serious', help: '手機上可以點的東西至少 44×44', nodes: bad.map((b) => ({ target: [b.target], failureSummary: `${b.w}×${b.h}` })) }]
}
