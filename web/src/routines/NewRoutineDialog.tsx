import { useEffect, useRef, useState, type FormEvent } from 'react'
import { ChevronRight, CircleAlert, MessageCircle, Sparkles, X } from 'lucide-react'
import { ApiError, routinesCreate, routinesPropose } from '../api/generated'
import { botsStore } from '../data/bots'
import { useStore } from '../data/store'
import { navigate } from '../router'
import { Button, IconButton } from '../ui/Button'
import { Switch } from '../ui/Controls'
import { TextInput, fieldClass } from '../ui/Field'
import { Icon } from '../ui/Icon'
import { Select } from '../ui/Menu'
import { StatusChip } from '../ui/Pill'
import { useToast } from '../ui/toast-context'
import { useFocusTrap } from '../ui/useFocusTrap'
import { cx } from '../ui/cx'
import { JITTER_CHOICES, jitterText, loadRoutines, newRoutineDialog, whenText, type Proposal } from './data'
import { useLockBody } from '../ui/useLockBody'

// 一句話建立定時任務（H02、L17；PRD §21：15 個欄位 → 1 句話＋確認）。
// 打字就解析（xbot.routines.propose），確認卡列出誰做／做什麼／什麼時候／結果貼到／下次執行；其他收在「進階設定」。


const EXAMPLES = ['每天早上 9 點整理新聞給我', '平日下午三點整理客服信', '每 30 分鐘檢查網站有沒有掛', '每週一早上十點寫週報']
const TIMES = Array.from({ length: 34 }, (_, i) => {
  const h = 6 + Math.floor(i / 2)
  return `${String(h).padStart(2, '0')}:${i % 2 ? '30' : '00'}`
})

function clockLabel(hhmm: string): string {
  const [h, m] = hhmm.split(':').map(Number)
  const d = new Date(2026, 0, 1, h, m)
  return whenText(d.getTime(), d).replace('今天', '')
}

function errText(err: unknown): string {
  if (err instanceof ApiError) return err.fields?.[0]?.message ?? err.message
  return err instanceof Error ? err.message : String(err)
}

function Row({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex min-h-[52px] flex-wrap items-center gap-x-3 gap-y-1 border-t border-line/70 py-2 first:border-t-0">
      <span className="w-[72px] flex-none text-sub text-ink-2">{label}</span>
      <div className="flex min-w-0 flex-1 flex-wrap items-center gap-2">{children}</div>
    </div>
  )
}

