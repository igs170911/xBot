import { afterEach, describe, expect, it } from 'vitest'
import { closeRightPanel, openFileHere, registerFileOpener, rightPanelStore, saveWidth, SIDEBAR, toggleRightPanel } from './panes'

describe('右邊面板與欄寬（2026-10-20）', () => {
  afterEach(() => {
    closeRightPanel()
    window.localStorage.clear()
  })

  it('電腦、檔案一次只開一個；同一顆鈕再按就關', () => {
    toggleRightPanel('computer')
    expect(rightPanelStore.get().kind).toBe('computer')
    toggleRightPanel('files')
    expect(rightPanelStore.get().kind).toBe('files')
    toggleRightPanel('files')
    expect(rightPanelStore.get().kind).toBeNull()
  })

  it('檔案連結：對話頁登記了就在旁邊打開那個 Bot 的檔案；別的 Bot、沒登記就照常換頁', () => {
    const opened: string[] = []
    const off = registerFileOpener((id, path) => (id === 'b1' ? (opened.push(path), true) : false))
    expect(openFileHere('/bots/b1/files?path=%2Fworkspace%2F%E6%AF%94%E5%83%B9.xlsx')).toBe(true)
    expect(opened).toEqual(['/workspace/比價.xlsx'])
    expect(openFileHere('/bots/b2/files?path=%2Fworkspace%2Fa.txt')).toBe(false)
    expect(openFileHere('/bots/b1/computer')).toBe(false)
    off()
    expect(openFileHere('/bots/b1/files?path=%2Fworkspace%2Fa.txt')).toBe(false)
  })

  it('寬度記在這個瀏覽器；回到預設就不記', () => {
    saveWidth(SIDEBAR, 320)
    expect(window.localStorage.getItem(SIDEBAR.key)).toBe('320')
    saveWidth(SIDEBAR, SIDEBAR.def)
    expect(window.localStorage.getItem(SIDEBAR.key)).toBeNull()
  })
})
