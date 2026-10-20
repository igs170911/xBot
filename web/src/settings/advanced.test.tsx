import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { fakeApi, type FakeApi } from '../test-api'
import { ToastProvider } from '../ui/Toast'
import { dispatchEvent } from '../data/events'
import { botsStore } from '../data/bots'
import { BackupPage } from './BackupPage'
import { PeoplePage, auditInput, isChange, opText } from './PeoplePage'
import { UpdateComputers, planSummary, type UpdatePlan } from './UpdateComputers'
import { AccessTab } from './bot/AccessTab'

let api: FakeApi
afterEach(() => vi.unstubAllGlobals())
const wrap = (el: React.ReactNode) => render(<ToastProvider>{el}</ToastProvider>)
const calls = (p: string) => api.calls.filter((c) => c.path === p)

// ── 備份與更新（J07）────────────────────────────────

it('備份與更新：版本、最近的備份、立即備份（背景做、做完說結果）、還原指令', async () => {
  let backups = [{ name: 'xbot-backup-20261018-030000.tar.gz', path: '/d/xbot-backups/xbot-backup-20261018-030000.tar.gz', size: 2 * 1024 * 1024, created_at: new Date(2026, 9, 18, 3, 0).getTime() }]
  let job: Record<string, unknown> | undefined
  const f = { name: 'xbot-backup-20261019-090000.tar.gz', path: '/d/xbot-backups/xbot-backup-20261019-090000.tar.gz', size: 0, created_at: 0 }
  api = fakeApi({
    'backup.list': () => ({ dir: '/d/xbot-backups', data_dir: '/d/xbot', backups, job }),
    'backup.create': () => (job = { running: true, started_at: 1, file: f }),
  })
  wrap(<BackupPage />)
  expect(await screen.findByText('/d/xbot')).toBeInTheDocument()
  expect(await screen.findByText('XBot v-test')).toBeInTheDocument()
  expect(screen.getAllByTestId('backup-row')).toHaveLength(1)
  expect(screen.getByText('make restore FILE=/d/xbot-backups/xbot-backup-20261018-030000.tar.gz')).toBeInTheDocument()
  expect(screen.getByText('make update')).toBeInTheDocument()

  vi.useFakeTimers({ shouldAdvanceTime: true })
  fireEvent.click(screen.getByRole('button', { name: '立即備份' }))
  expect(await screen.findByText(/正在備份資料目錄和每個 Bot 電腦的資料/)).toBeInTheDocument()
  expect(calls('backup.create')[0].input).toEqual({})
  const done = { ...f, size: 3 * 1024 * 1024, created_at: new Date(2026, 9, 19, 9, 0).getTime() }
  backups = [done, ...backups]
  job = { running: false, started_at: 1, finished_at: 2, file: done, files: 42, computers: 2, skipped: [{ bot_name: '阿哲', reason: 'cloud-01 連不上' }] }
  await act(async () => {
    await vi.advanceTimersByTimeAsync(3100)
  })
  vi.useRealTimers()
  expect(await screen.findByText(/已備份 42 個檔案和 2 台 Bot 電腦的資料（3\.0 MB）/)).toBeInTheDocument()
  await waitFor(() => expect(screen.getAllByTestId('backup-row')).toHaveLength(2))
  expect(screen.getByText('有 Bot 電腦的資料沒有備到')).toBeInTheDocument()
  expect(screen.getByText(/阿哲（cloud-01 連不上）/)).toBeInTheDocument()
  expect(screen.getByText('make restore FILE=/d/xbot-backups/xbot-backup-20261019-090000.tar.gz')).toBeInTheDocument()
})

it('備份與更新：還沒有備份時說會放在哪', async () => {
  api = fakeApi({ 'backup.list': () => ({ dir: '/d/xbot-backups', data_dir: '/d/xbot', backups: [] }) })
  wrap(<BackupPage />)
  expect(await screen.findByText('還沒有備份')).toBeInTheDocument()
  expect(screen.getByText('備份會放在 /d/xbot-backups')).toBeInTheDocument()
  expect(screen.getByText('make restore FILE=<備份檔>')).toBeInTheDocument()
})

