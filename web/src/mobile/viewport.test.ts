import { afterEach, describe, expect, it, vi } from 'vitest'
import { measure, watchViewport } from './viewport'

// 假的 visualViewport：可以改高度、觸發 resize。
function fakeViewport() {
  const listeners: (() => void)[] = []
  const vv = { height: window.innerHeight, offsetTop: 0, addEventListener: (_: string, f: () => void) => listeners.push(f) }
  Object.defineProperty(window, 'visualViewport', { value: vv, configurable: true })
  return {
    set(height: number, offsetTop = 0) {
      vv.height = height
      vv.offsetTop = offsetTop
      listeners.forEach((f) => f())
    },
  }
}

const frame = () => new Promise((r) => requestAnimationFrame(() => r(null)))

describe('watchViewport', () => {
  afterEach(() => {
    document.documentElement.removeAttribute('style')
    document.documentElement.classList.remove('standalone')
    delete (navigator as Navigator & { standalone?: boolean }).standalone
    vi.restoreAllMocks()
  })

  it('鍵盤開著時寫入可見範圍，收起來就拿掉', async () => {
    const vv = fakeViewport()
    watchViewport()
    vv.set(window.innerHeight - 300, 120)
    await frame()
    expect(document.documentElement.style.getPropertyValue('--app-h')).toBe(`${window.innerHeight - 300}px`)
    expect(document.documentElement.style.getPropertyValue('--app-top')).toBe('120px')
    vv.set(window.innerHeight)
    await frame()
    expect(document.documentElement.style.getPropertyValue('--app-h')).toBe('')
  })

  it('鍵盤收起來時，整頁鎖住的頁面（對話）放回原位；可以捲的頁面不動', async () => {
    const to = vi.spyOn(window, 'scrollTo').mockImplementation(() => {})
    Object.defineProperty(window, 'scrollY', { value: 200, configurable: true })
    const vv = fakeViewport()
    watchViewport()

    vv.set(window.innerHeight - 300)
    await frame()
    vv.set(window.innerHeight)
    await frame()
    expect(to).not.toHaveBeenCalled() // 沒鎖住：使用者自己捲到的位置不能動

    document.documentElement.style.overflow = 'hidden'
    vv.set(window.innerHeight - 300)
    await frame()
    vv.set(window.innerHeight)
    await frame()
    expect(to).toHaveBeenCalledWith(0, 0)
  })

  it('主畫面 App：鍵盤彈出時整頁也變矮，改拿整個螢幕（lvh）比，看得出鍵盤；剛打開少算狀態列不算鍵盤', async () => {
    Object.defineProperty(navigator, 'standalone', { value: true, configurable: true })
    vi.spyOn(measure, 'lvh').mockReturnValue(852)
    const inner = vi.spyOn(window, 'innerHeight', 'get')
    const vv = fakeViewport()
    inner.mockReturnValue(793) // 剛打開：innerHeight、可見範圍都少算狀態列
    watchViewport()
    expect(document.documentElement.classList.contains('standalone')).toBe(true)
    vv.set(793)
    await frame()
    expect(document.documentElement.style.getPropertyValue('--app-h')).toBe('')
    inner.mockReturnValue(450) // 鍵盤彈出：innerHeight 跟著變矮
    vv.set(450)
    await frame()
    expect(document.documentElement.style.getPropertyValue('--app-h')).toBe('450px')
    inner.mockReturnValue(852)
    vv.set(852)
    await frame()
    expect(document.documentElement.style.getPropertyValue('--app-h')).toBe('')
  })

  it('一般瀏覽器：不加 standalone，照舊拿 innerHeight 比', async () => {
    const vv = fakeViewport()
    watchViewport()
    expect(document.documentElement.classList.contains('standalone')).toBe(false)
    vv.set(window.innerHeight - 100) // 網址列之類的差距：不算鍵盤
    await frame()
    expect(document.documentElement.style.getPropertyValue('--app-h')).toBe('')
  })
})
