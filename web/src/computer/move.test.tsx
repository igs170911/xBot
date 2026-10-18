import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { fakeApi, type FakeApi } from '../test-api'
import { ToastProvider } from '../ui/Toast'
import { dispatchEvent } from '../data/events'
import { hostsStore } from '../settings/hostsData'
import { ComputersSection } from '../settings/ComputersSection'
import { WhereSection } from './MoveParts'
import { clock, moveDetail, moveStep, movesStore, sizeText, type MoveJob } from './moveData'

const res = { cpus: 4, mem_bytes: 8 * 2 ** 30, disk_free_bytes: 61 * 2 ** 30, disk_total_bytes: 100 * 2 ** 30, docker_version: '27.3.1', os: 'Debian', arch: 'amd64', hostname: 'cloud-01', rtt_ms: 18, tailscale: true }
function host(over: Record<string, unknown>) {
  return { id: 'h', name: 'x', kind: 'ssh', address: 'x', status: 'online', arch: 'amd64', max_running: 3, running: 0, resources: res, last_seen_at: 0, control_center: false, default: false, bots: [], queued: 0, created_at: 1, ...over }
}
function job(over: Partial<MoveJob> = {}): MoveJob {
  return { id: 'm1', bot_id: 'b1', bot_name: '阿凱', from: 'local', from_name: '這台主機', to: 'c1', to_name: 'cloud-01', state: 'running', phase: 'copy', volume: 'workspace', percent: 47, done: 1.3 * 2 ** 30, total: 2.8 * 2 ** 30, files: 0, created_at: 1, ...over }
}

let api: FakeApi
let moves: { running: boolean; jobs: MoveJob[] }
let home: boolean
beforeEach(() => {
  hostsStore.set({ hosts: null, progress: {} })
  movesStore.set(null)
  moves = { running: false, jobs: [] }
  home = false
  const hosts = [
    host({ id: 'local', name: '這台電腦', kind: 'local', control_center: true, bots: [{ id: 'b1', name: '阿凱', color: 'green', shape: 'square', state: 'running', running: true }] }),
    host({ id: 'c1', name: 'cloud-01' }),
    host({ id: 'o1', name: 'office-pc', status: 'offline' }),
  ]
  api = fakeApi({
    'hosts.list': () => ({ hosts }),
    'computers.move_status': () => moves,
    'computers.move': (i) => {
      moves = { running: true, jobs: [job({ state: 'queued', phase: undefined, percent: 0, to: i.host_id as string })] }
      return moves
    },
    'computers.move_all': (i) => {
      moves = { running: true, jobs: [job({ state: 'queued', phase: undefined, to: i.to_host_id as string })] }
      return moves
    },
    'computers.move_cancel': () => moves,
    'computers.status': () => ({ home }),
    'computers.egress_status': () => ({ exit_node: 'home-pc', has_auth_key: true, shared_key: false, bots: home ? ['b1'] : [], hosts: [{ host_id: 'c1', present: true, running: true, backend: 'Running', exit_node: 'home-pc', exit_node_online: true }] }),
    'computers.set_egress': (i) => {
      home = i.home as boolean
      return { exit_node: 'home-pc', has_auth_key: true, shared_key: false, bots: [], hosts: [], rebuilt: true }
    },
  })
})
afterEach(() => vi.unstubAllGlobals())

const wrap = (el: React.ReactNode) => render(<ToastProvider>{el}</ToastProvider>)

it('搬家的步驟與說明文字（E10）', () => {
  expect(moveStep(job({ phase: 'stop' }))).toBe(0)
  expect(moveStep(job({ phase: 'measure' }))).toBe(1)
  expect(moveStep(job({ phase: 'copy' }))).toBe(2)
  expect(moveStep(job({ phase: 'verify' }))).toBe(3)
  expect(moveStep(job({ state: 'done' }))).toBe(4)
  expect(sizeText(1.3 * 2 ** 30, 2.8 * 2 ** 30)).toBe('1.3 / 2.8 GB')
  expect(sizeText(120 * 2 ** 20, 300 * 2 ** 20)).toBe('120 / 300 MB')
  expect(moveDetail(job())).toBe('傳 workspace · 1.3 / 2.8 GB')
  expect(moveDetail(job({ state: 'failed', error: '新主機上的資料和原本的不一樣' }))).toContain('留在 這台主機，原本的資料沒動')
  const now = new Date(2026, 9, 18, 22, 0).getTime()
  expect(clock(new Date(2026, 9, 19, 22, 54).getTime(), now)).toBe('明天 22:54')
  expect(moveDetail(job({ state: 'done', keep_until: Date.now() + 3600_000 }))).toMatch(/^已搬到 cloud-01；這台主機 上的舊資料保留到/)
})

