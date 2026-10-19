import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import App from '../App'
import { fakeApi, opError, type FakeApi } from '../test-api'
import { resetBots, type Bot } from '../data/bots'
import { resetInbox } from '../data/inbox'
import { resetTeams, sectionsOf, type Team } from '../data/teams'
import { resetAgenda, daysLeft, resultLabel, whenInput } from '../data/agenda'
import { resetChat } from '../chat/chatStore'
import { PeerCard } from '../chat/PeerCard'
import type { ChatMsg } from '../chat/assemble'
import { groupsOf, managerChoices, memberProblem, overLimit, stateOf, treeOf, type Chart, type ChartBot } from './model'

// X03：側欄依團隊分區、團隊地圖（F04）、組織架構（F01）、建立團隊（F02/F03）、提案與討論（H04、待我處理）。

const now = Date.now()

function bot(id: string, name: string, extra: Partial<Bot> = {}): Bot {
  return {
    id, name, title: name + '職稱', description: '', soul: '', color: 'blue', shape: 'cursor', engine: 'hermes', model: '', fallback: '', effort: '',
    approval_mode: 'ask', archived: false, team_id: '', reports_to: '', can_manage: false, primary: false, created_at: now, updated_at: now, ...extra,
  } as Bot
}

function team(id: string, name: string, chief: string, members: string[]): Team {
  return { id, name, about: '', chief_id: chief, member_ids: [chief, ...members], memory: [], room_id: '', template: 'marketing', position: 1, created_by: 'owner', created_at: now }
}

function cbot(id: string, name: string, extra: Partial<ChartBot> = {}): ChartBot {
  return {
    id, name, title: '', color: 'blue', shape: 'cursor', model: '', reports_to: '使用者', reports_to_id: '', team: '', team_id: '', chief: false, primary: false,
    can_manage: false, state: 'idle', queued: 0, approvals: [], recent: [], ...extra,
  }
}

const TEMPLATES = {
  templates: [
    { key: 'marketing', name: '行銷團隊', about: '行銷主管帶內容、社群。', lead_manage: false, lead: { name: '行銷主管', title: '主管', persona: 'p' }, members: [{ name: '內容企劃', title: '內容', persona: '' }, { name: '社群經營', title: '社群', persona: '' }] },
    { key: 'engineering', name: '工程團隊', about: '技術主管帶前後端。', lead_manage: false, lead: { name: '技術主管', title: '主管', persona: '' }, members: [{ name: '前端工程師', title: '前端', persona: '' }] },
  ],
  team_max: 10,
  bots_max: 50,
  bots_total: 3,
}

describe('資料整理', () => {
  it('側欄分區：每個團隊一區、主管在前，沒有團隊的放最後', () => {
    const bots = [bot('a', '甲', { team_id: 't1' }), bot('b', '乙', { team_id: 't1' }), bot('c', '丙'), bot('d', '丁', { archived: true })]
    const secs = sectionsOf(bots, [team('t1', '行銷部', 'b', ['a'])])
    expect(secs.map((s) => s.title)).toEqual(['行銷部', '未分區'])
    expect(secs[0].bots.map((b) => b.id)).toEqual(['b', 'a'])
    expect(secs[1].bots.map((b) => b.id)).toEqual(['c'])
    expect(sectionsOf(bots, []).map((s) => s.title)).toEqual(['Bot'])
    expect(sectionsOf(bots.slice(0, 2), [team('t1', '行銷部', 'b', ['a'])]).map((s) => s.title)).toEqual(['行銷部'])
  })

  it('報告樹與可以選的主管（不能選自己和自己的下屬）', () => {
    const bots = [cbot('ceo', 'CEO'), cbot('dev', 'Dev', { reports_to_id: 'ceo' }), cbot('qa', 'QA', { reports_to_id: 'dev' }), cbot('ops', 'Ops')]
    expect(treeOf(bots).map((x) => `${x.bot.name}:${x.depth}`)).toEqual(['CEO:0', 'Dev:1', 'QA:2', 'Ops:0'])
    expect(managerChoices(bots, 'ceo').map((b) => b.id)).toEqual(['ops'])
    expect(managerChoices(bots, 'qa').map((b) => b.id)).toEqual(['ceo', 'dev', 'ops'])
  })

  it('狀態膠囊與分組', () => {
    expect(stateOf({ state: 'waiting', approvals: [] }).tone).toBe('warning')
    expect(stateOf({ state: 'idle', approvals: [{ subject: 'x', since: 1 }] }).label).toBe('等你允許')
    expect(stateOf({ state: 'working', approvals: [] }).label).toBe('工作中')
    const chart = { bots: [cbot('a', 'A'), cbot('b', 'B'), cbot('c', 'C')], teams: [team('t', '隊', 'a', ['b'])], bots_total: 3, bots_max: 50, team_max: 10 } as Chart
    const g = groupsOf(chart)
    expect(g.groups[0].bots.map((b) => b.id)).toEqual(['a', 'b'])
    expect(g.rest.map((b) => b.id)).toEqual(['c'])
  })

  it('建立團隊的檢查', () => {
    expect(overLimit(48, 5, 50)).toBe(true)
    expect(overLimit(45, 5, 50)).toBe(false)
    const p = (n: string) => ({ name: n, title: '', persona: '' })
    expect(memberProblem([p('主管')], 10)).toMatch('至少')
    expect(memberProblem([p('主管'), p('主管')], 10)).toMatch('重複')
    expect(memberProblem([p('主管'), p(' ')], 10)).toMatch('名字')
    expect(memberProblem(Array.from({ length: 11 }, (_, i) => p('x' + i)), 10)).toMatch('最多 10 位')
    expect(memberProblem([p('主管'), p('成員')], 10)).toBeNull()
  })

  it('提案與討論的文字', () => {
    expect(daysLeft(now + 2.5 * 86_400_000, now)).toBe(3)
    expect(daysLeft(now + 60_000, now)).toBe(1)
    expect(daysLeft(now - 1, now)).toBe(0)
    expect(whenInput('2026-10-20T15:00')).toBe('2026-10-20 15:00')
    expect(resultLabel({ state: 'vetoed', decided_at: 0 })).toBe('你否決了')
    expect(resultLabel({ state: 'auto' })).toBe('沒意見，已生效')
  })
})

