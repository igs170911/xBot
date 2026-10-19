import { useEffect, useImperativeHandle, useLayoutEffect, useRef, useState, type ClipboardEvent, type FormEvent, type KeyboardEvent, type Ref } from 'react'
import { ArrowUp, FileText, LoaderCircle, Plus, TriangleAlert, X } from 'lucide-react'
import { ATTACH_MAX, filesFrom, withAttachments, type Attachment, type Uploaded } from './attachments'
import { MentionMenu } from './MentionMenu'
import { mentionQuery } from './mentionQuery'
import { expandSkill, matchSkills, slashQuery, SkillMenu, useBotSkills, type Skill } from './SkillMenu'
import { usePhone } from '../mobile/nav'
import { IconButton } from '../ui/Button'
import { Icon } from '../ui/Icon'
import { cx } from '../ui/cx'
import { DictationBar, MicButton } from '../voice/Dictation'
import { useDictation } from '../voice/useDictation'

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
  /** 上傳一個附件到 Bot 的電腦（/workspace/uploads/），回傳路徑；沒給就不能附檔。 */
  onUpload?: (file: File) => Promise<Uploaded>
  /** 有給才顯示麥克風（語音輸入，E32）；辨識的花費算在這個 Bot 頭上。 */
  botId?: string
}

export type ComposerHandle = {
  /** 放字進輸入框（取代原本的字）並聚焦；text 沒給只聚焦。 */
  fill: (text?: string) => void
  /** 加附件（拖進對話時用）。 */
  attach: (files: File[]) => void
}

let nextId = 1

function sizeText(n: number): string {
  if (n < 1024 * 1024) return `${Math.max(1, Math.round(n / 1024))} KB`
  return `${(n / 1024 / 1024).toFixed(1)} MB`
}

// 不支援 field-sizing 的瀏覽器（Firefox、舊 Safari）用 JS 自動長高。
const fieldSizing = typeof CSS !== 'undefined' && typeof CSS.supports === 'function' && CSS.supports('field-sizing', 'content')