function RoutineForm({ initial, onDone }: { initial?: string; onDone: () => void }) {
  const bots = useStore(botsStore).bots ?? []
  const toast = useToast()
  const [text, setText] = useState(initial ?? EXAMPLES[0])
  const [botId, setBotId] = useState('')
  const [what, setWhat] = useState<string | null>(null) // 手動改過「做什麼」
  const [time, setTime] = useState('') // 句子沒寫時間時補的
  const [advanced, setAdvanced] = useState(false)
  const [jitter, setJitter] = useState(0)
  const [goalOn, setGoalOn] = useState(false)
  const [doneWhen, setDoneWhen] = useState('')
  const [rounds, setRounds] = useState(10)
  const [timezone, setTimezone] = useState('Asia/Taipei')
  const [hook, setHook] = useState(false)
  const [notify, setNotify] = useState(true)
  const [p, setP] = useState<Proposal | null>(null)
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string | null>(null)
  const seq = useRef(0)

  // 打字即時更新確認卡（停 250 毫秒才問一次）。
  useEffect(() => {
    const said = text.trim()
    const n = ++seq.current
    const t = window.setTimeout(() => {
      if (!said) {
        setP(null)
        return
      }
      const full = time ? `${said} ${time}` : said
      routinesPropose({ text: full, bot_id: botId || undefined, timezone: timezone.trim() || undefined })
        .then((out) => {
          if (n === seq.current) setP(out)
        })
        .catch((e: unknown) => {
          if (n === seq.current) setErr(errText(e))
        })
    }, 250)
    return () => window.clearTimeout(t)
  }, [text, botId, time, timezone])

  const missingTime = !!p?.missing.includes('time')
  const prompt = (what ?? p?.draft.prompt ?? '').trim()
  const kind = p?.draft.schedule?.kind
  const jitterOK = !(kind === 'interval' && jitter > 0 && jitter >= (p?.draft.schedule?.every_minutes ?? 0))
  const ready = !!p && p.ready && !!prompt && jitterOK && (!goalOn || !!doneWhen.trim())
  const bot = bots.find((b) => b.id === p?.draft.bot_id)

  const submit = async (e: FormEvent) => {
    e.preventDefault()
    if (!p || !ready) return
    setBusy(true)
    setErr(null)
    try {
      const out = await routinesCreate({
        ...p.draft,
        prompt,
        title: what !== null ? undefined : p.draft.title,
        jitter_minutes: jitter || undefined,
        notify_on_failure: notify,
        goal: goalOn ? { done_when: doneWhen.trim(), max_rounds: rounds } : undefined,
        hook: hook || undefined,
      })
      await loadRoutines()
      const r = out.routine
      onDone()
      toast.show({
        kind: 'done',
        text: r?.next_run_at ? `已建立 · ${bot?.name ?? 'Bot'}${whenText(r.next_run_at)} 第一次跑` : '已建立',
        action: { label: '看行事曆', onClick: () => navigate('/calendar') },
      })
    } catch (e2) {
      setErr(errText(e2))
      setBusy(false)
    }
  }

  const advSummary = [jitterText(jitter), goalOn ? `做到完成（最多 ${rounds} 輪）` : null, hook ? '網址觸發' : null].filter(Boolean).join(' · ')

  return (
    <form onSubmit={submit} aria-label="新增定時任務" className="flex min-h-0 flex-1 flex-col">
      <div className="flex min-h-0 flex-1 flex-col gap-4 overflow-y-auto px-6 pb-5 pt-5 max-sm:px-4">
        <div className="flex flex-col gap-2.5">
          <label htmlFor="routine-say" className="text-[15px]">
            用一句話說要它定時做什麼
          </label>
          <div className="flex h-[52px] items-center gap-2.5 rounded-[14px] border border-focus bg-inset pl-4 pr-2 focus-within:shadow-[0_0_0_3px_rgb(69_159_254/0.18)]">
            <span className="text-accent-ink">
              <Icon icon={Sparkles} size={20} />
            </span>
            <input
              id="routine-say"
              data-autofocus
              value={text}
              onChange={(e) => {
                setText(e.target.value)
                setTime('')
                setErr(null)
              }}
              placeholder="例：每天早上 9 點整理新聞給我"
              maxLength={2000}
              className="h-full min-w-0 flex-1 bg-transparent text-[16px] text-ink outline-none"
            />
            {text && <IconButton icon={X} label="清除" onClick={() => setText('')} />}
          </div>
          <div className="flex flex-wrap items-center gap-2 text-meta text-ink-3">
            試試：
            {EXAMPLES.slice(1).map((ex) => (
              <button key={ex} type="button" onClick={() => setText(ex)} className="h-7 rounded-full border border-line bg-transparent px-3 text-meta text-ink-2 hover:bg-control">
                {ex}
              </button>
            ))}
          </div>
        </div>

        <section aria-label="確認一下" data-testid="routine-confirm" className="flex flex-col rounded-card bg-card px-5 pb-2 pt-3.5">
          <div className="flex items-center justify-between gap-2 pb-1.5">
            <h3 className="m-0 text-[16px] font-normal">確認一下</h3>
            <StatusChip tone="accent" size="sm" icon={Sparkles}>
              系統判斷 · 都可以改
            </StatusChip>
          </div>
          <Row label="誰做">
            <Select
              label="誰做"
              hideLabel
              look="compact"
              value={p?.draft.bot_id ?? ''}
              options={bots.filter((b) => !b.archived).map((b) => ({ value: b.id, label: b.title ? `${b.name}（${b.title}）` : b.name }))}
              onChange={(v) => setBotId(v)}
              placeholder="還沒有 Bot"
            />
          </Row>
          <Row label="做什麼">
            <input
              aria-label="做什麼"
              value={what ?? p?.draft.prompt ?? ''}
              onChange={(e) => setWhat(e.target.value)}
              placeholder="要它做什麼"
              className={cx(fieldClass, 'h-9')}
            />
          </Row>
          <Row label="什麼時候">
            {missingTime ? (
              <>
                <span className="inline-flex items-center gap-1.5 text-ui-sm text-warning-ink" role="status">
                  <Icon icon={CircleAlert} size={16} />
                  沒看到時間，要幾點做？
                </span>
                <Select label="選時間" hideLabel look="compact" value={time} options={TIMES.map((t) => ({ value: t, label: clockLabel(t) }))} onChange={setTime} placeholder="選時間" />
              </>
            ) : (
              <span data-testid="routine-when" className="text-ui">
                {p?.card.when ?? '…'}
              </span>
            )}
          </Row>
          <Row label="結果貼到">
            <span className="inline-flex items-center gap-1.5 text-ui">
              <Icon icon={MessageCircle} size={16} className="text-ink-2" />
              {bot ? `和${bot.name}的對話（新的一條）` : '—'}
            </span>
          </Row>
          <Row label="下次執行">
            <span data-testid="routine-next" className="text-ui">
              {p?.problem ?? (p?.ready ? p.card.next : '—')}
            </span>
          </Row>
          {p && p.notes.length > 0 && <p className="m-0 border-t border-line/70 py-2 text-meta text-ink-3">{p.notes.join('；')}</p>}
        </section>

        <section className="flex flex-col rounded-card border border-line px-4">
          <button
            type="button"
            aria-expanded={advanced}
            onClick={() => setAdvanced((a) => !a)}
            className="flex h-12 items-center gap-2 bg-transparent text-left text-ui text-ink"
          >
            <span className={cx('text-ink-2 transition-transform', advanced && 'rotate-90')}>
              <Icon icon={ChevronRight} size={18} />
            </span>
            <span className="flex-1">進階設定</span>
            <span className="truncate text-meta text-ink-3">{advSummary}</span>
          </button>
          {advanced && (
            <div className="flex flex-col gap-3 border-t border-line/70 pb-4 pt-3">
              <div className="flex flex-wrap items-center justify-between gap-2">
                <span className="text-ui-sm">時間飄移</span>
                <Select
                  label="時間飄移"
                  hideLabel
                  look="compact"
                  value={String(jitter)}
                  options={JITTER_CHOICES.map((m) => ({ value: String(m), label: m === 0 ? '不飄移' : `${m} 分鐘內` }))}
                  onChange={(v) => setJitter(Number(v))}
                />
              </div>
              {!jitterOK && <p className="m-0 text-meta text-danger">每隔一段時間執行時，隨機延後要少於間隔</p>}
              <div className="flex flex-col gap-2">
                <Switch checked={goalOn} onChange={setGoalOn} label="做到完成為止">
                  做到完成為止
                </Switch>
                {goalOn && (
                  <div className="flex flex-wrap gap-3 pl-[50px]">
                    <TextInput label="完成條件" value={doneWhen} onChange={(e) => setDoneWhen(e.target.value)} placeholder="例：找到 10 個高佣金新品" maxLength={1000} wrapperClassName="min-w-[220px] flex-1" />
                    <TextInput
                      label="最多幾輪"
                      type="number"
                      min={1}
                      max={50}
                      value={rounds}
                      onChange={(e) => setRounds(Math.max(1, Math.min(50, Number(e.target.value) || 1)))}
                      wrapperClassName="w-24"
                      hint="上限 50"
                    />
                  </div>
                )}
              </div>
              <TextInput label="時區" value={timezone} onChange={(e) => setTimezone(e.target.value)} placeholder="Asia/Taipei" wrapperClassName="max-w-[260px]" />
              <Switch checked={hook} onChange={setHook} label="網址觸發">
                也開一個網址觸發（外部服務可以叫它跑）
              </Switch>
              <Switch checked={notify} onChange={setNotify} label="沒跑成功時通知我">
                沒跑成功時通知我
              </Switch>
            </div>
          )}
        </section>
        {err && (
          <p role="alert" className="m-0 text-sub text-danger">
            {err}
          </p>
        )}
      </div>
      <footer className="flex flex-none flex-wrap items-center gap-3 border-t border-divider px-6 py-3.5 max-sm:px-4">
        <span className="min-w-0 flex-1 text-meta text-ink-3">建立後隨時可以在定時任務裡改。</span>
        <Button onClick={onDone}>取消</Button>
        <Button type="submit" variant="primary" disabled={!ready} loading={busy}>
          建立
        </Button>
      </footer>
    </form>
  )
}

