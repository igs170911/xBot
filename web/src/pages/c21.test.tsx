import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import App from '../App'
import { fakeApi, type FakeApi } from '../test-api'
import { resetBots } from '../data/bots'
import { resetInbox } from '../data/inbox'
import { resetChat } from '../chat/chatStore'
import { applyEvent, applyRead, emptyChat } from '../chat/assemble'
import { auditResult, delegationInput, draftOf, resourceText, updateInput, usdToMicros, validate, NEW_DRAFT, type Agent } from '../data/agents'

// C21：已連線的代理（J12）、代為核准紀錄卡（C16、F08）、MCP 修改標註（MCP-13）。

const DELEGATION = {
  agent_id: 'ag1',
  enabled: true,
  categories: ['external_send'],
  daily_count: 5,
  daily_amount_micros: 0,
  expires_at: Date.now() + 7 * 86_400_000,
  used_today: 1,
  amount_today_micros: 0,
  never: ['刪除 Bot 或資料', '移除主機', '提高花費上限', '改代為核准授權'],
} satisfies Agent['delegation']

const AGENT: Agent = {
  id: 'ag1',
  name: 'Claude Code',
  status: 'active',
  scopes: ['read', 'operate'],
  resources: { bots: [], teams: [], hosts: [] },
  resource_scope: 'own',
  last_used_at: Date.now() - 3 * 60_000,
  expires_at: 0,
  revoked_at: 0,
  created_at: 1,
  rate_per_min: 60,
  daily_calls: 500,
  daily_spend_micros: 0,
  calls_today: 42,
  spend_today_micros: 0,
  delegation: DELEGATION,
}

const CODEX: Agent = { ...AGENT, id: 'ag2', name: 'Codex', resource_scope: 'all', resources: { bots: [], teams: [], hosts: [] }, delegation: { ...DELEGATION, agent_id: 'ag2', enabled: false, categories: [] } }

describe('資料整理', () => {
  it('金額、草稿與輸入', () => {
    expect(usdToMicros('2.5')).toBe(2_500_000)
    expect(usdToMicros('')).toBe(0)
    expect(usdToMicros('abc')).toBeNull()
    const d = draftOf(AGENT)
    expect(d.dg).toMatchObject({ on: true, cats: ['external_send'], count: '5', days: 7 })
    expect(updateInput('ag1', d)).toEqual({ id: 'ag1', scopes: ['read', 'operate'], resource_scope: 'own', daily_calls: 500, daily_spend_micros: 0 })
    expect(delegationInput('ag1', { ...d, dg: { ...d.dg, amount: '20', days: 30 } })).toEqual({
      id: 'ag1',
      enabled: true,
      categories: ['external_send'],
      daily_count: 5,
      daily_amount_micros: 20_000_000,
      days: 30,
    })
    expect(delegationInput('ag1', NEW_DRAFT)).toEqual({ id: 'ag1', enabled: false })
    expect(validate({ ...NEW_DRAFT, dg: { ...NEW_DRAFT.dg, on: true, cats: [], count: '0' } })).toMatchObject({ dgCats: expect.any(String), dgCount: expect.any(String) })
    expect(resourceText({ resource_scope: 'custom', resources: { bots: ['b1'], teams: [], hosts: [] } }, () => '小秘')).toBe('只有這些 Bot：小秘')
    expect(auditResult({ status: 'error', error_code: 'forbidden' }).label).toBe('被拒：權限不足')
    expect(auditResult({ status: 'pending_approval', error_code: 'pending_approval' }).label).toBe('等你核准')
  })

  it('代理送的訊息帶著代理名字（MCP-13）', () => {
    const read = {
      thread: { id: 't1', bot_id: 'b1', title: '', pinned: false, archived: false, unread: 0, last_message_at: 1, created_at: 1 },
      messages: [{ id: 'm1', seq: 1, sender: 'owner', kind: 'text', content: '整理價格', actor: 'agent:ag1', actor_name: 'Claude Code', via: 'mcp', created_at: 1, updated_at: 1 }],
      has_more: false,
    }
    const s = applyRead(emptyChat, read)
    expect(s.threads.t1.messages[0]).toMatchObject({ actorName: 'Claude Code', via: 'mcp' })
    const s2 = applyEvent(emptyChat, {
      id: 1,
      kind: 'message.created',
      bot_id: 'b1',
      at: 1,
      payload: { id: 'm2', seq: 2, scope: 'thread', scope_id: 't1', sender: 'owner', kind: 'text', content: 'x', meta: { actor: 'agent:ag1', actor_name: 'Codex', via: 'http' }, created_at: 1, updated_at: 1 },
    })
    expect(s2.threads.t1.messages[0]).toMatchObject({ actorName: 'Codex', via: 'http' })
  })
})

