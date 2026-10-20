import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import App from './App'
import { navigate } from './router'
import { fakeApi, type FakeApi } from './test-api'
import { resetBots } from './data/bots'
import { setRail } from './data/ui'
import { resetInbox } from './data/inbox'
import { resetChat } from './chat/chatStore'
import { dispatchEvent } from './data/events'
import { setPauseAll } from './data/pause'

const BOT = {
  id: 'b1',
  name: '抓蟲',
  title: 'Bug 重現',
  description: '照你的描述重做一次',
  soul: '',
  color: 'red',
  shape: 'shield',
  engine: 'hermes',
  model: '',
  effort: 'medium',
  approval_mode: 'ask',
  archived: false,
  created_at: 1,
  updated_at: 1,
}

let api: FakeApi
beforeEach(() => {
  api = fakeApi()
  window.history.replaceState(null, '', '/')
})
afterEach(() => {
  resetBots()
  setRail(false)
  resetInbox()
  resetChat()
  vi.unstubAllGlobals()
})

it('首頁是待我處理，並顯示服務版本', async () => {
  render(<App />)
  expect(await screen.findByRole('heading', { level: 1, name: '事情都處理完了' })).toBeInTheDocument()
  expect(screen.getByTestId('inbox-empty')).toHaveTextContent('都處理完了')
  expect(await screen.findByText('服務正常 · v-test')).toBeInTheDocument()
  const nav = screen.getByRole('navigation', { name: '側欄' })
  expect(within(nav).getByRole('link', { name: /待我處理/ })).toHaveAttribute('aria-current', 'page')
})

const APPROVAL = { id: 'i1', kind: 'approval', group: 'decide', bot_id: 'b1', ref: 'ap1', title: 'Claude Code 想要：刪除 Bot', body: '要允許嗎？', payload: { op: 'xbot.bots.delete' }, status: 'open', read: false, created_at: 1 }

it('真的待我處理：核准可以允許，送出 approvals.decide 後重讀', async () => {
  let open = true
  api.ops['inbox.list'] = (input) => ({ items: open && input.status === 'open' ? [APPROVAL] : [] })
  api.ops['bots.list'] = () => ({ bots: [BOT] })
  api.ops['approvals.decide'] = () => {
    open = false
    return { approval: { id: 'ap1', op: 'xbot.bots.delete', summary: '', params: {}, requested_by: 'agent:a', status: 'approved', created_at: 1 }, settled: true, executed: true, failed: false }
  }
  render(<App />)
  const card = await screen.findByTestId('inbox-item')
  expect(within(card).getByText('Claude Code 想要：刪除 Bot')).toBeInTheDocument()
  expect(screen.getByRole('heading', { level: 1, name: '有 1 件事等你' })).toBeInTheDocument()
  const nav = screen.getByRole('navigation', { name: '側欄' })
  expect(within(nav).getByLabelText('1 件等你')).toBeInTheDocument()
  // 刪除類用危險樣式（F08）
  fireEvent.click(within(card).getByRole('button', { name: '允許刪除' }))
  await waitFor(() => expect(screen.queryByTestId('inbox-item')).toBeNull())
  expect(api.calls.find((c) => c.path === 'approvals.decide')?.input).toEqual({ approval_id: 'ap1', decision: 'allow' })
})

it('退回後跳出可以復原的提示，按復原呼叫 inbox.undo', async () => {
  let open = true
  api.ops['inbox.list'] = (input) => ({ items: open && input.status === 'open' ? [{ ...APPROVAL, payload: { op: 'xbot.mail.send' } }] : [] })
  api.ops['approvals.decide'] = () => {
    open = false
    return { approval: { id: 'ap1', op: 'xbot.mail.send', summary: '', params: {}, requested_by: 'agent:a', status: 'denied', created_at: 1 }, settled: true, executed: false, failed: false }
  }
  api.ops['inbox.undo'] = () => {
    open = true
    return { item: { ...APPROVAL } }
  }
  render(<App />)
  const card = await screen.findByTestId('inbox-item')
  fireEvent.click(within(card).getByRole('button', { name: '退回' }))
  await waitFor(() => expect(screen.queryByTestId('inbox-item')).toBeNull())
  fireEvent.click(await screen.findByRole('button', { name: '復原' }))
  expect(await screen.findByTestId('inbox-item')).toBeInTheDocument()
  expect(api.calls.find((c) => c.path === 'inbox.undo')?.input).toEqual({ inbox_id: 'i1' })
})

