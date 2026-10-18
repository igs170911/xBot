import { useCallback, useEffect, useLayoutEffect, useRef, useState, type ReactNode } from 'react'
import { ChevronDown, CircleX, Cpu, Monitor, MessageSquare, OctagonPause, Plus, ShieldCheck, X } from 'lucide-react'
import {
  ApiError,
  chatListThreads,
  chatSend,
  chatStop,
  modelsGet,
  type ChatListThreadsOutput,
  type ModelsGetOutput,
} from '../api/generated'
import { botsStore, type Bot } from '../data/bots'
import { onReconnect } from '../data/events'
import { useStore } from '../data/store'
import { stampLabel } from '../data/ui'
import { navigate } from '../router'
import { Avatar } from '../ui/Avatar'
import { Button, IconButton } from '../ui/Button'
import { Icon } from '../ui/Icon'
import { MenuButton, type MenuItem } from '../ui/Menu'
import { cx } from '../ui/cx'
import { ModelSettingsDialog } from '../pages/ModelSettings'
import { activeTask, isActive, threadItems, type Activity, type ChatItem, type ChatMsg } from './assemble'
import { ChatComposer, type ComposerMode } from './ChatComposer'
import { chatStore, loadThread } from './chatStore'
import { WorkSteps } from './WorkSteps'

type Thread = ChatListThreadsOutput['threads'][number]

const APPROVAL_LABEL: Record<string, string> = { ask: '先問我', edits: '一般的事自己做', auto: '放手做', full: '完全放手' }

const GAP = 30 * 60 * 1000

// ── 訊息 ─────────────────────────────────────────────

function Stamp({ at }: { at: number }) {
  return <span className="self-center pt-1.5 text-meta text-ink-3">{stampLabel(at)}</span>
}

function Bubble({ msg }: { msg: ChatMsg }) {
  const mine = msg.sender === 'owner'
  return (
    <div
      data-testid={mine ? 'msg-owner' : 'msg-bot'}
      data-streaming={msg.streaming || undefined}
      className={cx(
        'whitespace-pre-wrap break-words rounded-bubble px-4 py-2.5 text-[15px] leading-[1.6]',
        mine ? 'max-w-[75%] self-end bg-bubble-user text-white' : 'max-w-[85%] self-start bg-bubble-bot',
      )}
    >
      {msg.content}
    </div>
  )
}

function OutcomeRow({ msg, activity }: { msg: ChatMsg; activity?: Activity }) {
  if (msg.status === 'error') {
    return (
      <div className="-mt-1 flex flex-wrap items-center gap-2">
        <span className="inline-flex h-7 items-center gap-1.5 rounded-full bg-control pl-2.5 pr-3 text-[13px] text-danger">
          <Icon icon={CircleX} size={14} />
          工作未完成
        </span>
        {activity?.error && activity.error !== msg.content && <span className="text-meta text-ink-3">{activity.error}</span>}
      </div>
    )
  }
  if (msg.status === 'interrupted') {
    return (
      <div className="-mt-1 flex flex-wrap items-center gap-2">
        <span className="inline-flex h-7 items-center gap-1.5 rounded-full bg-control pl-2.5 pr-3 text-[13px] text-warning-ink">
          <Icon icon={OctagonPause} size={14} />
          已停止
        </span>
        <span className="text-meta text-ink-3">做到一半的檔案都還在</span>
      </div>
    )
  }
  return null
}

function Messages({ items, bot }: { items: ChatItem[]; bot?: Bot }) {
  const out: ReactNode[] = []
  let last = 0
  for (const it of items) {
    if (it.type === 'activity') {
      out.push(<WorkSteps key={'a' + it.activity.taskId} a={it.activity} bot={bot} />)
      continue
    }
    const m = it.msg
    if (m.createdAt && m.seq !== Number.MAX_SAFE_INTEGER && (!last || m.createdAt - last > GAP)) out.push(<Stamp key={'t' + m.id} at={m.createdAt} />)
    if (m.createdAt) last = m.createdAt
    // 進行中：進度接在回覆下面（C01，最新的動態靠近輸入框）；做完：摺疊的步驟放在回答上面（C02）
    const live = it.activity && isActive(it.activity)
    if (it.activity && !live) out.push(<WorkSteps key={'a' + it.activity.taskId} a={it.activity} bot={bot} />)
    if (m.content || (!m.streaming && m.sender === 'owner')) out.push(<Bubble key={m.id} msg={m} />)
    if (it.activity && live) out.push(<WorkSteps key={'a' + it.activity.taskId} a={it.activity} bot={bot} />)
    out.push(<OutcomeRow key={'o' + m.id} msg={m} activity={it.activity} />)
  }
  return <>{out}</>
}

