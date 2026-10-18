import { useState } from 'react'
import { Check, Clock, KeyRound, TriangleAlert } from 'lucide-react'
import { ApiError, enginesSwitch, inboxDismiss, type EnginesSwitchInput } from '../api/generated'
import { inboxStore, loadInbox } from '../data/inbox'
import { useStore } from '../data/store'
import { Avatar } from '../ui/Avatar'
import { Button } from '../ui/Button'
import { Checkbox } from '../ui/Controls'
import { Dialog } from '../ui/Dialog'
import { Icon } from '../ui/Icon'
import { cx } from '../ui/cx'
import { useToast } from '../ui/toast-context'
import { hhmm, quotaTitle, type Quota, type SwitchOption } from './quota'

// 引擎額度用完卡（EN-7，design/C09「額度用完 · 換一個模型繼續」）：待我處理和對話裡用同一張。
// 選一個你有的引擎或模型 → xbot.engines.switch（要用訂閱而還沒同意時，先問你同意）並把那一則重送；
// 「等額度恢復」＝收掉卡片（xbot.inbox.dismiss）。

const LETTERS = 'ABCDEFGHIJ'

function errText(err: unknown): string {
  if (err instanceof ApiError) return err.fields?.[0]?.message || err.message
  return err instanceof Error ? err.message : String(err)
}