it('Bot 在對話裡的提問：在待我處理就地回答（chat.respond）', async () => {
  let open = true
  api.ops['bots.list'] = () => ({ bots: [BOT] })
  api.ops['inbox.list'] = (input) => ({
    items:
      open && input.status === 'open'
        ? [{ id: 'q1', kind: 'question', group: 'decide', bot_id: 'b1', task_id: 't1', thread_id: 'th1', title: '抓蟲有問題問你', body: '要比日本賣場嗎？', payload: { prompt: '要比日本賣場嗎？', choices: ['要', '不用'] }, status: 'open', read: false, created_at: 1, expires_at: Date.now() + 7 * 60_000 }]
        : [],
  })
  api.ops['chat.respond'] = () => {
    open = false
    return { inbox_id: 'q1', task_id: 't1', thread_id: 'th1', status: 'resolved', resolution: 'answered' }
  }
  render(<App />)
  const card = await screen.findByTestId('ask-card')
  expect(within(card).getByTestId('ask-expiry')).toHaveTextContent('還有 7 分鐘')
  fireEvent.click(within(card).getByRole('button', { name: /不用/ }))
  await waitFor(() => expect(screen.queryByTestId('ask-card')).toBeNull())
  expect(api.calls.find((c) => c.path === 'chat.respond')?.input).toEqual({ inbox_id: 'q1', text: '不用' })
})

it('過期的提問可以請它再問一次', async () => {
  let asked = false
  api.ops['inbox.list'] = (input) => ({
    items: input.status === 'expired' && !asked ? [{ id: 'x1', kind: 'question', group: 'decide', bot_id: 'b1', task_id: 't1', thread_id: 'th1', title: '信差有問題問你', body: '要不要退訂這 12 份電子報？', payload: {}, status: 'expired', resolution: 'expired', read: true, created_at: 1 }] : [],
  })
  api.ops['chat.ask_again'] = () => {
    asked = true
    return { bot_id: 'b1', thread_id: 'th1', task_id: 't2', message_id: 'm' }
  }
  render(<App />)
  const card = await screen.findByTestId('inbox-expired')
  expect(card).toHaveTextContent('要不要退訂這 12 份電子報？')
  fireEvent.click(within(card).getByRole('button', { name: '請它再問一次' }))
  await waitFor(() => expect(screen.queryByTestId('inbox-expired')).toBeNull())
  expect(api.calls.find((c) => c.path === 'chat.ask_again')?.input).toEqual({ inbox_id: 'x1' })
})

it('側欄連結用前端路由換頁', async () => {
  render(<App />)
  const nav = await screen.findByRole('navigation', { name: '側欄' })
  fireEvent.click(within(nav).getByRole('link', { name: /辦公室/ }))
  expect(window.location.pathname).toBe('/office')
  // 頁面按需載入（v1.0）：等那一頁的程式載入
  expect(await screen.findByRole('heading', { level: 1, name: '辦公室' }, { timeout: 5000 })).toBeInTheDocument()
  act(() => navigate('/'))
  // 待我處理是非同步讀的：讀完才知道「事情都處理完了」。
  expect(await screen.findByRole('heading', { level: 1, name: '事情都處理完了' })).toBeInTheDocument()
  await screen.findByText('服務正常 · v-test')
})

it('側欄的 Bot 清單來自 bots.list，有人改了 Bot 就重讀', async () => {
  let bots = [BOT]
  api.ops['bots.list'] = () => ({ bots })
  render(<App />)
  const nav = await screen.findByRole('navigation', { name: '側欄' })
  expect(await within(nav).findByRole('link', { name: /抓蟲/ })).toHaveAttribute('href', '/bots/b1')
  bots = [BOT, { ...BOT, id: 'b2', name: '小秘', shape: 'cursor', color: 'blue' }]
  act(() => dispatchEvent({ id: 9, kind: 'op', at: 1, payload: { op: 'xbot.bots.create', actor: 'agent:a', status: 'ok' } }))
  expect(await within(nav).findByRole('link', { name: /小秘/ })).toBeInTheDocument()
  // 工作中的 Bot：最後一句話那行改成「工作中…」
  act(() => dispatchEvent({ id: 10, kind: 'task.state', bot_id: 'b2', thread_id: 't', task_id: 'k', at: 2, payload: { state: 'running' } }))
  expect(within(within(nav).getByRole('link', { name: /小秘/ })).getByText('工作中…')).toBeInTheDocument()
})

