import { useState, type FormEvent, type ReactNode } from 'react'
import { ArrowUp, Mic, Plus } from 'lucide-react'
import { Avatar, type AvatarProps } from './Avatar'
import { IconButton } from './Button'
import { cx } from './cx'

// 泡泡：你的 #5A5A5A 靠右，Bot 的 #262626 靠左；圓角 20、15 px（N03）。
export function Bubble({ from, children, bot, name }: { from: 'me' | 'bot'; children: ReactNode; bot?: Pick<AvatarProps, 'shape' | 'color' | 'state'>; name?: ReactNode }) {
  const body = (
    <div className={cx('rounded-bubble px-4 py-2.5 text-body', from === 'me' ? 'bg-bubble-user' : 'bg-bubble-bot')}>{children}</div>
  )
  if (from === 'me') return <div className="max-w-[75%] self-end">{body}</div>
  if (!name) return <div className="max-w-[75%] self-start">{body}</div>
  // 群組裡 Bot 訊息帶頭像和名字
  return (
    <div className="mt-1 flex max-w-[75%] flex-col gap-1 self-start">
      <span className="flex items-center gap-1.5 pl-1 text-meta text-ink-2">
        {bot && <Avatar {...bot} dot="none" size={20} blink={false} />}
        {name}
      </span>
      {body}
    </div>
  )
}

// 正在輸入：三個點，顏色跟著 Bot。
export function TypingDots({ color = '#FF6700', label = '正在輸入' }: { color?: string; label?: string }) {
  return (
    <span role="status" aria-label={label} className="inline-flex h-[26px] items-center gap-[5px] self-start rounded-full bg-inset px-3">
      {[1, 0.65, 0.35].map((o) => (
        <span key={o} className="size-1.5 rounded-full" style={{ background: color, opacity: o }} />
      ))}
    </span>
  )
}

export type ComposerProps = {
  placeholder?: string
  onSend?: (text: string) => void
  onAttach?: () => void
  onVoice?: () => void
  disabled?: boolean
}

// Composer：膠囊 52 高、#2F2F2F；左附加、右語音與白色送出鈕（34 圓）。
export function Composer({ placeholder = '輸入訊息…', onSend, onAttach, onVoice, disabled }: ComposerProps) {
  const [text, setText] = useState('')
  const submit = (e: FormEvent) => {
    e.preventDefault()
    const t = text.trim()
    if (!t) return
    onSend?.(t)
    setText('')
  }
  return (
    <form onSubmit={submit} className="flex h-[52px] items-center gap-1.5 rounded-full bg-control pl-2 pr-[9px]">
      <IconButton icon={Plus} label="附加" shape="round" strong onClick={onAttach} />
      <label className="flex min-w-0 flex-1 items-center self-stretch">
        <span className="sr-only-text">{placeholder.replace(/…$/, '')}</span>
        <input
          value={text}
          onChange={(e) => setText(e.target.value)}
          placeholder={placeholder}
          disabled={disabled}
          className="h-10 min-w-0 flex-1 bg-transparent text-ui text-ink outline-none focus-visible:outline-none"
        />
      </label>
      <IconButton icon={Mic} label="語音輸入" shape="round" onClick={onVoice} />
      <IconButton icon={ArrowUp} label="送出" variant="send" type="submit" disabled={disabled} />
    </form>
  )
}
