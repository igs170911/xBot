import type { Page } from '@playwright/test'

// 破版檢查（手機 phone.spec、平板 settings.spec 共用）：不能有橫向捲動，也不能有元素超出畫面（可以橫向捲動的容器裡面的除外）。
// root：只看這個元素裡面（例：桌面的設定視窗），超出它的邊也算破版——外框 overflow:hidden 會把超出的部分直接切掉，畫面上看不到。
// 給 root 時，裡面大的捲動區（例：設定的內容欄）也不能被撐寬到要左右捲。
export async function overflow(page: Page, root?: string) {
  return page.evaluate((sel) => {
    const W = document.documentElement.clientWidth
    const top = sel ? document.querySelector(sel) : null
    if (sel && !top) return [`找不到 ${sel}`]
    const box = top ? top.getBoundingClientRect() : { left: 0, right: W }
    const bad: string[] = []
    if (document.documentElement.scrollWidth > W + 1) bad.push(`page scrollWidth ${document.documentElement.scrollWidth} > ${W}`)
    const inside = (r: DOMRect) => r.right <= box.right + 1 && r.left >= box.left - 1
    const clipped = (el: Element) => {
      for (let p = el.parentElement; p && p !== top; p = p.parentElement) {
        const s = getComputedStyle(p)
        if (/(auto|scroll|hidden|clip)/.test(s.overflowX)) return inside(p.getBoundingClientRect())
      }
      return false
    }
    const label = (el: Element) => el.tagName.toLowerCase() + (el.getAttribute('data-testid') ? `[${el.getAttribute('data-testid')}]` : '') + ' "' + (el.textContent ?? '').trim().slice(0, 30) + '"'
    for (const el of (top ?? document.body).querySelectorAll('*')) {
      const r = el.getBoundingClientRect()
      if (r.width === 0 || r.height === 0) continue
      const s = getComputedStyle(el)
      if (s.visibility === 'hidden' || s.position === 'fixed') continue
      if (!inside(r) && !clipped(el) && !el.closest('.sr-only-text,[aria-hidden="true"]')) bad.push(`${label(el)} ${Math.round(r.left)}..${Math.round(r.right)}`)
      if (top && /(auto|scroll)/.test(s.overflowY) && el.clientHeight > 200 && el.scrollWidth > el.clientWidth + 1) bad.push(`${label(el)} 要左右捲：${el.scrollWidth} > ${el.clientWidth}`)
    }
    return bad.slice(0, 8)
  }, root)
}
