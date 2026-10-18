import { useEffect, useState } from 'react'
import { Copy, Eye, EyeOff, History, KeyRound, Pause, Pencil, Play, RotateCcw, Target, Trash2, TriangleAlert, Webhook, X } from 'lucide-react'
import {
  ApiError,
  routinesDelete,
  routinesHookCreate,
  routinesHookDelete,
  routinesHookRotate,
  routinesLogs,
  routinesPause,
  routinesResume,
  routinesRunNow,
  routinesUpdate,
} from '../api/generated'
import { botsStore, type Bot } from '../data/bots'
import { useStore } from '../data/store'
import { useNetwork } from '../data/network'
import { useCopy } from '../chat/useCopy'
import { linkClick, navigate } from '../router'
import { Button, IconButton } from '../ui/Button'
import { Card } from '../ui/Card'
import { SegmentedControl, Switch } from '../ui/Controls'
import { ConfirmDialog } from '../ui/Dialog'
import { TextInput, Textarea } from '../ui/Field'
import { Icon } from '../ui/Icon'
import { Select } from '../ui/Menu'
import { StatusChip } from '../ui/Pill'
import { useToast } from '../ui/toast-context'
import { cx } from '../ui/cx'
import {
  JITTER_CHOICES,
  RUN_STATUS,
  TRIGGER_LABEL,
  durationText,
  hookURL,
  jitterText,
  loadRoutines,
  routineStatus,
  routinesStore,
  whenText,
  type Routine,
  type Run,
} from './data'

// 定時任務清單與執行紀錄（H03）＋網址觸發（H06）。

function errText(err: unknown): string {
  if (err instanceof ApiError) return err.fields?.[0]?.message ?? err.message
  return err instanceof Error ? err.message : String(err)
}

function useAction() {
  const toast = useToast()
  const [busy, setBusy] = useState<string | null>(null)
  const run = async (key: string, fn: () => Promise<unknown>, done?: string) => {
    setBusy(key)
    try {
      await fn()
      await loadRoutines()
      if (done) toast.show({ kind: 'done', text: done })
    } catch (e) {
      toast.show({ kind: 'error', text: errText(e) })
    } finally {
      setBusy(null)
    }
  }
  return [busy, run] as const
}

function meta(r: Routine, bot?: Bot): string {
  return [bot?.name ?? 'Bot', r.when, r.jitter_minutes ? jitterText(r.jitter_minutes) : null, r.thread_id ? `結果貼到和${bot?.name ?? 'Bot'}的對話` : null]
    .filter(Boolean)
    .join(' · ')
}

function HookRow({ r }: { r: Routine }) {
  useNetwork() // 對外網址讀到後重畫
  const [copied, copy] = useCopy()
  const [busy, act] = useAction()
  if (!r.hook) return null
  return (
    <div className="flex flex-wrap items-center gap-2 pl-7">
      <Icon icon={Webhook} size={16} className="text-ink-2" />
      <code className="min-w-0 flex-1 truncate rounded-field border border-line bg-inset px-3 py-2 font-mono text-[12.5px]">{hookURL(r.hook.path)}</code>
      <Button size="sm" icon={Copy} onClick={() => void copy(hookURL(r.hook!.path))}>
        {copied ? '已複製' : '複製'}
      </Button>
      <Button size="sm" icon={KeyRound} loading={busy === 'rotate'} onClick={() => void act('rotate', () => routinesHookRotate({ id: r.id }), '已換新金鑰，舊的立刻失效')}>
        換新金鑰
      </Button>
    </div>
  )
}

