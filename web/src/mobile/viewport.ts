// iPhone 的鍵盤彈出時，100dvh 不會變小（Android Chrome 靠 interactive-widget=resizes-content 會）。
// 這裡把「真正看得到的高度」寫成 --app-h，對話頁用它當高度：鍵盤出來時輸入框貼在鍵盤上面、頁首不會被推出畫面。

export function watchViewport() {
  const vv = window.visualViewport
  if (!vv) return
  const root = document.documentElement
  const update = () => {
    root.style.setProperty('--app-h', `${Math.round(vv.height)}px`)
    // iPhone 會把整頁往上推讓輸入框露出來；對話頁的高度已經跟著縮，推回頂端就好（長頁面不動）。
    if (vv.height < window.innerHeight - 1 && root.scrollHeight <= vv.height + 1 && window.scrollY > 0) window.scrollTo(0, 0)
  }
  update()
  vv.addEventListener('resize', update)
  vv.addEventListener('scroll', update)
}
