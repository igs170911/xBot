import { useState, type ReactNode } from 'react'
import { Check, ChevronDown, CircleX, Code, Copy, GraduationCap, Hand, OctagonPause, Phone, Play, Repeat, RotateCcw, Square, Unplug, Volume2 } from 'lucide-react'
import { ApiError, judgeCorrect } from '../api/generated'
import { AgentMark } from '../ui/AgentMark'
import { colorHex } from '../ui/avatar-data'
import { Button } from '../ui/Button'
import { Icon } from '../ui/Icon'
import { cx } from '../ui/cx'
import { JudgeTag } from '../ui/Pill'
import { setCorrection, type Activity, type ChatMsg } from './assemble'
import type { CardProps } from './cards'
import { chatStore } from './chatStore'
import { clockLabel, isLong, judgeBy, VERDICT_LABEL } from './format'
import { Markdown } from './markdown/Markdown'
import { FileTiles, filesIn, splitAttachments } from './Attachments'
import { botFileLinker, FileLinkContext } from './markdown/fileLink'
import { splitBubbles } from './markdown/bubbles'
import { isRich } from './markdown/parse'
import { useCopy } from './useCopy'
import { VoiceNoteBar } from '../voice/chatVoice'
import { useReadAloud } from '../voice/useChatVoice'

/** 太長的訊息先收起，按「顯示全部」展開（對話和群組共用）。 */
export function Collapsible({ long, mine, children }: { long: boolean; mine: boolean; children: ReactNode }) {
  const [open, setOpen] = useState(false)
  if (!long) return <>{children}</>
  return (
    <div className="flex flex-col gap-1.5">
      <div className={cx(!open && 'overflow-hidden [mask-image:linear-gradient(to_bottom,#000_75%,transparent)]', !open && (mine ? 'max-h-[320px]' : 'max-h-[640px]'))}>{children}</div>
      <button
        type="button"
        aria-expanded={open}
        onClick={() => setOpen((o) => !o)}
        className={cx('relative flex h-7 items-center gap-1 self-start rounded-full bg-transparent px-2 text-meta touch-target hover:bg-black/20', mine ? 'text-ink' : 'text-accent-ink')}
      >
        {open ? '收起' : '顯示全部'}
        <Icon icon={ChevronDown} size={14} className={cx('transition-transform', open && 'rotate-180')} />
      </button>
    </div>
  )
}

// ── 訊息動作（design/C02）──────────────────────────────
// 動作列排在泡泡下面（佔自己的高度），懸停才顯示；不疊在下一則訊息上面。

function ActionButton({ icon, label, onClick, pressed, tone }: { icon: typeof Copy; label: string; onClick: () => void; pressed?: boolean; tone?: string }) {
  return (
    <button
      type="button"
      aria-label={label}
      title={label}
      aria-pressed={pressed}
      onClick={onClick}
      className={cx('relative flex size-8 items-center justify-center rounded-seg bg-transparent touch-target hover:bg-control hover:text-ink aria-pressed:bg-control aria-pressed:text-ink', tone ?? 'text-ink-2')}
    >
      <Icon icon={icon} size={18} />
    </button>
  )
}

function BotActions({ msg, raw, onRaw, always, botId }: { msg: ChatMsg; raw: boolean; onRaw: () => void; always: boolean; botId?: string }) {
  const [copied, copy] = useCopy()
  const read = useReadAloud(botId, msg)
  const speaking = read.state !== 'idle'
  return (
    <div
      role="toolbar"
      aria-label="訊息動作"
      className={cx('-my-1.5 flex h-8 items-center gap-0.5 self-start transition-opacity', !always && 'opacity-0 focus-within:opacity-100 group-hover:opacity-100 [@media(hover:none)]:hidden')}
    >
      <ActionButton icon={copied ? Check : Copy} label={copied ? '已複製' : '複製'} tone={copied ? 'text-success' : undefined} onClick={() => void copy(msg.content)} />
      <ActionButton icon={Code} label="原始內容" pressed={raw} onClick={onRaw} />
      {botId && <ActionButton icon={speaking ? Square : Volume2} label={speaking ? '停止朗讀' : '唸出來'} pressed={speaking} onClick={read.toggle} />}
      <span className="pl-1.5 text-meta text-ink-3">{clockLabel(msg.updatedAt || msg.createdAt)}</span>
    </div>
  )
}

function OwnerActions({ msg }: { msg: ChatMsg }) {
  const [copied, copy] = useCopy()
  return (
    <div className="-my-1.5 flex h-8 items-center gap-0.5 self-end opacity-0 transition-opacity focus-within:opacity-100 group-hover:opacity-100 [@media(hover:none)]:hidden">
      <span className="pr-1.5 text-meta text-ink-3">{clockLabel(msg.createdAt)}</span>
      <ActionButton icon={copied ? Check : Copy} label={copied ? '已複製' : '複製'} tone={copied ? 'text-success' : undefined} onClick={() => void copy(msg.content)} />
    </div>
  )
}