it('側欄最上面是大總管（主要 Bot）的大卡片，清單不再重複列它；每一列有職稱和最後一句話', async () => {
  const chief = { ...BOT, id: 'b0', name: '水哥哥', title: '幕僚長', primary: true }
  api.ops['bots.list'] = () => ({ bots: [chief, { ...BOT, title: '工程' }] })
  api.ops['chat.list_threads'] = (input) => ({
    threads: input.bot_id === 'b1' ? [{ id: 't1', bot_id: 'b1', title: '修登入', pinned: false, archived: false, unread: 1, last_message_at: 5, created_at: 1, preview: '登入問題修好了' }] : [],
  })
  render(<App />)
  const nav = await screen.findByRole('navigation', { name: '側欄' })
  const card = await within(nav).findByTestId('chief-card')
  expect(card).toHaveAttribute('href', '/bots/b0')
  expect(card).toHaveAccessibleName(/水哥哥（大總管）/)
  expect(within(nav).getAllByRole('link', { name: /水哥哥/ })).toHaveLength(1)
  const row = within(nav).getByRole('link', { name: /抓蟲/ })
  expect(row).toHaveTextContent('工程')
  expect(await within(row).findByText('登入問題修好了')).toBeInTheDocument()
  expect(within(row).getByRole('img', { name: '有新訊息' })).toBeInTheDocument()
  expect(api.calls.find((c) => c.path === 'chat.list_threads')?.input).toMatchObject({ preview: true })
})

it('側欄可以縮成只有圖示，記在這個瀏覽器；設定和不常用的頁面收在左下角「設定與更多」', async () => {
  api.ops['bots.list'] = () => ({ bots: [BOT] })
  render(<App />)
  const nav = await screen.findByRole('navigation', { name: '側欄' })
  expect(within(nav).queryByText('全部檔案')).toBeNull()
  fireEvent.click(within(nav).getByRole('button', { name: '設定與更多' }))
  fireEvent.click(await screen.findByRole('menuitem', { name: '全部檔案' }))
  expect(window.location.pathname).toBe('/files')
  fireEvent.click(within(nav).getByRole('button', { name: '縮小側欄' }))
  await screen.findByRole('button', { name: '展開側欄' })
  const rail = screen.getByRole('navigation', { name: '側欄' })
  expect(await within(rail).findByRole('link', { name: /抓蟲/ })).toBeInTheDocument()
  expect(within(rail).queryByText('待我處理')).toBeNull()
  expect(window.localStorage.getItem('xbot.sidebar.rail')).toBe('1')
  fireEvent.click(within(rail).getByRole('button', { name: '展開側欄' }))
  expect(await within(screen.getByRole('navigation', { name: '側欄' })).findByText('待我處理')).toBeInTheDocument()
  expect(window.localStorage.getItem('xbot.sidebar.rail')).toBe('0')
})

it('沒有 Bot 時側欄有新增按鈕；新增 Bot 表單呼叫 bots.create 並打開對話', async () => {
  let created = false
  api.ops['bots.list'] = () => ({ bots: created ? [BOT] : [] })
  api.ops['bots.create'] = (input) => {
    created = true
    return { ...BOT, name: input.name, description: input.description ?? '', shape: input.shape, color: input.color }
  }
  render(<App />)
  const nav = await screen.findByRole('navigation', { name: '側欄' })
  await within(nav).findByText(/還沒有 Bot/)
  // 沒有 Bot 時首頁是「建立第一個 Bot」（A09）
  expect(await screen.findByRole('heading', { level: 1, name: '建立第一個 Bot' })).toBeInTheDocument()
  expect(within(nav).getByRole('button', { name: '新增' })).toBeInTheDocument() // 標題列的＋（新增 Bot、群組、團隊）
  fireEvent.click(within(nav).getByRole('button', { name: '建立 Bot' }))
  const dlg = await screen.findByRole('dialog', { name: '新增 Bot' })
  fireEvent.click(within(dlg).getByRole('button', { name: '建立' }))
  expect(await within(dlg).findByText('請填名字')).toBeInTheDocument()
  fireEvent.change(within(dlg).getByLabelText('名字'), { target: { value: '抓蟲' } })
  fireEvent.change(within(dlg).getByLabelText('它負責什麼（一句話）'), { target: { value: '重現 Bug' } })
  fireEvent.click(within(dlg).getByRole('radio', { name: '盾牌' }))
  fireEvent.click(within(dlg).getByRole('radio', { name: '紅' }))
  fireEvent.click(within(dlg).getByRole('button', { name: '建立「抓蟲」' }))
  await waitFor(() => expect(window.location.pathname).toBe('/bots/b1'))
  expect(api.calls.find((c) => c.path === 'bots.create')?.input).toEqual({ name: '抓蟲', description: '重現 Bug', shape: 'shield', color: 'red' })
  expect(screen.queryByRole('dialog', { name: '新增 Bot' })).toBeNull()
  expect(await screen.findByTestId('chat-empty')).toHaveTextContent('抓蟲')
})

