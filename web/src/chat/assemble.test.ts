import { describe, expect, it } from 'vitest'
import type { ChatReadOutput, XBotEvent } from '../api/generated'
import { activeTask, applyEvent, applyRead, emptyChat, formatElapsed, threadItems, workSummary, type ChatState } from './assemble'

const T = 'th1'
const TASK = 'task1'
const BOT = 'b1'

function msgCreated(id: number, m: { id: string; seq: number; sender: string; content?: string; meta?: object }, at = 1000): XBotEvent {
  return {
    id,
    kind: 'message.created',
    bot_id: BOT,
    task_id: TASK,
    at,
    payload: { id: m.id, seq: m.seq, scope: 'thread', scope_id: T, parent_id: '', sender: m.sender, kind: 'text', content: m.content ?? '', attachments: [], meta: m.meta ?? {}, created_at: at, updated_at: at },
  }
}

const text = (t: string, at = 2000): XBotEvent => ({ kind: 'chat.text', bot_id: BOT, thread_id: T, task_id: TASK, at, payload: { message_id: 'm2', text: t } })
const state = (id: number, st: string, at: number, verdict?: string): XBotEvent => ({ id, kind: 'task.state', bot_id: BOT, thread_id: T, task_id: TASK, at, payload: { state: st, verdict } })
const tool = (id: number, kind: 'chat.tool_call' | 'chat.tool_result', toolId: string, at: number, extra: object = {}): XBotEvent => ({
  id,
  kind,
  bot_id: BOT,
  thread_id: T,
  task_id: TASK,
  at,
  payload: { type: kind === 'chat.tool_call' ? 'tool_call' : 'tool_result', tool: { id: toolId, name: 'browser_open', ...extra } },
})
const done = (id: number, at: number, finalText: string, status = 'complete', verdict = 'answer'): XBotEvent => ({
  id,
  kind: 'chat.done',
  bot_id: BOT,
  thread_id: T,
  task_id: TASK,
  at,
  payload: { message_id: 'm2', status, verdict, text: finalText },
})

function run(events: XBotEvent[], s: ChatState = emptyChat): ChatState {
  return events.reduce(applyEvent, s)
}

// 一輪完整的事件：你的訊息 → 開機 → Bot 回覆串流 → 一個工具 → 完成
const turn: XBotEvent[] = [
  msgCreated(1, { id: 'm1', seq: 1, sender: 'owner', content: '幫我比價', meta: { task_id: TASK } }, 1000),
  state(2, 'waiting_box', 1001),
  msgCreated(3, { id: 'm2', seq: 2, sender: 'bot:b1', meta: { task_id: TASK, streaming: true } }, 1500),
  state(4, 'running', 1600),
  { kind: 'chat.thinking', bot_id: BOT, thread_id: T, task_id: TASK, at: 1700, payload: { message_id: 'm2', text: '…' } },
  tool(5, 'chat.tool_call', 't1', 1800, { context: '打開蝦皮' }),
  tool(6, 'chat.tool_result', 't1', 1900, { ok: true }),
  text('好，'),
  text('比完了。'),
]

