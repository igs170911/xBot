import { afterEach, describe, expect, it, vi } from 'vitest'
import { installRovingKeys } from './rovingKeys'

function group(role: 'radio' | 'tab') {
  const g = document.createElement('div')
  g.setAttribute('role', role === 'radio' ? 'radiogroup' : 'tablist')
  const clicks: string[] = []
  for (const k of ['a', 'b', 'c']) {
    const b = document.createElement('button')
    b.setAttribute('role', role)
    b.textContent = k
    b.onclick = () => clicks.push(k)
    // jsdom 沒有版面：當成看得到
    b.getClientRects = () => [{}] as unknown as DOMRectList
    g.append(b)
  }
  document.body.append(g)
  return { buttons: [...g.querySelectorAll('button')], clicks }
}

const key = (el: Element, k: string) => el.dispatchEvent(new KeyboardEvent('keydown', { key: k, bubbles: true, cancelable: true }))

afterEach(() => {
  document.body.innerHTML = ''
})

describe('installRovingKeys（v1.0：自己畫的單選組、分頁也能用方向鍵）', () => {
  installRovingKeys()

  it('←→ 在單選組裡移動並選取，頭尾相接；Home／End', () => {
    const { buttons, clicks } = group('radio')
    buttons[0].focus()
    key(buttons[0], 'ArrowRight')
    expect(document.activeElement).toBe(buttons[1])
    key(buttons[1], 'ArrowDown')
    key(buttons[2], 'ArrowRight')
    expect(document.activeElement).toBe(buttons[0])
    key(buttons[0], 'End')
    key(buttons[2], 'Home')
    expect(clicks).toEqual(['b', 'c', 'a', 'c', 'a'])
  })

  it('分頁也一樣；元件自己處理過（preventDefault）就不動', () => {
    const { buttons, clicks } = group('tab')
    buttons[1].focus()
    key(buttons[1], 'ArrowLeft')
    expect(document.activeElement).toBe(buttons[0])
    const spy = vi.fn((e: Event) => e.preventDefault())
    buttons[0].addEventListener('keydown', spy)
    key(buttons[0], 'ArrowRight')
    expect(spy).toHaveBeenCalled()
    expect(document.activeElement).toBe(buttons[0])
    expect(clicks).toEqual(['a'])
  })
})
