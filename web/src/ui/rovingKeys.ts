// 單選組與分頁的方向鍵（v1.0 無障礙，WAI-ARIA radiogroup／tablist）：介面裡有很多自己畫的 role="radio"／role="tab"
// 按鈕（主題、形狀、顏色、檔案分組、篩選分頁…），在這裡統一補上：←→↑↓ 移到上一個／下一個並選取，Home／End 到頭尾。
// 元件自己處理過的（例：SegmentedControl 會 preventDefault）不再處理。

const STEP: Record<string, number> = { ArrowRight: 1, ArrowDown: 1, ArrowLeft: -1, ArrowUp: -1 }

function onKeyDown(e: KeyboardEvent) {
  if (e.defaultPrevented || e.altKey || e.ctrlKey || e.metaKey) return
  const el = e.target as HTMLElement | null
  const role = el?.getAttribute?.('role')
  if (!el || (role !== 'radio' && role !== 'tab')) return
  const group = el.closest(role === 'radio' ? '[role="radiogroup"]' : '[role="tablist"]')
  if (!group) return
  const items = [...group.querySelectorAll<HTMLElement>(`[role="${role}"]`)].filter(
    (x) => x.closest(role === 'radio' ? '[role="radiogroup"]' : '[role="tablist"]') === group && !x.hasAttribute('disabled') && x.getAttribute('aria-disabled') !== 'true' && x.getClientRects().length > 0,
  )
  const i = items.indexOf(el)
  if (i < 0 || items.length < 2) return
  let j: number
  if (e.key in STEP) j = (i + STEP[e.key] + items.length) % items.length
  else if (e.key === 'Home') j = 0
  else if (e.key === 'End') j = items.length - 1
  else return
  e.preventDefault()
  items[j].focus()
  items[j].click()
}

let installed = false

export function installRovingKeys() {
  if (installed || typeof document === 'undefined') return
  installed = true
  document.addEventListener('keydown', onKeyDown)
}
