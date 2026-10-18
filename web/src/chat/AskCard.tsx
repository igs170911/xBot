import { useState, type FormEvent, type ReactNode } from 'react'
import { ArrowUp, Check, Clock, Eye, EyeOff, KeyRound, Lock, ShieldAlert, X } from 'lucide-react'
import { ApiError, chatRespond, type ChatRespondInput, type ChatRespondOutput } from '../api/generated'
import { useNow } from '../data/ui'
import { Avatar } from '../ui/Avatar'
import { Button, IconButton } from '../ui/Button'
import { OptionList } from '../ui/Card'
import { fieldClass } from '../ui/Field'
import { Icon } from '../ui/Icon'
import { cx } from '../ui/cx'
import { answeredLine, type Answered, type Ask } from './ask'

// Bot 在等你回覆的卡（design/C08 核准、C09 提問、C10 密碼）：對話裡和待我處理用同一張，
// 回覆一律呼叫 xbot.chat.respond。送出後由事件（chat.answered／inbox.updated）收成一行結果。

export type AskBot = { name: string; shape?: string; color?: string }

const LETTERS = 'ABCDEFGHIJ'

function errText(err: unknown): string {
  if (err instanceof ApiError) return err.fields?.[0]?.message || err.message
  return err instanceof Error ? err.message : String(err)
}

function Expiry({ at }: { at: number }) {
  const now = useNow(true)
  const left = Math.max(0, Math.ceil((at - now) / 60_000))
  return (
    <span className="inline-flex h-7 flex-none items-center gap-1.5 rounded-full bg-warning-soft px-2.5 text-[13px] text-warning-ink" data-testid="ask-expiry">
      <Icon icon={Clock} size={14} />
      {left > 0 ? `10 分鐘內沒回覆會過期（還有 ${left} 分鐘）` : '快過期了'}
    </span>
  )
}

function Head({ bot, title, sub, aside }: { bot?: AskBot; title: ReactNode; sub?: ReactNode; aside?: ReactNode }) {
  return (
    <div className="flex flex-wrap items-start gap-x-3 gap-y-2">
      <Avatar shape={bot?.shape} color={bot?.color} state="notifying" dot="waiting" ring="#262626" size={34} />
      <div className="flex min-w-0 flex-[1_1_180px] flex-col">
        <h2 className="m-0 text-[16px] font-normal leading-[1.45]">{title}</h2>
        {sub && <span className="text-[13px] text-ink-2">{sub}</span>}
      </div>
      {aside}
    </div>
  )
}

export function AskCard({
  ask,
  bot,
  sub,
  onDone,
}: {
  ask: Ask
  bot?: AskBot
  /** 標題下面那一行（例：「帳房 · 小秘交辦的工作」）。 */
  sub?: ReactNode
  onDone?: (out: ChatRespondOutput) => void
}) {
  const [busy, setBusy] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)
  const name = bot?.name ?? ask.botName ?? 'Bot'

  const respond = async (key: string, input: Omit<ChatRespondInput, 'inbox_id'>) => {
    setBusy(key)
    setError(null)
    try {
      const out = await chatRespond({ inbox_id: ask.inboxId, ...input })
      onDone?.(out)
    } catch (err) {
      setError(errText(err))
    } finally {
      setBusy(null)
    }
  }

  const expiry = ask.expiresAt ? <Expiry at={ask.expiresAt} /> : undefined
  const alert = error && (
    <p role="alert" className="m-0 text-sub text-danger">
      {error}
    </p>
  )

  if (ask.kind === 'approval') {
    const choices = ask.choices?.length ? ask.choices : ['once', 'deny']
    return (
      <section aria-label="需要允許這項操作" data-testid="ask-card" data-kind="approval" className="flex flex-col gap-3 rounded-card bg-card px-5 py-4">
        <Head bot={bot} title="需要允許這項操作" sub={sub ?? `${name} 想執行指令`} aside={expiry} />
        <div className="flex flex-col gap-1 rounded-menu bg-inset px-3.5 py-3">
          {ask.description && <span className="text-ui">{ask.description}</span>}
          <code className="whitespace-pre-wrap break-all font-mono text-[13px] text-ink-2">{ask.command || ask.prompt}</code>
        </div>
        {alert}
        <div className="flex flex-wrap items-center gap-2">
          <Button variant="primary" loading={busy === 'once'} disabled={!!busy} onClick={() => void respond('once', { choice: 'once' })}>
            允許這次
          </Button>
          {choices.includes('always') && (
            <Button loading={busy === 'always'} disabled={!!busy} onClick={() => void respond('always', { choice: 'always' })}>
              一律允許
            </Button>
          )}
          <Button variant="outline" loading={busy === 'deny'} disabled={!!busy} onClick={() => void respond('deny', { choice: 'deny' })}>
            拒絕
          </Button>
          {choices.includes('always') && <span className="ml-auto text-meta text-ink-3 max-sm:ml-0">一律允許＝以後這個指令不再問</span>}
        </div>
      </section>
    )
  }

  if (ask.kind === 'secret') return <SecretCard ask={ask} bot={bot} sub={sub} expiry={expiry} busy={busy} alert={alert} respond={respond} />

  if (ask.questions && ask.questions.length > 1) return <MultiQuestion ask={ask} bot={bot} sub={sub} expiry={expiry} busy={busy} alert={alert} respond={respond} />

  return <SingleQuestion ask={ask} bot={bot} name={name} sub={sub} expiry={expiry} busy={busy} alert={alert} respond={respond} />
}