function RoutineCard({ r, bot, selected, onSelect }: { r: Routine; bot?: Bot; selected: boolean; onSelect: () => void }) {
  const st = routineStatus(r)
  const [busy, act] = useAction()
  const g = r.goal
  return (
    <Card
      data-testid="routine-card"
      data-status={st.key}
      layout="flex flex-col gap-2 px-[22px] py-4"
      className={cx('cursor-pointer outline-1 -outline-offset-1', selected ? 'outline outline-focus' : 'hover:outline hover:outline-line')}
      onClick={(e) => {
        if ((e.target as HTMLElement).closest('button,a,code')) return
        onSelect()
      }}
    >
      <div className="flex flex-wrap items-center gap-2.5">
        <h3 className={cx('m-0 min-w-0 text-[16px] font-normal', st.key === 'paused' && 'text-ink-2')}>{r.title}</h3>
        <StatusChip size="sm" tone={st.tone}>
          {st.label}
        </StatusChip>
        <span className="flex-1" />
        {st.key === 'problem' ? (
          <Button size="sm" icon={RotateCcw} loading={busy === 'run'} onClick={() => void act('run', () => routinesRunNow({ id: r.id }), '已開始重跑')}>
            重跑一次
          </Button>
        ) : st.key === 'paused' ? (
          <Button size="sm" icon={Play} loading={busy === 'resume'} onClick={() => void act('resume', () => routinesResume({ id: r.id }), '已恢復')}>
            恢復
          </Button>
        ) : (
          r.status === 'active' &&
          !r.running && <IconButton icon={Play} label="現在跑一次" onClick={() => void act('run', () => routinesRunNow({ id: r.id }), '已開始跑')} />
        )}
        {r.status === 'active' && <IconButton icon={Pause} label="暫停" onClick={() => void act('pause', () => routinesPause({ id: r.id }), '已暫停')} />}
        <IconButton icon={Pencil} label="修改" onClick={onSelect} />
      </div>
      <p className="m-0 pl-7 text-sub text-ink-2">{meta(r, bot)}</p>
      <p className="m-0 pl-7 text-sub text-ink-2">
        {r.last_run_at ? <>上次 {whenText(r.last_run_at)}</> : '還沒跑過'}
        {r.next_run_at ? <> · 下次 {whenText(r.next_run_at)}</> : r.status === 'done' ? ' · 已結束' : ''}
      </p>
      {g && (
        <div className="ml-7 flex flex-col gap-2 rounded-group bg-inset px-4 py-3">
          <span className="flex items-center gap-2 text-sub">
            <Icon icon={Target} size={16} className="text-ink-2" />
            <span className="text-ink-2">完成條件</span>
            {g.done_when}
          </span>
          <span className="flex items-center gap-3">
            <span className="h-1.5 flex-1 overflow-hidden rounded-full bg-control" role="progressbar" aria-valuemin={0} aria-valuemax={g.max_rounds} aria-valuenow={g.rounds}>
              <span className="block h-full rounded-full bg-accent" style={{ width: `${Math.min(100, (g.rounds / g.max_rounds) * 100)}%` }} />
            </span>
            <span className="text-sub">
              已跑 {g.rounds} / {g.max_rounds} 輪
            </span>
          </span>
          <span className="text-meta text-ink-3">
            {g.summary ? `成果：${g.summary} · ` : ''}進度記在 <code className="font-mono">{g.progress_file}</code>
          </span>
        </div>
      )}
      <HookRow r={r} />
      {st.key === 'problem' && r.last_error && (
        <p className="m-0 flex items-start gap-2 pl-7 text-sub text-danger" role="status">
          <Icon icon={TriangleAlert} size={16} className="mt-0.5" />
          {r.last_error}
          {r.failures ? `（已自動重試 ${Math.max(0, r.failures - 1)} 次，下次 ${r.retry_at ? whenText(r.retry_at) : '稍後'}）` : ''}
        </p>
      )}
    </Card>
  )
}

function curlFor(r: Routine): string {
  return `curl -X POST ${hookURL(r.hook!.path)} \\\n  -H 'Authorization: Bearer ${r.hook!.secret ?? '<金鑰>'}' \\\n  -H 'X-XBot-Test: 1' \\\n  -d '{"text":"測試"}'`
}