let api: FakeApi
beforeEach(() => {
  api = fakeApi()
})
afterEach(() => {
  resetBots()
  resetInbox()
  resetChat()
  vi.unstubAllGlobals()
  vi.useRealTimers()
})

describe('設定 → 已連線的代理', () => {
  beforeEach(() => {
    window.history.replaceState(null, '', '/settings/agents')
    api.ops['agents.list'] = () => ({ agents: [AGENT, CODEX] })
    api.ops['audit.list'] = () => ({
      entries: [
        { id: 3, actor: 'agent:ag1', via: 'mcp', op: 'xbot.bots.update', request_id: '', target: 'b1', params: {}, status: 'ok', error_code: '', created_at: Date.now() },
        { id: 2, actor: 'agent:ag1', via: 'mcp', op: 'xbot.bots.delete', request_id: '', target: 'b2', params: {}, status: 'pending_approval', error_code: 'pending_approval', created_at: Date.now() },
        { id: 1, actor: 'owner', via: 'ui', op: 'xbot.bots.create', request_id: '', target: '', params: {}, status: 'ok', error_code: '', created_at: Date.now() },
      ],
    })
  })

  it('列出代理、代為核准標籤與最近的呼叫', async () => {
    render(<App />)
    expect(await screen.findByRole('heading', { level: 1, name: '已連線的代理（MCP）' })).toBeInTheDocument()
    const rows = await screen.findAllByTestId('agent-row')
    expect(rows).toHaveLength(2)
    expect(within(rows[0]).getByText('代為核准')).toBeInTheDocument()
    expect(within(rows[0]).getByText(/只能碰它建立的/)).toBeInTheDocument()
    expect(within(rows[1]).queryByText('代為核准')).toBeNull()
    expect(within(rows[1]).getByText(/全部 Bot/)).toBeInTheDocument()
    // 只列代理的呼叫，不列你自己的
    const audit = await screen.findAllByTestId('audit-row')
    expect(audit).toHaveLength(2)
    expect(within(audit[1]).getByText('等你核准')).toBeInTheDocument()
  })

  it('改權限：打開代為核准、勾類別，自動儲存', async () => {
    api.ops['agents.update_scopes'] = (input) => ({ ...CODEX, scopes: input.scopes })
    api.ops['agents.set_delegation'] = () => ({ ...DELEGATION, agent_id: 'ag2' })
    render(<App />)
    const rows = await screen.findAllByTestId('agent-row')
    fireEvent.click(within(rows[1]).getByRole('button', { name: '權限' }))
    const dlg = await screen.findByRole('dialog', { name: 'Codex 的權限' })
    expect(within(dlg).getByTestId('never-delegate')).toHaveTextContent('刪除 Bot 或資料')
    fireEvent.click(within(dlg).getByRole('switch', { name: '允許代為核准' }))
    fireEvent.click(within(dlg).getByRole('checkbox', { name: '小額花費' }))
    fireEvent.change(within(dlg).getByRole('textbox', { name: '每日代為核准金額' }), { target: { value: '20' } })
    fireEvent.click(within(dlg).getByRole('radio', { name: '30 天' }))
    await waitFor(() => expect(api.calls.some((c) => c.path === 'agents.set_delegation')).toBe(true))
    const last = api.calls.filter((c) => c.path === 'agents.set_delegation').at(-1)
    expect(last?.input).toEqual({ id: 'ag2', enabled: true, categories: ['external_send', 'spend_small'], daily_count: 5, daily_amount_micros: 20_000_000, days: 30 })
    expect(api.calls.filter((c) => c.path === 'agents.update_scopes').at(-1)?.input).toMatchObject({ id: 'ag2', resource_scope: 'all' })
    expect(await within(dlg).findByText('已儲存，下一次呼叫就生效')).toBeInTheDocument()
  })

  it('撤銷後可以復原', async () => {
    let revoked = 0
    api.ops['agents.list'] = () => ({ agents: [revoked ? { ...AGENT, status: 'revoked', revoked_at: revoked } : AGENT] })
    api.ops['agents.revoke'] = () => {
      revoked = Date.now()
      return { ...AGENT, status: 'revoked', revoked_at: revoked }
    }
    api.ops['agents.restore'] = () => {
      revoked = 0
      return AGENT
    }
    render(<App />)
    const row = await screen.findByTestId('agent-row')
    fireEvent.click(within(row).getByRole('button', { name: '撤銷' }))
    expect(await screen.findByText('已撤銷，權杖立即失效')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: '復原' }))
    await waitFor(() => expect(screen.queryByText('已撤銷，權杖立即失效')).toBeNull())
    expect(api.calls.some((c) => c.path === 'agents.restore')).toBe(true)
  })

  it('連接新的代理：預設只能碰它建立的，產生配對碼', async () => {
    api.ops['agents.pair_start'] = (input) => ({ code: 'ABCD-EFGH', expires_at: Date.now() + 300_000, pair_path: '/api/v1/agents.pair', scopes: input.scopes, resource_scope: input.resource_scope })
    render(<App />)
    await screen.findAllByTestId('agent-row')
    fireEvent.click(screen.getByRole('button', { name: '連接新的代理' }))
    const dlg = await screen.findByRole('dialog', { name: '連接新的代理' })
    expect(within(dlg).getByRole('radio', { name: '它建立的' })).toHaveAttribute('aria-checked', 'true')
    fireEvent.click(within(dlg).getByRole('button', { name: '產生配對碼' }))
    expect(await within(dlg).findByTestId('pair-code')).toHaveTextContent('ABCD-EFGH')
    expect(api.calls.find((c) => c.path === 'agents.pair_start')?.input).toMatchObject({ scopes: ['read', 'operate'], resource_scope: 'own', daily_calls: 500 })
  })
})

