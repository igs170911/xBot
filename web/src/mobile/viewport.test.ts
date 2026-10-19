import { afterEach, describe, expect, it, vi } from 'vitest'
import { watchViewport } from './viewport'

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
})
