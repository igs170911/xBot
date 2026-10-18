import { createElement, useCallback, useEffect, useLayoutEffect, useRef, useState, type ReactNode } from 'react'
import { Activity as ActivityIcon, Cpu, Ellipsis, Monitor, Plus, Settings, X } from 'lucide-react'
import { ApiError, chatListThreads, chatSend, chatStop, inboxMarkRead, modelsGet, type ModelsGetOutput } from '../api/generated'
import { botsStore, type Bot } from '../data/bots'
import { onEvent, onReconnect } from '../data/events'
import { useStore } from '../data/store'
import { stampLabel } from '../data/ui'
import { navigate } from '../router'
import { Avatar } from '../ui/Avatar'
import { Button, IconButton } from '../ui/Button'
import { Icon } from '../ui/Icon'
import { MenuButton } from '../ui/Menu'
import { cx } from '../ui/cx'
import { ModelSettingsDialog } from '../pages/ModelSettings'
import { activeTask, isActive, threadItems, type Activity, type ChatItem } from './assemble'
import { cardFor } from './cards'
import { ChatComposer, type ComposerHandle, type ComposerMode } from './ChatComposer'
import { chatStore, loadOlder, loadThread } from './chatStore'
import { EmptyState } from './EmptyState'
import { BotMessage, JudgeRow, OutcomeRow, OwnerMessage } from './Message'
import { ModelPicker } from './ModelPicker'
import { ProgressDialog } from './ProgressDialog'
import { ThreadMenu, type Thread } from './ThreadMenu'
import { WorkSteps } from './WorkSteps'

const GAP = 30 * 60 * 1000

// ── 訊息 ─────────────────────────────────────────────

/** 隊友交辦、群組裡 Bot 回 [silent]（後面 40 字內）＝不發言，不顯示（LazyBoy is_silent）。 */
function isSilentText(c: string): boolean {
  const t = c.trim().replace(/^`+|`+$/g, '').trim()
  return t.toLowerCase().startsWith('[silent]') && t.length < 48
}

function Stamp({ at }: { at: number }) {
  return <span className="self-center pt-1.5 text-meta text-ink-3">{stampLabel(at)}</span>
}

