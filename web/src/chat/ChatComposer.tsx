import { useImperativeHandle, useLayoutEffect, useRef, useState, type FormEvent, type KeyboardEvent, type Ref } from 'react'
import { ArrowUp, LoaderCircle } from 'lucide-react'
import { MentionMenu } from './MentionMenu'
import { mentionQuery } from './mentionQuery'
import { usePhone } from '../mobile/nav'
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
  /** 從外面放字進輸入框或聚焦（例：空狀態的建議）。 */
  handle?: Ref<ComposerHandle>
}

export type ComposerHandle = {
  /** 放字進輸入框（取代原本的字）並聚焦；text 沒給只聚焦。 */
  fill: (text?: string) => void
}

// 不支援 field-sizing 的瀏覽器（Firefox、舊 Safari）用 JS 自動長高。
const fieldSizing = typeof CSS !== 'undefined' && typeof CSS.supports === 'function' && CSS.supports('field-sizing', 'content')

// 輸入框（design/C05-Composer）：#2F2F2F 膠囊 52 高；有字才能送出（白色圓鈕）；
// Bot 工作中右側是停止鈕（白底黑方塊），正在停止時變灰色轉圈。
export function ChatComposer({ name, mode, onSend, onStop, hint, handle }: ChatComposerProps) {
  const [text, setText] = useState('')
  const [sending, setSending] = useState(false)
  const input = useRef<HTMLTextAreaElement>(null)
  const empty = text.trim() === ''
  const phone = usePhone()
  const query = mentionQuery(text)
  const [closedAt, setClosedAt] = useState<string | null>(null)
  const showMention = query !== null && closedAt !== text

  const caretToEnd = useRef(false)
  const pickMention = (who: string) => {
    setText(text.replace(/@([^\s@]*)$/, '@' + who + ' '))
    caretToEnd.current = true
    input.current?.focus()
  }
  useImperativeHandle(handle, () => ({
    fill: (t) => {
      if (t !== undefined) setText(t)
      caretToEnd.current = true
      input.current?.focus()
    },
  }))

  useLayoutEffect(() => {
    const el = input.current
    if (el && caretToEnd.current) {
      caretToEnd.current = false
      el.setSelectionRange(el.value.length, el.value.length)
    }
    if (fieldSizing || !el) return
    el.style.height = 'auto'
    el.style.height = `${Math.min(el.scrollHeight, 160)}px`
  }, [text])

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
    if (e.key === 'Escape' && showMention) {
      e.preventDefault()
      setClosedAt(text)
      return
    }
    if (e.key === 'Enter' && !e.shiftKey && !e.nativeEvent.isComposing && e.keyCode !== 229) {
      e.preventDefault()
      void submit()
    }
  }

  const placeholder = mode === 'stopping' ? `正在停止${name}…` : mode === 'working' ? (phone ? '這輪做完才會送出' : `補充說明給${name}…`) : `交代${name}…`
  const line = hint ?? (mode === 'working' ? `${name}工作中 · 要改做別的事，先按停止` : mode === 'stopping' ? '正在停止 · 做到一半的檔案會保留' : '')

  return (
    <div className="relative flex flex-col gap-2">
      {showMention && <MentionMenu query={query ?? ''} exclude={name} onPick={pickMention} />}
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
      {line && !phone && <span className="self-center text-center text-meta text-ink-3">{line}</span>}
    </div>
  )
}