// ── 成員與紀錄（J08）────────────────────────────────

it('紀錄的篩選 → audit.list 的輸入', () => {
  const now = new Date(2026, 9, 19, 15, 30).getTime()
  expect(auditInput({ actor: '', domain: 'all', status: '', days: 'all' }, now)).toEqual({ limit: 50 })
  // 只看變更要在畫面這邊濾，一次多讀一點
  expect(auditInput({ actor: '', domain: 'changes', status: '', days: 'all' }, now)).toEqual({ limit: 200 })
  expect(auditInput({ actor: 'agent:a1', domain: 'bots', status: 'error', days: '1' }, now)).toEqual({
    limit: 200,
    actor: 'agent:a1',
    op: 'xbot.bots',
    status: 'error',
    since: new Date(2026, 9, 19).getTime(),
  })
  expect(auditInput({ actor: '', domain: 'changes', status: '', days: '7' }, now).since).toBe(new Date(2026, 9, 13).getTime())
  expect(opText('xbot.bots.create')).toBe('建立一個 Bot')
  expect(opText('xbot.unknown.op')).toBe('xbot.unknown.op')
})

it('只看變更：查看類與例行操作（標已讀、擷取縮圖、列出代理）不算', () => {
  expect(isChange('xbot.bots.update')).toBe(true)
  expect(isChange('xbot.approvals.decide')).toBe(true)
  expect(isChange('xbot.bots.list')).toBe(false)
  expect(isChange('xbot.inbox.mark_read')).toBe(false)
  expect(isChange('xbot.computers.snapshot')).toBe(false)
  expect(isChange('xbot.agents.list')).toBe(false)
  // 認不得的操作照樣列出來，不要藏起來
  expect(isChange('xbot.unknown.op')).toBe(true)
})

it('成員與紀錄：你、連線的代理、預設只看變更、紀錄依誰與種類篩選、載入更早的', async () => {
  botsStore.set({ bots: [{ id: 'b1', name: '小秘', shape: 'circle', color: 'blue' }] as never, activity: {} })
  const entry = (id: number, actor: string, op: string, status = 'ok') => ({ id, actor, via: 'mcp', op, request_id: '', target: 'b1', params: {}, status, error_code: status === 'error' ? 'forbidden' : '', created_at: new Date(2026, 9, 19, 9, id).getTime() })
  api = fakeApi({
    'agents.list': () => ({ agents: [{ id: 'a1', name: 'Claude Code', status: 'active' }] }),
    'audit.list': (i) => {
      if (i.before) return { entries: [entry(1, 'owner', 'xbot.bots.update')] }
      if (i.actor === 'agent:a1') return { entries: [entry(5, 'agent:a1', 'xbot.bots.delete', 'error')] }
      return { entries: [entry(4, 'owner', 'xbot.inbox.mark_read'), entry(3, 'bot:b1', 'xbot.collab.message_bot'), entry(2, 'agent:a1', 'xbot.bots.create')], next_before: 2 }
    },
  })
  wrap(<PeoplePage />)
  expect(await screen.findByText('你（擁有者）')).toBeInTheDocument()
  expect(await screen.findByText('已連線的代理 · 1')).toBeInTheDocument()
  expect(screen.getByTestId('member-agents')).toHaveAttribute('href', '/settings/advanced/agents')

  // 預設只看變更：「把通知標為已讀」不列；不夠一頁就自己再往前讀
  await waitFor(() => expect(screen.getAllByTestId('audit-row')).toHaveLength(3))
  const rows = screen.getAllByTestId('audit-row')
  expect(rows[0]).toHaveTextContent('小秘')
  expect(rows[1]).toHaveTextContent('Claude Code')
  expect(rows[1]).toHaveTextContent('建立一個 Bot')
  expect(screen.queryByText('把通知標為已讀')).toBeNull()
  expect(calls('audit.list')[0].input).toMatchObject({ limit: 200 })
  expect(calls('audit.list')[0].input.since).toBeGreaterThan(0)
  expect(calls('audit.list')[1].input.before).toBe(2)

  // 全部，含查看：一頁一頁讀，按「載入更早的紀錄」才往前
  fireEvent.click(screen.getByRole('button', { name: /只看變更/ }))
  fireEvent.click(await screen.findByRole('option', { name: '全部，含查看' }))
  await waitFor(() => expect(screen.getAllByTestId('audit-row')).toHaveLength(3))
  expect(screen.getByText('把通知標為已讀')).toBeInTheDocument()
  expect(calls('audit.list').at(-1)?.input).toMatchObject({ limit: 50 })
  fireEvent.click(screen.getByRole('button', { name: '載入更早的紀錄' }))
  await waitFor(() => expect(screen.getAllByTestId('audit-row')).toHaveLength(4))
  expect(calls('audit.list').at(-1)?.input.before).toBe(2)

  fireEvent.click(screen.getByRole('button', { name: /所有人與 Bot/ }))
  fireEvent.click(await screen.findByRole('option', { name: 'Claude Code（代理）' }))
  await waitFor(() => expect(screen.getAllByTestId('audit-row')).toHaveLength(1))
  expect(screen.getByTestId('audit-row')).toHaveTextContent('被拒：權限不足')
  expect(calls('audit.list').at(-1)?.input.actor).toBe('agent:a1')
})