let api: FakeApi
beforeEach(() => {
  api = fakeApi()
})
afterEach(() => {
  resetBots()
  resetInbox()
  resetTeams()
  resetAgenda()
  resetChat()
  vi.unstubAllGlobals()
})

describe('側欄與團隊地圖', () => {
  beforeEach(() => {
    window.history.replaceState(null, '', '/teams')
    api.ops['bots.list'] = () => ({ bots: [bot('a', '內容企劃', { team_id: 't1', reports_to: 'b' }), bot('b', '行銷主管', { team_id: 't1' }), bot('c', '小秘')] })
    api.ops['teams.list'] = () => ({ teams: [team('t1', '行銷團隊', 'b', ['a'])] })
    api.ops['teams.chart'] = () => ({
      bots: [
        cbot('b', '行銷主管', { team: '行銷團隊', team_id: 't1', chief: true, state: 'working', model: 'gpt-x' }),
        cbot('a', '內容企劃', { team: '行銷團隊', team_id: 't1', reports_to: '行銷主管', reports_to_id: 'b', state: 'waiting' }),
        cbot('c', '小秘'),
      ],
      teams: [team('t1', '行銷團隊', 'b', ['a'])],
      bots_total: 3,
      bots_max: 50,
      team_max: 10,
    })
  })

  it('側欄依團隊分區；地圖畫出團隊框、節點與狀態', async () => {
    render(<App />)
    const nav = await screen.findByRole('navigation', { name: '側欄' })
    const sec = await within(nav).findByRole('group', { name: '行銷團隊' })
    const links = within(sec).getAllByRole('link')
    expect(links[0]).toHaveTextContent('行銷主管')
    expect(within(links[0]).getByRole('img', { name: '主管' })).toBeInTheDocument()
    expect(within(nav).getByRole('group', { name: '未分區' })).toHaveTextContent('小秘')
    const map = await screen.findByRole('main', { name: '團隊地圖' })
    const box = await within(map).findByRole('group', { name: '行銷團隊' })
    const nodes = within(box).getAllByTestId('map-node')
    expect(nodes).toHaveLength(2)
    expect(nodes[0]).toHaveTextContent('工作中')
    expect(nodes[1]).toHaveTextContent('等你回覆')
    expect(within(map).getByRole('group', { name: '未分區' })).toHaveTextContent('小秘')
  })

  it('刪除團隊要先確認', async () => {
    api.ops['teams.delete'] = () => ({ deleted: true, name: '行銷團隊', members: 2 })
    render(<App />)
    fireEvent.click(await screen.findByRole('button', { name: '刪除團隊 行銷團隊' }))
    const dlg = await screen.findByRole('alertdialog')
    expect(dlg).toHaveTextContent('Bot、報告線、群組')
    fireEvent.click(within(dlg).getByRole('button', { name: '刪除團隊' }))
    await waitFor(() => expect(api.calls.some((c) => c.path === 'teams.delete' && c.input.id === 't1')).toBe(true))
  })
})

