import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { fakeApi, opError, type FakeApi } from '../test-api'
import { ToastProvider } from '../ui/Toast'
import { ComputerActions, NetworkAllow, blockedReason, useComputer, type ComputerView } from './ComputerActions'

function viewOf(over: Partial<ComputerView> = {}, state = 'running'): ComputerView {
  return {
    computer: { bot_id: 'b1', host_id: 'h1', container_id: 'c1', state, running: state === 'running', novnc_port: 0, hermes_port: 0 },
    bot_name: '小編',
    host_name: '這台主機',
    idle_minutes: 30,
    last_active_at: 1,
    sleeps_at: 0,
    busy: false,
    outdated: false,
    allow_private: [],
    home: false,
    moving: false,
    ...over,
  }
}

let api: FakeApi
let view: ComputerView
beforeEach(() => {
  view = viewOf()
  api = fakeApi({
    'computers.status': () => view,
    'computers.sleep': () => (view = viewOf({}, 'sleeping')),
    'computers.restart': () => view,
    'computers.rebuild': () => view,
    'computers.purge': () => (view = viewOf({}, 'none')),
    'computers.set_network': (i) => {
      const list = i.allow_private as string[]
      if (list.includes('8.8.8.8')) opError(400, 'invalid_input', '不是私有網路')
      return (view = viewOf({ allow_private: list }))
    },
  })
})
afterEach(() => vi.unstubAllGlobals())

function Both() {
  const st = useComputer('b1')
  return (
    <>
      <ComputerActions botId="b1" botName="小編" state={st} />
      <NetworkAllow botId="b1" botName="小編" state={st} />
    </>
  )
}
const wrap = () => render(<ToastProvider><Both /></ToastProvider>)
const calls = (p: string) => api.calls.filter((c) => c.path === p)

it('哪些動作現在不能做：工作中、搬家中、沒開機、還沒有電腦', () => {
  expect(blockedReason(viewOf({ busy: true }), 'restart')).toMatch(/正在工作/)
  expect(blockedReason(viewOf({ moving: true }), 'purge')).toMatch(/搬家/)
  expect(blockedReason(viewOf({}, 'sleeping'), 'sleep')).toBe('已經在休眠')
  expect(blockedReason(viewOf({}, 'sleeping'), 'rebuild')).toBeNull()
  expect(blockedReason(viewOf({}, 'none'), 'purge')).toBe('還沒有電腦')
  expect(blockedReason(viewOf(), 'sleep')).toBeNull()
})

it('休眠直接做；重新開機、重建先確認（E10 的文字）', async () => {
  wrap()
  fireEvent.click(await screen.findByRole('button', { name: '休眠' }))
  await waitFor(() => expect(calls('computers.sleep')).toHaveLength(1))
  expect(calls('computers.sleep')[0].input).toEqual({ bot_id: 'b1' })
  await waitFor(() => expect(screen.getByRole('button', { name: '休眠' })).toBeDisabled())

  fireEvent.click(screen.getByRole('button', { name: '重建' }))
  const dlg = await screen.findByRole('alertdialog', { name: '重建小編的電腦？' })
  expect(dlg.textContent).toContain('家目錄、工作資料夾、瀏覽器登入都保留')
  fireEvent.click(within(dlg).getByRole('button', { name: '重建' }))
  await waitFor(() => expect(calls('computers.rebuild')).toHaveLength(1))
})

it('重新開機的確認可以取消', async () => {
  wrap()
  fireEvent.click(await screen.findByRole('button', { name: '重新開機' }))
  const dlg = await screen.findByRole('alertdialog', { name: '重新開機小編的電腦？' })
  expect(dlg.textContent).toContain('正在做的工作會中斷，檔案和瀏覽器登入都會保留')
  fireEvent.click(within(dlg).getByRole('button', { name: '取消' }))
  await waitFor(() => expect(screen.queryByRole('alertdialog')).toBeNull())
  expect(calls('computers.restart')).toHaveLength(0)
})

it('刪除電腦資料是危險確認，確認後才呼叫 purge', async () => {
  wrap()
  fireEvent.click(await screen.findByRole('button', { name: '刪除電腦資料' }))
  const dlg = await screen.findByRole('alertdialog', { name: '刪除小編的電腦和裡面所有資料？' })
  expect(dlg.textContent).toContain('無法復原')
  expect(calls('computers.purge')).toHaveLength(0)
  fireEvent.click(within(dlg).getByRole('button', { name: '刪除電腦資料' }))
  await waitFor(() => expect(calls('computers.purge')).toHaveLength(1))
})

it('Bot 工作中：休眠、重開、重建、刪除都停用，說明為什麼', async () => {
  view = viewOf({ busy: true })
  wrap()
  expect(await screen.findByText(/小編正在工作：休眠、重新開機、重建要等它做完或先停止/)).toBeTruthy()
  for (const name of ['休眠', '重新開機', '重建', '刪除電腦資料']) expect(screen.getByRole('button', { name })).toBeDisabled()
})

it('私有網路例外：打開要確認；關掉直接做；不合法的位址顯示原因', async () => {
  wrap()
  const box = await screen.findByPlaceholderText(/192\.168\.1\.10/)
  fireEvent.change(box, { target: { value: '192.168.1.30:1234' } })
  fireEvent.click(screen.getByRole('button', { name: '加入例外' }))
  const dlg = await screen.findByRole('alertdialog', { name: '讓小編的電腦連到 192.168.1.30:1234？' })
  expect(calls('computers.set_network')).toHaveLength(0)
  fireEvent.click(within(dlg).getByRole('button', { name: '打開' }))
  await waitFor(() => expect(calls('computers.set_network')).toHaveLength(1))
  expect(calls('computers.set_network')[0].input).toEqual({ bot_id: 'b1', allow_private: ['192.168.1.30:1234'] })
  expect(await screen.findByRole('button', { name: '移除 192.168.1.30:1234' })).toBeTruthy()

  fireEvent.change(box, { target: { value: '8.8.8.8' } })
  fireEvent.click(screen.getByRole('button', { name: '加入例外' }))
  fireEvent.click(within(await screen.findByRole('alertdialog')).getByRole('button', { name: '打開' }))
  expect(await screen.findByRole('alert')).toHaveTextContent('不是私有網路')

  fireEvent.click(screen.getByRole('button', { name: '移除 192.168.1.30:1234' }))
  await waitFor(() => expect(calls('computers.set_network').at(-1)?.input).toEqual({ bot_id: 'b1', allow_private: [] }))
  await waitFor(() => expect(screen.queryByRole('button', { name: '移除 192.168.1.30:1234' })).toBeNull())
})
