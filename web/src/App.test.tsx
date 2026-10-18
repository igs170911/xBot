import { act, fireEvent, render, screen, within } from '@testing-library/react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import App from './App'
import { navigate } from './router'

beforeEach(() => {
  vi.stubGlobal('fetch', async () => new Response(JSON.stringify({ ok: true, version: 'v-test' })))
  window.history.replaceState(null, '', '/')
})
afterEach(() => vi.unstubAllGlobals())

it('首頁是待我處理，並顯示服務版本', async () => {
  render(<App />)
  expect(screen.getByRole('heading', { level: 1, name: '早安，有 5 件事等你' })).toBeInTheDocument()
  expect(await screen.findByText('服務正常 · v-test')).toBeInTheDocument()
  const nav = screen.getByRole('navigation', { name: '側欄' })
  expect(within(nav).getByRole('link', { name: /待我處理/ })).toHaveAttribute('aria-current', 'page')
})

it('核定卡就地處理：件數減 1，可以復原', async () => {
  render(<App />)
  fireEvent.click(screen.getByRole('button', { name: '允許這次' }))
  expect(screen.getByRole('heading', { level: 1, name: '早安，有 4 件事等你' })).toBeInTheDocument()
  fireEvent.click(screen.getByRole('button', { name: '復原' }))
  expect(screen.getByRole('heading', { level: 1, name: '早安，有 5 件事等你' })).toBeInTheDocument()
  await screen.findByText('服務正常 · v-test')
})

it('側欄連結用前端路由換頁', async () => {
  render(<App />)
  const nav = screen.getByRole('navigation', { name: '側欄' })
  fireEvent.click(within(nav).getByRole('link', { name: /辦公室/ }))
  expect(window.location.pathname).toBe('/office')
  expect(screen.getByRole('heading', { level: 1, name: '辦公室' })).toBeInTheDocument()
  act(() => navigate('/'))
  expect(screen.getByRole('heading', { level: 1, name: '早安，有 5 件事等你' })).toBeInTheDocument()
  await screen.findByText('服務正常 · v-test')
})

it('手機抽屜：打開後焦點在關閉鈕，Esc 關掉並回到選單鈕', async () => {
  render(<App />)
  const opener = screen.getByRole('button', { name: '打開側欄' })
  opener.focus()
  fireEvent.click(opener)
  const drawer = screen.getByRole('dialog', { name: '側欄' })
  const close = within(drawer).getByRole('button', { name: '關閉側欄' })
  expect(close).toHaveFocus()
  fireEvent.keyDown(close, { key: 'Escape' })
  expect(screen.queryByRole('dialog', { name: '側欄' })).toBeNull()
  expect(opener).toHaveFocus()
  await screen.findByText('服務正常 · v-test')
})

it('/dev/components 列出元件與頭像矩陣（7 列 × 10 形狀）', () => {
  window.history.replaceState(null, '', '/dev/components')
  render(<App />)
  expect(screen.getByRole('heading', { level: 1, name: '元件（Grok 風格 · Midnight）' })).toBeInTheDocument()
  const table = screen.getByRole('table', { name: '頭像形狀、眼睛與顏色' })
  expect(within(table).getAllByRole('row')).toHaveLength(8)
  expect(table.querySelectorAll('[data-shape]')).toHaveLength(70)
})
