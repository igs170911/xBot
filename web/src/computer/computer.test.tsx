import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { fakeApi, type FakeApi } from '../test-api'
import { ToastProvider } from '../ui/Toast'
import { botsStore } from '../data/bots'
import { inboxStore } from '../data/inbox'
import { ComputerPage } from './ComputerPage'
import { OfficePage } from './OfficePage'
import { AUTO_RETRIES, RETRY_COOLDOWN, RETRY_DELAY, keysymOf, statusText } from './vnc'

// 假的 noVNC：記下每次連線的網址與 viewOnly，測試自己觸發 connect／disconnect。
type Fake = EventTarget & { url: string; viewOnly: boolean; keys: Array<[number, boolean]>; pasted: string[]; disconnect: () => void }
const conns: Fake[] = []
vi.mock('@novnc/novnc', () => ({
  default: class extends EventTarget {
    url: string
    viewOnly = false
    keys: Array<[number, boolean]> = []
    pasted: string[] = []
    scaleViewport = true
    clipViewport = false
    constructor(_t: HTMLElement, url: string) {
      super()
      this.url = url
      conns.push(this as unknown as Fake)
    }
    disconnect() {
      this.dispatchEvent(new Event('disconnect'))
    }
    sendKey(k: number, _c: string | null, down = true) {
      this.keys.push([k, down])
    }
    clipboardPasteFrom(t: string) {
      this.pasted.push(t)
    }
    focus() {}
  },
}))

const bot = { id: 'b1', name: '小編', shape: 'circle', color: 'pink' }
function screenOf(over: Record<string, unknown> = {}) {
  return { bot_id: 'b1', name: '小編', host_id: 'h1', host_name: '這台主機', box: 'running', host_status: 'online', working: true, paused: false, last_active_at: Date.now() - 3 * 60_000, viewers: 1, you: false, vnc: '/api/v1/computers/vnc?bot=b1', ...over }
}

let api: FakeApi
let state: ReturnType<typeof screenOf>
beforeEach(() => {
  conns.length = 0
  state = screenOf({ request: { inbox_id: 'i1', reason: 'Threads 要輸入簡訊驗證碼', thread_id: 't1', at: 1 } })
  botsStore.set({ bots: [bot] as never, activity: {} })
  api = fakeApi({
    'computers.screen': () => state,
    'computers.takeover': () => (state = screenOf({ working: false, paused: true, you: true, takeover: { by: 'owner', by_name: '你', since: 1, task_id: 'k1' } })),
    'computers.handback': (i) => {
      state = screenOf({ working: true })
      return { note: (i.note as string) || '我剛剛在電腦上操作過，請從目前畫面繼續。', resumed_task_id: 'k2', thread_id: 't1', screen: state }
    },
  })
})
afterEach(() => {
  vi.unstubAllGlobals()
  vi.useRealTimers()
})

const wrap = (el: React.ReactNode) => render(<ToastProvider>{el}</ToastProvider>)

it('全視窗（E02）：先只能看；接手後改用 control 連線、可以操作；交還帶說明，回到只能看', async () => {
  wrap(<ComputerPage botId="b1" />)
  expect(await screen.findByRole('heading', { name: '小編的電腦' })).toBeTruthy()
  expect(screen.getByTestId('takeover-request').textContent).toContain('Threads 要輸入簡訊驗證碼')
  await waitFor(() => expect(conns).toHaveLength(1))
  expect(conns[0].url).toMatch(/\/api\/v1\/computers\/vnc\?bot=b1$/)
  expect(conns[0].viewOnly).toBe(true)
  act(() => {
    conns[0].dispatchEvent(new Event('connect'))
  })
  expect(await screen.findByText('你在看')).toBeTruthy()

  fireEvent.click(screen.getAllByRole('button', { name: /接手操作/ })[0])
  expect(await screen.findByRole('heading', { name: '你正在操作小編的電腦' })).toBeTruthy()
  await waitFor(() => expect(conns).toHaveLength(2))
  expect(conns[1].url).toMatch(/mode=control$/)
  expect(conns[1].viewOnly).toBe(false)
  act(() => {
    conns[1].dispatchEvent(new Event('connect'))
  })
  fireEvent.click(screen.getByRole('button', { name: '貼上文字' }))
  fireEvent.change(screen.getByRole('textbox', { name: '要貼上的文字' }), { target: { value: '483 921' } })
  fireEvent.click(screen.getByRole('button', { name: '貼上' }))
  expect(conns[1].pasted).toEqual(['483 921'])

  fireEvent.change(screen.getByRole('textbox', { name: '交還時要說什麼' }), { target: { value: '輸入了驗證碼' } })
  fireEvent.click(screen.getByRole('button', { name: '完成，交還給小編' }))
  expect(await screen.findByTestId('handed-back')).toBeTruthy()
  expect(screen.getByTestId('handed-back').textContent).toContain('輸入了驗證碼')
  expect(api.calls.find((c) => c.path === 'computers.handback')?.input).toEqual({ bot_id: 'b1', note: '輸入了驗證碼' })
  await waitFor(() => expect(conns.at(-1)?.url).toMatch(/bot=b1$/))
})

