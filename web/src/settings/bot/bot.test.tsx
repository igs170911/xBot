import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { fakeApi, type FakeApi } from '../../test-api'
import { ToastProvider } from '../../ui/Toast'
import { BotSettingsPage } from '../BotSettings'
import { ImportBotDialog, openImportBot } from './share'

const BOT = { id: 'b1', name: '小秘', title: '幕僚長', description: '每天整理重點', soul: '說話簡短', color: 'blue', shape: 'cursor', eyes: '', engine: 'hermes', model: '', fallback: '', effort: '', approval_mode: 'ask', memory_enabled: true, archived: false, primary: true, created_at: 1, updated_at: 1 }

let api: FakeApi
let bot: Record<string, unknown>
beforeEach(() => {
  bot = { ...BOT }
  api = fakeApi({
    'bots.get': () => bot,
    'bots.update': (input) => {
      bot = { ...bot, ...input }
      return bot
    },
    'engines.status': () => ({ bots: [], host: [] }),
    'models.presets': () => ({ presets: [] }),
  })
  window.history.replaceState(null, '', '/settings/bots/b1')
})
afterEach(() => vi.unstubAllGlobals())

const page = (tab?: 'abilities' | 'permissions' | 'advanced', sub?: string) =>
  render(
    <ToastProvider>
      <BotSettingsPage botId="b1" tab={tab} sub={sub} />
    </ToastProvider>,
  )

it('基本（G02）：4 個分頁、名字離開欄位就存、名字留白恢復並提示', async () => {
  page()
  const nav = await screen.findByRole('navigation', { name: 'Bot 設定分頁' })
  expect(within(nav).getAllByRole('link').map((a) => a.textContent)).toEqual(['基本', '能力', '權限與花費', '進階'])
  expect(within(nav).getByRole('link', { name: '基本' })).toHaveAttribute('aria-current', 'page')
  const name = screen.getByLabelText('名字')
  fireEvent.change(name, { target: { value: '阿秘' } })
  fireEvent.blur(name)
  await waitFor(() => expect(api.calls.find((c) => c.path === 'bots.update')?.input).toEqual({ id: 'b1', name: '阿秘' }))
  fireEvent.change(name, { target: { value: '  ' } })
  fireEvent.blur(name)
  expect(await screen.findByText('名字不能空白')).toBeInTheDocument()
  expect(name).toHaveValue('阿秘')
  // 人設預設收起成預覽，展開才是文字框
  expect(screen.queryByLabelText('常駐指示')).toBeNull()
  fireEvent.click(screen.getByRole('button', { name: '展開' }))
  const soul = screen.getByLabelText('常駐指示')
  fireEvent.change(soul, { target: { value: '說話簡短。花錢前先問我。' } })
  fireEvent.blur(soul)
  await waitFor(() => expect(api.calls.filter((c) => c.path === 'bots.update').at(-1)?.input).toEqual({ id: 'b1', soul: '說話簡短。花錢前先問我。' }))
})

it('外觀（G03）：點形狀、眼睛、顏色立刻存；還原吉祥物', async () => {
  page()
  fireEvent.click(await screen.findByRole('button', { name: '換外觀' }))
  const sheet = screen.getByRole('dialog', { name: '外觀' })
  fireEvent.click(within(within(sheet).getByRole('radiogroup', { name: '形狀' })).getByRole('radio', { name: '星星' }))
  await waitFor(() => expect(api.calls.filter((c) => c.path === 'bots.update').at(-1)?.input).toEqual({ id: 'b1', shape: 'star' }))
  fireEvent.click(within(within(sheet).getByRole('radiogroup', { name: '眼睛' })).getByRole('radio', { name: '開心' }))
  await waitFor(() => expect(api.calls.filter((c) => c.path === 'bots.update').at(-1)?.input).toEqual({ id: 'b1', eyes: 'happy' }))
  fireEvent.click(within(sheet).getByRole('radio', { name: '粉紅' }))
  await waitFor(() => expect(api.calls.filter((c) => c.path === 'bots.update').at(-1)?.input).toEqual({ id: 'b1', color: 'pink' }))
  fireEvent.click(within(sheet).getByRole('button', { name: '還原吉祥物' }))
  await waitFor(() => expect(api.calls.filter((c) => c.path === 'bots.update').at(-1)?.input).toEqual({ id: 'b1', shape: 'cursor', eyes: 'dot', image: '' }))
})

it('上傳圖片（G04）：格式不對、超過 10 MB 留在原畫面並說明', async () => {
  page()
  fireEvent.click(await screen.findByRole('button', { name: '上傳圖片' }))
  const dlg = screen.getByRole('dialog', { name: '換成圖片' })
  const input = within(dlg).getByLabelText('選擇頭像圖片')
  fireEvent.change(input, { target: { files: [new File(['x'], 'a.svg', { type: 'image/svg+xml' })] } })
  expect(await within(dlg).findByText('只接受 PNG、JPEG、GIF、WebP')).toBeInTheDocument()
  const big = new File(['x'], 'big.png', { type: 'image/png' })
  Object.defineProperty(big, 'size', { value: 11 << 20 })
  fireEvent.change(input, { target: { files: [big] } })
  expect(await within(dlg).findByText('圖片超過 10 MB，請換小一點的')).toBeInTheDocument()
  expect(within(dlg).getByRole('button', { name: '用這張' })).toBeDisabled()
})

