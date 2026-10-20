import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { fakeApi, type FakeApi } from '../test-api'
import { ToastProvider } from '../ui/Toast'
import { LessonCard } from './LessonCard'
import { LessonsBlock } from './LessonsBlock'

const BOT = { name: '水哥哥', shape: 'cursor', color: 'blue' }
const card = (payload: Record<string, unknown>) =>
  ({
    id: 'i1',
    kind: 'lesson',
    status: 'open',
    bot_id: 'b1',
    task_id: 't1',
    title: '水哥哥學到一件事',
    body: payload.lesson,
    payload,
    created_at: 1,
  }) as unknown as Parameters<typeof LessonCard>[0]['item']

let api: FakeApi
let lessons: { all: { id: string; text: string }[]; bot: { id: string; text: string }[] }
beforeEach(() => {
  lessons = { all: [{ id: 'a1', text: '查資料先用 web_search' }], bot: [] }
  api = fakeApi({
    'lessons.decide': () => ({}),
    'lessons.list': () => ({ ...lessons, max: 12 }),
    'lessons.add': (i) => {
      const l = { id: 'n' + lessons.bot.length, text: String(i.text) }
      lessons.bot.push(l)
      return l
    },
    'lessons.delete': (i) => {
      lessons.all = lessons.all.filter((l) => l.id !== i.id)
      return { deleted: true }
    },
  })
})
afterEach(() => vi.unstubAllGlobals())

const wrap = (el: React.ReactNode) => render(<ToastProvider>{el}</ToastProvider>)

describe('學到一件事的卡', () => {
  it('照卡片上的字加給這個 Bot；沒改字就不送 text', async () => {
    wrap(<LessonCard item={card({ lesson: '交出前先檢查檔案真的存在', why: '說做完了但檔案沒產生' })} bot={BOT} />)
    expect(screen.getByText('為什麼：說做完了但檔案沒產生')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: '加給水哥哥' }))
    await waitFor(() => expect(api.calls.some((c) => c.path === 'lessons.decide')).toBe(true))
    const c = api.calls.find((c) => c.path === 'lessons.decide')!
    expect(c.input).toEqual({ inbox_id: 'i1', apply: 'bot' })
  })

  it('改過的字一起送；有技能時主按鈕是加進技能', async () => {
    wrap(<LessonCard item={card({ lesson: '先列出要查的網站', skill: 'deep-research' })} bot={BOT} />)
    expect(screen.getByText('技能可以改')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: '改字' }))
    fireEvent.change(screen.getByRole('textbox', { name: '守則' }), { target: { value: '先列出要查的三個網站' } })
    fireEvent.click(screen.getByRole('button', { name: '加進技能「deep-research」' }))
    await waitFor(() => expect(api.calls.some((c) => c.path === 'lessons.decide')).toBe(true))
    expect(api.calls.find((c) => c.path === 'lessons.decide')!.input).toEqual({ inbox_id: 'i1', apply: 'skill', text: '先列出要查的三個網站' })
  })
})

describe('記憶分頁的「學到的事」', () => {
  it('列出給所有 Bot 的；可以加一條給這個 Bot、刪掉一條', async () => {
    wrap(<LessonsBlock botId="b1" botName="水哥哥" />)
    expect(await screen.findByText('查資料先用 web_search')).toBeTruthy()
    expect(screen.getByText('所有 Bot')).toBeTruthy()
    fireEvent.change(screen.getByRole('textbox', { name: '自己加一條' }), { target: { value: '寄信前先給我看' } })
    fireEvent.click(screen.getByRole('button', { name: '加上' }))
    expect(await screen.findByText('寄信前先給我看')).toBeTruthy()
    expect(api.calls.find((c) => c.path === 'lessons.add')!.input).toEqual({ bot_id: 'b1', text: '寄信前先給我看' })
    fireEvent.click(screen.getByRole('button', { name: '刪掉「查資料先用 web_search」' }))
    await waitFor(() => expect(screen.queryByText('查資料先用 web_search')).toBeNull())
    expect(api.calls.find((c) => c.path === 'lessons.delete')!.input).toEqual({ id: 'a1' })
  })
})