it('畫面斷線（E10）：8 秒後重試、再冷卻 15 秒，2 次都失敗就請你按重新連線', async () => {
  vi.useFakeTimers({ shouldAdvanceTime: true })
  state = screenOf()
  wrap(<ComputerPage botId="b1" />)
  await waitFor(() => expect(conns).toHaveLength(1))
  for (let i = 0; i < AUTO_RETRIES; i++) {
    act(() => conns.at(-1)!.dispatchEvent(new Event('disconnect')))
    expect(screen.getByTestId('vnc-status').textContent).toBe(statusText({ kind: 'retrying', attempt: i + 1, seconds: (i ? RETRY_COOLDOWN : RETRY_DELAY) / 1000 }))
    await act(async () => {
      await vi.advanceTimersByTimeAsync(i ? RETRY_COOLDOWN : RETRY_DELAY)
    })
    await waitFor(() => expect(conns).toHaveLength(i + 2))
  }
  act(() => conns.at(-1)!.dispatchEvent(new Event('disconnect')))
  expect(screen.getByTestId('vnc-status').textContent).toBe('畫面連不上，請按重新連線')
  fireEvent.click(within(screen.getByTestId('computer-stage')).getByRole('button', { name: '重新連線' }))
  await waitFor(() => expect(conns).toHaveLength(AUTO_RETRIES + 2))
})

it('電腦休眠、主機離線時不連畫面，顯示狀態（E10）', async () => {
  state = screenOf({ box: 'sleeping', working: false })
  const { unmount } = wrap(<ComputerPage botId="b1" />)
  expect(await screen.findByText('休眠中')).toBeTruthy()
  unmount()
  state = screenOf({ host_status: 'offline' })
  wrap(<ComputerPage botId="b1" />)
  expect(await screen.findByText('主機離線')).toBeTruthy()
  expect(conns).toHaveLength(0)
})

it('辦公室（E03）：依主機分組、縮圖、等你的卡片、篩選', async () => {
  const hosts = [
    { id: 'h1', name: '這台主機', kind: 'local', status: 'online', max_running: 4, running: 2, default: true, resources: { cpus: 8, mem_bytes: 16 * 2 ** 30 } },
    { id: 'h2', name: 'cloud-01', kind: 'ssh', status: 'online', max_running: 3, running: 1, default: false, resources: { cpus: 4, mem_bytes: 8 * 2 ** 30 } },
  ]
  botsStore.set({ bots: [bot, { id: 'b2', name: '阿凱', shape: 'squircle', color: 'green' }, { id: 'b3', name: '數據喵', shape: 'blob', color: 'purple' }] as never, activity: {} })
  inboxStore.set({ items: [] })
  api.ops['hosts.list'] = () => ({ hosts })
  api.ops['computers.screens'] = () => ({
    screens: [
      screenOf({ request: { inbox_id: 'i1', reason: '要輸入驗證碼', at: 1 } }),
      screenOf({ bot_id: 'b2', name: '阿凱', box: 'sleeping', working: false }),
      screenOf({ bot_id: 'b3', name: '數據喵', host_id: 'h2', host_name: 'cloud-01' }),
    ],
  })
  api.ops['computers.snapshot'] = (i) => ({ bot_id: i.bot_id, available: true, image: '/9j/4AAQ', media_type: 'image/jpeg', taken_at: 1 })
  wrap(<OfficePage />)
  const groups = await screen.findAllByTestId('host-group')
  expect(groups).toHaveLength(2)
  expect(within(groups[0]).getByText('開機 2 / 4')).toBeTruthy()
  const cards = within(groups[0]).getAllByTestId('office-card')
  expect(cards.map((c) => c.dataset.status)).toEqual(['waiting', 'sleeping'])
  expect(within(cards[0]).getByText(/等你接手/)).toBeTruthy()
  await waitFor(() => expect(within(cards[0]).getByRole('img', { name: '小編的電腦畫面' })).toBeTruthy())
  expect(within(cards[0]).getByRole('link', { name: '接手' }).getAttribute('href')).toBe('/bots/b1/computer')
  expect(api.calls.filter((c) => c.path === 'computers.snapshot').map((c) => c.input.bot_id).sort()).toEqual(['b1', 'b3'])

  fireEvent.click(screen.getByRole('tab', { name: /休眠中/ }))
  expect(screen.getAllByTestId('host-group')).toHaveLength(1)
  expect(screen.getAllByTestId('office-card')).toHaveLength(1)
})

it('手機鍵盤的字元轉成 X11 keysym', () => {
  expect(keysymOf('a')).toBe(0x61)
  expect(keysymOf('é')).toBe(0xe9)
  expect(keysymOf('中')).toBe(0x01000000 + 0x4e2d)
  expect(keysymOf('\n')).toBe(0xff0d)
})
