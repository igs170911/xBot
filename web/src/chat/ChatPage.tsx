import { createElement, useCallback, useEffect, useLayoutEffect, useRef, useState, type ReactNode } from 'react'
import { Activity as ActivityIcon, AudioLines, ChevronDown, ChevronLeft, Compass, Cpu, Crown, Ellipsis, FolderOpen, GraduationCap, Monitor, Phone, Plus, Settings, Trash2, Volume2, VolumeX, X } from 'lucide-react'
import { isRecording, teachStartStore, useLesson } from '../teaching/teachingData'
import { ApiError, chatListThreads, chatSend, chatStop, filesUploadForm, inboxMarkRead, modelsGet, type ModelsGetOutput } from '../api/generated'
import { botsStore, type Bot, look } from '../data/bots'
import { onEvent, onReconnect } from '../data/events'
import { useStore } from '../data/store'
import { stampLabel } from '../data/ui'
import { goBack, navigate } from '../router'
import { inboxStore, loadInbox } from '../data/inbox'
import { AskCard } from './AskCard'
import { askFromItem } from './ask'
import { usePhone } from '../mobile/nav'
import { Avatar } from '../ui/Avatar'
import { Button, IconButton } from '../ui/Button'
import { Icon } from '../ui/Icon'
import { MenuButton } from '../ui/Menu'
import { cx } from '../ui/cx'
import { ModelSettingsDialog } from '../pages/ModelSettings'
import { activeTask, isActive, threadItems, type Activity, type ChatItem } from './assemble'
import { cardFor } from './cards'
import { takeMention } from '../files/mention'
import { ChatComposer, type ComposerHandle, type ComposerMode } from './ChatComposer'
import { ATTACH_DIR, filesFrom, hasFiles } from './attachments'
import { chatStore, loadOlder, loadThread } from './chatStore'
import { EmptyState } from './EmptyState'
import { BotMessage, JudgeRow, OutcomeRow, OwnerMessage } from './Message'
import { QuotaCard } from './QuotaCard'
import { PreCheckRow } from './PreCheck'
import { runPreCheck } from './precheckData'
import { ModelPicker } from './ModelPicker'
import { usesPlan } from './format'
import { ProgressDialog } from './ProgressDialog'
import { ThreadMenu, type Thread } from './ThreadMenu'
import { DeleteThreadDialog } from './DeleteThread'
import { ThreadDrawer } from './ThreadDrawer'
import { TakeoverBar } from './TakeoverBar'
import { WorkSteps } from './WorkSteps'
import { FirstHome, FirstTasks, useFirstHome } from '../onboard/FirstHome'
import { startTour } from '../onboard/state'
import { ChatAnnouncer } from './Announcer'
import { ComputerPanel } from '../computer/ComputerPanel'
import { useNarrow } from '../computer/hooks'
import { useAutoRead, useAutoReadReplies } from '../voice/useChatVoice'
import { computerPanelStore } from '../computer/screenData'

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
      // 先判斷列（C15）放在泡泡下面、懸停的複製列上面
      if (m.content || !m.streaming) out.push(<OwnerMessage key={m.id} msg={m} below={<PreCheckRow taskId={m.taskId} />} />)
    } else if (m.content) out.push(<BotMessage key={m.id} msg={m} last={isLast} botId={bot?.id} color={bot?.color} />)
    if (it.activity && live) out.push(<WorkSteps key={'a' + it.activity.taskId} a={it.activity} bot={bot} />)
    const retryText = m.taskId ? asked.get(m.taskId) : undefined
    if (m.quota && bot) out.push(<QuotaCard key={'q' + m.id} quota={m.quota} botId={bot.id} bot={bot} />)
    else out.push(<OutcomeRow key={'o' + m.id} msg={m} activity={it.activity} onRetry={isLast && canRetry && retryText ? () => onRetry(retryText) : undefined} />)
    if (m.sender !== 'owner' && !Card) out.push(<JudgeRow key={'j' + m.id} msg={m} threadId={threadId} />)
  }
  // 工作步驟裡已經畫了的提問卡
  const shown = new Set(items.flatMap((it) => (it.type === 'activity' ? [it.activity.request?.inboxId] : [it.activity?.request?.inboxId])).filter(Boolean) as string[])
  out.push(<PendingAsks key="pending-asks" threadId={threadId} shown={shown} bot={bot} />)
  return <>{out}</>
}