describe('事件組成對話', () => {
  // 2026-10-19 實際遇到：工作早就結束，但重新整理後畫面一直顯示「排隊中 · 30 分」。
  // 原因：補讀的步驟只讀到舊的 queued／running，沒讀到最後的 done。chat.read 的 tasks 說了算，舊狀態也不能蓋掉。
  it('服務說工作做完了：補讀到的舊狀態不會把它變回排隊中', () => {
    const read = {
      thread: { id: T, bot_id: 'b1', title: '', pinned: false, archived: false, unread: 0, last_message_at: 1, created_at: 1 },
      messages: [{ id: 'm1', seq: 1, sender: 'owner', kind: 'text', content: '/goal 有這個技能嗎', task_id: 'k1', created_at: 1000, updated_at: 1000 }],
      has_more: false,
      tasks: { k1: 'done' },
    } as unknown as ChatReadOutput
    let s = applyRead(emptyChat, read)
    expect(activeTask(s, T)).toBeUndefined()
    s = applyEvent(s, { id: 1, kind: 'task.state', bot_id: 'b1', thread_id: T, task_id: 'k1', at: 1000, payload: { state: 'queued' } })
    s = applyEvent(s, { id: 2, kind: 'task.state', bot_id: 'b1', thread_id: T, task_id: 'k1', at: 1001, payload: { state: 'running' } })
    expect(activeTask(s, T)).toBeUndefined()
    expect(s.tasks.k1.state).toBe('done')
  })

  it('文字增量依序接起來，Bot 的回覆前面接著工作進度', () => {
    const s = run(turn)
    const items = threadItems(s, T)
    expect(items.map((i) => (i.type === 'message' ? i.msg.content : 'activity'))).toEqual(['幫我比價', '好，比完了。'])
    const reply = items[1]
    expect(reply.type === 'message' && reply.msg.streaming).toBe(true)
    expect(reply.type === 'message' && reply.activity?.steps).toEqual([
      { id: 't1', name: 'browser_open', label: '打開蝦皮', status: 'ok', at: 1800, endAt: 1900 },
    ])
    expect(activeTask(s, T)?.state).toBe('running')
  })

  it('還沒有回覆訊息時（開機中），工作進度接在最後，且算思考中', () => {
    const s = run(turn.slice(0, 2))
    const items = threadItems(s, T)
    expect(items).toHaveLength(2)
    expect(items[1]).toMatchObject({ type: 'activity', activity: { state: 'waiting_box' } })
  })

  it('thinking 之後收到文字就不再顯示思考中', () => {
    const s1 = run(turn.slice(0, 5))
    expect(s1.tasks[TASK].thinking).toBe(true)
    const s2 = applyEvent(s1, text('嗨'))
    expect(s2.tasks[TASK].thinking).toBe(false)
  })

  it('chat.done 用完整的最後回覆取代串流內容，並結束工作', () => {
    const s = run([...turn, state(7, 'done', 61_000, 'answer'), done(8, 61_000, '好，比完了。三家都比過。')])
    const reply = threadItems(s, T)[1]
    expect(reply.type === 'message' && reply.msg).toMatchObject({ content: '好，比完了。三家都比過。', streaming: false, status: 'complete' })
    expect(activeTask(s, T)).toBeUndefined()
    expect(s.tasks[TASK]).toMatchObject({ state: 'done', verdict: 'answer', startedAt: 1000, endedAt: 61_000 })
    expect(workSummary(s.tasks[TASK])).toBe('工作了 1 分鐘 · 1 步')
  })

  it('斷線重連補送的事件不會重複（步驟、訊息都只有一份）', () => {
    const s1 = run(turn)
    // 重連後伺服器從 last_event_id 之前一點開始補送：已處理過的 id 會略過
    const replay = turn.filter((e) => e.id && e.id >= 3)
    const s2 = run([...replay, tool(9, 'chat.tool_call', 't2', 2100), tool(10, 'chat.tool_result', 't2', 2200, { ok: false })], s1)
    const reply = threadItems(s2, T)[1]
    expect(reply.type === 'message' && reply.msg.content).toBe('好，比完了。')
    expect(threadItems(s2, T)).toHaveLength(2)
    expect(s2.tasks[TASK].steps.map((x) => [x.id, x.status])).toEqual([
      ['t1', 'ok'],
      ['t2', 'fail'],
    ])
  })

  it('補送的事件沒有 thread_id 時，靠 task_id 對回對話串', () => {
    const s1 = run(turn.slice(0, 3))
    const noThread: XBotEvent = { id: 20, kind: 'chat.done', bot_id: BOT, task_id: TASK, at: 5000, payload: { message_id: 'm2', status: 'error', verdict: 'failed', text: '工作未完成：開不了機' } }
    const s2 = applyEvent(s1, noThread)
    const reply = threadItems(s2, T)[1]
    expect(reply.type === 'message' && reply.msg).toMatchObject({ content: '工作未完成：開不了機', status: 'error', streaming: false })
  })

  it('chat.error 記在工作上', () => {
    const s = run([...turn.slice(0, 4), { id: 30, kind: 'chat.error', bot_id: BOT, thread_id: T, task_id: TASK, at: 3000, payload: { type: 'error', text: '模型沒有回應', fatal: true } }])
    expect(s.tasks[TASK].error).toBe('模型沒有回應')
  })

  it('串流到一半才打開：先收到的片段先放著，chat.read 的快照比較短時保留本地', () => {
    const s1 = applyEvent(emptyChat, text('前半'))
    expect(threadItems(s1, T)).toHaveLength(1)
    const read: ChatReadOutput = {
      thread: { id: T, bot_id: BOT, title: '', pinned: false, archived: false, unread: 0, last_message_at: 0, created_at: 0 },
      messages: [
        { id: 'm1', seq: 1, sender: 'owner', kind: 'text', content: '幫我比價', task_id: TASK, created_at: 1000, updated_at: 1000 },
        { id: 'm2', seq: 2, sender: 'bot:b1', kind: 'text', content: '', task_id: TASK, streaming: true, created_at: 1500, updated_at: 1500 },
      ],
      has_more: false,
    }
    const s2 = applyRead(s1, read)
    const items = threadItems(s2, T)
    expect(items.map((i) => i.type === 'message' && i.msg.content)).toEqual(['幫我比價', '前半'])
    expect(activeTask(s2, T)?.state).toBe('running')
  })

  it('chat.read 的完成快照：已結束的工作不算進行中，花多久從訊息時間算', () => {
    const read: ChatReadOutput = {
      thread: { id: T, bot_id: BOT, title: '', pinned: false, archived: false, unread: 0, last_message_at: 0, created_at: 0 },
      messages: [
        { id: 'm1', seq: 1, sender: 'owner', kind: 'text', content: '嗨', task_id: TASK, created_at: 1000, updated_at: 1000 },
        { id: 'm2', seq: 2, sender: 'bot:b1', kind: 'text', content: '你好', task_id: TASK, status: 'complete', created_at: 1500, updated_at: 4000 },
      ],
      has_more: false,
    }
    const s = applyRead(emptyChat, read)
    expect(activeTask(s, T)).toBeUndefined()
    expect(s.tasks[TASK]).toMatchObject({ state: 'done', verdict: 'answer', startedAt: 1000, endedAt: 4000 })
    // 本地已經收到 chat.done，再讀到舊的串流快照也不會倒退
    const s2 = run([...turn, done(8, 9000, '最後的回覆')])
    const s3 = applyRead(s2, { ...read, messages: [read.messages[0], { ...read.messages[1], content: '好，', streaming: true, status: undefined }] })
    const reply = threadItems(s3, T)[1]
    expect(reply.type === 'message' && reply.msg.content).toBe('最後的回覆')
  })

  it('只有你的訊息、看不出工作狀態時，不顯示進行中', () => {
    const read: ChatReadOutput = {
      thread: { id: T, bot_id: BOT, title: '', pinned: false, archived: false, unread: 0, last_message_at: 0, created_at: 0 },
      messages: [{ id: 'm1', seq: 1, sender: 'owner', kind: 'text', content: '嗨', task_id: TASK, created_at: 1000, updated_at: 1000 }],
      has_more: false,
    }
    const s = applyRead(emptyChat, read)
    expect(activeTask(s, T)).toBeUndefined()
    expect(threadItems(s, T)).toHaveLength(1)
    // 之後收到 task.state 就知道了
    expect(activeTask(applyEvent(s, state(50, 'running', 2000)), T)?.state).toBe('running')
  })
})

describe('時間格式', () => {
  it('經過時間', () => {
    expect(formatElapsed(40_000)).toBe('40 秒')
    expect(formatElapsed(72_000)).toBe('1 分 12 秒')
    expect(formatElapsed(120_000)).toBe('2 分')
  })
})
