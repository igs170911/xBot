import { useRef, useState, type FormEvent, type KeyboardEvent } from 'react'
import { ArrowUp, LoaderCircle } from 'lucide-react'
import { IconButton } from '../ui/Button'
import { Icon } from '../ui/Icon'
import { cx } from '../ui/cx'

export type ComposerMode = 'idle' | 'working' | 'stopping'

export type ChatComposerProps = {
  /** Bot 的名字，用在提示文字。 */
  name: string
  mode: ComposerMode
  /** 送出；回傳 true 表示收下了（清空輸入框），false 保留文字讓你再送。 */
  onSend: (text: string) => Promise<boolean>
  onStop: () => void
  /** 輸入框下方的一行說明（覆寫預設）。 */
  hint?: string
}

// 輸入框（design/C05-Composer）：#2F2F2F 膠囊 52 高；有字才能送出（白色圓鈕）；
// Bot 工作中右側是停止鈕（白底黑方塊），正在停止時變灰色轉圈。
export function ChatComposer({ name, mode, onSend, onStop, hint }: ChatComposerProps) {
  const [text, setText] = useState('')
  const [sending, setSending] = useState(false)
  const input = useRef<HTMLTextAreaElement>(null)
  const empty = text.trim() === ''

  const submit = async (e?: FormEvent) => {
    e?.preventDefault()
    const t = text.trim()
    if (!t || sending) return
    setSending(true)
    try {
      if (await onSend(t)) setText('')
    } finally {
      setSending(false)
      input.current?.focus()
    }
  }

  const onKeyDown = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    // 中文輸入法選字時的 Enter 不是送出
    if (e.key === 'Enter' && !e.shiftKey && !e.nativeEvent.isComposing && e.keyCode !== 229) {
      e.preventDefault()
      void submit()
    }
  }

  const placeholder = mode === 'stopping' ? `正在停止${name}…` : mode === 'working' ? `補充說明給${name}…` : `交代${name}…`
  const line = hint ?? (mode === 'working' ? `${name}工作中 · 要改做別的事，先按停止` : mode === 'stopping' ? '正在停止 · 做到一半的檔案會保留' : '')

  return (
    <div className="flex flex-col gap-2">
      <form onSubmit={submit} className="flex min-h-[52px] items-center gap-0.5 rounded-[26px] bg-control py-[8px] pl-4 pr-[9px]">
        <label className="flex min-w-0 flex-1">
          <span className="sr-only-text">{placeholder.replace(/…$/, '')}</span>
          <textarea
            ref={input}
            rows={1}
            value={text}
            onChange={(e) => setText(e.target.value)}
            onKeyDown={onKeyDown}
            placeholder={placeholder}
            className="field-sizing-content max-h-40 min-h-9 min-w-0 flex-1 resize-none bg-transparent px-1.5 py-[6px] text-[15px] leading-6 text-ink outline-none focus-visible:outline-none"
          />
        </label>
        {(mode === 'idle' || !empty) && (
          <IconButton icon={sending ? LoaderCircle : ArrowUp} label="送出" variant="send" type="submit" disabled={empty || sending} className={cx(sending && '[&_svg]:animate-spin')} />
        )}
        {mode === 'working' && (
          <button
            type="button"
            aria-label={`停止${name}`}
            onClick={onStop}
            className="relative flex size-[34px] flex-none items-center justify-center rounded-full bg-ink touch-target hover:brightness-[1.08]"
          >
            <span className="size-[11px] rounded-[2px] bg-app" />
          </button>
        )}
        {mode === 'stopping' && (
          <button type="button" aria-label="正在停止" disabled className="flex size-[34px] flex-none items-center justify-center rounded-full bg-hover text-ink-2 disabled:cursor-default">
            <Icon icon={LoaderCircle} size={18} className="animate-spin" />
          </button>
        )}
      </form>
      {line && <span className="self-center text-center text-meta text-ink-3">{line}</span>}
    </div>
  )
}