function HookSection({ r }: { r: Routine }) {
  const [show, setShow] = useState(false)
  const [copiedURL, copyURL] = useCopy()
  const [copiedCurl, copyCurl] = useCopy()
  const [busy, act] = useAction()
  const net = useNetwork().status
  if (!r.hook)
    return (
      <section className="flex flex-col gap-2 rounded-group bg-inset px-4 py-3.5">
        <h3 className="m-0 text-ui font-normal">網址觸發</h3>
        <p className="m-0 text-sub text-ink-2">外部服務（GitHub、表單、Slack…）送一個請求，就叫它跑一次；送來的內容會當成資料交給 Bot。</p>
        <Button size="sm" icon={Webhook} className="self-start" loading={busy === 'create'} onClick={() => void act('create', () => routinesHookCreate({ id: r.id }), '已開網址觸發')}>
          開一個網址
        </Button>
      </section>
    )
  const secret = r.hook.secret ?? ''
  return (
    <section className="flex flex-col gap-3 rounded-group bg-inset px-4 py-3.5" data-testid="hook-section">
      <h3 className="m-0 text-ui font-normal">網址與金鑰</h3>
      {net && !net.public_url && (
        <p className="m-0 text-meta text-ink-3" data-testid="hook-private">
          這個網址只有這台電腦{net.tailscale.url ? '和你的 tailnet' : ''}連得到；要讓外部服務呼叫，到
          <a href="/settings/advanced/network" onClick={linkClick('/settings/advanced/network')} className="mx-1 text-accent-ink">
            網路與 Tailscale
          </a>
          開外部入口。
        </p>
      )}
      <div className="flex flex-col divide-y divide-line rounded-field border border-line">
        <div className="flex items-center gap-3 px-3 py-2">
          <span className="w-10 flex-none text-meta text-ink-3">網址</span>
          <code className="min-w-0 flex-1 truncate font-mono text-[12.5px]">{hookURL(r.hook.path)}</code>
          <Button size="sm" icon={Copy} onClick={() => void copyURL(hookURL(r.hook!.path))}>
            {copiedURL ? '已複製' : '複製'}
          </Button>
        </div>
        <div className="flex flex-wrap items-center gap-x-3 gap-y-1.5 px-3 py-2">
          <span className="w-10 flex-none text-meta text-ink-3">金鑰</span>
          <code className="min-w-[160px] flex-1 truncate font-mono text-[12.5px]" data-testid="hook-secret">
            Bearer {show ? secret : '•'.repeat(Math.min(24, secret.length || 24))}
          </code>
          <Button size="sm" icon={show ? EyeOff : Eye} onClick={() => setShow((s) => !s)}>
            {show ? '隱藏' : '顯示'}
          </Button>
          <Button size="sm" loading={busy === 'rotate'} onClick={() => void act('rotate', () => routinesHookRotate({ id: r.id }), '已換新金鑰，舊的立刻失效')}>
            換新金鑰
          </Button>
        </div>
      </div>
      <div className="flex flex-col rounded-field border border-line">
        <div className="flex items-center justify-between gap-2 border-b border-line px-3 py-1.5">
          <span className="text-meta text-ink-3">在這台主機用指令送一個測試（不會開始工作）</span>
          <Button size="sm" onClick={() => void copyCurl(curlFor(r))}>
            {copiedCurl ? '已複製' : '複製指令'}
          </Button>
        </div>
        <pre className="m-0 overflow-x-auto px-3 py-2 font-mono text-[12px] text-ink-2">{curlFor({ ...r, hook: { ...r.hook, secret: show ? secret : '<金鑰>' } })}</pre>
      </div>
      <p className="m-0 text-meta text-ink-3">XBot 要開著才收得到；同一個網址至少隔 60 秒，內容最多 64 KiB。也可以用 X-XBot-Signature: sha256=…（HMAC）代替 Bearer。</p>
      <Button size="sm" variant="danger" className="self-start" loading={busy === 'off'} onClick={() => void act('off', () => routinesHookDelete({ id: r.id }), '網址觸發已關閉')}>
        關掉網址觸發
      </Button>
    </section>
  )
}

const LOG_FILTERS = [
  { value: 'all', label: '全部' },
  { value: 'done', label: '已完成' },
  { value: 'problem', label: '要你處理' },
  { value: 'catchup', label: '補跑' },
] as const

