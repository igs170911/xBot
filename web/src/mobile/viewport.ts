// iPhone 的鍵盤彈出時，頁面不會變矮；Safari 是把「看得到的範圍」（visualViewport）往下移、變矮。
// 這裡把看得到的範圍寫成 --app-top、--app-h；手機的對話頁用 fixed 貼著它：鍵盤出來時，
// 頁首在可見範圍的最上面、輸入框貼著鍵盤（Android Chrome 靠 interactive-widget=resizes-content，offsetTop 一直是 0）。

export function watchViewport() {
  const vv = window.visualViewport
  if (!vv) return
  const root = document.documentElement
  let frame = 0
  const update = () => {
    cancelAnimationFrame(frame)
    frame = requestAnimationFrame(() => {
      root.style.setProperty('--app-h', `${Math.round(vv.height)}px`)
      root.style.setProperty('--app-top', `${Math.max(0, Math.round(vv.offsetTop))}px`)
    })
  }
  update()
  vv.addEventListener('resize', update)
  vv.addEventListener('scroll', update)
}
