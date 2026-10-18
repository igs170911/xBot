import { useState, type FormEvent } from 'react'
import { ChevronDown } from 'lucide-react'
import { roomsCreate } from '../api/generated'
import { botsStore, look } from '../data/bots'
import { newRoomDialog, upsertRoom } from '../data/rooms'
import { useStore } from '../data/store'
import { navigate } from '../router'
import { Avatar } from '../ui/Avatar'
import { Button } from '../ui/Button'
import { Dialog } from '../ui/Dialog'
import { TextInput, Textarea } from '../ui/Field'
import { Icon } from '../ui/Icon'
import { cx } from '../ui/cx'
import { ROOM_MAX, ROOM_MIN, RULES, autoName, type Rule } from './rules'

// 新增群組對話（design/D03）：勾成員就能建立；名稱自動、回應規則預設「自動挑一位」，其他收在進階設定。

function NewRoomForm({ onDone }: { onDone: () => void }) {
  const bots = (useStore(botsStore).bots ?? []).filter((b) => !b.archived)
  const [picked, setPicked] = useState<string[]>([])
  const [name, setName] = useState('')
  const [rule, setRule] = useState<Rule>('auto')
  const [bulletin, setBulletin] = useState('')
  const [more, setMore] = useState(false)
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string | null>(null)
  const chosen = picked.map((id) => bots.find((b) => b.id === id)).filter((b) => !!b)
  const enough = picked.length >= ROOM_MIN
  const toggle = (id: string) =>
    setPicked((p) => (p.includes(id) ? p.filter((x) => x !== id) : p.length >= ROOM_MAX ? p : [...p, id]))

  const submit = async (e: FormEvent) => {
    e.preventDefault()
    if (!enough) return
    setBusy(true)
    setErr(null)
    try {
      const room = await roomsCreate({ member_ids: picked, name: name.trim() || undefined, responder_rule: rule, bulletin: bulletin.trim() || undefined })
      upsertRoom(room)
      onDone()
      navigate(`/rooms/${room.id}`)
    } catch (e2) {
      setErr(e2 instanceof Error ? e2.message : String(e2))
      setBusy(false)
    }
  }

  return (
    <form onSubmit={submit} aria-label="新增群組對話" className="flex flex-col gap-4">
      <div className="flex flex-col gap-2">
        <div className="flex items-baseline justify-between text-meta text-ink-3">
          <span>成員</span>
          <span data-testid="room-count">
            {picked.length} / {ROOM_MAX}
          </span>
        </div>
        <div role="group" aria-label="成員" className="grid grid-cols-2 gap-2 max-sm:grid-cols-1">
          {bots.map((b) => {
            const on = picked.includes(b.id)
            const full = !on && picked.length >= ROOM_MAX
            return (
              <button
                key={b.id}
                type="button"
                role="checkbox"
                aria-checked={on}
                aria-disabled={full || undefined}
                onClick={() => toggle(b.id)}
                className={cx(
                  'flex min-h-14 items-center gap-2.5 rounded-group border px-3 text-left',
                  on ? 'border-ink/60 bg-control' : 'border-line bg-transparent hover:bg-control/50',
                  full && 'opacity-50',
                )}
              >
                <Avatar {...look(b)} dot="none" size={30} blink={false} />
                <span className="flex min-w-0 flex-1 flex-col leading-[1.35]">
                  <span className="truncate text-ui-sm text-ink">{b.name}</span>
                  <span className="truncate text-meta text-ink-3">{b.title || b.description || ' '}</span>
                </span>
                <span aria-hidden="true" className={cx('flex size-[18px] flex-none items-center justify-center rounded-chk', on ? 'bg-ink' : 'border-[1.5px] border-check-off')}>
                  {on && (
                    <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="var(--color-on-ink)" strokeWidth="3" strokeLinecap="round" strokeLinejoin="round">
                      <path d="M20 6 9 17l-5-5" />
                    </svg>
                  )}
                </span>
              </button>
            )
          })}
        </div>
        {bots.length < ROOM_MIN && <p className="m-0 text-meta text-ink-2">至少要有 2 個 Bot 才能開群組，先新增 Bot。</p>}
      </div>

      <p className="m-0 text-ui-sm text-ink-2" data-testid="room-name-preview">
        名稱：{name.trim() || (chosen.length ? autoName(chosen.map((b) => b.name)) : '用成員名字自動命名')}
      </p>

      <button type="button" aria-expanded={more} onClick={() => setMore((m) => !m)} className="flex items-center gap-1 self-start bg-transparent p-0 text-ui-sm text-ink-2 hover:text-ink">
        進階設定
        <Icon icon={ChevronDown} size={16} className={cx('transition-transform', !more && '-rotate-90')} />
      </button>
      {more && (
        <div className="flex flex-col gap-4">
          <TextInput label="名稱" hint="可以留白，用成員名字命名" value={name} onChange={(e) => setName(e.target.value)} maxLength={60} placeholder={chosen.length ? autoName(chosen.map((b) => b.name)) : ''} />
          <fieldset className="m-0 flex flex-col gap-1.5 border-0 p-0">
            <legend className="mb-1.5 p-0 text-meta text-ink-3">沒有點名時，誰回答</legend>
            <div role="radiogroup" aria-label="回應規則" className="flex flex-col gap-1">
              {RULES.map((r) => (
                <button
                  key={r.value}
                  type="button"
                  role="radio"
                  aria-checked={rule === r.value}
                  onClick={() => setRule(r.value)}
                  className={cx('flex items-start gap-2.5 rounded-field px-2.5 py-1.5 text-left', rule === r.value ? 'bg-control' : 'bg-transparent hover:bg-control/50')}
                >
                  <span aria-hidden="true" className={cx('mt-[3px] flex size-4 flex-none items-center justify-center rounded-full border-[1.5px]', rule === r.value ? 'border-ink' : 'border-check-off')}>
                    {rule === r.value && <span className="size-2 rounded-full bg-ink" />}
                  </span>
                  <span className="flex flex-col">
                    <span className="text-ui-sm text-ink">{r.label(chosen[0]?.name ?? '第一位成員')}</span>
                    <span className="text-meta text-ink-3">{r.note}</span>
                  </span>
                </button>
              ))}
            </div>
          </fieldset>
          <Textarea label="群組指示（選填）" value={bulletin} onChange={(e) => setBulletin(e.target.value)} maxLength={4000} rows={3} placeholder="誰負責什麼、語氣、目標、檢查清單…" />
        </div>
      )}

      {!enough && <p className="m-0 text-meta text-ink-3">至少要 2 個 Bot 才能開群組</p>}
      {err && (
        <p role="alert" className="m-0 text-meta text-danger">
          {err}
        </p>
      )}
      <div className="flex flex-wrap justify-end gap-2">
        <Button variant="secondary" onClick={onDone} disabled={busy}>
          取消
        </Button>
        <Button variant="primary" type="submit" loading={busy} disabled={!enough}>
          建立群組（{picked.length} 位）
        </Button>
      </div>
    </form>
  )
}

export function NewRoomDialog() {
  const open = useStore(newRoomDialog)
  const close = () => newRoomDialog.set(false)
  return (
    <Dialog open={open} onClose={close} title="新增群組對話" description="選 2 到 10 個 Bot。在群組裡用 @名字 指定誰負責；沒有點名時，照回應規則決定誰回答。" width={640}>
      {open && <NewRoomForm onDone={close} />}
    </Dialog>
  )
}