function Logs({ r }: { r: Routine }) {
  const [runs, setRuns] = useState<Run[] | null>(null)
  const [filter, setFilter] = useState<(typeof LOG_FILTERS)[number]['value']>('all')
  useEffect(() => {
    let live = true
    routinesLogs({ id: r.id })
      .then((out) => live && setRuns(out.runs))
      .catch(() => live && setRuns([]))
    return () => {
      live = false
    }
  }, [r.id, r.updated_at, r.last_task_id, r.running])
  const shown = (runs ?? []).filter((x) =>
    filter === 'all' ? true : filter === 'done' ? x.status === 'done' : filter === 'problem' ? x.status === 'failed' || x.status === 'retrying' : x.trigger === 'catchup',
  )
  const count = (v: string) => (runs ?? []).filter((x) => (v === 'done' ? x.status === 'done' : v === 'problem' ? x.status === 'failed' || x.status === 'retrying' : v === 'catchup' ? x.trigger === 'catchup' : true)).length
  return (
    <section className="flex flex-col gap-2" aria-label="執行紀錄">
      <SegmentedControl label="篩選執行紀錄" value={filter} onChange={setFilter} options={LOG_FILTERS.map((f) => ({ value: f.value, label: `${f.label} ${count(f.value)}` }))} />
      {runs === null ? (
        <p className="m-0 text-sub text-ink-3">讀取中…</p>
      ) : shown.length === 0 ? (
        <p className="m-0 py-3 text-sub text-ink-3">還沒有紀錄。</p>
      ) : (
        <ul className="m-0 flex list-none flex-col p-0" data-testid="run-list">
          {shown.map((x) => {
            const st = RUN_STATUS[x.status] ?? RUN_STATUS.done
            const at = x.started_at || x.scheduled_at || x.created_at
            const href = r.thread_id ? `/bots/${r.bot_id}/threads/${r.thread_id}` : undefined
            return (
              <li key={x.id} className="flex flex-col gap-1 border-b border-divider py-2.5 last:border-b-0">
                <span className="flex flex-wrap items-center gap-2">
                  <span className="text-ui-sm">{whenText(at)}</span>
                  <StatusChip size="sm">{TRIGGER_LABEL[x.trigger] ?? x.trigger}</StatusChip>
                  <StatusChip size="sm" tone={st.tone}>
                    {st.label}
                  </StatusChip>
                  <span className="flex-1" />
                  {x.finished_at > 0 && x.started_at > 0 && <span className="text-meta text-ink-3">{durationText(x.started_at, x.finished_at)}</span>}
                </span>
                {(x.error || x.payload_bytes > 0) && (
                  <span className="text-meta text-ink-2">
                    {x.error}
                    {x.payload_bytes > 0 && `${x.error ? ' · ' : ''}收到 ${x.payload_bytes} 位元組`}
                  </span>
                )}
                {x.task_id && href && (
                  <a href={href} onClick={linkClick(href)} className="self-start text-meta">
                    打開對話
                  </a>
                )}
              </li>
            )
          })}
        </ul>
      )}
    </section>
  )
}

function Settings({ r }: { r: Routine }) {
  const [title, setTitle] = useState(r.title)
  const [prompt, setPrompt] = useState(r.prompt)
  const [jitter, setJitter] = useState(r.jitter_minutes)
  const [notify, setNotify] = useState(r.notify_on_failure)
  const [busy, act] = useAction()
  const dirty = title !== r.title || prompt !== r.prompt || jitter !== r.jitter_minutes || notify !== r.notify_on_failure
  return (
    <section className="flex flex-col gap-3" aria-label="修改">
      <TextInput label="標題" value={title} onChange={(e) => setTitle(e.target.value)} maxLength={80} />
      <Textarea label="做什麼（每次交給 Bot 的指示）" value={prompt} onChange={(e) => setPrompt(e.target.value)} rows={3} />
      <div className="flex flex-wrap items-end gap-4">
        <Select label="時間飄移" look="compact" value={String(jitter)} options={JITTER_CHOICES.map((m) => ({ value: String(m), label: m === 0 ? '不飄移' : `${m} 分鐘內` }))} onChange={(v) => setJitter(Number(v))} />
        <Switch checked={notify} onChange={setNotify} label="沒跑成功時通知我">
          沒跑成功時通知我
        </Switch>
      </div>
      <Button
        size="sm"
        variant="primary"
        className="self-start"
        disabled={!dirty || !title.trim() || !prompt.trim()}
        loading={busy === 'save'}
        onClick={() => void act('save', () => routinesUpdate({ id: r.id, title: title.trim(), prompt: prompt.trim(), jitter_minutes: jitter, notify_on_failure: notify }), '已儲存')}
      >
        儲存
      </Button>
    </section>
  )
}