it('在哪裡跑（E01）：切換主機＝搬家，確認後開始，進度卡可以取消；從家裡出去', async () => {
  wrap(<WhereSection botId="b1" botName="阿凱" hostId="local" />)
  const group = await screen.findByRole('radiogroup', { name: '在哪裡跑' })
  expect(within(group).getByRole('radio', { name: '這台主機' }).getAttribute('aria-checked')).toBe('true')
  expect(screen.getByText(/家裡 · 用你家裡的網路/)).toBeTruthy()

  fireEvent.click(within(group).getByRole('radio', { name: 'cloud-01（雲端）' }))
  const dlg = await screen.findByRole('dialog')
  expect(within(dlg).getByText('把阿凱的電腦搬到 cloud-01？')).toBeTruthy()
  expect(within(dlg).getByText(/登入都會跟著搬過去/)).toBeTruthy()
  fireEvent.click(within(dlg).getByRole('button', { name: '開始搬家' }))
  await waitFor(() => expect(api.calls.some((c) => c.path === 'computers.move' && c.input.host_id === 'c1' && c.input.bot_id === 'b1')).toBe(true))

  // 進度事件：卡片顯示 4 步、進度與取消。
  act(() => dispatchEvent({ id: 1, kind: 'computers.move', at: 1, payload: { running: true, jobs: [job()] } }))
  const card = await screen.findByTestId('move-card')
  expect(within(card).getByText('阿凱：這台主機 → cloud-01')).toBeTruthy()
  expect(within(card).getByText('登入狀態跟著走')).toBeTruthy()
  expect(within(card).getByText('傳到 cloud-01').closest('li')?.getAttribute('aria-current')).toBe('step')
  expect(within(card).getByRole('progressbar').getAttribute('aria-valuenow')).toBe('47')
  fireEvent.click(within(card).getByRole('button', { name: '取消搬家' }))
  await waitFor(() => expect(api.calls.some((c) => c.path === 'computers.move_cancel')).toBe(true))

  // 從家裡出去：打開要確認（填 exit node、說明存取控制）。
  const row = screen.getByTestId('egress-row')
  fireEvent.click(within(row).getByRole('switch', { name: '從家裡出去' }))
  const eg = await screen.findByRole('dialog')
  expect(within(eg).getByText(/autogroup:internet/)).toBeTruthy()
  expect((within(eg).getByLabelText('家裡那台電腦') as HTMLInputElement).value).toBe('home-pc')
  fireEvent.click(within(eg).getByRole('button', { name: '開啟' }))
  await waitFor(() => expect(api.calls.some((c) => c.path === 'computers.set_egress' && c.input.home === true)).toBe(true))
  await waitFor(() => expect(within(screen.getByTestId('egress-row')).getByRole('switch').getAttribute('aria-checked')).toBe('true'))
  // 關掉不用確認。
  fireEvent.click(within(screen.getByTestId('egress-row')).getByRole('switch'))
  await waitFor(() => expect(api.calls.some((c) => c.path === 'computers.set_egress' && c.input.home === false)).toBe(true))
})

it('主機清單「搬家」（J06、16.1a）：選要搬去的主機，全部搬過去，進度顯示在搬家區', async () => {
  wrap(<ComputersSection />)
  const rows = await screen.findAllByTestId('host-row')
  expect((within(rows[1]).getByRole('button', { name: '搬家' }) as HTMLButtonElement).disabled).toBe(true) // 上面沒有 Bot
  expect(within(rows[2]).queryByRole('button', { name: '搬家' })).toBeNull() // 離線：先重新連線
  fireEvent.click(within(rows[0]).getByRole('button', { name: '搬家' }))
  const dlg = await screen.findByRole('dialog')
  expect(within(dlg).getByText('把 這台電腦 上的 Bot 全部搬走')).toBeTruthy()
  expect(within(dlg).queryByRole('radio', { name: 'office-pc' })).toBeNull() // 離線的不能選
  expect(within(within(dlg).getByRole('list', { name: '要搬的 Bot' })).getByText('阿凱')).toBeTruthy()
  fireEvent.click(within(dlg).getByRole('button', { name: '全部搬到 cloud-01' }))
  await waitFor(() => expect(api.calls.some((c) => c.path === 'computers.move_all' && c.input.from_host_id === 'local' && c.input.to_host_id === 'c1')).toBe(true))
  expect(await screen.findByTestId('moves')).toBeTruthy()
  expect(screen.getByText(/從家裡出去/)).toBeTruthy()
})