// ── 空狀態（design/C03）──────────────────────────────

function Intro({ bot }: { bot: Bot }) {
  const sub = [bot.title, bot.description].filter(Boolean).join(' · ')
  return (
    <div className="flex flex-col items-center gap-1.5 pb-3 text-center" data-testid="chat-empty">
      <Avatar shape={bot.shape} color={bot.color} state="happy" dot="none" size={72} />
      <h1 className="m-0 mt-1.5 text-[24px] leading-[1.35]">{bot.name}</h1>
      {sub && <span className="text-ui-sm text-ink-2">{sub}</span>}
      <span className="mt-1.5 flex flex-wrap justify-center gap-1.5 text-meta text-ink-2">
        <span className="inline-flex h-[26px] items-center gap-1.5 rounded-full border border-line px-2.5">
          <Icon icon={ShieldCheck} size={14} />
          {APPROVAL_LABEL[bot.approval_mode] ?? bot.approval_mode}
        </span>
        <span className="inline-flex h-[26px] items-center gap-1.5 rounded-full border border-line px-2.5">
          <Icon icon={Monitor} size={14} />
          這台主機 · 第一次交辦時開機
        </span>
      </span>
    </div>
  )
}

// ── 提示卡 ───────────────────────────────────────────

function NoModelCard({ name, onSetup }: { name: string; onSetup: () => void }) {
  return (
    <section aria-label="還沒設定模型" className="flex flex-wrap items-center gap-x-4 gap-y-3 rounded-card bg-card px-[18px] py-3.5">
      <span className="flex size-9 flex-none items-center justify-center rounded-full bg-warning-soft text-warning-ink">
        <Icon icon={Cpu} size={18} />
      </span>
      <div className="flex min-w-0 flex-[999_1_240px] flex-col">
        <span className="text-body">還沒設定模型</span>
        <span className="text-sub text-ink-2">{name}要有模型才能回話。設定一次，所有 Bot 都能用。</span>
      </div>
      <Button variant="primary" onClick={onSetup}>
        設定模型
      </Button>
    </section>
  )
}

type Notice = { kind: 'busy' | 'error'; text: string }

function NoticeRow({ notice, onStop, onClose }: { notice: Notice; onStop: () => void; onClose: () => void }) {
  return (
    <div role="alert" className="flex flex-wrap items-center gap-2 rounded-menu bg-card py-1.5 pl-3.5 pr-1.5 text-ui-sm">
      <span className={cx('min-w-0 flex-1', notice.kind === 'error' ? 'text-danger' : 'text-warning-ink')}>{notice.text}</span>
      {notice.kind === 'busy' && (
        <Button size="sm" onClick={onStop}>
          停止它
        </Button>
      )}
      <IconButton icon={X} label="關閉提示" size="sm" onClick={onClose} />
    </div>
  )
}

// ── 頁首 ─────────────────────────────────────────────

function statusOf(bot: Bot, act: Activity | undefined, busyElsewhere: boolean): { text: string; color?: string; pulse?: boolean } {
  if (act?.state === 'stopping') return { text: '正在停止', color: '#5A5A5A' }
  if (act?.state === 'waiting_input' || act?.state === 'waiting_approval') return { text: '等你回覆', color: 'var(--color-warning)' }
  if (act?.state === 'waiting_box') return { text: '開機中', color: 'var(--color-accent)', pulse: true }
  if (act) return { text: '工作中', color: 'var(--color-accent)', pulse: true }
  if (busyElsewhere) return { text: '工作中 · 另一個對話串', color: 'var(--color-accent)' }
  return { text: bot.archived ? '已封存' : '待命' }
}