it('沒登入（從別的裝置打開）時顯示登入頁，輸入登入碼後進入', async () => {
  api.auth = { authenticated: false, local: false }
  render(<App />)
  const input = await screen.findByLabelText('登入碼')
  fireEvent.change(input, { target: { value: 'bad' } })
  fireEvent.click(screen.getByRole('button', { name: '登入' }))
  expect(await screen.findByText('登入碼或權杖不正確')).toBeInTheDocument()
  fireEvent.change(input, { target: { value: 'good' } })
  fireEvent.click(screen.getByRole('button', { name: '登入' }))
  expect(await screen.findByRole('heading', { level: 1, name: /件事等你|事情都處理完了/ })).toBeInTheDocument()
  await screen.findByText('服務正常 · v-test')
})

it('手機抽屜：打開後焦點在關閉鈕，Esc 關掉並回到選單鈕', async () => {
  // 手機寬度：對話分頁（L01）左上的吉祥物打開側欄
  const mm = window.matchMedia
  window.matchMedia = ((q: string) => ({ matches: q.includes('max-width'), media: q, addEventListener() {}, removeEventListener() {} })) as unknown as typeof window.matchMedia
  window.history.replaceState(null, '', '/chats')
  render(<App />)
  try {
    const opener = await screen.findByRole('button', { name: '打開側欄' })
    opener.focus()
    fireEvent.click(opener)
    // findBy 可能在頁面剛畫好、effect（訂閱 drawerStore、移焦點）還沒跑時就回來：等畫面跟上，不要假設同步。
    const drawer = await screen.findByRole('dialog', { name: '側欄' })
    const close = within(drawer).getByRole('button', { name: '關閉側欄' })
    await waitFor(() => expect(close).toHaveFocus())
    fireEvent.keyDown(close, { key: 'Escape' })
    await waitFor(() => expect(screen.queryByRole('dialog', { name: '側欄' })).toBeNull())
    await waitFor(() => expect(opener).toHaveFocus())
  } finally {
    window.matchMedia = mm
  }
})

it('/dev/components 列出元件與頭像矩陣（7 列 × 10 形狀）', async () => {
  window.history.replaceState(null, '', '/dev/components')
  render(<App />)
  expect(await screen.findByRole('heading', { level: 1, name: '元件（Grok 風格 · Midnight）' }, { timeout: 5000 })).toBeInTheDocument()
  const table = screen.getByRole('table', { name: '頭像形狀、眼睛與顏色' })
  expect(within(table).getAllByRole('row')).toHaveLength(8)
  expect(table.querySelectorAll('[data-shape]')).toHaveLength(70)
})

it('全部停下中：最上方有提示，按「繼續」恢復', async () => {
  let paused = true
  api.ops['collab.pause_state'] = () => ({ paused })
  api.ops['collab.pause_all'] = (input) => {
    paused = !!input.paused
    return { paused }
  }
  render(<App />)
  const banner = await screen.findByTestId('pause-banner')
  fireEvent.click(within(banner).getByRole('button', { name: '繼續' }))
  await waitFor(() => expect(api.calls.find((c) => c.path === 'collab.pause_all')?.input).toEqual({ paused: false }))
  await waitFor(() => expect(screen.queryByTestId('pause-banner')).toBeNull())
})

it('全部停下中：按 ✕ 只收起提示，Bot 還是停著；再按一次全部停下又出現', async () => {
  window.localStorage.removeItem('xbot.pauseBannerHidden')
  let paused = true
  api.ops['collab.pause_state'] = () => ({ paused })
  api.ops['collab.pause_all'] = (input) => {
    paused = !!input.paused
    return { paused, stopped: 0 }
  }
  render(<App />)
  const banner = await screen.findByTestId('pause-banner')
  fireEvent.click(within(banner).getByRole('button', { name: '收起提示（Bot 還是停著）' }))
  await waitFor(() => expect(screen.queryByTestId('pause-banner')).toBeNull())
  expect(api.calls.some((c) => c.path === 'collab.pause_all')).toBe(false)
  expect(window.localStorage.getItem('xbot.pauseBannerHidden')).toBe('1')
  // 收起後在「⋯」選單還找得到「繼續」
  fireEvent.click(screen.getAllByRole('button', { name: /更多|設定與更多/ })[0])
  expect(await screen.findByRole('menuitem', { name: '繼續（恢復 Bot 之間的往來）' })).toBeInTheDocument()
  fireEvent.keyDown(document.activeElement ?? document.body, { key: 'Escape' })
  await act(async () => {
    await setPauseAll(true)
  })
  expect(await screen.findByTestId('pause-banner')).toBeInTheDocument()
})
