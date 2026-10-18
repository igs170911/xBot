import { useCallback, useEffect, useState, type FormEvent } from 'react'
import { Hammer, Moon, Network, Plus, RotateCw, Trash2, X } from 'lucide-react'
import {
  ApiError,
  computersPurge,
  computersRebuild,
  computersRestart,
  computersSetNetwork,
  computersSleep,
  computersStatus,
  type ComputersStatusOutput,
} from '../api/generated'
import { onEvent } from '../data/events'
import { Button } from '../ui/Button'
import { ConfirmDialog } from '../ui/Dialog'
import { Icon } from '../ui/Icon'
import { useToast } from '../ui/toast-context'
import { cx } from '../ui/cx'

// 電腦的動作（E01 面板、E02 全視窗、Bot 設定 › 應用程式與電腦）：休眠、重新開機、重建、刪除電腦資料，
// 以及這台電腦可以連的私有網路（SEC-4 白名單）。以前只有指令列（xbot computers …）。
// 確認文字照 E10「確認 · 重新開機」；刪除電腦資料是 confirm 級、不能代為核准，網頁上一律再問一次。

export type ComputerView = ComputersStatusOutput

function errText(err: unknown): string {
  return err instanceof Error ? err.message : String(err)
}

/** 一台電腦的狀態（xbot.computers.status），電腦狀態變了就重讀。 */
export function useComputer(botId: string) {
  const [view, setView] = useState<ComputerView | null>(null)
  const [error, setError] = useState<string | null>(null)
  const reload = useCallback(() => {
    computersStatus({ bot_id: botId })
      .then((v) => {
        setView(v)
        setError(null)
      })
      .catch((e: unknown) => setError(errText(e)))
  }, [botId])
  useEffect(() => {
    reload()
    return onEvent((e) => {
      if ((e.kind === 'computer.status' && e.bot_id === botId) || e.kind === 'task.state' || e.kind === 'computers.move' || e.kind === 'computers.update') reload()
    })
  }, [botId, reload])
  return { view, setView, error, reload }
}

type Action = 'sleep' | 'restart' | 'rebuild' | 'purge'

/** 這個動作現在能不能做；不能時回原因。 */
export function blockedReason(v: ComputerView | null, a: Action): string | null {
  if (!v) return '讀取中'
  if (v.moving) return '正在搬家，搬完再操作'
  if (v.busy) return `${v.bot_name}正在工作，先停止再操作`
  const st = v.computer?.state ?? ''
  if (a === 'sleep' && st !== 'running') return st === 'sleeping' ? '已經在休眠' : '電腦沒有開機'
  if (a === 'restart' && st !== 'running') return '電腦沒有開機'
  if ((a === 'rebuild' || a === 'purge') && (st === 'none' || st === '')) return '還沒有電腦'
  return null
}

const CONFIRM: Record<Exclude<Action, 'sleep'>, (name: string) => { title: string; body: string; ok: string; danger?: boolean }> = {
  restart: (n) => ({ title: `重新開機${n}的電腦？`, body: '正在做的工作會中斷，檔案和瀏覽器登入都會保留。', ok: '重新開機' }),
  rebuild: (n) => ({
    title: `重建${n}的電腦？`,
    body: '用目前的映像與設定重新建立這台電腦。家目錄、工作資料夾、瀏覽器登入都保留；自己裝的程式會不見。原本開著的會再開機。',
    ok: '重建',
  }),
  purge: (n) => ({
    title: `刪除${n}的電腦和裡面所有資料？`,
    body: `家目錄、工作資料夾、瀏覽器登入、引擎紀錄都會刪掉，無法復原。${n}本身保留，下次有工作時開一台全新的電腦。`,
    ok: '刪除電腦資料',
    danger: true,
  }),
}

/** 休眠、重新開機、重建、刪除電腦資料。 */
export function ComputerActions({ botId, botName, state, className }: { botId: string; botName: string; state: ReturnType<typeof useComputer>; className?: string }) {
  const toast = useToast()
  const [ask, setAsk] = useState<Exclude<Action, 'sleep'> | null>(null)
  const [busy, setBusy] = useState<Action | null>(null)
  const v = state.view

  const run = async (a: Action) => {
    setBusy(a)
    try {
      const fn = { sleep: computersSleep, restart: computersRestart, rebuild: computersRebuild, purge: computersPurge }[a]
      state.setView(await fn({ bot_id: botId }))
      const done = { sleep: `${botName}的電腦休眠了`, restart: `${botName}的電腦重新開機好了`, rebuild: `${botName}的電腦重建好了`, purge: `已刪除${botName}的電腦資料` }[a]
      toast.show({ kind: 'done', text: done })
      setAsk(null)
    } catch (err) {
      toast.show({ kind: err instanceof ApiError && err.code === 'pending_approval' ? 'info' : 'error', text: errText(err) })
    } finally {
      setBusy(null)
      state.reload()
    }
  }

  const btn = (a: Action, label: string, icon: typeof Moon, variant: 'outline' | 'danger' = 'outline') => {
    const why = blockedReason(v, a)
    return (
      <Button
        size="sm"
        variant={variant}
        icon={icon}
        disabled={!!why || (busy !== null && busy !== a)}
        loading={busy === a && a === 'sleep'}
        title={why ?? undefined}
        onClick={() => (a === 'sleep' ? void run('sleep') : setAsk(a))}
      >
        {label}
      </Button>
    )
  }
  const c = ask ? CONFIRM[ask](botName) : null
  const note = v?.moving ? '正在搬家，搬完再操作。' : v?.busy ? `${botName}正在工作：休眠、重新開機、重建要等它做完或先停止。` : v?.outdated ? '電腦映像或安全設定更新了，建議重建（資料保留）。' : null

  return (
    <section className={cx('flex flex-col gap-2.5 rounded-group px-4 py-3.5', className ?? 'bg-panel')} aria-label="電腦的動作" data-testid="computer-actions">
      <div>
        <h3 className="m-0 text-ui">電腦</h3>
        <p className="m-0 mt-0.5 text-meta text-ink-2">休眠或重開都會保留檔案與登入；有新工作會自動開機。</p>
      </div>
      <div className="flex flex-wrap gap-2">
        {btn('sleep', '休眠', Moon)}
        {btn('restart', '重新開機', RotateCw)}
        {btn('rebuild', '重建', Hammer)}
        {btn('purge', '刪除電腦資料', Trash2, 'danger')}
      </div>
      {note && <p className={cx('m-0 text-meta', v?.outdated && !v.busy && !v.moving ? 'text-warning-ink' : 'text-ink-2')}>{note}</p>}
      {state.error && !v && <p className="m-0 text-meta text-danger">{state.error}</p>}
      <ConfirmDialog
        open={!!c}
        title={c?.title ?? ''}
        description={c?.body}
        confirmText={c?.ok ?? ''}
        danger={c?.danger}
        busy={!!ask && busy === ask}
        onConfirm={() => ask && void run(ask)}
        onCancel={() => setAsk(null)}
      />
    </section>
  )
}