// ── 泡泡 ─────────────────────────────────────────────

/** Bot 的氣泡：底色混一點 Bot 的頭像顏色（深淺主題都看得清楚字）。 */
export function botBubble(color: string): string {
  return `color-mix(in srgb, ${colorHex(color)} 20%, var(--color-bubble-bot))`
}

/** 你的訊息：#5A5A5A 靠右；照原樣顯示（不轉 Markdown）。 */
export function OwnerMessage({ msg, below, botId }: { msg: ChatMsg; below?: ReactNode; botId?: string }) {
  // 附件：訊息後面的「附件：路徑」不顯示，改成圖片／檔案卡（像一般聊天軟體）
  const { text, paths } = botId ? splitAttachments(msg.content) : { text: msg.content, paths: [] }
  return (
    <div className="group flex flex-col gap-2">
      {text && (
        <div data-testid="msg-owner" className="max-w-[75%] self-end whitespace-pre-wrap break-words rounded-bubble bg-bubble-user px-4 py-2.5 text-[15px] leading-[1.6] text-ink">
          <Collapsible long={isLong(msg)} mine>
            {text}
          </Collapsible>
        </div>
      )}
      {botId && <FileTiles botId={botId} paths={paths} mine />}
      {msg.actorName && <AgentMark name={msg.actorName} via={msg.via} verb="傳送" className="self-end" />}
      {below}
      {msg.content && <OwnerActions msg={msg} />}
    </div>
  )
}

/** 一段回覆的泡泡；有表格、程式碼、標題時放寬（C02、C07）。 */
export function BotBubble({ text, color, botId, children }: { text: string; color?: string; botId?: string; children?: ReactNode }) {
  const rich = isRich(text)
  return (
    <div
      data-testid="bot-bubble"
      style={color ? { background: botBubble(color) } : undefined}
      className={cx('min-w-0 self-start rounded-bubble bg-bubble-bot text-[15px]', rich ? 'w-full px-6 pb-[22px] pt-5 leading-[1.75] max-sm:px-4 max-sm:pb-4 max-sm:pt-3.5' : 'max-w-[85%] px-4 py-2.5 leading-[1.6]')}
    >
      {children}
      {botId ? (
        <FileLinkContext.Provider value={botFileLinker(botId)}>
          <Markdown text={text} />
        </FileLinkContext.Provider>
      ) : (
        <Markdown text={text} />
      )}
    </div>
  )
}

/**
 * Bot 的回覆：照段落拆成幾個連著的泡泡（像人傳訊息一段一則；splitBubbles），表格、程式碼不拆。
 * 複製、朗讀、原始內容、判斷還是整則一份。
 */
export function BotMessage({ msg, last, botId, color }: { msg: ChatMsg; last: boolean; botId?: string; /** Bot 的頭像顏色：氣泡帶一點這個顏色 */ color?: string }) {
  const [raw, setRaw] = useState(false)
  const done = !msg.streaming
  const parts = splitBubbles(msg.content)
  const voice = msg.voice && done ? <VoiceNoteBar botId={botId} msg={msg} note={msg.voice} /> : null
  return (
    <div className="group flex flex-col gap-2">
      <div data-testid="msg-bot" data-streaming={msg.streaming || undefined} aria-busy={msg.streaming || undefined} className="flex min-w-0 flex-col">
        <Collapsible long={isLong(msg)} mine={false}>
          {raw ? (
            <div
              style={color ? { background: botBubble(color) } : undefined}
              className="min-w-0 self-start rounded-bubble bg-bubble-bot px-4 py-2.5 text-[15px]"
            >
              {voice}
              <pre className="m-0 whitespace-pre-wrap break-words font-mono text-[13px] leading-[1.7] text-ink/85">{msg.content}</pre>
            </div>
          ) : (
            <div className="flex min-w-0 flex-col gap-1.5">
              {parts.map((part, i) => (
                <BotBubble key={i} text={part} color={color} botId={botId}>
                  {i === 0 && voice}
                </BotBubble>
              ))}
            </div>
          )}
        </Collapsible>
      </div>
      {done && botId && !raw && <FileTiles botId={botId} paths={filesIn(msg.content)} />}
      {done && msg.content && <BotActions msg={msg} raw={raw} onRaw={() => setRaw((r) => !r)} always={last} botId={botId} />}
    </div>
  )
}

// ── 這一輪的結果（design/C11：已停止、工作未完成）────────────

