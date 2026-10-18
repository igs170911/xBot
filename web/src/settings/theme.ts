import { createStore, useStore } from '../data/store'

// 主題（J01）：Midnight 深色是預設；Daylight 淺色；或跟隨系統。
// 只存在這個瀏覽器（localStorage），不用按儲存；淺色只覆寫顏色 token（index.css 的 [data-theme="light"]）。
export type ThemeChoice = 'dark' | 'light' | 'system'

const KEY = 'xbot.theme'

function read(): ThemeChoice {
  try {
    const v = window.localStorage.getItem(KEY)
    if (v === 'light' || v === 'system' || v === 'dark') return v
  } catch {
    // 無痕視窗或封鎖網站資料：用預設
  }
  return 'dark'
}

export const themeStore = createStore<ThemeChoice>('dark')

function systemLight(): boolean {
  return typeof window.matchMedia === 'function' && window.matchMedia('(prefers-color-scheme: light)').matches
}

/** 實際要用的顏色。 */
export function resolveTheme(choice: ThemeChoice): 'dark' | 'light' {
  if (choice === 'system') return systemLight() ? 'light' : 'dark'
  return choice
}

function apply(choice: ThemeChoice) {
  const root = document.documentElement
  if (resolveTheme(choice) === 'light') root.setAttribute('data-theme', 'light')
  else root.removeAttribute('data-theme')
}

export function setTheme(choice: ThemeChoice) {
  themeStore.set(choice)
  try {
    window.localStorage.setItem(KEY, choice)
  } catch {
    // 存不下來也照樣切換這一次
  }
  apply(choice)
}

let started = false

/** 開網頁時套用上次選的主題，並在「跟隨系統」時跟著系統切換。 */
export function initTheme() {
  const choice = read()
  themeStore.set(choice)
  apply(choice)
  if (started || typeof window.matchMedia !== 'function') return
  started = true
  window.matchMedia('(prefers-color-scheme: light)').addEventListener?.('change', () => {
    if (themeStore.get() === 'system') apply('system')
  })
}

export function useTheme(): ThemeChoice {
  return useStore(themeStore)
}