// ── 更新電腦（B13 標記 → xbot.computers.update）──────────

const PLAN: UpdatePlan = {
  running: false,
  total: 2,
  done: 0,
  hosts: [
    { host_id: 'local', name: '這台主機', image_outdated: false, state: 'pending', bots: [{ bot_id: 'b1', name: '小秘', state: 'pending' }] },
    { host_id: 'h2', name: 'cloud-01', image_outdated: true, state: 'pending', bots: [{ bot_id: 'b2', name: '阿哲', state: 'pending' }] },
    { host_id: 'h3', name: 'office-pc', image_outdated: true, state: 'skipped', error: '主機不在線上', bots: [] },
  ],
}

it('更新電腦：計畫的摘要（離線跳過的主機不算）', () => {
  expect(planSummary(PLAN)).toEqual({ bots: 2, images: 1, names: ['小秘', '阿哲'] })
})

it('更新電腦：有要更新的才出現；確認後在背景更新，進度看事件', async () => {
  let n = 0
  api = fakeApi({
    'computers.update': () => {
      n++
      return n === 1 ? PLAN : { ...PLAN, running: true, started_at: 1 }
    },
  })
  wrap(<UpdateComputers />)
  expect(await screen.findByTestId('update-needed')).toHaveTextContent('2 台電腦要更新，1 台主機的電腦映像過期')
  fireEvent.click(screen.getByRole('button', { name: '更新電腦' }))
  const dlg = await screen.findByRole('alertdialog', { name: '更新電腦？' })
  expect(dlg).toHaveTextContent('會重建：小秘、阿哲')
  fireEvent.click(within(dlg).getByRole('button', { name: '開始更新' }))
  expect(await screen.findByTestId('update-progress')).toHaveTextContent('正在更新電腦 0/2')
  act(() => dispatchEvent({ kind: 'computers.update', at: 1, payload: { ...PLAN, running: true, done: 1, current: '阿哲' } }))
  expect(screen.getByTestId('update-progress')).toHaveTextContent('正在更新電腦 1/2 · 正在處理阿哲')
  api.ops['computers.update'] = () => ({ running: false, total: 0, done: 0, hosts: [] })
  act(() => dispatchEvent({ kind: 'computers.update', at: 2, payload: { ...PLAN, running: false, done: 2, hosts: [{ ...PLAN.hosts[0], bots: [{ bot_id: 'b1', name: '小秘', state: 'skipped', reason: 'Bot 正在工作' }] }] } }))
  expect(await screen.findByTestId('update-done')).toHaveTextContent('小秘（Bot 正在工作）沒有更新')
})