export function OutcomeRow({ msg, activity, onRetry, onResume, busy }: { msg: ChatMsg; activity?: Activity; onRetry?: () => void; onResume?: () => void; busy?: boolean }) {
  if (msg.status !== 'error' && msg.status !== 'interrupted') return null
  if (msg.status === 'interrupted' && msg.restart) return <RestartRow msg={msg} onResume={onResume} busy={busy} />
  const error = msg.status === 'error'
  const detail = error ? (activity?.error && activity.error !== msg.content ? activity.error : '') : '做到一半的檔案都還在'
  return (
    <div className={cx('-mt-1 flex min-h-11 flex-wrap items-center gap-x-2.5 gap-y-1.5 rounded-group border py-1.5 pl-3.5 pr-1.5', error ? 'border-danger/40 bg-danger-soft' : 'border-line')} data-testid="outcome">
      <span className={cx('flex', error ? 'text-danger' : 'text-ink-2')}>
        <Icon icon={error ? CircleX : OctagonPause} size={16} />
      </span>
      <span className="min-w-0 flex-1 text-ui-sm">
        <span className={error ? 'text-danger' : 'text-ink'}>{error ? '工作未完成' : '已停止'}</span>
        {detail && <span className="text-ink-2"> · {detail}</span>}
      </span>
      {onRetry && (
        <Button size="sm" variant={error ? 'primary' : 'secondary'} icon={RotateCcw} onClick={onRetry} disabled={busy}>
          {error ? '再試一次' : '重做'}
        </Button>
      )}
    </div>
  )
}

// 服務重開時做到一半被中斷、沒有自動接著做的工作（系統已經接著做過一次，或是定時任務、Webhook 的工作）：「接著做」。
function RestartRow({ msg, onResume, busy }: { msg: ChatMsg; onResume?: () => void; busy?: boolean }) {
  return (
    <div className="-mt-1 flex min-h-11 flex-wrap items-center gap-x-2.5 gap-y-1.5 rounded-group border border-line py-1.5 pl-3.5 pr-1.5" data-testid="outcome" data-restart="">
      <span className="flex text-ink-2">
        <Icon icon={Unplug} size={16} />
      </span>
      <span className="min-w-0 flex-1 text-ui-sm">
        <span className="text-ink">做到一半被中斷了</span>
        <span className="text-ink-2"> · {msg.resumed ? '已經接著做' : '服務重開過，檔案都還在'}</span>
      </span>
      {onResume && !msg.resumed && (
        <Button size="sm" variant="primary" icon={Play} onClick={onResume} disabled={busy}>
          接著做
        </Button>
      )}
    </div>
  )
}

// ── 系統判斷（X04，PRD §20 FJ-7：「系統判斷：已完成 · 規則 · 不對？」）────────────

// Bot 回完一輪後的系統判斷；「不對？」選正確的那一種，記下來給快判校正（xbot.judge.correct）。
// 只在正常結束、而且判斷不是「已完成」時顯示（已完成是常態，每則都標會變雜訊）；你改過的照樣顯示。
export function JudgeRow({ msg, threadId }: { msg: ChatMsg; threadId: string }) {
  const [error, setError] = useState<string | null>(null)
  const j = msg.judge
  if (!j || msg.streaming || msg.status !== 'complete' || !msg.taskId) return null
  if (j.verdict === 'done' && !j.corrected) return null
  const taskId = msg.taskId
  const pick = (verdict: string) => {
    setError(null)
    judgeCorrect({ task_id: taskId, verdict: verdict as 'done' })
      .then(() => chatStore.set((s) => setCorrection(s, threadId, msg.id, verdict)))
      .catch((err: unknown) => setError(err instanceof ApiError || err instanceof Error ? err.message : String(err)))
  }
  const menu = Object.entries(VERDICT_LABEL)
    .filter(([v]) => v !== (j.corrected || j.verdict))
    .map(([v, label]) => ({ label, onSelect: () => pick(v) }))
  return (
    <div className="-mt-1 flex flex-wrap items-center gap-2" data-testid="judge-row" title={j.reason}>
      <JudgeTag menu={menu}>
        {VERDICT_LABEL[j.verdict] ?? j.label}
        {j.by === 'model' && j.confidence ? <span className="text-ink-3"> · {judgeBy(j.by, j.confidence)}</span> : null}
        {j.corrected && <span className="text-ink"> · 你改成{VERDICT_LABEL[j.corrected] ?? j.corrected}</span>}
      </JudgeTag>
      {error && (
        <span role="alert" className="text-meta text-danger">
          {error}
        </span>
      )}
    </div>
  )
}

// 系統在對話裡記的一行（kind=activity，sender=system）：例「自動繼續：剛才只說要做，現在真的去做」（X04）。
export function ActivityLine({ msg }: CardProps) {
  return (
    <p className="m-0 flex items-center gap-1.5 self-center text-meta text-ink-3" data-testid="activity-line" data-activity={msg.activity}>
      <Icon
        icon={msg.activity === 'call' ? Phone : msg.activity === 'teaching' ? GraduationCap : msg.activity === 'takeover' || msg.activity === 'handback' ? Hand : msg.activity === 'restart_resume' ? RotateCcw : Repeat}
        size={14}
      />
      {msg.content}
    </p>
  )
}