describe('建立團隊（F02/F03）', () => {
  beforeEach(() => {
    window.history.replaceState(null, '', '/teams')
    api.ops['teams.chart'] = () => ({ bots: [], teams: [], bots_total: 0, bots_max: 50, team_max: 10 })
    api.ops['teams.templates'] = () => TEMPLATES
    api.ops['teams.suggest'] = () => ({ template: TEMPLATES.templates[1], matched: true, by: 'rule' })
    api.ops['teams.create'] = (i) => ({ team: team('t9', String(i.name ?? '工程團隊'), 'l', []), lead_id: 'l', member_ids: [], created: ['l'], note: 'ok' })
  })

  it('預設行銷團隊；一句話換成工程；進階改名、移除成員後建立', async () => {
    render(<App />)
    fireEvent.click((await screen.findAllByRole('button', { name: '建立團隊' }))[0])
    const dlg = await screen.findByRole('dialog', { name: /建立團隊/ })
    expect(await within(dlg).findByRole('radio', { name: /行銷團隊/ })).toHaveAttribute('aria-checked', 'true')
    expect(within(dlg).getByRole('button', { name: '建立 3 個 Bot' })).toBeEnabled()
    expect(within(dlg).getByTestId('team-total')).toHaveTextContent('建立後共 6 個 Bot（上限 50）')
    fireEvent.change(within(dlg).getByRole('textbox', { name: '一句話說你要什麼團隊' }), { target: { value: '幫我做網站' } })
    fireEvent.click(within(dlg).getByRole('button', { name: '幫我挑' }))
    await waitFor(() => expect(within(dlg).getByRole('radio', { name: /工程團隊/ })).toHaveAttribute('aria-checked', 'true'))
    expect(within(dlg).getByTestId('team-hint')).toHaveTextContent('建議用「工程團隊」')
    fireEvent.click(within(dlg).getByRole('radio', { name: /行銷團隊/ }))
    fireEvent.click(within(dlg).getByRole('button', { name: /進階設定/ }))
    fireEvent.change(within(dlg).getByRole('textbox', { name: '團隊名稱' }), { target: { value: '社群小隊' } })
    fireEvent.change(within(dlg).getByRole('textbox', { name: '主管名字' }), { target: { value: '阿主' } })
    fireEvent.click(within(dlg).getByRole('button', { name: '移除 社群經營' }))
    fireEvent.click(within(dlg).getByRole('button', { name: '建立 2 個 Bot' }))
    await waitFor(() => expect(api.calls.some((c) => c.path === 'teams.create')).toBe(true))
    const call = api.calls.find((c) => c.path === 'teams.create')!
    expect(call.input).toMatchObject({ template: 'marketing', name: '社群小隊', group_chat: true, lead: { name: '阿主' } })
    expect(call.input.members).toEqual([{ name: '內容企劃', title: '內容', persona: '' }])
  })

  it('超過 50 個 Bot 時不能建立', async () => {
    api.ops['teams.templates'] = () => ({ ...TEMPLATES, bots_total: 49 })
    render(<App />)
    fireEvent.click((await screen.findAllByRole('button', { name: '建立團隊' }))[0])
    const dlg = await screen.findByRole('dialog', { name: /建立團隊/ })
    expect(await within(dlg).findByRole('button', { name: '建立 3 個 Bot' })).toBeDisabled()
    expect(within(dlg).getByText(/先封存不用的 Bot/)).toBeInTheDocument()
  })
})