it('進階 › 歷史（G12）：列出設定的變更與誰改的', async () => {
  api.ops['audit.list'] = () => ({
    entries: [
      { id: 2, actor: 'agent:a1', via: 'mcp', op: 'xbot.bots.update', request_id: '', target: 'b1', params: { id: 'b1', approval_mode: 'auto' }, status: 'pending_approval', error_code: '', created_at: 2 },
      { id: 1, actor: 'owner', via: 'ui', op: 'xbot.chat.send', request_id: '', target: 'b1', params: {}, status: 'ok', error_code: '', created_at: 1 },
    ],
  })
  page('advanced', 'history')
  const list = await screen.findByRole('group', { name: '設定的變更' })
  expect(list).toHaveTextContent('權限改成「放手做」')
  expect(list).toHaveTextContent('外部代理（MCP）')
  expect(list).toHaveTextContent('等你核准')
  expect(list).not.toHaveTextContent('chat')
  expect(api.calls.find((c) => c.path === 'audit.list')?.input).toEqual({ target: 'b1', limit: 200 })
})

it('分享（G13）：下載 .xbot 檔；不勾技能時重新匯出', async () => {
  const exp = (input: Record<string, unknown>) => ({ filename: '小秘.xbot', content: '{"format":"xbot.bot"}', bytes: 21, skills: input.no_skills ? 0 : 3, apps: 1 })
  api.ops['bots.export'] = exp
  const url = vi.fn(() => 'blob:x')
  vi.stubGlobal('URL', Object.assign(URL, { createObjectURL: url, revokeObjectURL: vi.fn() }))
  page()
  fireEvent.click(await screen.findByRole('button', { name: '分享' }))
  const dlg = screen.getByRole('dialog', { name: '分享小秘' })
  expect(await within(dlg).findByText('3 個技能')).toBeInTheDocument()
  expect(dlg).toHaveTextContent('API 金鑰')
  fireEvent.click(within(dlg).getByRole('checkbox', { name: /技能/ }))
  fireEvent.click(within(dlg).getByRole('button', { name: '下載 .xbot 檔' }))
  await waitFor(() => expect(api.calls.filter((c) => c.path === 'bots.export').at(-1)?.input).toEqual({ id: 'b1', no_skills: true }))
  expect(url).toHaveBeenCalled()
})

it('匯入（G14）：選檔 → 檢查內容（橘色＝要你處理）→ 匯入', async () => {
  const out = {
    preview: true,
    bot: { ...BOT, id: '', name: '小秘 2' },
    renamed: true,
    original: '小秘',
    items: [
      { kind: 'persona', status: 'ok', title: '人設與頭像', detail: '照原樣帶入' },
      { kind: 'apps', status: 'action', title: '應用程式 1 個', detail: 'Gmail 要你登入' },
      { kind: 'permissions', status: 'info', title: '權限', detail: '一律從「先問我」開始' },
    ],
    skills_added: [],
    skills_merged: [],
    apps_missing: ['Gmail'],
    remaining: 48,
    can_create: true,
    exported_at: 1,
    bytes: 2048,
  }
  api.ops['bots.import'] = (input) => (input.preview ? out : { ...out, preview: false, bot: { ...out.bot, id: 'n1', name: input.name }, skills_added: ['s1'] })
  render(
    <ToastProvider>
      <ImportBotDialog />
    </ToastProvider>,
  )
  openImportBot()
  const dlg = await screen.findByRole('dialog', { name: '匯入 Bot' })
  expect(within(dlg).getByRole('button', { name: '下一步' })).toBeDisabled()
  fireEvent.change(within(dlg).getByLabelText('選擇 .xbot 檔'), { target: { files: [new File(['{"format":"xbot.bot"}'], '小秘.xbot', { type: 'application/json' })] } })
  await waitFor(() => expect(within(dlg).getByRole('button', { name: '下一步' })).toBeEnabled())
  fireEvent.click(within(dlg).getByRole('button', { name: '下一步' }))
  const items = await screen.findByRole('list', { name: '會帶入的內容' })
  expect(within(items).getAllByRole('listitem').map((li) => li.getAttribute('data-status'))).toEqual(['ok', 'action', 'info'])
  expect(screen.getByText('還可以建立 48 個 Bot')).toBeInTheDocument()
  expect(screen.getByLabelText('名字')).toHaveValue('小秘 2')
  fireEvent.click(screen.getByRole('button', { name: '匯入' }))
  await waitFor(() => expect(window.location.pathname).toBe('/bots/n1'))
  expect(api.calls.filter((c) => c.path === 'bots.import').at(-1)?.input).toMatchObject({ filename: '小秘.xbot', name: '小秘 2' })
})
