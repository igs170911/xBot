// iPhone 的鍵盤彈出時，頁面不會變矮；Safari 是把「看得到的範圍」（visualViewport）往下移、變矮。
// 鍵盤開著時把看得到的範圍寫成 --app-top、--app-h；手機的對話頁用 fixed 貼著它：頁首在可見範圍最上面、輸入框貼著鍵盤。
// 鍵盤沒開時把兩個變數拿掉，用滿版高度（--app-full，見 index.css）。Android Chrome 靠 interactive-widget=resizes-content，不會進到鍵盤分支。
//
// 從主畫面打開的 App（standalone）不一樣（2026-10-20 iOS 18 真機時間線）：鍵盤彈出時整頁（innerHeight）也跟著變矮
// （852 → 450），拿 innerHeight 比永遠看不出鍵盤，輸入框會被蓋住。這裡改拿 100lvh（一直是整個螢幕 852）比，
// 並在 <html> 加上 standalone（診斷用）。滿版高度仍然用 dvh：可見範圍有時一直是 793，用 lvh 底部會被切掉（index.css）。

/** 看得到的高度比頁面矮這麼多，才算鍵盤開著（狀態列、網址列的差距比這小）。 */
const KEYBOARD_MIN = 150

/** 從主畫面打開的 App（iPhone 用 navigator.standalone，其他用 display-mode）。 */
export function isStandalone(): boolean {
  return !!(navigator as Navigator & { standalone?: boolean }).standalone || !!window.matchMedia?.('(display-mode: standalone)').matches
}

/** 100lvh 實際是幾 px（測試換成假的）。 */
export const measure = {
  lvh(): number {
    const probe = document.createElement('div')
    probe.style.cssText = 'position:absolute;visibility:hidden;pointer-events:none;top:0;height:100lvh'
    document.body.appendChild(probe)
    const h = probe.offsetHeight
    probe.remove()
    return h
  },
}

export function watchViewport() {
  const root = document.documentElement
  const standalone = isStandalone()
  root.classList.toggle('standalone', standalone)
  const vv = window.visualViewport
  if (!vv) return
  let frame = 0
  let open = false
  const update = () => {
    cancelAnimationFrame(frame)
    frame = requestAnimationFrame(() => {
      // 比較的基準：一般瀏覽器用 innerHeight（鍵盤不會讓它變矮）；主畫面 App 用整個螢幕（lvh）
      const full = standalone ? Math.max(window.innerHeight, measure.lvh()) : window.innerHeight
      const keyboard = full - vv.height > KEYBOARD_MIN
      if (keyboard) {
        root.style.setProperty('--app-h', `${Math.round(vv.height)}px`)
        root.style.setProperty('--app-top', `${Math.max(0, Math.round(vv.offsetTop))}px`)
      } else {
        root.style.removeProperty('--app-h')
        root.style.removeProperty('--app-top')
        // 鍵盤收起來後 iPhone 有時把整頁留在往上推的位置，對話頁下面空一塊、像超出範圍。
        // 整頁本來就不能捲（對話頁鎖住了）時，把它放回原位；可以捲的頁面不動，免得跳回最上面。
        if (open && root.style.overflow === 'hidden' && window.scrollY !== 0) window.scrollTo(0, 0)
      }
      open = keyboard
    })
  }
  update()
  vv.addEventListener('resize', update)
  vv.addEventListener('scroll', update)
}