describe('組織架構（F01）', () => {
  beforeEach(() => {
    window.history.replaceState(null, '', '/org')
    api.ops['teams.chart'] = () => ({ bots: [cbot('ceo', 'CEO'), cbot('dev', 'Dev', { reports_to_id: 'ceo', reports_to: 'CEO' })], teams: [], bots_total: 2, bots_max: 50, team_max: 10 })
    api.ops['teams.approval_items'] = () => ({ items: ['簽約', '上架'], max_items: 20, max_chars: 40 })
    api.ops['teams.set_approval_items'] = (i) => ({ items: i.items, max_items: 20, max_chars: 40 })
    api.ops['teams.set_reports_to'] = (i) => ({ bot_id: i.bot_id, reports_to: i.reports_to ?? '' })
    api.ops['bots.update'] = (i) => bot(String(i.id), 'Dev', { can_manage: !!i.can_manage })
  })

  it('改報告對象、開團隊管理權限、改只有你能決定的事', async () => {
    render(<App />)
    const rows = await screen.findAllByTestId('org-row')
    expect(rows.map((r) => r.textContent)).toEqual([expect.stringContaining('CEO'), expect.stringContaining('Dev')])
    fireEvent.click(rows[1])
    const panel = await screen.findByRole('region', { name: 'Dev 的設定' })
    fireEvent.click(within(panel).getByRole('button', { name: /向誰報告/ }))
    fireEvent.click(await screen.findByRole('option', { name: '你（董事長）' }))
    await waitFor(() => expect(api.calls.find((c) => c.path === 'teams.set_reports_to')?.input).toEqual({ bot_id: 'dev' }))
    fireEvent.click(within(panel).getByRole('switch', { name: '團隊管理權限' }))
    await waitFor(() => expect(api.calls.find((c) => c.path === 'bots.update')?.input).toEqual({ id: 'dev', can_manage: true }))
    const items = await screen.findByTestId('approval-items')
    expect(items).toHaveTextContent('簽約')
    fireEvent.click(within(items).getByRole('button', { name: '移除 簽約' }))
    await waitFor(() => expect(api.calls.find((c) => c.path === 'teams.set_approval_items')?.input).toEqual({ items: ['上架'] }))
  })

  it('CEO 的主管選單不列出自己的下屬', async () => {
    render(<App />)
    fireEvent.click((await screen.findAllByTestId('org-row'))[0])
    const panel = await screen.findByRole('region', { name: 'CEO 的設定' })
    fireEvent.click(within(panel).getByRole('button', { name: /向誰報告/ }))
    const opts = await screen.findAllByRole('option')
    expect(opts.map((o) => o.textContent)).toEqual(['你（董事長）'])
  })
})

const PROPOSAL = {
  id: 'i1', kind: 'proposal', bot_id: 'a', task_id: '', thread_id: '', ref: 'ag1', title: '小秘 提案：把日報改成週報', body: '日報大多沒有新進度',
  payload: { agenda_id: 'ag1', kind: 'propose', due_at: now + 3 * 86_400_000 - 60_000, subject: '把日報改成週報' }, status: 'open', resolution: '', created_at: now, read_at: 0, expires_at: 0,
}
const DISCUSSION = {
  ...PROPOSAL, id: 'i2', kind: 'discussion', ref: 'ag2', title: '帳房 想找你討論：11 月廣告預算', body: '維持還是加碼？',
  payload: { agenda_id: 'ag2', kind: 'discuss', due_at: now + 86_400_000, subject: '11 月廣告預算' },
}

describe('待我處理：提案與討論', () => {
  beforeEach(() => {
    window.history.replaceState(null, '', '/')
    api.ops['bots.list'] = () => ({ bots: [bot('a', '小秘')] })
    api.ops['inbox.list'] = (i) => ({ items: (i.status ?? 'open') === 'open' ? [PROPOSAL, DISCUSSION] : [] })
    api.ops['agenda.decide'] = (i) => ({ id: i.id, bot_id: 'a', bot_name: '小秘', kind: i.id === 'ag1' ? 'propose' : 'discuss', subject: 's', details: '', due_at: now, state: { accept: 'accepted', veto: 'vetoed', answer: 'answered', decline: 'declined' }[String(i.decision)], note: '', created_at: now })
  })

  it('提案：3 天後生效；否決可以寫原因', async () => {
    render(<App />)
    const card = (await screen.findAllByTestId('agenda-item')).find((c) => c.dataset.kind === 'propose')!
    expect(card).toHaveTextContent('3 天後生效')
    expect(card).toHaveTextContent('把日報改成週報')
    fireEvent.click(within(card).getByRole('button', { name: '否決' }))
    fireEvent.change(within(card).getByRole('textbox', { name: '否決的原因（選填）' }), { target: { value: '還要看' } })
    fireEvent.click(within(card).getByRole('button', { name: '確定否決' }))
    await waitFor(() => expect(within(card).getByRole('status')).toHaveTextContent('14 天內不會再提'))
    expect(api.calls.find((c) => c.path === 'agenda.decide')?.input).toEqual({ id: 'ag1', decision: 'veto', note: '還要看' })
  })

  it('討論：直接回覆、改時間', async () => {
    api.ops['agenda.decide'] = (i) => {
      if (i.decision === 'accept' && i.when === 'bad') opError(400, 'invalid_input', '時間格式要像 2026-10-20 15:00')
      return { id: i.id, bot_id: 'a', bot_name: '小秘', kind: 'discuss', subject: 's', details: '', due_at: now + 2 * 86_400_000, state: i.decision === 'answer' ? 'answered' : 'open', note: '', created_at: now }
    }
    render(<App />)
    const card = (await screen.findAllByTestId('agenda-item')).find((c) => c.dataset.kind === 'discuss')!
    const send = within(card).getByRole('button', { name: '送出回覆' })
    expect(send).toBeDisabled()
    fireEvent.click(within(card).getByRole('button', { name: '改時間' }))
    fireEvent.change(within(card).getByLabelText('改到什麼時候（台北時間）'), { target: { value: '2026-10-20T15:00' } })
    fireEvent.click(within(card).getByRole('button', { name: '確定改時間' }))
    await waitFor(() => expect(api.calls.find((c) => c.path === 'agenda.decide')?.input).toEqual({ id: 'ag2', decision: 'accept', when: '2026-10-20 15:00' }))
    fireEvent.change(within(card).getByRole('textbox', { name: '你的回覆' }), { target: { value: '維持就好' } })
    fireEvent.click(send)
    await waitFor(() => expect(within(card).getByRole('status')).toHaveTextContent('已回覆'))
  })
})

