import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { fakeApi, type FakeApi } from '../test-api'
import { newBotDialog } from '../data/ui'
import { NewBotDialog } from './NewBotDialog'

// 新增 Bot（W02）的「幫我想」（X05）：改了說明，之前想好的職稱與常駐指示不能在建立時偷偷用上。

let api: FakeApi
beforeEach(() => {
  api = fakeApi({
    'bots.suggest': (i) => ({ name: '新聞小編', title: '新聞編輯', description: String(i.text), soul: '每天早上整理新聞重點', by: 'model', p: 0.87 }),
    'bots.create': (i) => ({ id: 'b9', title: '', description: '', soul: '', engine: 'hermes', model: '', approval_mode: 'ask', archived: false, created_at: 1, updated_at: 1, ...i }),
  })
  newBotDialog.set(true)
})
afterEach(() => {
  newBotDialog.set(false)
  vi.unstubAllGlobals()
})

const created = () => api.calls.find((c) => c.path === 'bots.create')?.input

it('幫我想：寫「系統建議」，不顯示百分比；沒改說明就照建議建立', async () => {
  render(<NewBotDialog />)
  fireEvent.change(screen.getByLabelText('它負責什麼（一句話）'), { target: { value: '每天早上整理新聞給我' } })
  fireEvent.click(screen.getByRole('button', { name: '幫我想名字和職稱' }))
  const hint = await screen.findByTestId('bot-suggest')
  expect(hint).toHaveTextContent('系統建議：職稱 新聞編輯')
  expect(hint.textContent).not.toMatch(/%|系統判斷/)
  expect(screen.getByLabelText('名字')).toHaveValue('新聞小編')
  fireEvent.click(screen.getByRole('button', { name: '建立「新聞小編」' }))
  await waitFor(() => expect(created()).toBeTruthy())
  expect(created()).toMatchObject({ name: '新聞小編', description: '每天早上整理新聞給我', title: '新聞編輯', soul: '每天早上整理新聞重點' })
})

it('想好之後改了說明：建議拿掉，建立時不帶舊的職稱與常駐指示；再想一次會換掉自動填的名字', async () => {
  render(<NewBotDialog />)
  const desc = screen.getByLabelText('它負責什麼（一句話）')
  fireEvent.change(desc, { target: { value: '每天早上整理新聞給我' } })
  fireEvent.click(screen.getByRole('button', { name: '幫我想名字和職稱' }))
  await screen.findByTestId('bot-suggest')
  fireEvent.change(desc, { target: { value: '幫我回覆客服信' } })
  expect(screen.queryByTestId('bot-suggest')).toBeNull()
  fireEvent.click(screen.getByRole('button', { name: '建立「新聞小編」' }))
  await waitFor(() => expect(created()).toBeTruthy())
  expect(created()).toMatchObject({ name: '新聞小編', description: '幫我回覆客服信' })
  expect(created()?.title).toBeUndefined()
  expect(created()?.soul).toBeUndefined()
})

it('想的時候又改了說明：回來的建議丟掉', async () => {
  render(<NewBotDialog />)
  const desc = screen.getByLabelText('它負責什麼（一句話）')
  fireEvent.change(desc, { target: { value: '每天早上整理新聞給我' } })
  fireEvent.click(screen.getByRole('button', { name: '幫我想名字和職稱' }))
  fireEvent.change(desc, { target: { value: '幫我回覆客服信' } })
  await waitFor(() => expect(screen.getByRole('button', { name: '幫我想名字和職稱' })).toBeEnabled())
  expect(screen.queryByTestId('bot-suggest')).toBeNull()
  expect(screen.getByLabelText('名字')).toHaveValue('')
})