function Drawer({ r, bot, onClose }: { r: Routine; bot?: Bot; onClose: () => void }) {
  const [confirm, setConfirm] = useState(false)
  const [busy, act] = useAction()
  return (
    <aside
      aria-label="定時任務詳細"
      data-testid="routine-drawer"
      className="flex w-full flex-col gap-4 border-divider bg-panel px-5 py-4 lg:sticky lg:top-0 lg:h-dvh lg:w-[380px] lg:flex-none lg:overflow-y-auto lg:border-l"
    >
      <header className="flex items-center gap-2">
        <Icon icon={History} size={20} className="text-ink-2" />
        <h2 className="m-0 flex-1 text-[17px] font-normal">執行紀錄</h2>
        <IconButton icon={X} label="關閉" onClick={onClose} />
      </header>
      <div className="flex flex-col gap-0.5">
        <span className="text-ui">{r.title}</span>
        <span className="text-meta text-ink-3">{meta(r, bot)}</span>
      </div>
      <Logs r={r} />
      <HookSection r={r} />
      <Settings key={r.updated_at} r={r} />
      <Button size="sm" variant="danger" icon={Trash2} className="self-start" onClick={() => setConfirm(true)}>
        刪除這個定時任務
      </Button>
      <p className="m-0 text-meta text-ink-3">結果貼在和 Bot 的同一條對話；點紀錄可以打開。</p>
      <ConfirmDialog
        open={confirm}
        danger
        title="刪除定時任務？"
        description={`「${r.title}」和它的執行紀錄、網址觸發會一起刪除，無法復原。`}
        confirmText="刪除"
        busy={busy === 'delete'}
        onCancel={() => setConfirm(false)}
        onConfirm={() =>
          void act(
            'delete',
            async () => {
              await routinesDelete({ id: r.id })
              setConfirm(false)
              onClose()
            },
            '已刪除',
          )
        }
      />
    </aside>
  )
}

const FILTERS = ['all', 'running', 'problem', 'paused'] as const
type Filter = (typeof FILTERS)[number]
const FILTER_LABEL: Record<Filter, string> = { all: '全部', running: '執行中', problem: '有問題', paused: '已暫停' }

export function RoutineList({ selectedId }: { selectedId?: string }) {
  const { routines, error } = useStore(routinesStore)
  const bots = useStore(botsStore).bots ?? []
  const [filter, setFilter] = useState<Filter>('all')
  const list = routines ?? []
  const count = (f: Filter) => (f === 'all' ? list.length : list.filter((r) => routineStatus(r).key === f).length)
  const shown = list.filter((r) => filter === 'all' || routineStatus(r).key === filter)
  const selected = list.find((r) => r.id === selectedId)
  return (
    <div className="flex flex-col gap-3">
      <div className="flex justify-end">
        <SegmentedControl label="篩選定時任務" value={filter} onChange={setFilter} options={FILTERS.map((f) => ({ value: f, label: `${FILTER_LABEL[f]} ${count(f)}` }))} />
      </div>
      {error && (
        <p role="alert" className="m-0 text-sub text-danger">
          {error}
        </p>
      )}
      {routines === null ? (
        <p className="m-0 text-sub text-ink-3">讀取中…</p>
      ) : shown.length === 0 ? (
        <p className="m-0 rounded-card bg-panel px-5 py-8 text-center text-sub text-ink-2">{list.length === 0 ? '還沒有定時任務。用一句話新增一個，例如「每天早上 9 點整理新聞給我」。' : '這一類沒有定時任務。'}</p>
      ) : (
        shown.map((r) => (
          <RoutineCard key={r.id} r={r} bot={bots.find((b) => b.id === r.bot_id)} selected={r.id === selectedId} onSelect={() => navigate(`/calendar/routines/${r.id}`)} />
        ))
      )}
      {selected && (
        <div className="lg:hidden">
          <Drawer r={selected} bot={bots.find((b) => b.id === selected.bot_id)} onClose={() => navigate('/calendar/routines')} />
        </div>
      )}
    </div>
  )
}

/** 桌面版右側的抽屜（H03）。 */
export function RoutineDrawer({ selectedId }: { selectedId?: string }) {
  const routines = useStore(routinesStore).routines ?? []
  const bots = useStore(botsStore).bots ?? []
  const r = routines.find((x) => x.id === selectedId)
  if (!r) return null
  return (
    <div className="max-lg:hidden">
      <Drawer r={r} bot={bots.find((b) => b.id === r.bot_id)} onClose={() => navigate('/calendar/routines')} />
    </div>
  )
}