/**
 * 這條對話串還沒回答的提問（待我處理裡的同一張卡）：工作步驟裡沒畫出來時也要出現在對話裡，就地回答
 * （2026-10-19：Bot 提問後狀態被改回執行中，提問只剩在待我處理）。
 */
function PendingAsks({ threadId, shown, bot }: { threadId: string; shown: Set<string>; bot?: Bot }) {
  const items = useStore(inboxStore).items ?? []
  const asks = items.filter((it) => it.thread_id === threadId && it.task_id && (it.kind === 'question' || it.kind === 'approval' || it.kind === 'secret') && !shown.has(it.id))
  if (!asks.length) return null
  return (
    <>
      {asks.map((it) => (
        <div key={it.id} data-testid="pending-ask">
          <AskCard ask={askFromItem(it)} bot={bot ? { name: bot.name, shape: bot.shape, color: bot.color } : undefined} onDone={() => void loadInbox()} />
        </div>
      ))}
    </>
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
  if (act?.state === 'queued') return { text: '排隊中', color: 'var(--color-warning)' }
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
  computerOpen,
  onComputer,
  autoRead,
  onAutoRead,
  onDelete,
}: {
  bot: Bot
  act?: Activity
  busyElsewhere: boolean
  threads: Thread[] | null
  threadId?: string
  models: ModelsGetOutput | null
  onSetupModels: () => void
  onProgress: () => void
  computerOpen?: boolean
  onComputer: () => void
  autoRead: boolean
  onAutoRead?: () => void
  onDelete: () => void
}) {
  const st = statusOf(bot, act, busyElsewhere)
  const { lesson } = useLesson(bot.id)
  return (
    <header className="flex h-14 flex-none items-center gap-2 pl-5 pr-3 max-md:h-12 max-md:gap-1.5 max-md:pl-3 max-md:pr-1.5">
      <span className="flex items-center gap-2 max-md:hidden">
        <Avatar {...look(bot)} state={act ? 'working' : 'idle'} dot="none" ring="#070707" size={26} />
        <span className="whitespace-nowrap text-[16px]">{bot.name}</span>
      </span>
      {lesson && isRecording(lesson) && (
        <button type="button" onClick={onComputer} className="inline-flex h-6 flex-none items-center gap-1.5 rounded-full bg-danger-soft px-2.5 text-meta text-danger" data-testid="teaching-pill">
          <span className="size-1.5 rounded-full bg-danger" />
          {lesson.state === 'paused' ? '教學已暫停' : '教學中'}
        </button>
      )}
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
        onDelete={onDelete}
      />
      <span className="flex-1" />
      <ModelPicker bot={bot} models={models} onSetup={onSetupModels} className="max-sm:max-w-[124px]" />
      {autoRead && <Icon icon={AudioLines} size={16} className="text-accent-ink" label="自動朗讀開著" />}
      <IconButton icon={Phone} label={`和${bot.name}通話`} onClick={() => navigate(`/bots/${bot.id}/call`)} />
      <IconButton icon={FolderOpen} label={`${bot.name}的檔案`} onClick={() => navigate(`/bots/${bot.id}/files`)} />
      <IconButton icon={Monitor} label={`${bot.name}的電腦`} pressed={computerOpen} onClick={onComputer} data-tour="computer" />
      <MenuButton
        label="更多"
        align="end"
        items={[
          { label: '目前進度', icon: ActivityIcon, onSelect: onProgress },
          { label: '新對話串', icon: Plus, onSelect: () => navigate(`/bots/${bot.id}/threads/new`) },
          { label: '教它一件事', icon: GraduationCap, onSelect: () => teachStartStore.set({ botId: bot.id, threadId }) },
          { label: '看導覽', icon: Compass, onSelect: startTour },
          ...(onAutoRead ? [{ label: autoRead ? '這個對話串不要自動朗讀' : '這個對話串自動朗讀回覆', icon: autoRead ? VolumeX : Volume2, onSelect: onAutoRead }] : []),
          { label: '聲音與朗讀', icon: AudioLines, onSelect: () => navigate(`/bots/${bot.id}/voice`) },
          { label: 'Bot 設定', icon: Settings, separator: true, onSelect: () => navigate(`/settings/bots/${encodeURIComponent(bot.id)}`) },
          { label: '模型與金鑰設定', icon: Settings, onSelect: () => navigate('/settings/models') },
          ...(threadId ? [{ label: '刪除這個對話串', icon: Trash2, danger: true, separator: true, onSelect: onDelete }] : []),
        ]}
        trigger={(p) => <IconButton {...p} icon={Ellipsis} label="更多" />}
      />
    </header>
  )
}