type Inner = {
  ask: Ask
  bot?: AskBot
  sub?: ReactNode
  expiry?: ReactNode
  busy: string | null
  alert: ReactNode
  respond: (key: string, input: Omit<ChatRespondInput, 'inbox_id'>) => Promise<void>
}

function SingleQuestion({ ask, bot, name, sub, expiry, busy, alert, respond }: Inner & { name: string }) {
  const [text, setText] = useState('')
  const options = (ask.choices ?? []).map((c, i) => ({ key: LETTERS[i] ?? String(i + 1), label: c }))
  const submit = (e: FormEvent) => {
    e.preventDefault()
    if (text.trim()) void respond('text', { text: text.trim() })
  }
  return (
    <section aria-label={`${name}的提問`} data-testid="ask-card" data-kind="question" className="flex flex-col gap-3 rounded-card bg-card px-5 py-4">
      <Head bot={bot} title={ask.prompt || '需要你的回覆'} sub={sub ?? `${name} · 等你回覆`} aside={expiry} />
      {options.length > 0 && (
        <div className="overflow-hidden rounded-menu border border-line bg-[#2A2A2A] px-2 [&>div>button:first-child]:border-t-0">
          <OptionList options={options} onPick={(k) => void respond('opt' + k, { text: options.find((o) => o.key === k)?.label ?? '' })} />
        </div>
      )}
      <form onSubmit={submit} className="relative flex">
        <label className="sr-only-text" htmlFor={`ask-${ask.inboxId}`}>
          {options.length ? '或輸入你自己的答案' : `回覆${name}`}
        </label>
        <input
          id={`ask-${ask.inboxId}`}
          value={text}
          onChange={(e) => setText(e.target.value)}
          placeholder={options.length ? '或輸入你自己的答案' : `回覆${name}…`}
          disabled={!!busy}
          className={cx(fieldClass, 'h-11 pr-12')}
        />
        <span className="absolute right-1.5 top-1.5">
          <IconButton icon={ArrowUp} label="送出回答" variant="send" size="sm" type="submit" disabled={!text.trim() || !!busy} />
        </span>
      </form>
      {alert}
      <div className="flex">
        <Button size="sm" variant="ghost" disabled={!!busy} loading={busy === 'skip'} onClick={() => void respond('skip', { decline: true })}>
          略過這題
        </Button>
      </div>
    </section>
  )
}

function MultiQuestion({ ask, bot, sub, expiry, busy, alert, respond }: Inner) {
  const qs = ask.questions ?? []
  const [i, setI] = useState(0)
  const [answers, setAnswers] = useState<string[]>(() => qs.map(() => ''))
  const q = qs[i]
  const set = (v: string) => setAnswers((a) => a.map((x, j) => (j === i ? v : x)))
  const options = (q?.choices ?? []).map((c, k) => ({ key: LETTERS[k] ?? String(k + 1), label: c }))
  const last = i === qs.length - 1
  return (
    <section aria-label="Bot 的提問" data-testid="ask-card" data-kind="questions" className="flex flex-col gap-3 rounded-card bg-card px-5 py-4">
      <Head bot={bot} title={q?.question || ask.prompt} sub={sub} aside={expiry} />
      <div className="flex items-center gap-3 text-meta text-ink-2">
        <span>
          第 {i + 1} / {qs.length} 題
        </span>
        <span className="h-1 flex-1 rounded-full bg-line">
          <span className="block h-1 rounded-full bg-accent" style={{ width: `${((i + 1) / qs.length) * 100}%` }} />
        </span>
      </div>
      {options.length > 0 && (
        <div className="overflow-hidden rounded-menu border border-line bg-[#2A2A2A] px-2 [&>div>button:first-child]:border-t-0">
          <OptionList options={options} value={options.find((o) => o.label === answers[i])?.key} onPick={(k) => set(options.find((o) => o.key === k)?.label ?? '')} />
        </div>
      )}
      <label className="sr-only-text" htmlFor={`ask-${ask.inboxId}-${i}`}>
        你的回答
      </label>
      <input
        id={`ask-${ask.inboxId}-${i}`}
        value={answers[i] ?? ''}
        onChange={(e) => set(e.target.value)}
        placeholder={options.length ? '或輸入你自己的答案' : '你的回答'}
        className={cx(fieldClass, 'h-11')}
      />
      {alert}
      <div className="flex flex-wrap items-center gap-2">
        {i > 0 && (
          <Button size="sm" onClick={() => setI(i - 1)}>
            上一題
          </Button>
        )}
        <span className="flex-1" />
        <Button size="sm" variant="ghost" disabled={!!busy} onClick={() => void respond('skip', { decline: true })}>
          略過
        </Button>
        {last ? (
          <Button size="sm" variant="primary" loading={busy === 'send'} disabled={!!busy || answers.some((a) => !a.trim())} onClick={() => void respond('send', { answers: answers.map((a) => a.trim()) })}>
            送出回答
          </Button>
        ) : (
          <Button size="sm" variant="primary" disabled={!answers[i]?.trim()} onClick={() => setI(i + 1)}>
            下一題
          </Button>
        )}
      </div>
    </section>
  )
}