describe('等你的事（H04）', () => {
  it('列出等你的、接下來一週、最近處理過的、待你核定', async () => {
    window.history.replaceState(null, '', '/agenda')
    const item = { id: 'ag1', bot_id: 'a', bot_name: '小秘', kind: 'propose', subject: '把日報改成週報', details: '', due_at: now + 86_400_000, state: 'open', note: '', created_at: now }
    api.ops['agenda.list'] = () => ({
      from: now, to: now + 7 * 86_400_000, open_max: 3,
      pending: [item],
      decided: [{ ...item, id: 'ag0', subject: '週末加發一篇', state: 'accepted', decided_at: now }],
      calendar: [{ id: 'agenda:ag1', at: item.due_at, kind: 'propose', title: item.subject, bot_id: 'a', bot_name: '小秘', agenda_id: 'ag1', state: 'open' }],
      approvals: [{ id: 'ap', bot_id: 'a', bot_name: '小秘', subject: '簽約', created_at: now }],
    })
    render(<App />)
    const main = await screen.findByRole('main', { name: '等你的事' })
    expect(await within(main).findByTestId('agenda-item')).toHaveTextContent('把日報改成週報')
    expect(main).toHaveTextContent('每個 Bot 提案＋討論最多 3 件，待你核定另計最多 10 件')
    expect(within(main).getByRole('link', { name: /1 件待你核定/ })).toHaveAttribute('href', '/')
    expect(within(main).getByTestId('agenda-decided')).toHaveTextContent('你同意了')
    expect(screen.getByTestId('agenda-calendar')).toHaveTextContent('小秘 的提案生效：把日報改成週報')
  })
})

describe('對話裡的組織事件行', () => {
  it('建立 Bot、改資料、建團隊、提案、討論都畫成一行', () => {
    const line = (event: string, content: string, extra: Record<string, string> = {}) => {
      const msg = { id: event, sender: 'bot:a', kind: 'peer', content, createdAt: now, peer: { event, ...extra } } as unknown as ChatMsg
      const { container, unmount } = render(<PeerCard msg={msg} threadId="t" />)
      const el = container.querySelector('[data-testid="peer-line"]')
      const out = { text: el?.textContent, href: el?.querySelector('a')?.getAttribute('href') }
      unmount()
      return out
    }
    expect(line('bot_created', '設計師', { peer_id: 'b9' })).toEqual({ text: '建立了 Bot「設計師」', href: '/bots/b9' })
    expect(line('profile_updated', '更新了職稱')).toEqual({ text: '更新了職稱', href: undefined })
    expect(line('team_created', '行銷團隊', { room_id: 'r1' })).toEqual({ text: '建立了團隊「行銷團隊」', href: '/rooms/r1' })
    expect(line('proposal', '改成週報')).toEqual({ text: '送出提案：改成週報', href: '/agenda' })
    expect(line('discussion', '下季先做哪個')).toEqual({ text: '想找你討論：下季先做哪個', href: '/agenda' })
    // Bot 改了群組（xbot.collab.update_group）
    expect(line('group_updated', '改了群組「發表會」：名稱、加入成員', { room_id: 'r2' })).toEqual({ text: '改了群組「發表會」：名稱、加入成員', href: '/rooms/r2' })
  })
})