function Messages({ items, bot, threadId, onRetry, canRetry }: { items: ChatItem[]; bot?: Bot; threadId: string; onRetry: (text: string) => void; canRetry: boolean }) {
  const out: ReactNode[] = []
  let last = 0
  // 每件工作是哪句話交代的（重試時再送一次）
  const asked = new Map<string, string>()
  for (const it of items) if (it.type === 'message' && it.msg.sender === 'owner' && it.msg.taskId) asked.set(it.msg.taskId, it.msg.content)
  const lastBot = items.findLast((it) => it.type === 'message' && it.msg.sender !== 'owner')
  for (const it of items) {
    if (it.type === 'activity') {
      out.push(<WorkSteps key={'a' + it.activity.taskId} a={it.activity} bot={bot} />)
      continue
    }
    const m = it.msg
    if (m.silent || (m.sender !== 'owner' && !m.streaming && isSilentText(m.content))) continue
    if (m.createdAt && m.seq !== Number.MAX_SAFE_INTEGER && (!last || m.createdAt - last > GAP)) out.push(<Stamp key={'t' + m.id} at={m.createdAt} />)
    if (m.createdAt) last = m.createdAt
    // 進行中：進度接在回覆下面（C01，最新的動態靠近輸入框）；做完：摺疊的步驟放在回答上面（C02）
    const live = it.activity && isActive(it.activity)
    if (it.activity && !live) out.push(<WorkSteps key={'a' + it.activity.taskId} a={it.activity} bot={bot} />)
    const Card = cardFor(m.kind)
    const isLast = it === lastBot
    if (Card) out.push(createElement(Card, { key: m.id, msg: m, bot, threadId, activity: it.activity }))
    else if (m.sender === 'owner') {
      if (m.content || !m.streaming) out.push(<OwnerMessage key={m.id} msg={m} />)
    } else if (m.content) out.push(<BotMessage key={m.id} msg={m} last={isLast} />)
    if (it.activity && live) out.push(<WorkSteps key={'a' + it.activity.taskId} a={it.activity} bot={bot} />)
    const retryText = m.taskId ? asked.get(m.taskId) : undefined
    out.push(<OutcomeRow key={'o' + m.id} msg={m} activity={it.activity} onRetry={isLast && canRetry && retryText ? () => onRetry(retryText) : undefined} />)
    if (m.sender !== 'owner' && !Card) out.push(<JudgeRow key={'j' + m.id} msg={m} threadId={threadId} />)
  }
  return <>{out}</>
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
  models,
  onSetupModels,
  onProgress,
}: {
  bot: Bot
  act?: Activity
  busyElsewhere: boolean
  threads: Thread[] | null
  threadId?: string
  models: ModelsGetOutput | null
  onSetupModels: () => void
  onProgress: () => void
}) {
  const st = statusOf(bot, act, busyElsewhere)
  return (
    <header className="flex h-14 flex-none items-center gap-2 pl-5 pr-3 max-md:h-12 max-md:gap-1.5 max-md:pl-3 max-md:pr-1.5">
      <span className="flex items-center gap-2 max-md:hidden">
        <Avatar shape={bot.shape} color={bot.color} state={act ? 'working' : 'idle'} dot="none" ring="#070707" size={26} />
        <span className="text-[16px]">{bot.name}</span>
      </span>
      <span className="flex items-center gap-1.5 whitespace-nowrap text-[13px] text-ink-3" data-testid="bot-status">
        {st.color && <span className={cx('size-1.5 rounded-full', st.pulse && 'animate-pulse')} style={{ background: st.color }} />}
        <span className="max-sm:sr-only-text">{st.text}</span>
      </span>
      <span className="mx-1 h-5 w-px bg-line max-sm:hidden" />
      <ThreadMenu
        botName={bot.name}
        threads={threads}
        threadId={threadId}
        onPick={(id) => navigate(`/bots/${bot.id}/threads/${id}`)}
        onNew={() => navigate(`/bots/${bot.id}/threads/new`)}
      />
      <span className="flex-1" />
      <ModelPicker bot={bot} models={models} onSetup={onSetupModels} className="max-sm:max-w-[124px]" />
      <IconButton icon={Monitor} label={`${bot.name}的電腦`} onClick={() => navigate(`/bots/${bot.id}/computer`)} />
      <MenuButton
        label="更多"
        align="end"
        items={[
          { label: '目前進度', icon: ActivityIcon, onSelect: onProgress },
          { label: '新對話串', icon: Plus, onSelect: () => navigate(`/bots/${bot.id}/threads/new`) },
          { label: '引擎與權限', icon: Settings, separator: true, onSelect: () => navigate(`/settings/bots/${encodeURIComponent(bot.id)}`) },
          { label: '模型與金鑰設定', icon: Settings, onSelect: () => navigate('/settings/models') },
        ]}
        trigger={(p) => <IconButton {...p} icon={Ellipsis} label="更多" />}
      />
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

/** 對話頁（design/C01～C07）：/bots/:id 與 /bots/:id/threads/:threadId（new 表示開新的對話串）。 */
export function ChatPage({ botId, threadParam }: { botId: string; threadParam?: string }) {
  const { bots, activity } = useStore(botsStore)
  const bot = bots?.find((b) => b.id === botId)
  const [threads, reloadThreads] = useThreads(botId)
  const chat = useStore(chatStore)
  const [models, setModels] = useState<ModelsGetOutput | null>(null)
  const [modelDialog, setModelDialog] = useState(false)
  const [progress, setProgress] = useState(false)
  const [noticeState, setNoticeState] = useState<{ n: Notice; thread: string } | null>(null)
  const [stopRequested, setStopRequested] = useState<string | null>(null)
  const [olderBusy, setOlderBusy] = useState(false)
  const scroller = useRef<HTMLDivElement>(null)
  const composer = useRef<ComposerHandle>(null)
  const stick = useRef(true)
  const keepFromBottom = useRef<number | null>(null)

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
    stick.current = true
    const load = () => loadThread(threadId).catch((err: unknown) => setNotice({ kind: 'error', text: err instanceof Error ? err.message : String(err) }))
    void load()
    return onReconnect(() => void load())
  }, [threadId, setNotice])

  // 打開對話就把這條對話串的通知標成已讀（B04 未讀數）；看著對話時新來的也一樣。
  useEffect(() => {
    if (!threadId) return
    const read = () => void inboxMarkRead({ thread_id: threadId }).catch(() => {})
    read()
    return onEvent((e) => {
      if (e.kind === 'inbox.created' && e.thread_id === threadId) read()
    })
  }, [threadId])

  const items = threadId ? threadItems(chat, threadId) : []
  const th = threadId ? chat.threads[threadId] : undefined
  const act = threadId ? activeTask(chat, threadId) : undefined
  const botAct = activity[botId]
  const busyElsewhere = !act && !!botAct && botAct.state !== 'done' && botAct.threadId !== threadId
  const stopping = act?.state === 'stopping' || (!!act && stopRequested === act.taskId)
  const mode: ComposerMode = act ? (stopping ? 'stopping' : 'working') : 'idle'
  const noModel = models !== null && !models.ready && !bot?.model
  const name = bot?.name ?? 'Bot'

  // 新內容進來時，如果本來就在最下面，就跟著捲到底；載入更早的訊息時保持畫面不跳
  const lastItem = items[items.length - 1]
  const lastLen = lastItem?.type === 'message' ? lastItem.msg.content.length : 0
  useLayoutEffect(() => {
    const el = scroller.current
    if (!el) return
    if (keepFromBottom.current !== null) {
      el.scrollTop = el.scrollHeight - keepFromBottom.current
      keepFromBottom.current = null
    } else if (stick.current) el.scrollTop = el.scrollHeight
  }, [items.length, lastLen, act?.steps.length, noModel, notice])

  const onScroll = () => {
    const el = scroller.current
    if (el) stick.current = el.scrollHeight - el.scrollTop - el.clientHeight < 80
  }

  const older = async () => {
    if (!threadId || olderBusy) return
    const el = scroller.current
    setOlderBusy(true)
    try {
      if (el) keepFromBottom.current = el.scrollHeight - el.scrollTop
      await loadOlder(threadId)
    } catch (err) {
      keepFromBottom.current = null
      setNotice({ kind: 'error', text: err instanceof Error ? err.message : String(err) })
    } finally {
      setOlderBusy(false)
    }
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

  const loading = !!threadId && !th?.loaded && items.length === 0
  const empty = !loading && items.length === 0

  return (
    <main className="flex h-[calc(100dvh-56px)] min-w-0 flex-1 flex-col bg-app md:h-dvh" aria-label={`和${name}的對話`}>
      <Header
        bot={bot}
        act={act}
        busyElsewhere={busyElsewhere}
        threads={threads}
        threadId={threadId}
        models={models}
        onSetupModels={() => setModelDialog(true)}
        onProgress={() => setProgress(true)}
      />
      <div ref={scroller} onScroll={onScroll} className="min-h-0 flex-1 overflow-y-auto">
        <div
          className={cx('mx-auto box-border flex min-h-full max-w-[828px] flex-col gap-3.5 px-6 pb-2 pt-3 max-sm:px-3', empty ? 'justify-center' : 'justify-end')}
          aria-live="polite"
          aria-busy={loading || undefined}
        >
          {th?.hasMore && (
            <Button size="sm" variant="ghost" className="self-center" loading={olderBusy} onClick={() => void older()}>
              載入更早的訊息
            </Button>
          )}
          {empty && <EmptyState bot={bot} onPick={(t) => composer.current?.fill(t)} onFree={() => composer.current?.fill()} />}
          {threadId && <Messages items={items} bot={bot} threadId={threadId} canRetry={!act} onRetry={(t) => void send(t)} />}
        </div>
      </div>
      <div className="flex-none px-6 pb-4 pt-2 max-sm:px-3 max-sm:pb-[max(12px,env(safe-area-inset-bottom))]">
        <div className="mx-auto flex max-w-[780px] flex-col gap-2">
          {noModel && <NoModelCard name={name} onSetup={() => setModelDialog(true)} />}
          {notice && <NoticeRow notice={notice} onStop={() => void stop(false)} onClose={() => setNotice(null)} />}
          <ChatComposer handle={composer} name={name} mode={mode} onSend={send} onStop={() => void stop()} />
        </div>
      </div>
      <ModelSettingsDialog open={modelDialog} onClose={() => setModelDialog(false)} onSaved={(m) => setModels(m)} />
      <ProgressDialog open={progress} onClose={() => setProgress(false)} bot={bot} chat={chat} threadId={threadId} act={act} botAct={botAct} />
    </main>
  )
}