function Header({
  bot,
  act,
  busyElsewhere,
  threads,
  threadId,
  model,
  onModel,
}: {
  bot: Bot
  act?: Activity
  busyElsewhere: boolean
  threads: Thread[] | null
  threadId?: string
  model: string
  onModel: () => void
}) {
  const st = statusOf(bot, act, busyElsewhere)
  const current = threads?.find((t) => t.id === threadId)
  const items: MenuItem[] = [
    ...(threads ?? []).map((t) => ({ label: (t.id === threadId ? '✓ ' : '') + (t.title || '新的對話'), icon: MessageSquare, onSelect: () => navigate(`/bots/${bot.id}/threads/${t.id}`) })),
    { label: '新對話串', icon: Plus, separator: !!threads?.length, onSelect: () => navigate(`/bots/${bot.id}/threads/new`) },
  ]
  return (
    <header className="flex h-14 flex-none items-center gap-2 pl-5 pr-3 max-md:h-12 max-md:pl-3">
      <span className="flex items-center gap-2 max-md:hidden">
        <Avatar shape={bot.shape} color={bot.color} state={act ? 'working' : 'idle'} dot="none" ring="#070707" size={26} />
        <span className="text-[16px]">{bot.name}</span>
      </span>
      <span className="flex items-center gap-1.5 whitespace-nowrap text-[13px] text-ink-3" data-testid="bot-status">
        {st.color && <span className={cx('size-1.5 rounded-full', st.pulse && 'animate-pulse')} style={{ background: st.color }} />}
        {st.text}
      </span>
      <span className="mx-1 h-5 w-px bg-line max-sm:hidden" />
      <MenuButton
        label="對話串"
        items={items}
        trigger={(p) => (
          <button
            {...p}
            type="button"
            className="relative flex h-8 min-w-0 max-w-[220px] items-center gap-1.5 rounded-full border border-line bg-transparent pl-3 pr-2 text-sub text-ink touch-target hover:bg-control"
          >
            <Icon icon={MessageSquare} size={16} className="text-ink-2" />
            <span className="truncate">{threadId ? current?.title || '對話' : '新對話串'}</span>
            {!!threads?.length && <span className="text-[12px] text-ink-3">{threads.length}</span>}
            <Icon icon={ChevronDown} size={16} className="text-ink-2" />
          </button>
        )}
      />
      <span className="flex-1" />
      <button
        type="button"
        onClick={onModel}
        aria-label={`模型：${model}（設定）`}
        className="relative flex h-8 min-w-0 max-w-[200px] items-center gap-1.5 whitespace-nowrap rounded-full bg-card pl-3.5 pr-2.5 text-sub text-ink touch-target hover:bg-control max-sm:hidden"
      >
        <span className="truncate">{model}</span>
        <Icon icon={ChevronDown} size={16} className="text-ink-2" />
      </button>
    </header>
  )
}

// ── 對話頁 ───────────────────────────────────────────

function useThreads(botId: string) {
  const [threads, setThreads] = useState<Thread[] | null>(null)
  const reload = useCallback(
    () =>
      chatListThreads({ bot_id: botId })
        .then((o) => setThreads(o.threads))
        .catch(() => setThreads((t) => t ?? [])),
    [botId],
  )
  // ChatPage 以 Bot id 當 key：換 Bot 時整個重來，這裡不用先清空
  useEffect(() => {
    void reload()
  }, [reload])
  return [threads, reload] as const
}

