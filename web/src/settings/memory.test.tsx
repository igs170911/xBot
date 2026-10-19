import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { fakeApi, type FakeApi } from '../test-api'
import { ToastProvider } from '../ui/Toast'
import { MemoryTab } from './BotCapabilities'
import { changeKind, entryOrigin, originLink, type MemChange } from './memoryHistory'

const BOT = { id: 'b1', name: '水哥哥', title: '', description: '', soul: '', color: 'blue', shape: 'cursor', eyes: '', engine: 'hermes', model: '', fallback: '', effort: '', approval_mode: 'ask', memory_enabled: true, archived: false, primary: false, created_at: 1, updated_at: 1 } as unknown as Parameters<typeof MemoryTab>[0]['bot']

const WRONG = 'xbot 工具沒有刪除 Bot／團隊的能力'
const at = new Date(2026, 9, 18, 14, 30).getTime()

const change = (c: Partial<MemChange>): MemChange => ({ id: 'c', target: 'memory', actor: 'owner', added: [], removed: [], created_at: at, undoable: false, ...c })

let api: FakeApi
let changes: MemChange[]
beforeEach(() => {
  changes = [
    change({ id: 'c3', target: 'user', actor: 'bot:b1', added: [], removed: [], undoable: true }),
    change({ id: 'c2', actor: 'owner', added: ['客戶 A 用月結（含運費）'], removed: ['客戶 A 用月結'] }),
    change({ id: 'c1', actor: 'bot:b1', added: [WRONG], thread_id: 't9', thread_title: '整理客戶名單' }),
  ]
  const list = (entries: string[], limit: number) => ({ entries, used: entries.join('\n§\n').length, limit })
  api = fakeApi({
    'memory.read': () => ({ user: list(['喜歡簡短回答'], 1375), memory: list([WRONG, '客戶 A 用月結（含運費）'], 2200), enabled: true, computer: true }),
    'memory.history': () => ({ changes }),
    'memory.write': (i) => ({ user: list(['喜歡簡短回答'], 1375), memory: list(i.entries as string[], 2200), enabled: true, computer: true }),
  })
  window.history.replaceState(null, '', '/settings/bots/b1/abilities/memory')
})
afterEach(() => vi.unstubAllGlobals())

const tab = (bot = BOT) =>
  render(
    <ToastProvider>
      <MemoryTab bot={bot} onBot={() => {}} />
    </ToastProvider>,
  )

describe('記憶的變更', () => {
  it('新增、拿掉都沒有的是「整理」，不是刪除', () => {
    expect(changeKind({ added: [], removed: [] })).toBe('整理')
    expect(changeKind({ added: ['a'], removed: [] })).toBe('新增')
    expect(changeKind({ added: [], removed: ['a'] })).toBe('刪除')
    expect(changeKind({ added: ['b'], removed: ['a'] })).toBe('修改')
  })

  it('每一條找最新那次加進來的變更；連到對話串或群組', () => {
    const cs = [change({ id: 'new', added: ['x'] }), change({ id: 'old', added: ['x'] }), change({ id: 'u', target: 'user', added: ['y'] })]
    expect(entryOrigin('x', 'memory', cs)?.id).toBe('new')
    expect(entryOrigin('y', 'memory', cs)).toBeUndefined()
    expect(originLink({ thread_id: 't1' }, 'b1')).toBe('/bots/b1/threads/t1')
    expect(originLink({ thread_id: 't1', room_id: 'r1' }, 'b1')).toBe('/rooms/r1')
    expect(originLink({}, 'b1')).toBeUndefined()
  })
})

describe('記憶分頁（G06）', () => {
  it('每一條顯示誰在哪條對話串記的；修改顯示舊 → 新；只調整順序不顯示紅色的「刪除」', async () => {
    tab()
    const facts = await screen.findByRole('group', { name: 'Bot 記住的事' })
    const full = (text: string) => (_: string, el: Element | null) => el?.tagName === 'SPAN' && el.textContent === text
    expect(await within(facts).findByText(full('水哥哥 · 10/18 在「整理客戶名單」中記下'))).toBeInTheDocument()
    const link = within(facts).getByRole('link', { name: '整理客戶名單' })
    expect(link).toHaveAttribute('href', '/bots/b1/threads/t9')
    expect(within(facts).getByText('你 · 10/18 修改')).toBeInTheDocument()

    const recent = screen.getByRole('group', { name: '最近的變更' })
    expect(within(recent).queryByText('刪除')).toBeNull()
    expect(within(recent).getByText('整理')).toBeInTheDocument()
    expect(within(recent).getByText('內容沒變，只調整了順序或重複的條目')).toBeInTheDocument()
    expect(within(recent).getByText('客戶 A 用月結')).toHaveClass('line-through')
    expect(within(recent).getByText('→ 客戶 A 用月結（含運費）')).toBeInTheDocument()
  })

  it('刪掉記錯的一條：提示新的對話串才會用到，可以直接開新對話串', async () => {
    tab()
    const facts = await screen.findByRole('group', { name: 'Bot 記住的事' })
    fireEvent.click((await within(facts).findAllByRole('button', { name: '刪除這一條' }))[0])
    await waitFor(() => expect(api.calls.find((c) => c.path === 'memory.write')?.input.entries).toEqual(['客戶 A 用月結（含運費）']))
    expect(await screen.findByText('已刪除一條。新的對話串才會用到')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: '開新對話串' }))
    expect(window.location.pathname).toBe('/bots/b1/threads/new')
  })

  it('不是 Hermes 的 Bot：說明不會用到這裡的記憶，不去讀它的電腦', async () => {
    tab({ ...BOT, engine: 'claude-code' })
    expect(await screen.findByText(/水哥哥用 Claude Code 引擎，不會讀這裡的記憶/)).toBeInTheDocument()
    expect(screen.getByRole('link', { name: '換引擎' })).toHaveAttribute('href', '/settings/bots/b1/advanced')
    expect(api.calls.some((c) => c.path.startsWith('memory.'))).toBe(false)
  })
})
