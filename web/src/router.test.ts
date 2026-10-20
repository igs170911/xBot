import { afterEach, describe, expect, it, vi } from 'vitest'
import { backTo, navigate } from './router'

afterEach(() => vi.restoreAllMocks())

describe('backTo', () => {
  it('上一頁就是對話列表：用瀏覽器的上一頁', () => {
    navigate('/chats')
    navigate('/bots/b1/threads/t1')
    const back = vi.spyOn(window.history, 'back').mockImplementation(() => {})
    backTo('/chats')
    expect(back).toHaveBeenCalledOnce()
  })

  it('換過對話串（上一頁是另一條對話串）：直接換成對話列表，不是一頁一頁往回走', () => {
    navigate('/chats')
    navigate('/bots/b1/threads/t1')
    navigate('/bots/b1/threads/t2')
    const back = vi.spyOn(window.history, 'back').mockImplementation(() => {})
    backTo('/chats')
    expect(back).not.toHaveBeenCalled()
    expect(window.location.pathname).toBe('/chats')
  })

  it('直接打開（從通知點進來）：換成對話列表', () => {
    window.history.replaceState(null, '', '/bots/b1/threads/t1')
    const back = vi.spyOn(window.history, 'back').mockImplementation(() => {})
    backTo('/chats')
    expect(back).not.toHaveBeenCalled()
    expect(window.location.pathname).toBe('/chats')
  })
})