function SecretCard({ ask, bot, sub, expiry, busy, alert, respond }: Inner) {
  const fields = ask.fields?.length ? ask.fields : [{ name: 'value', label: '秘密輸入', kind: 'password' as const }]
  const [values, setValues] = useState<Record<string, string>>({})
  const [show, setShow] = useState(false)
  const name = bot?.name ?? ask.botName ?? 'Bot'
  const ready = fields.every((f) => (values[f.name] ?? '').length > 0)
  const submit = (e: FormEvent) => {
    e.preventDefault()
    if (!ready) return
    const input = fields.length === 1 && fields[0].name === 'value' ? { value: values.value } : { values }
    void respond('send', input)
  }
  return (
    <form onSubmit={submit} aria-label="需要你輸入資料" data-testid="ask-card" data-kind="secret" className="flex flex-col gap-3 rounded-card bg-card px-5 py-4">
      <Head
        bot={bot}
        title={ask.prompt || `${name}需要你輸入資料`}
        sub={sub ?? `${name} · 等你回覆`}
        aside={
          expiry ?? (
            <span className="inline-flex h-7 flex-none items-center gap-1.5 rounded-full border border-line px-2.5 text-[13px] text-ink-2">
              <Icon icon={KeyRound} size={14} />
              {fields.length > 1 ? '帳號＋密碼' : fields[0].kind === 'code' ? '驗證碼' : '秘密輸入'}
            </span>
          )
        }
      />
      <div className={cx('grid gap-3', fields.length > 1 && 'sm:grid-cols-2')}>
        {fields.map((f) => {
          const id = `ask-${ask.inboxId}-${f.name}`
          const secret = f.kind !== 'text'
          return (
            <div key={f.name} className="flex flex-col gap-1">
              <label htmlFor={id} className="text-meta text-ink-3">
                {f.label}
              </label>
              <span className="relative flex items-center">
                <input
                  id={id}
                  type={secret && !show ? 'password' : 'text'}
                  autoComplete={f.kind === 'code' ? 'one-time-code' : secret ? 'off' : 'username'}
                  inputMode={f.kind === 'code' ? 'numeric' : undefined}
                  value={values[f.name] ?? ''}
                  onChange={(e) => setValues((v) => ({ ...v, [f.name]: e.target.value }))}
                  disabled={!!busy}
                  className={cx(fieldClass, 'h-11', secret && 'pr-11', f.kind === 'code' && 'font-mono tracking-[0.4em]')}
                />
                {secret && (
                  <span className="absolute right-1">
                    <IconButton icon={show ? EyeOff : Eye} label={show ? '隱藏' : '顯示'} size="sm" pressed={show} onClick={() => setShow((s) => !s)} />
                  </span>
                )}
              </span>
            </div>
          )
        })}
      </div>
      <span className="flex items-center gap-1.5 text-meta text-ink-2">
        <Icon icon={Lock} size={14} />
        直接交給 Bot 的電腦使用，不會寫進對話紀錄。
      </span>
      {alert}
      <div className="flex flex-wrap items-center gap-2">
        <Button variant="primary" type="submit" loading={busy === 'send'} disabled={!ready || !!busy}>
          送出
        </Button>
        <Button variant="ghost" disabled={!!busy} loading={busy === 'decline'} onClick={() => void respond('decline', { decline: true })}>
          不提供
        </Button>
      </div>
    </form>
  )
}

/** 回覆後的一行結果（C08～C10「處理後」）。 */
export function AnsweredChip({ a }: { a: Pick<Answered, 'kind' | 'resolution' | 'text'> }) {
  const l = answeredLine(a)
  const icon = l.tone === 'expired' ? Clock : l.tone === 'muted' ? (a.resolution === 'deny' ? X : ShieldAlert) : a.kind === 'secret' ? Lock : Check
  return (
    <span
      data-testid="ask-answered"
      className={cx(
        'inline-flex min-h-8 max-w-full items-center gap-1.5 self-start rounded-full border border-line px-3 text-[13px]',
        l.tone === 'ok' ? 'text-ink' : 'text-ink-2',
      )}
    >
      <Icon icon={icon} size={14} color={l.tone === 'ok' ? 'var(--color-success)' : undefined} />
      <span className="truncate">{l.text}</span>
    </span>
  )
}