/** 這台電腦可以連的私有網路（xbot.computers.set_network）：預設全部擋掉；打開要再確認一次，關掉不用。 */
export function NetworkAllow({ botId, botName, state, className }: { botId: string; botName: string; state: ReturnType<typeof useComputer>; className?: string }) {
  const toast = useToast()
  const [draft, setDraft] = useState('')
  const [pending, setPending] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [fieldErr, setFieldErr] = useState<string | null>(null)
  const list = state.view?.allow_private ?? []

  const save = async (next: string[], done: string) => {
    setBusy(true)
    try {
      state.setView(await computersSetNetwork({ bot_id: botId, allow_private: next }))
      toast.show({ kind: 'done', text: done })
      return true
    } catch (err) {
      if (err instanceof ApiError && err.code === 'invalid_input') {
        setFieldErr(err.fields?.find((f) => f.field === 'allow_private')?.message ?? err.message)
      } else toast.show({ kind: err instanceof ApiError && err.code === 'pending_approval' ? 'info' : 'error', text: errText(err) })
      return false
    } finally {
      setBusy(false)
    }
  }

  const submit = (e: FormEvent) => {
    e.preventDefault()
    const t = draft.trim()
    if (!t) return
    if (list.includes(t)) {
      setFieldErr('已經在清單裡了')
      return
    }
    setFieldErr(null)
    setPending(t)
  }

  return (
    <section className={cx('flex flex-col gap-2.5 rounded-group px-4 py-3.5', className ?? 'bg-panel')} aria-label="可以連的私有網路" data-testid="network-allow">
      <div className="flex items-start gap-2">
        <Icon icon={Network} size={16} className="mt-0.5 text-ink-2" />
        <div className="min-w-0 flex-1">
          <h3 className="m-0 text-ui">禁止存取主機與區網</h3>
          <p className="m-0 mt-0.5 text-meta text-ink-2">{botName}的電腦只能上外部網路，碰不到這台主機、路由器、NAS 和其他電腦。要讓它連某台機器，加一個例外。</p>
        </div>
      </div>
      {list.length > 0 && (
        <ul className="m-0 flex list-none flex-wrap gap-2 p-0" aria-label="例外">
          {list.map((a) => (
            <li key={a} className="flex h-8 items-center gap-1 rounded-full bg-control pl-3 pr-1 font-mono text-[13px]">
              {a}
              <button
                type="button"
                aria-label={`移除 ${a}`}
                disabled={busy}
                onClick={() => void save(list.filter((x) => x !== a), `${botName}的電腦不能再連 ${a}`)}
                className="flex size-6 items-center justify-center rounded-full text-ink-2 hover:bg-hover hover:text-ink disabled:opacity-40"
              >
                <Icon icon={X} size={14} />
              </button>
            </li>
          ))}
        </ul>
      )}
      <form onSubmit={submit} className="flex gap-2 max-sm:flex-col">
        <label className="flex min-w-0 flex-1 flex-col gap-1">
          <span className="sr-only-text">要開放的位址</span>
          <input
            value={draft}
            onChange={(e) => {
              setDraft(e.target.value)
              setFieldErr(null)
            }}
            placeholder="例：192.168.1.10、192.168.1.0/24、http://192.168.1.20:8080"
            aria-invalid={!!fieldErr || undefined}
            className="h-9 min-w-0 rounded-field border border-line bg-inset px-3 text-ui-sm outline-none focus:border-focus"
          />
        </label>
        <Button size="sm" icon={Plus} type="submit" disabled={!draft.trim() || busy}>
          加入例外
        </Button>
      </form>
      {fieldErr && (
        <p className="m-0 text-meta text-danger" role="alert">
          {fieldErr}
        </p>
      )}
      <p className="m-0 text-meta text-ink-3">可以寫 IP、網段（最大 /8）、IP:埠或網址，只支援 IPv4。立刻生效。</p>
      <ConfirmDialog
        open={!!pending}
        title={`讓${botName}的電腦連到 ${pending ?? ''}？`}
        description={`打開後，${botName}和它開的網頁、程式都能連到這個位址。只開你信任的機器，不要開整個區網。`}
        confirmText="打開"
        busy={busy}
        onConfirm={() => {
          if (!pending) return
          void save([...list, pending], `${botName}的電腦可以連 ${pending}`).then((ok) => {
            if (ok) setDraft('')
            setPending(null)
          })
        }}
        onCancel={() => setPending(null)}
      />
    </section>
  )
}