/** 對話頁（design/C01～C03）：/bots/:id 與 /bots/:id/threads/:threadId（new 表示開新的對話串）。 */
export function ChatPage({ botId, threadParam }: { botId: string; threadParam?: string }) {
  const { bots, activity } = useStore(botsStore)
  const bot = bots?.find((b) => b.id === botId)
  const [threads, reloadThreads] = useThreads(botId)
  const chat = useStore(chatStore)
  const [models, setModels] = useState<ModelsGetOutput | null>(null)
  const [modelDialog, setModelDialog] = useState(false)
  const [noticeState, setNoticeState] = useState<{ n: Notice; thread: string } | null>(null)
  const [stopRequested, setStopRequested] = useState<string | null>(null)
  const scroller = useRef<HTMLDivElement>(null)
  const stick = useRef(true)

  const isNew = threadParam === 'new'
  const threadId = isNew ? undefined : (threadParam ?? threads?.[0]?.id)
  // 提示只屬於當下這條對話串：換對話串就不再顯示
  const here = threadId ?? 'new'
  const notice = noticeState?.thread === here ? noticeState.n : null
  const setNotice = useCallback((n: Notice | null) => setNoticeState(n ? { n, thread: here } : null), [here])

  // /bots/:id 直接帶到最近的對話串
  useEffect(() => {
    if (!threadParam && threads?.length) navigate(`/bots/${botId}/threads/${threads[0].id}`, { replace: true })
  }, [threadParam, threads, botId])

  useEffect(() => {
    modelsGet({})
      .then(setModels)
      .catch(() => setModels(null))
  }, [])

  useEffect(() => {
    if (!threadId) return
    const load = () => loadThread(threadId).catch((err: unknown) => setNotice({ kind: 'error', text: err instanceof Error ? err.message : String(err) }))
    void load()
    return onReconnect(() => void load())
  }, [threadId, setNotice])

  const items = threadId ? threadItems(chat, threadId) : []
  const act = threadId ? activeTask(chat, threadId) : undefined
  const botAct = activity[botId]
  const busyElsewhere = !act && !!botAct && botAct.state !== 'done' && botAct.threadId !== threadId
  const stopping = act?.state === 'stopping' || (!!act && stopRequested === act.taskId)
  const mode: ComposerMode = act ? (stopping ? 'stopping' : 'working') : 'idle'
  const noModel = models !== null && !models.ready && !bot?.model
  const name = bot?.name ?? 'Bot'

  // 新內容進來時，如果本來就在最下面，就跟著捲到底
  const lastLen = items.length ? (items[items.length - 1].type === 'message' ? (items[items.length - 1] as { msg: ChatMsg }).msg.content.length : 0) : 0
  useLayoutEffect(() => {
    const el = scroller.current
    if (el && stick.current) el.scrollTop = el.scrollHeight
  }, [items.length, lastLen, act?.steps.length, noModel, notice])

  const onScroll = () => {
    const el = scroller.current
    if (el) stick.current = el.scrollHeight - el.scrollTop - el.clientHeight < 80
  }

  const send = async (text: string): Promise<boolean> => {
    setNotice(null)
    stick.current = true
    try {
      const out = await chatSend({ bot_id: botId, text, thread_id: threadId })
      if (out.thread_id !== threadId) {
        void reloadThreads()
        navigate(`/bots/${botId}/threads/${out.thread_id}`, { replace: isNew || !threadParam })
      }
      return true
    } catch (err) {
      if (err instanceof ApiError && err.code === 'conflict' && err.retryable) {
        setNotice({ kind: 'busy', text: `${name}正在忙另一件事，等它做完或先停止。` })
      } else if (err instanceof ApiError && err.code === 'conflict') {
        const m = await modelsGet({}).catch(() => null)
        setModels(m)
        if (!m || m.ready) setNotice({ kind: 'error', text: err.message })
      } else {
        setNotice({ kind: 'error', text: err instanceof Error ? err.message : String(err) })
      }
      return false
    }
  }

  const stop = async (onlyThisThread = true) => {
    if (act) setStopRequested(act.taskId)
    try {
      await chatStop({ bot_id: botId, thread_id: onlyThisThread ? threadId : undefined })
      if (!onlyThisThread) setNotice(null)
    } catch (err) {
      setStopRequested(null)
      setNotice({ kind: 'error', text: err instanceof Error ? err.message : String(err) })
    }
  }

  if (bots && !bot) {
    return (
      <main className="flex flex-1 flex-col items-center justify-center gap-3 p-8 text-center">
        <h1 className="m-0 text-page">找不到這個 Bot</h1>
        <p className="m-0 text-sub text-ink-2">它可能被封存或刪除了。</p>
      </main>
    )
  }
  if (!bot) return <main className="flex-1" aria-busy="true" />

  const loading = !!threadId && !chat.threads[threadId]?.loaded && items.length === 0
  const empty = !loading && items.length === 0
  const model = bot.model || (models?.default ? models.default.model : models ? '還沒設定模型' : '')

  return (
    <main className="flex h-[calc(100dvh-56px)] min-w-0 flex-1 flex-col bg-app md:h-dvh" aria-label={`和${name}的對話`}>
      <Header bot={bot} act={act} busyElsewhere={busyElsewhere} threads={threads} threadId={threadId} model={model} onModel={() => setModelDialog(true)} />
      <div ref={scroller} onScroll={onScroll} className="min-h-0 flex-1 overflow-y-auto">
        <div
          className={cx(
            'mx-auto box-border flex min-h-full max-w-[828px] flex-col gap-3.5 px-6 pb-2 pt-3 max-sm:px-4',
            empty ? 'justify-center' : 'justify-end',
          )}
          aria-live="polite"
          aria-busy={loading || undefined}
        >
          {empty && <Intro bot={bot} />}
          <Messages items={items} bot={bot} />
        </div>
      </div>
      <div className="flex-none px-6 pb-4 pt-2 max-sm:px-3 max-sm:pb-[max(12px,env(safe-area-inset-bottom))]">
        <div className="mx-auto flex max-w-[780px] flex-col gap-2">
          {noModel && <NoModelCard name={name} onSetup={() => setModelDialog(true)} />}
          {notice && <NoticeRow notice={notice} onStop={() => void stop(false)} onClose={() => setNotice(null)} />}
          <ChatComposer name={name} mode={mode} onSend={send} onStop={() => void stop()} />
        </div>
      </div>
      <ModelSettingsDialog open={modelDialog} onClose={() => setModelDialog(false)} onSaved={(m) => setModels(m)} />
    </main>
  )
}
