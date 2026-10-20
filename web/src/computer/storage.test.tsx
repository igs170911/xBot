import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { fakeApi, opError, type FakeApi } from '../test-api'
import { ToastProvider } from '../ui/Toast'
import { ComputerStorage, type Usage } from './ComputerStorage'

const MB = 1024 * 1024
const usage = (over: Partial<Usage> = {}): Usage => {
  const u = { workspace: 120 * MB, uploads: 30 * MB, caches: 260 * MB, browser: 40 * MB, tools: 0, other: 10 * MB, ...over }
  return { ...u, total: u.workspace + u.uploads + u.caches + u.browser + u.tools + u.other }
}

let api: FakeApi
let current: Usage
beforeEach(() => {
  current = usage()
  api = fakeApi({
    'computers.storage': () => ({ bot_id: 'b1', has_computer: true, usage: current }),
    'computers.clean': () => {
      const freed = current.caches
      current = usage({ caches: 0 })
      return { bot_id: 'b1', has_computer: true, result: { scratch: 20 * MB, uploads: 0, browser: freed - 20 * MB, freed }, usage: current }
    },
  })
})
afterEach(() => vi.unstubAllGlobals())

const wrap = (working = false) => render(<ToastProvider><ComputerStorage botId="b1" botName="小編" working={working} /></ToastProvider>)
const calls = (p: string) => api.calls.filter((c) => c.path === p)

it('看空間花在哪裡：總量、每一類、哪些會自動清', async () => {
  wrap()
  const box = screen.getByTestId('computer-storage')
  expect(await within(box).findByText('共 460 MB')).toBeInTheDocument()
  expect(calls('computers.storage')[0].input).toEqual({ bot_id: 'b1' })
  const rows = within(box).getAllByRole('listitem').map((li) => li.textContent)
  expect(rows).toEqual([
    '工作成果不會自動刪除120 MB',
    '你傳的附件30 天後自動刪除30 MB',
    '快取與暫存每天自動清理260 MB',
    '瀏覽器登入資料40 MB',
    '它裝的工具0 B',
    '其他10 MB',
  ])
  expect(within(box).getByText('工作成果、附件和登入不會被清掉。')).toBeInTheDocument()
})

it('清理快取：顯示清出多少，用量跟著更新', async () => {
  wrap()
  fireEvent.click(await screen.findByRole('button', { name: '清理快取' }))
  await waitFor(() => expect(calls('computers.clean')).toHaveLength(1))
  expect(calls('computers.clean')[0].input).toEqual({ bot_id: 'b1' })
  expect(await screen.findByText('清出 260 MB')).toBeInTheDocument()
  expect(await screen.findByText('共 200 MB')).toBeInTheDocument()

  fireEvent.click(screen.getByRole('button', { name: '清理快取' }))
  expect(await screen.findByText('目前沒有可以清的快取')).toBeInTheDocument()
})

it('Bot 正在工作時不能清；伺服器說在工作也只提示', async () => {
  const view = wrap(true)
  const btn = await screen.findByRole('button', { name: '清理快取' })
  expect(btn).toBeDisabled()
  expect(btn).toHaveAttribute('title', '小編正在工作，等它做完再清理')
  view.unmount()

  api.ops['computers.clean'] = () => opError(409, 'conflict', '這個 Bot 正在工作，等它做完再清理')
  wrap()
  fireEvent.click(await screen.findByRole('button', { name: '清理快取' }))
  expect(await screen.findByText('這個 Bot 正在工作，等它做完再清理')).toBeInTheDocument()
  expect(screen.queryByRole('alert')).toBeNull()
})

it('還沒有電腦：不顯示分類和清理', async () => {
  api.ops['computers.storage'] = () => ({ bot_id: 'b1', has_computer: false, usage: usage({ workspace: 0, uploads: 0, caches: 0, browser: 0, other: 0 }) })
  wrap()
  expect(await screen.findByText('還沒有電腦')).toBeInTheDocument()
  expect(screen.queryByRole('button', { name: '清理快取' })).toBeNull()
  expect(screen.queryAllByRole('listitem')).toHaveLength(0)
})

it('量不到時顯示原因', async () => {
  api.ops['computers.storage'] = () => opError(409, 'conflict', '這個 Bot 的電腦所在的主機連不上：cloud-01')
  wrap()
  expect(await screen.findByRole('alert')).toHaveTextContent('主機連不上')
})
