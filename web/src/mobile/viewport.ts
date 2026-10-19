// iPhone 的鍵盤彈出時，頁面不會變矮；Safari 是把「看得到的範圍」（visualViewport）往下移、變矮。
// 鍵盤開著時把看得到的範圍寫成 --app-top、--app-h；手機的對話頁用 fixed 貼著它：頁首在可見範圍最上面、輸入框貼著鍵盤。
// 鍵盤沒開時把兩個變數拿掉，用滿版高度（100dvh）：從主畫面打開的 App 模式下，visualViewport 平常就比螢幕矮
// （2026-10-19 實機截圖：輸入框下面空了一大塊）。Android Chrome 靠 interactive-widget=resizes-content，不會進到鍵盤分支。

/** 看得到的高度比頁面矮這麼多，才算鍵盤開著（狀態列、網址列的差距比這小）。 */
const KEYBOARD_MIN = 150

export function watchViewport() {
  const vv = window.visualViewport
  if (!vv) return
  const root = document.documentElement
  let frame = 0
  const update = () => {
    cancelAnimationFrame(frame)
    frame = requestAnimationFrame(() => {
      const keyboard = window.innerHeight - vv.height > KEYBOARD_MIN
      if (keyboard) {
        root.style.setProperty('--app-h', `${Math.round(vv.height)}px`)
        root.style.setProperty('--app-top', `${Math.max(0, Math.round(vv.offsetTop))}px`)
      } else {
        root.style.removeProperty('--app-h')
        root.style.removeProperty('--app-top')
      }
    })
  }
  update()
  vv.addEventListener('resize', update)
  vv.addEventListener('scroll', update)
}