// 輸入框（design/C05-Composer）：#2F2F2F 膠囊 52 高；有字才能送出（白色圓鈕）；
// Bot 工作中右側是停止鈕（白底黑方塊），正在停止時變灰色轉圈。
export function ChatComposer({ name, mode, onSend, onStop, hint, handle, onUpload, botId }: ChatComposerProps) {
  const [text, setText] = useState('')
  const [sending, setSending] = useState(false)
  const [files, setFiles] = useState<Attachment[]>([])
  const [attachNote, setAttachNote] = useState<string | null>(null)
  const input = useRef<HTMLTextAreaElement>(null)
  const picker = useRef<HTMLInputElement>(null)
  const uploading = files.some((f) => f.state === 'uploading')
  const ready = files.filter((f) => f.state === 'done' && f.path)
  const empty = text.trim() === '' && ready.length === 0
  const phone = usePhone()
  const query = mentionQuery(text)
  const [closedAt, setClosedAt] = useState<string | null>(null)
  const showMention = query !== null && closedAt !== text
  const skills = useBotSkills(botId)
  const slash = slashQuery(text)
  const showSkills = !!botId && slash !== null && closedAt !== text
  const [active, setActive] = useState(0)
  const skillHits = showSkills ? matchSkills(skills, slash ?? '') : []

  const caretToEnd = useRef(false)
  // 語音輸入：聽到的字接在輸入框後面，可以再改了才送。
  const dictation = useDictation({
    botId,
    onText: (heard) => {
      setText((t) => (t.trim() ? t.replace(/\s*$/, ' ') + heard : heard))
      caretToEnd.current = true
      input.current?.focus()
    },
  })
  const pickSkill = (s: Skill) => {
    setText('/' + s.name + ' ')
    setActive(0)
    caretToEnd.current = true
    input.current?.focus()
  }
  const pickMention = (who: string) => {
    setText(text.replace(/@([^\s@]*)$/, '@' + who + ' '))
    caretToEnd.current = true
    input.current?.focus()
  }
  // 附件：一個一個上傳，畫面上是縮圖小卡（C05「8 · 附件」）。
  const attach = (list: File[]) => {
    if (!onUpload || list.length === 0) return
    const room = ATTACH_MAX - files.length
    if (room <= 0) {
      setAttachNote(`一則訊息最多附 ${ATTACH_MAX} 個檔案`)
      return
    }
    setAttachNote(list.length > room ? `一則訊息最多附 ${ATTACH_MAX} 個檔案，多的沒有加進來` : null)
    const added: Attachment[] = list.slice(0, room).map((f) => ({
      id: nextId++,
      name: f.name,
      size: f.size,
      preview: f.type.startsWith('image/') && typeof URL.createObjectURL === 'function' ? URL.createObjectURL(f) : undefined,
      state: 'uploading',
    }))
    setFiles((cur) => [...cur, ...added])
    added.forEach((a, i) => {
      onUpload(list[i])
        .then((up) => setFiles((cur) => cur.map((x) => (x.id === a.id ? { ...x, state: 'done', path: up.path } : x))))
        .catch((err: unknown) => setFiles((cur) => cur.map((x) => (x.id === a.id ? { ...x, state: 'error', error: err instanceof Error ? err.message : String(err) } : x))))
    })
    input.current?.focus()
  }
  const removeFile = (id: number) =>
    setFiles((cur) => {
      const f = cur.find((x) => x.id === id)
      if (f?.preview && typeof URL.revokeObjectURL === 'function') URL.revokeObjectURL(f.preview)
      return cur.filter((x) => x.id !== id)
    })
  const filesRef = useRef(files)
  useEffect(() => {
    filesRef.current = files
  }, [files])
  useEffect(
    () => () => {
      for (const f of filesRef.current) if (f.preview && typeof URL.revokeObjectURL === 'function') URL.revokeObjectURL(f.preview)
    },
    [],
  )

  useImperativeHandle(handle, () => ({
    fill: (t) => {
      if (t !== undefined) setText(t)
      caretToEnd.current = true
      input.current?.focus()
    },
    attach: (list) => attach(list),
  }))

  const onPaste = (e: ClipboardEvent<HTMLTextAreaElement>) => {
    if (!onUpload) return
    const pasted = filesFrom(e.clipboardData?.files)
    if (pasted.length === 0) return
    e.preventDefault()
    attach(pasted)
  }

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
    if (empty || sending || uploading) return
    const t = withAttachments(expandSkill(text, skills), ready.map((f) => f.path ?? ''))
    setSending(true)
    try {
      if (await onSend(t)) {
        setText('')
        for (const f of files) if (f.preview && typeof URL.revokeObjectURL === 'function') URL.revokeObjectURL(f.preview)
        setFiles([])
        setAttachNote(null)
      }
    } finally {
      setSending(false)
      input.current?.focus()
    }
  }

  const onKeyDown = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    // 中文輸入法選字時的 Enter 不是送出
    if (e.key === 'Escape' && (showMention || showSkills)) {
      e.preventDefault()
      setClosedAt(text)
      return
    }
    if (showSkills && skillHits.length && (e.key === 'ArrowDown' || e.key === 'ArrowUp')) {
      e.preventDefault()
      setActive((a) => (a + (e.key === 'ArrowDown' ? 1 : skillHits.length - 1)) % skillHits.length)
      return
    }
    if (showSkills && skillHits.length && (e.key === 'Enter' || e.key === 'Tab') && !e.shiftKey && !e.nativeEvent.isComposing && e.keyCode !== 229) {
      e.preventDefault()
      pickSkill(skillHits[Math.min(active, skillHits.length - 1)])
      return
    }
    if (e.key === 'Enter' && !e.shiftKey && !e.nativeEvent.isComposing && e.keyCode !== 229) {
      e.preventDefault()
      void submit()
    }
  }

  const placeholder = mode === 'stopping' ? `正在停止${name}…` : mode === 'working' ? (phone ? '這輪做完才會送出' : `補充說明給${name}…`) : `交代${name}…`
  const line = hint ?? (mode === 'working' ? `${name}工作中 · 要改做別的事，先按停止` : mode === 'stopping' ? '正在停止 · 做到一半的檔案會保留' : uploading ? '附件上傳好才能送出' : '')

  return (
    <div className="relative flex flex-col gap-2">
      {showMention && <MentionMenu query={query ?? ''} exclude={name} onPick={pickMention} />}
      {showSkills && <SkillMenu skills={skills} query={slash ?? ''} active={Math.min(active, Math.max(0, skillHits.length - 1))} onPick={pickSkill} />}
      {files.length > 0 && (
        <ul className="m-0 flex list-none flex-wrap gap-2 p-0" aria-label="附件" data-testid="attachments">
          {files.map((f) => (
            <li key={f.id} className={cx('flex h-12 max-w-[240px] items-center gap-2 rounded-field bg-control pl-1.5 pr-1', f.state === 'error' && 'ring-1 ring-danger')} title={f.error ?? f.path ?? f.name}>
              {f.preview ? (
                <img
                  src={f.preview}
                  alt=""
                  className="size-9 flex-none rounded-[8px] object-cover"
                  onError={() => setFiles((cur) => cur.map((x) => (x.id === f.id ? { ...x, preview: undefined } : x)))}
                />
              ) : (
                <span className="flex size-9 flex-none items-center justify-center rounded-[8px] bg-hover text-ink-2">
                  <Icon icon={f.state === 'error' ? TriangleAlert : FileText} size={18} />
                </span>
              )}
              <span className="flex min-w-0 flex-col">
                <span className="truncate text-ui-sm">{f.name}</span>
                <span className={cx('truncate text-[12px]', f.state === 'error' ? 'text-danger' : 'text-ink-3')}>
                  {f.state === 'uploading' ? '上傳中…' : f.state === 'error' ? `沒有傳上去：${f.error ?? ''}` : sizeText(f.size)}
                </span>
              </span>
              {f.state === 'uploading' ? (
                <Icon icon={LoaderCircle} size={16} className="mx-1.5 flex-none animate-spin text-ink-2" />
              ) : (
                <button type="button" aria-label={`移除 ${f.name}`} onClick={() => removeFile(f.id)} className="flex size-7 flex-none items-center justify-center rounded-full text-ink-2 hover:bg-hover hover:text-ink">
                  <Icon icon={X} size={14} />
                </button>
              )}
            </li>
          ))}
          {files.length > 1 && <li className="self-center text-meta text-ink-3">{files.length} 個 · {sizeText(files.reduce((n, f) => n + f.size, 0))}</li>}
        </ul>
      )}
      {attachNote && <span className="text-meta text-warning-ink">{attachNote}</span>}
      {dictation.active && dictation.phase.kind !== 'starting' && <DictationBar d={dictation} />}
      <form hidden={dictation.active && dictation.phase.kind !== 'starting'} onSubmit={submit} className={cx('flex min-h-[52px] items-center gap-0.5 rounded-[26px] bg-control py-[8px] pr-[9px]', onUpload ? 'pl-[7px]' : 'pl-4')}>
        {onUpload && (
          <>
            <IconButton icon={Plus} label="附加檔案" onClick={() => picker.current?.click()} disabled={files.length >= ATTACH_MAX} />
            <input
              ref={picker}
              type="file"
              multiple
              hidden
              aria-hidden="true"
              tabIndex={-1}
              data-testid="attach-input"
              onChange={(e) => {
                attach(filesFrom(e.target.files))
                e.target.value = ''
              }}
            />
          </>
        )}
        {/* label 撐滿輸入列的高度（含上下留白），點輸入列任何地方都會聚焦到輸入框（手機至少 44 高） */}
        <label className="-my-[8px] flex min-w-0 flex-1 items-center self-stretch py-[8px]">
          <span className="sr-only-text">{placeholder.replace(/…$/, '')}</span>
          <textarea
            ref={input}
            rows={1}
            value={text}
            onChange={(e) => setText(e.target.value)}
            onKeyDown={onKeyDown}
            onPaste={onPaste}
            placeholder={placeholder}
            className="field-sizing-content max-h-40 min-h-9 min-w-0 flex-1 resize-none bg-transparent px-1.5 py-[6px] text-[15px] leading-6 text-ink outline-none focus-visible:outline-none"
          />
        </label>
        {botId && <MicButton d={dictation} />}
        {(mode === 'idle' || !empty) && (
          <IconButton icon={sending ? LoaderCircle : ArrowUp} label="送出" variant="send" type="submit" disabled={empty || sending || uploading} className={cx(sending && '[&_svg]:animate-spin')} />
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