/** 手機的頁首（L03）：返回（帶等你的件數）、通話｜大頭像｜對話串、電腦；下面置中一顆「名字 · 對話串標題」膠囊，點了看 Bot 資料（L10）。 */
function PhoneHeader({ bot, act, busyElsewhere, threads, threadId, onProgress, onDelete }: { bot: Bot; act?: Activity; busyElsewhere: boolean; threads: Thread[] | null; threadId?: string; onProgress: () => void; onDelete: () => void }) {
  const st = statusOf(bot, act, busyElsewhere)
  const waiting = useStore(inboxStore).items?.length ?? 0
  const current = threads?.find((t) => t.id === threadId)
  const [threadsOpen, setThreadsOpen] = useState(false)
  const title = threadId ? current?.title || '對話' : '新對話串'
  const waitingMe = act?.state === 'waiting_input' || act?.state === 'waiting_approval'
  const round = 'flex size-11 flex-none items-center justify-center rounded-full border border-divider bg-panel text-ink active:bg-control'
  return (
    <header className="flex flex-none flex-col items-center gap-2 bg-app px-3 pb-2 pt-[max(8px,env(safe-area-inset-top))]">
      <div className="flex w-full items-center gap-2">
        <button type="button" aria-label={waiting ? `返回（${waiting} 件等你）` : '返回'} onClick={() => goBack('/chats')} className="flex h-11 flex-none items-center gap-1 rounded-full border border-divider bg-panel pl-2 pr-3 text-ink active:bg-control">
          <Icon icon={ChevronLeft} size={22} />
          {waiting > 0 && <span className="flex h-6 min-w-6 items-center justify-center rounded-full bg-control px-1.5 text-meta">{waiting}</span>}
        </button>
        <button type="button" aria-label={`和${bot.name}通話`} className={round} onClick={() => navigate(`/bots/${bot.id}/call`)}>
          <Icon icon={Phone} size={20} />
        </button>
        <span className="flex flex-1 justify-center">
          <button type="button" aria-label={`${bot.name}的資料`} onClick={() => navigate(`/bots/${bot.id}/profile`)} className="flex size-11 items-center justify-center rounded-full">
            <Avatar shape={bot.shape} color={bot.color} state={waitingMe ? 'notifying' : act ? 'working' : 'idle'} dot={waitingMe ? 'waiting' : 'none'} ring="#070707" size={40} />
          </button>
          <span className="sr-only-text" data-testid="bot-status">
            {st.text}
          </span>
        </span>
        <ThreadDrawer
          botName={bot.name}
          threads={threads}
          threadId={threadId}
          open={threadsOpen}
          onOpenChange={setThreadsOpen}
          onPick={(id) => navigate(`/bots/${bot.id}/threads/${id}`)}
          onNew={() => navigate(`/bots/${bot.id}/threads/new`)}
          onDelete={onDelete}
          onProgress={onProgress}
        />
        <button type="button" aria-label={`${bot.name}的檔案`} className={round} onClick={() => navigate(`/bots/${bot.id}/files`)}>
          <Icon icon={FolderOpen} size={20} />
        </button>
        <button type="button" aria-label={`${bot.name}的電腦`} className={round} onClick={() => navigate(`/bots/${bot.id}/computer`)}>
          <Icon icon={Monitor} size={20} />
        </button>
      </div>
      <button
        type="button"
        onClick={() => setThreadsOpen(true)}
        aria-label={`對話串：${current?.title || '對話'}`}
        aria-expanded={threadsOpen}
        className="flex h-11 max-w-full items-center gap-1.5 rounded-full border border-divider bg-panel px-4 text-ink active:bg-control"
      >
        <span className="flex-none text-ui">{bot.name}</span>
        {bot.primary && (
          <span className="text-ink-3">
            <Icon icon={Crown} size={14} strokeWidth={2} />
          </span>
        )}
        <span className="min-w-0 truncate text-sub text-ink-2" >
          {title}
        </span>
        <Icon icon={ChevronDown} size={16} className="flex-none text-ink-2" />
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

/** 對話頁（design/C01～C07）：/bots/:id 與 /bots/:id/threads/:threadId（new 表示開新的對話串）。 */
export function ChatPage({ botId, threadParam }: { botId: string; threadParam?: string }) {
  const { bots, activity } = useStore(botsStore)
  const bot = bots?.find((b) => b.id === botId)
  const [threads, reloadThreads] = useThreads(botId)
  const chat = useStore(chatStore)
  const [models, setModels] = useState<ModelsGetOutput | null>(null)
  const [modelDialog, setModelDialog] = useState(false)
  const [progress, setProgress] = useState(false)
  const [deleting, setDeleting] = useState(false)
  const [noticeState, setNoticeState] = useState<{ n: Notice; thread: string } | null>(null)
  const [stopRequested, setStopRequested] = useState<string | null>(null)
  const [olderBusy, setOlderBusy] = useState(false)
  const phone = usePhone()
  const scroller = useRef<HTMLDivElement>(null)
  const composer = useRef<ComposerHandle>(null)
  // 從檔案頁按「在對話中提到」：把檔案路徑放進輸入框（C33、L19）。
  const hasBot = !!bot
  useEffect(() => {
    if (!hasBot) return
    const t = takeMention(botId)
    if (t) composer.current?.fill(t)
  }, [hasBot, botId])
  const stick = useRef(true)
  const keepFromBottom = useRef<number | null>(null)
  // 把檔案拖進對話（C05「8 · 附件」）：放開就上傳到它電腦的 /workspace/uploads/，送出時附上路徑。
  const [dropping, setDropping] = useState(false)
  const dragDepth = useRef(0)
  const upload = useCallback(async (f: File) => {
    const [up] = await filesUploadForm(botId, [f], ATTACH_DIR)
    if (!up) throw new Error('沒有傳上去')
    return up
  }, [botId])

  // 電腦面板（E01）：寬螢幕放在對話右邊；窄的螢幕直接開全視窗的電腦頁。
  const panelOpen = useStore(computerPanelStore)
  const wide = !useNarrow('(max-width: 1179px)')
  const showPanel = panelOpen && wide
  const onComputer = () => (wide ? computerPanelStore.set(!panelOpen) : navigate(`/bots/${botId}/computer`))

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
  // 自動朗讀（E32，VC-1）：Bot 設定或這個對話串開著時，看著它寫完的回覆念出來並變成語音訊息。
  const auto = useAutoRead(botId, threadId)
  useAutoReadReplies(botId, threadId, items, auto.autoRead)
  const th = threadId ? chat.threads[threadId] : undefined
  const act = threadId ? activeTask(chat, threadId) : undefined
  const botAct = activity[botId]
  const busyElsewhere = !act && !!botAct && botAct.state !== 'done' && botAct.threadId !== threadId
  const stopping = act?.state === 'stopping' || (!!act && stopRequested === act.taskId)
  const mode: ComposerMode = act ? (stopping ? 'stopping' : 'working') : 'idle'
  const noModel = models !== null && !models.ready && !bot?.model && !(bot && usesPlan(bot))
  const name = bot?.name ?? 'Bot'
  const firstHome = useFirstHome(bots)

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
      if (out.task_id) runPreCheck(botId, text, out.task_id) // 先判斷（C15）：只是建議，背景跑
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
    <div className="flex min-w-0 flex-1">
    <main
      className="relative flex h-[var(--app-h,100dvh)] min-w-0 flex-1 flex-col bg-app max-md:fixed max-md:inset-x-0 max-md:top-[var(--app-top,0px)] max-md:z-10"
      aria-label={`和${name}的對話`}
      onDragEnter={(e) => {
        if (!hasFiles(e.dataTransfer)) return
        e.preventDefault()
        dragDepth.current++
        setDropping(true)
      }}
      onDragOver={(e) => {
        if (!hasFiles(e.dataTransfer)) return
        e.preventDefault()
        e.dataTransfer.dropEffect = 'copy'
      }}
      onDragLeave={() => {
        dragDepth.current = Math.max(0, dragDepth.current - 1)
        if (dragDepth.current === 0) setDropping(false)
      }}
      onDrop={(e) => {
        if (!hasFiles(e.dataTransfer)) return
        e.preventDefault()
        dragDepth.current = 0
        setDropping(false)
        composer.current?.attach(filesFrom(e.dataTransfer.files))
      }}
    >
      {dropping && (
        <div className="pointer-events-none absolute inset-3 z-30 flex flex-col items-center justify-center gap-2 rounded-card border-2 border-dashed border-accent bg-app/85 text-center" data-testid="drop-overlay">
          <Icon icon={FolderOpen} size={28} className="text-accent-ink" />
          <p className="m-0 text-title">放開就傳給{name}</p>
          <p className="m-0 text-sub text-ink-2">檔案會放進它電腦的 /workspace/uploads/，送出時附上路徑</p>
        </div>
      )}
      {/* 頁面標題給螢幕閱讀器（畫面上的名字在頁首）；空白對話的歡迎畫面自己有 h1 */}
      {!(empty && !firstHome) && <h1 className="sr-only-text">和{name}的對話</h1>}
      {phone ? (
        <PhoneHeader bot={bot} act={act} busyElsewhere={busyElsewhere} threads={threads} threadId={threadId} onProgress={() => setProgress(true)} onDelete={() => setDeleting(true)} />
      ) : (
      <Header
        bot={bot}
        act={act}
        busyElsewhere={busyElsewhere}
        threads={threads}
        threadId={threadId}
        models={models}
        onSetupModels={() => setModelDialog(true)}
        onProgress={() => setProgress(true)}
        onDelete={() => setDeleting(true)}
        computerOpen={showPanel}
        onComputer={onComputer}
        autoRead={auto.autoRead}
        onAutoRead={threadId ? auto.toggle : undefined}
      />
      )}
      <div ref={scroller} onScroll={onScroll} className="min-h-0 flex-1 overflow-y-auto">
        <div
          className={cx('mx-auto box-border flex min-h-full max-w-[828px] flex-col gap-3.5 px-6 pb-2 pt-3 max-sm:px-3', empty ? (firstHome ? 'justify-start pt-8' : 'justify-center') : 'justify-end')}
          aria-busy={loading || undefined}
        >
          {th?.hasMore && (
            <Button size="sm" variant="ghost" className="self-center" loading={olderBusy} onClick={() => void older()}>
              載入更早的訊息
            </Button>
          )}
          {empty && (firstHome ? <FirstHome bot={bot} /> : <EmptyState bot={bot} onPick={(t) => composer.current?.fill(t)} onFree={() => composer.current?.fill()} />)}
          {threadId && <Messages items={items} bot={bot} threadId={threadId} canRetry={!act} onRetry={(t) => void send(t)} />}
        </div>
      </div>
      <div className="flex-none px-6 pb-4 pt-2 max-sm:px-3 max-sm:pb-[max(12px,env(safe-area-inset-bottom))]">
        <div className="mx-auto flex max-w-[780px] flex-col gap-2">
          {noModel && <NoModelCard name={name} onSetup={() => setModelDialog(true)} />}
          <TakeoverBar botId={botId} name={name} queued={act?.state === 'queued'} />
          {notice && <NoticeRow notice={notice} onStop={() => void stop(false)} onClose={() => setNotice(null)} />}
          {empty && firstHome && <FirstTasks onPick={(t) => composer.current?.fill(t)} />}
          <div data-tour="composer" className="rounded-[26px]">
            <ChatComposer handle={composer} name={name} mode={mode} onSend={send} onStop={() => void stop()} onUpload={upload} botId={botId} />
          </div>
        </div>
      </div>
      <ChatAnnouncer key={threadId ?? ''} items={items} name={name} ready={!threadId || !!th?.loaded} />
      <ModelSettingsDialog open={modelDialog} onClose={() => setModelDialog(false)} onSaved={(m) => setModels(m)} />
      <DeleteThreadDialog
        threadId={threadId}
        open={deleting}
        onClose={() => setDeleting(false)}
        onDeleted={() => {
          navigate(`/bots/${botId}/threads/new`, { replace: true })
          void reloadThreads()
        }}
      />
      <ProgressDialog open={progress} onClose={() => setProgress(false)} bot={bot} chat={chat} threadId={threadId} act={act} botAct={botAct} />
    </main>
    {showPanel && <ComputerPanel key={bot.id} bot={bot} />}
    </div>
  )
}