describe('待我處理：代為核准紀錄卡', () => {
  const CARD = {
    id: 'd1',
    kind: 'delegated',
    group: 'done',
    bot_id: 'b1',
    ref: 'ap1',
    title: '已由 Claude Code（MCP）代為核准 · 對外發送',
    body: '發一則貼文 · 今天第 1／5 次',
    payload: { actor_name: 'Claude Code', reversible: true, category_name: '對外發送' },
    status: 'resolved',
    resolution: 'delegated',
    read: false,
    created_at: Date.now(),
  }

  it('10 分鐘內可以復原，復原後顯示已復原', async () => {
    window.history.replaceState(null, '', '/')
    let undone = false
    api.ops['inbox.list'] = (input) => ({
      items: input.kind === 'delegated' ? [{ ...CARD, ...(undone ? { resolution: 'undone' } : { undo_until: Date.now() + 9 * 60_000 }) }] : [],
    })
    api.ops['inbox.undo'] = () => {
      undone = true
      return { item: { ...CARD, resolution: 'undone' } }
    }
    render(<App />)
    const card = await screen.findByTestId('inbox-delegated')
    expect(within(card).getByText(CARD.title)).toBeInTheDocument()
    expect(within(card).getByText('還能復原 9 分鐘')).toBeInTheDocument()
    await act(async () => {
      fireEvent.click(within(card).getByRole('button', { name: '復原' }))
    })
    await waitFor(() => expect(screen.getByTestId('inbox-delegated')).toHaveTextContent('已復原'))
    expect(api.calls.find((c) => c.path === 'inbox.undo')?.input).toEqual({ inbox_id: 'd1' })
  })

  it('不能復原的動作沒有復原按鈕', async () => {
    window.history.replaceState(null, '', '/')
    api.ops['inbox.list'] = (input) => ({ items: input.kind === 'delegated' ? [{ ...CARD, payload: { reversible: false } }] : [] })
    render(<App />)
    const card = await screen.findByTestId('inbox-delegated')
    expect(within(card).queryByRole('button', { name: '復原' })).toBeNull()
    expect(within(card).getByText('這個動作做了就不能復原')).toBeInTheDocument()
  })
})
