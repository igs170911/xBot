import { useEffect, useState, type ReactNode } from 'react'
import { Check, ChevronDown, CircleX, Code, Copy, OctagonPause, RotateCcw, Square, Volume2 } from 'lucide-react'
import { Button } from '../ui/Button'
import { Icon } from '../ui/Icon'
import { cx } from '../ui/cx'
import type { Activity, ChatMsg } from './assemble'
import { clockLabel, isLong } from './format'
import { Markdown } from './markdown/Markdown'
import { isRich } from './markdown/parse'
import { useCopy } from './useCopy'

function Collapsible({ long, mine, children }: { long: boolean; mine: boolean; children: ReactNode }) {
  const [open, setOpen] = useState(false)
  if (!long) return <>{children}</>
  return (
    <div className="flex flex-col gap-1.5">
      <div className={cx(!open && 'overflow-hidden [mask-image:linear-gradient(to_bottom,#000_75%,transparent)]', !open && (mine ? 'max-h-[320px]' : 'max-h-[640px]'))}>{children}</div>
      <button
        type="button"
        aria-expanded={open}
        onClick={() => setOpen((o) => !o)}
        className={cx('relative flex h-7 items-center gap-1 self-start rounded-full bg-transparent px-2 text-meta touch-target hover:bg-black/20', mine ? 'text-white/85' : 'text-accent-ink')}
      >
        {open ? '收起' : '顯示全部'}
        <Icon icon={ChevronDown} size={14} className={cx('transition-transform', open && 'rotate-180')} />
      </button>
    </div>
  )
}

// ── 訊息動作（design/C02）──────────────────────────────

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

const canSpeak = () => typeof window !== 'undefined' && 'speechSynthesis' in window && typeof SpeechSynthesisUtterance !== 'undefined'

function useSpeak(text: string): [boolean, () => void] {
  const [on, setOn] = useState(false)
  useEffect(
    () => () => {
      if (on) window.speechSynthesis.cancel()
    },
    [on],
  )
  const toggle = () => {
    const s = window.speechSynthesis
    if (on) {
      s.cancel()
      setOn(false)
      return
    }
    s.cancel()
    const u = new SpeechSynthesisUtterance(text.replace(/[`*#>|_~]/g, ''))
    u.lang = 'zh-TW'
    u.onend = () => setOn(false)
    u.onerror = () => setOn(false)
    s.speak(u)
    setOn(true)
  }
  return [on, toggle]
}

function BotActions({ msg, raw, onRaw, always }: { msg: ChatMsg; raw: boolean; onRaw: () => void; always: boolean }) {
  const [copied, copy] = useCopy()
  const [speaking, speak] = useSpeak(msg.content)
  return (
    <div
      role="toolbar"
      aria-label="訊息動作"
      className={cx('flex items-center gap-0.5 self-start transition-opacity', always ? '-mt-2' : 'absolute left-0 top-full z-10 -mt-1 rounded-seg bg-app opacity-0 focus-within:opacity-100 group-hover:opacity-100 [@media(hover:none)]:hidden')}
    >
      <ActionButton icon={copied ? Check : Copy} label={copied ? '已複製' : '複製'} tone={copied ? 'text-success' : undefined} onClick={() => void copy(msg.content)} />
      <ActionButton icon={Code} label="原始內容" pressed={raw} onClick={onRaw} />
      {canSpeak() && <ActionButton icon={speaking ? Square : Volume2} label={speaking ? '停止朗讀' : '唸出來'} pressed={speaking} onClick={speak} />}
      <span className="pl-1.5 text-meta text-ink-3">{clockLabel(msg.updatedAt || msg.createdAt)}</span>
    </div>
  )
}

function OwnerActions({ msg }: { msg: ChatMsg }) {
  const [copied, copy] = useCopy()
  return (
    <div className="absolute right-0 top-full z-10 -mt-1 flex items-center gap-0.5 rounded-seg bg-app opacity-0 transition-opacity focus-within:opacity-100 group-hover:opacity-100 [@media(hover:none)]:hidden">
      <span className="pr-1.5 text-meta text-ink-3">{clockLabel(msg.createdAt)}</span>
      <ActionButton icon={copied ? Check : Copy} label={copied ? '已複製' : '複製'} tone={copied ? 'text-success' : undefined} onClick={() => void copy(msg.content)} />
    </div>
  )
}

// ── 泡泡 ─────────────────────────────────────────────

/** 你的訊息：#5A5A5A 靠右；照原樣顯示（不轉 Markdown）。 */
export function OwnerMessage({ msg }: { msg: ChatMsg }) {
  return (
    <div className="group relative flex flex-col gap-2">
      <div data-testid="msg-owner" className="max-w-[75%] self-end whitespace-pre-wrap break-words rounded-bubble bg-bubble-user px-4 py-2.5 text-[15px] leading-[1.6] text-white">
        <Collapsible long={isLong(msg)} mine>
          {msg.content}
        </Collapsible>
      </div>
      {msg.content && <OwnerActions msg={msg} />}
    </div>
  )
}

/** Bot 的回覆：Markdown 排版；有表格、程式碼、標題時泡泡放寬（C02、C07）。 */
export function BotMessage({ msg, last }: { msg: ChatMsg; last: boolean }) {
  const [raw, setRaw] = useState(false)
  const rich = isRich(msg.content)
  const done = !msg.streaming
  return (
    <div className="group relative flex flex-col gap-2">
      <div
        data-testid="msg-bot"
        data-streaming={msg.streaming || undefined}
        className={cx('min-w-0 self-start rounded-bubble bg-bubble-bot text-[15px]', rich ? 'w-full px-6 pb-[22px] pt-5 leading-[1.75] max-sm:px-4 max-sm:pb-4 max-sm:pt-3.5' : 'max-w-[85%] px-4 py-2.5 leading-[1.6]')}
      >
        <Collapsible long={isLong(msg)} mine={false}>
          {raw ? <pre className="m-0 whitespace-pre-wrap break-words font-mono text-[13px] leading-[1.7] text-ink/85">{msg.content}</pre> : <Markdown text={msg.content} />}
        </Collapsible>
      </div>
      {done && msg.content && <BotActions msg={msg} raw={raw} onRaw={() => setRaw((r) => !r)} always={last} />}
    </div>
  )
}

// ── 這一輪的結果（design/C11：已停止、工作未完成）────────────

export function OutcomeRow({ msg, activity, onRetry, busy }: { msg: ChatMsg; activity?: Activity; onRetry?: () => void; busy?: boolean }) {
  if (msg.status !== 'error' && msg.status !== 'interrupted') return null
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