export function QuotaCard({
  quota,
  botId,
  bot,
  sub,
  onDone,
}: {
  quota: Quota
  botId: string
  bot?: { name: string; shape?: string; color?: string }
  sub?: string
  onDone?: () => void
}) {
  const toast = useToast()
  const open = useStore(inboxStore).items
  const [busy, setBusy] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [done, setDone] = useState<string | null>(null)
  const [consent, setConsent] = useState<SwitchOption | null>(null)
  const [agree, setAgree] = useState(false)
  const name = bot?.name ?? quota.bot_name ?? 'Bot'
  // 卡已經處理過（換了引擎、等額度、被新的卡取代）：對話裡收成一行。
  const settled = !!quota.inbox_id && open !== null && !open.some((i) => i.id === quota.inbox_id)

  const run = async (key: string, opt: SwitchOption, link = false) => {
    setBusy(key)
    setError(null)
    try {
      const input: EnginesSwitchInput = { bot_id: botId, engine: opt.engine as EnginesSwitchInput['engine'], model: opt.model || '' }
      if (link) input.link = true
      if (quota.inbox_id) input.inbox_id = quota.inbox_id
      if (quota.inbox_id && quota.retry_text) input.resend = true
      const out = await enginesSwitch(input)
      setConsent(null)
      setAgree(false)
      const line = `已換成 ${opt.label}${out.task_id ? '，重新送出' : ''}`
      setDone(line)
      toast.show({ kind: 'done', text: `${name}${line}` })
      void loadInbox()
      onDone?.()
    } catch (err) {
      setError(errText(err))
    } finally {
      setBusy(null)
    }
  }
  const wait = async () => {
    setBusy('wait')
    setError(null)
    try {
      if (quota.inbox_id) await inboxDismiss({ inbox_id: quota.inbox_id })
      setDone(quota.reset_at ? `等額度恢復（約 ${hhmm(quota.reset_at)}）再請${name}繼續` : `等額度恢復再請${name}繼續`)
      void loadInbox()
      onDone?.()
    } catch (err) {
      setError(errText(err))
    } finally {
      setBusy(null)
    }
  }
  const pick = (key: string, o: SwitchOption) => (o.needs_link ? setConsent(o) : void run(key, o))

  if (done || settled) {
    return (
      <span data-testid="quota-done" className="inline-flex min-h-8 max-w-full items-center gap-1.5 self-start rounded-full border border-line px-3 text-[13px] text-ink">
        <Icon icon={Check} size={14} color="var(--color-success)" />
        <span className="truncate">{done ?? `已處理 · ${quotaTitle(quota)}`}</span>
      </span>
    )
  }
  const rows = quota.options.map((o, i) => ({ key: LETTERS[i] ?? String(i + 1), o }))
  const waitKey = LETTERS[rows.length] ?? String(rows.length + 1)
  return (
    <section aria-label="換引擎繼續" data-testid="quota-card" data-kind={quota.kind} className="flex flex-col gap-3 rounded-card bg-card px-5 py-4">
      <div className="flex flex-wrap items-start gap-x-3 gap-y-2">
        <Avatar shape={bot?.shape} color={bot?.color} state="notifying" dot="waiting" ring="#262626" size={34} />
        <div className="flex min-w-0 flex-[1_1_180px] flex-col">
          <h2 className="m-0 text-[16px] font-normal leading-[1.45]">{quotaTitle(quota)}，要換哪個繼續？</h2>
          <span className="text-[13px] text-ink-2">
            {sub ?? name}
            {quota.options.length > 0 ? ` · 換了以後${name}之後的工作都用新的引擎` : ''}
          </span>
        </div>
      </div>
      {quota.message && <p className="m-0 whitespace-pre-wrap break-words rounded-menu bg-inset px-3.5 py-2.5 font-mono text-[12.5px] text-ink-3">{quota.message}</p>}
      <div role="group" aria-label="換哪個繼續" className="overflow-hidden rounded-menu border border-line bg-[#2A2A2A]">
        {rows.map(({ key, o }) => (
          <button
            key={key}
            type="button"
            disabled={!!busy}
            onClick={() => pick(key, o)}
            className="flex min-h-12 w-full items-center gap-3.5 border-0 border-t border-line bg-transparent px-3.5 text-left text-ui text-ink first:border-t-0 hover:bg-control/60 disabled:opacity-60"
          >
            <span aria-hidden="true" className="flex size-6 flex-none items-center justify-center rounded-chk bg-line text-[12.5px] text-ink-2">
              {key}
            </span>
            <span className="min-w-0 flex-1">{busy === key ? '換過去中…' : o.label}</span>
            {o.note && <span className={cx('flex-none text-[13px]', o.needs_link ? 'text-warning-ink' : 'text-ink-3')}>{o.note}</span>}
          </button>
        ))}
        <button
          type="button"
          disabled={!!busy}
          onClick={() => void wait()}
          className="flex min-h-12 w-full items-center gap-3.5 border-0 border-t border-line bg-transparent px-3.5 text-left text-ui text-ink first:border-t-0 hover:bg-control/60 disabled:opacity-60"
        >
          <span aria-hidden="true" className="flex size-6 flex-none items-center justify-center rounded-chk bg-line text-[12.5px] text-ink-2">
            {waitKey}
          </span>
          <span className="min-w-0 flex-1">等額度恢復再繼續</span>
          <span className="inline-flex flex-none items-center gap-1 text-[13px] text-ink-3">
            <Icon icon={Clock} size={14} />
            {quota.reset_at ? hhmm(quota.reset_at) : '時間不確定'}
          </span>
        </button>
      </div>
      {quota.options.length === 0 && <p className="m-0 text-sub text-ink-2">這台電腦上沒有其他登入過的引擎或設定好的模型。可以到「設定 › AI 模型」加一個。</p>}
      {error && (
        <p role="alert" className="m-0 text-sub text-danger">
          {error}
        </p>
      )}

      <Dialog
        open={!!consent}
        onClose={() => setConsent(null)}
        busy={busy !== null}
        width={480}
        title={`讓${name}改用 ${consent?.label ?? ''}？`}
        actions={
          <>
            <Button onClick={() => setConsent(null)} disabled={busy !== null}>
              取消
            </Button>
            <Button variant="primary" disabled={!agree} loading={busy === 'consent'} onClick={() => consent && void run('consent', consent, true)}>
              同意並換過去
            </Button>
          </>
        }
      >
        <ul className="m-0 flex list-none flex-col gap-2 p-0 text-ui-sm">
          <li className="flex gap-2.5">
            <span className="pt-0.5 text-ink-2">
              <Icon icon={KeyRound} size={16} />
            </span>
            <span>會把這台電腦上這個 CLI 的登入憑證唯讀放進{name}的電腦，只有這個 Bot 能用；隨時可以在 Bot 設定收回。</span>
          </li>
          <li className="flex gap-2.5">
            <span className="pt-0.5 text-warning-ink">
              <Icon icon={TriangleAlert} size={16} />
            </span>
            <span>{name}的工作會用掉你這份訂閱的額度。</span>
          </li>
        </ul>
        <Checkbox checked={agree} onChange={setAgree} className="mt-2">
          我同意讓{name}使用我的訂閱
        </Checkbox>
      </Dialog>
    </section>
  )
}
