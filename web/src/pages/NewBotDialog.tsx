import { useState, type FormEvent } from 'react'
import { ApiError, botsCreate } from '../api/generated'
import { upsertBot } from '../data/bots'
import { useStore } from '../data/store'
import { newBotDialog } from '../data/ui'
import { navigate } from '../router'
import { Avatar } from '../ui/Avatar'
import { COLORS, COLOR_IDS, COLOR_LABELS, SHAPE_IDS, SHAPE_LABELS, type AvatarColor, type AvatarShape } from '../ui/avatar-data'
import { Button } from '../ui/Button'
import { Dialog } from '../ui/Dialog'
import { TextInput } from '../ui/Field'
import { cx } from '../ui/cx'

// 新增 Bot 最簡版（W02）：名字、一句話說明、頭像形狀與顏色。
// G01 的「一句話產生建議」要等快判（X05），這裡先做手填的表單。

const pick = <T,>(xs: readonly T[]): T => xs[Math.floor(Math.random() * xs.length)]

function NewBotForm({ onDone }: { onDone: () => void }) {
  const [name, setName] = useState('')
  const [description, setDescription] = useState('')
  const [shape, setShape] = useState<AvatarShape>(() => pick(SHAPE_IDS))
  const [color, setColor] = useState<AvatarColor>(() => pick(COLOR_IDS))
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<unknown>(null)

  const submit = async (e: FormEvent) => {
    e.preventDefault()
    if (!name.trim()) {
      setErr(new ApiError(400, { code: 'invalid_input', message: '請填名字', retryable: false, fields: [{ field: 'name', message: '請填名字' }] }))
      return
    }
    setBusy(true)
    setErr(null)
    try {
      const bot = await botsCreate({ name: name.trim(), description: description.trim() || undefined, shape, color })
      upsertBot(bot)
      onDone()
      navigate(`/bots/${bot.id}`)
    } catch (e2) {
      setErr(e2)
      setBusy(false)
    }
  }

  const fieldErr = (f: string) => (err instanceof ApiError ? err.fields?.find((x) => x.field === f)?.message : undefined)
  const general = err && !(err instanceof ApiError && err.fields?.length) ? (err instanceof Error ? err.message : String(err)) : null

  return (
    <form onSubmit={submit} className="flex flex-col gap-4" aria-label="新增 Bot">
      <div className="flex items-center gap-4">
        <span className="flex size-16 flex-none items-center justify-center rounded-group bg-inset">
          <Avatar shape={shape} color={color} state="happy" dot="none" size={48} label={`${SHAPE_LABELS[shape]}、${COLOR_LABELS[color]}`} />
        </span>
        <div className="flex min-w-0 flex-1 flex-col gap-3">
          <TextInput label="名字" value={name} onChange={(e) => setName(e.target.value)} placeholder="例：小秘" maxLength={40} error={fieldErr('name')} data-autofocus />
        </div>
      </div>
      <TextInput
        label="它負責什麼（一句話）"
        value={description}
        onChange={(e) => setDescription(e.target.value)}
        placeholder="例：每天早上整理新聞給我"
        maxLength={200}
        error={fieldErr('description')}
      />
      <fieldset className="m-0 flex flex-col gap-1.5 border-0 p-0">
        <legend className="mb-1.5 p-0 text-meta text-ink-3">形狀</legend>
        <div role="radiogroup" aria-label="形狀" className="grid grid-cols-5 gap-1.5">
          {SHAPE_IDS.map((s) => (
            <button
              key={s}
              type="button"
              role="radio"
              aria-checked={s === shape}
              aria-label={SHAPE_LABELS[s]}
              onClick={() => setShape(s)}
              className={cx('flex h-12 items-center justify-center rounded-field border', s === shape ? 'border-focus bg-control' : 'border-transparent bg-inset hover:bg-control')}
            >
              <Avatar shape={s} color={color} state="idle" dot="none" size={32} blink={false} />
            </button>
          ))}
        </div>
      </fieldset>
      <fieldset className="m-0 flex flex-col gap-1.5 border-0 p-0">
        <legend className="mb-1.5 p-0 text-meta text-ink-3">顏色</legend>
        <div role="radiogroup" aria-label="顏色" className="flex flex-wrap gap-2">
          {COLOR_IDS.map((c) => (
            <button
              key={c}
              type="button"
              role="radio"
              aria-checked={c === color}
              aria-label={COLOR_LABELS[c]}
              onClick={() => setColor(c)}
              className={cx('relative size-8 rounded-full touch-target', c === color && 'ring-2 ring-ink ring-offset-2 ring-offset-panel')}
              style={{ background: COLORS[c] }}
            />
          ))}
        </div>
      </fieldset>
      <p className="m-0 text-meta text-ink-3">權限預設「先問我」，其他之後都能改。電腦在第一次交辦時才開機。</p>
      {general && (
        <p role="alert" className="m-0 text-meta text-danger">
          {general}
        </p>
      )}
      <div className="flex flex-wrap justify-end gap-2">
        <Button variant="secondary" onClick={onDone} disabled={busy}>
          取消
        </Button>
        <Button variant="primary" type="submit" loading={busy}>
          {name.trim() ? `建立「${name.trim()}」` : '建立'}
        </Button>
      </div>
    </form>
  )
}

export function NewBotDialog() {
  const open = useStore(newBotDialog)
  const close = () => newBotDialog.set(false)
  return (
    <Dialog open={open} onClose={close} title="新增 Bot" width={520}>
      {open && <NewBotForm onDone={close} />}
    </Dialog>
  )
}