// H02：760 寬的對話框；手機（L17）是全螢幕。
export function NewRoutineDialog() {
  const { open, text } = useStore(newRoutineDialog)
  const ref = useRef<HTMLDivElement>(null)
  const close = () => newRoutineDialog.set({ open: false })
  const onKeyDown = useFocusTrap(ref, open, close)
  useLockBody(open) // 手機上它蓋滿整個畫面：後面的頁面不跟著捲
  if (!open) return null
  return (
    <div className="fixed-screen z-50 flex items-center justify-center bg-black/60 sm:p-4" onMouseDown={(e) => e.target === e.currentTarget && close()}>
      <div
        ref={ref}
        role="dialog"
        aria-modal="true"
        aria-labelledby="new-routine-title"
        tabIndex={-1}
        onKeyDown={onKeyDown}
        className="flex h-full max-h-[var(--app-full)] w-full max-w-[760px] flex-col overflow-hidden border-divider bg-panel shadow-dialog outline-none sm:h-auto sm:max-h-[calc(100dvh-32px)] sm:rounded-card sm:border"
      >
        <header className="flex h-16 flex-none items-center gap-3 border-b border-divider pl-6 pr-4 max-sm:pl-4">
          <h2 id="new-routine-title" className="m-0 flex-1 text-[18px] font-normal">
            新增定時任務
          </h2>
          <IconButton icon={X} label="關閉" onClick={close} />
        </header>
        <RoutineForm initial={text} onDone={close} />
      </div>
    </div>
  )
}