it('更新電腦：都是最新的時不顯示', async () => {
  api = fakeApi({ 'computers.update': () => ({ running: false, total: 0, done: 0, hosts: [{ host_id: 'local', name: '這台主機', image_outdated: false, state: 'up_to_date', bots: [] }] }) })
  const { container } = wrap(<UpdateComputers />)
  await waitFor(() => expect(calls('computers.update')).toHaveLength(1))
  expect(container.textContent).toBe('')
})

// ── Bot 設定 › 應用程式與電腦：在哪裡跑 ────────────────────

describe('Bot 設定 › 應用程式與電腦', () => {
  const BOT = { id: 'b1', name: '小秘', title: '', description: '', soul: '', color: 'blue', shape: 'cursor', eyes: '', engine: 'hermes', model: '', fallback: '', effort: '', approval_mode: 'ask', memory_enabled: true, archived: false, primary: true, host_id: '', created_at: 1, updated_at: 1 }
  const HOSTS = [
    { id: 'local', name: '這台主機', kind: 'local', status: 'online', control_center: true, bots: [], running: 0, max_running: 4 },
    { id: 'h2', name: 'cloud-01', kind: 'ssh', status: 'online', control_center: false, bots: [], running: 0, max_running: 4 },
  ]
  const status = (state: string, host = 'local') => ({
    computer: { bot_id: 'b1', host_id: host, container_id: state === 'none' ? '' : 'c1', state, running: state === 'running', novnc_port: 0, hermes_port: 0 },
    bot_name: '小秘', host_name: '', idle_minutes: 30, last_active_at: 0, sleeps_at: 0, busy: false, outdated: false, allow_private: [], home: false, moving: false,
  })
  let box = 'running'
  beforeEach(() => {
    box = 'running'
    api = fakeApi({
      'apps.list': () => ({ connections: [] }),
      'hosts.list': () => ({ hosts: HOSTS }),
      'computers.status': () => status(box),
      'computers.storage': () => ({ bot_id: 'b1', has_computer: box !== 'none', usage: { workspace: 0, uploads: 0, caches: 0, browser: 0, tools: 0, other: 0, total: 0 } }),
      'computers.move_status': () => ({ jobs: [] }),
      'computers.move': () => ({ jobs: [] }),
      'bots.update': (i) => ({ ...BOT, ...i }),
    })
  })
  const access = () => wrap(<AccessTab bot={BOT as never} onSaved={() => {}} />)

  it('在哪裡跑：已經有電腦時換主機開搬家確認（不是只改設定）', async () => {
    access()
    expect(await screen.findByText('換主機就是搬家：家目錄、工作資料夾、瀏覽器登入都跟著走')).toBeInTheDocument()
    expect(screen.queryByText(/之後的版本/)).toBeNull()
    fireEvent.click(screen.getByRole('button', { name: /這台主機（這台）/ }))
    fireEvent.click(await screen.findByRole('option', { name: 'cloud-01' }))
    const dlg = await screen.findByRole('dialog', { name: '把小秘的電腦搬到 cloud-01？' })
    expect(calls('bots.update')).toHaveLength(0)
    fireEvent.click(within(dlg).getByRole('button', { name: '開始搬家' }))
    await waitFor(() => expect(calls('computers.move')[0]?.input).toEqual({ bot_id: 'b1', host_id: 'h2' }))
    expect(screen.getByTestId('computer-actions')).toBeInTheDocument()
    expect(screen.getByTestId('computer-storage')).toBeInTheDocument()
    expect(screen.getByTestId('network-allow')).toBeInTheDocument()
  })

  it('在哪裡跑：還沒有電腦時只改第一次開機放的位置', async () => {
    box = 'none'
    access()
    expect(await screen.findByText('還沒有電腦：第一次開機時放在這台主機')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: /自動擺放/ }))
    fireEvent.click(await screen.findByRole('option', { name: 'cloud-01' }))
    await waitFor(() => expect(calls('bots.update')[0]?.input).toEqual({ id: 'b1', host_id: 'h2' }))
    expect(screen.queryByRole('dialog')).toBeNull()
  })
})
