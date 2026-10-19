import { useEffect, useMemo, useState, type ReactNode } from 'react'
import { CircleCheck, CircleSlash, FolderInput, KeyRound, ShieldCheck, TriangleAlert } from 'lucide-react'
import { importDetect, importPlan, importRun } from '../api/generated'
import { loadBots } from '../data/bots'
import { goBack, navigate } from '../router'
import { errText } from '../settings/format'
import { Avatar } from '../ui/Avatar'
import { Button } from '../ui/Button'
import { Checkbox } from '../ui/Controls'
import { TextInput } from '../ui/Field'
import { Icon } from '../ui/Icon'
import { cx } from '../ui/cx'
import { countNew, defaultSelection, effective, human, importRunStore, selectable, toggle, useImportRun, type Found, type Plan, type PlanItem } from './importData'
import { loadOnboard } from './state'

// 帶入舊設定（V10，畫板 F10 的版型）：從 LazyBoy、OpenMausBot 匯入 Bot、技能、記憶、定時任務、團隊、群組，
// 模型金鑰與瀏覽器登入要你明確勾選。左邊是 Bot 與它們的東西，右邊是「加入前請看一下」，底下說明會建立幾個 Bot。

const SUB_ORDER = ['skills', 'memory', 'data', 'login', 'routine']

function Done({ text = '已經帶進來了' }: { text?: string }) {
  return (
    <span className="inline-flex items-center gap-1 text-meta text-success">
      <Icon icon={CircleCheck} size={14} />
      {text}
    </span>
  )
}

function ItemCheck({ plan, sel, it, onToggle }: { plan: Plan; sel: Set<string>; it: PlanItem; onToggle: (key: string, on: boolean) => void }) {
  if (it.status === 'done')
    return (
      <div className="flex items-center gap-2.5 py-1 text-ui-sm" data-testid={`item-${it.key}`}>
        <span className="flex-1">{it.title}</span>
        <Done />
      </div>
    )
  if (it.status === 'blocked')
    return (
      <div className="flex items-start gap-2.5 py-1 text-ui-sm text-ink-2" data-testid={`item-${it.key}`}>
        <Icon icon={CircleSlash} size={16} className="mt-0.5" />
        <span className="flex-1">
          {it.title}
          <span className="block text-meta text-ink-3">{it.detail}</span>
        </span>
      </div>
    )
  return (
    <div data-testid={`item-${it.key}`} className="py-1">
      <Checkbox checked={sel.has(it.key)} disabled={!selectable(plan, sel, it)} onChange={(on) => onToggle(it.key, on)}>
        <span className="block">
          {it.title}
          {it.explicit && <span className="ml-2 rounded-full bg-warning-soft px-2 py-px text-meta text-warning-ink">要你同意</span>}
        </span>
        {it.detail && <span className="block text-meta text-ink-2">{it.detail}</span>}
        {it.attention && sel.has(it.key) && <span className="block text-meta text-warning-ink">{it.attention}</span>}
      </Checkbox>
    </div>
  )
}

function BotCard({ plan, sel, it, name, onName, onToggle }: { plan: Plan; sel: Set<string>; it: PlanItem; name: string; onName: (v: string) => void; onToggle: (key: string, on: boolean) => void }) {
  const subs = plan.items.filter((x) => x.bot === it.key).sort((a, b) => SUB_ORDER.indexOf(a.kind) - SUB_ORDER.indexOf(b.kind))
  const on = sel.has(it.key)
  return (
    <li className="flex flex-col gap-2 border-line px-4 py-3.5 not-first:border-t" data-testid={`bot-${it.key}`}>
      <div className="flex items-center gap-3">
        <Avatar shape={it.shape} color={it.color} img={it.image || undefined} dot="none" size={40} blink={false} />
        <span className="min-w-0 flex-1">
          <span className="block text-ui">{it.original}</span>
          {it.detail && <span className="block truncate text-[13px] text-ink-2">{it.detail}</span>}
        </span>
        {it.status === 'done' ? (
          <Done />
        ) : (
          <Checkbox checked={on} onChange={(v) => onToggle(it.key, v)} className="flex-none">
            帶入
          </Checkbox>
        )}
      </div>
      {it.status === 'ready' && on && (
        <TextInput
          label={`${it.original} 帶進來後的名字`}
          value={name}
          maxLength={100}
          onChange={(e) => onName(e.target.value)}
          hint={it.renamed && name === it.name ? `你已經有一個「${it.original}」，先改叫「${it.name}」，可以改。` : undefined}
        />
      )}
      {subs.length > 0 && (
        <div className="ml-[52px] flex flex-col max-sm:ml-0" aria-label={`${it.original} 的東西`} role="group">
          {subs.map((s) => (
            <ItemCheck key={s.key} plan={plan} sel={sel} it={s} onToggle={onToggle} />
          ))}
        </div>
      )}
    </li>
  )
}

function Note({ n, title, tone, children }: { n: number; title: string; tone?: 'warning'; children: ReactNode }) {
  return (
    <section className="flex gap-3 border-line px-4 py-3 not-first:border-t" aria-label={title}>
      <span className={cx('flex size-6 flex-none items-center justify-center rounded-full text-meta', tone ? 'bg-warning-soft text-warning-ink' : 'bg-control text-ink-2')}>{n}</span>
      <div className="flex min-w-0 flex-1 flex-col gap-1">
        <h3 className="m-0 flex items-center gap-2 text-ui">
          {title}
          {tone && <span className="rounded-full bg-warning-soft px-2 py-px text-meta text-warning-ink">要你處理</span>}
        </h3>
        <div className="text-ui-sm text-ink-2">{children}</div>
      </div>
    </section>
  )
}

function Progress({ onDone }: { onDone: () => void }) {
  const run = useImportRun()
  if (!run) return null
  const running = run.state === 'running'
  const failed = run.results.filter((r) => r.status === 'failed')
  return (
    <div className="mx-auto flex w-full max-w-[720px] flex-col gap-4 px-6 pb-8 pt-4 max-sm:px-4" data-testid="import-progress">
      <header className="flex flex-col gap-0.5">
        <h1 className="m-0 flex items-center gap-2 text-[22px] max-md:sr-only-text">
          <Icon icon={FolderInput} size={22} color="var(--color-accent)" />
          {running ? `正在從 ${run.label} 帶進來` : failed.length ? '帶完了，有幾項沒成功' : '帶完了'}
        </h1>
        <span className="text-sub text-ink-2">{running ? (run.step ?? '準備中') : `${run.done} 項，建立 ${run.bots.length} 個 Bot`}</span>
      </header>
      <div role="progressbar" aria-label="匯入進度" aria-valuenow={run.percent} aria-valuemin={0} aria-valuemax={100} className="h-2 overflow-hidden rounded-full bg-control">
        <div className="h-full rounded-full bg-accent transition-[width]" style={{ width: `${run.percent}%` }} />
      </div>
      <ul aria-label="結果" className="m-0 flex list-none flex-col overflow-hidden rounded-group bg-card p-0">
        {run.results.map((r) => (
          <li key={r.key} data-status={r.status} className="flex items-start gap-3 border-line px-4 py-2.5 not-first:border-t">
            <span className={cx('pt-0.5', r.status === 'ok' ? 'text-success' : r.status === 'failed' ? 'text-danger' : 'text-ink-3')}>
              <Icon icon={r.status === 'ok' ? CircleCheck : r.status === 'failed' ? TriangleAlert : CircleSlash} size={16} />
            </span>
            <span className="min-w-0 flex-1 text-ui-sm">
              {r.title}
              {r.detail && <span className="block text-meta text-ink-2">{r.detail}</span>}
            </span>
          </li>
        ))}
      </ul>
      {run.error && (
        <p role="alert" className="m-0 text-meta text-danger">
          {run.error}
        </p>
      )}
      {!running && (
        <div className="flex flex-wrap justify-end gap-2">
          <Button onClick={onDone}>回到預演</Button>
          {run.bots[0] && (
            <Button variant="primary" onClick={() => navigate(`/bots/${run.bots[0]}`)}>
              去看帶進來的 Bot
            </Button>
          )}
        </div>
      )}
    </div>
  )
}

export function ImportPage() {
  const [sources, setSources] = useState<Found[] | null>(null)
  const [srcId, setSrcId] = useState('')
  const [other, setOther] = useState('')
  const [path, setPath] = useState('')
  const [plan, setPlan] = useState<Plan | null>(null)
  const [sel, setSel] = useState<Set<string>>(new Set())
  const [names, setNames] = useState<Record<string, string>>({})
  const [err, setErr] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [showRun, setShowRun] = useState(false)
  const run = useImportRun()

  const loadPlan = (input: { source?: string; path?: string }) => {
    setErr(null)
    setBusy(true)
    return importPlan(input)
      .then((p) => {
        setPlan(p)
        setSel(defaultSelection(p))
        setNames(Object.fromEntries(p.items.filter((it) => it.kind === 'bot' && it.status === 'ready').map((it) => [it.key, it.name ?? it.title])))
      })
      .catch((e) => setErr(errText(e)))
      .finally(() => setBusy(false))
  }

  const choose = (id: string) => {
    setPath('')
    setSrcId(id)
    void loadPlan({ source: id })
  }

  useEffect(() => {
    importDetect({})
      .then((d) => {
        setSources(d.sources)
        if (d.sources[0]) {
          setSrcId(d.sources[0].id)
          return loadPlan({ source: d.sources[0].id })
        }
      })
      .catch((e) => {
        setSources([])
        setErr(errText(e))
      })
    // 只在打開這一頁時找一次
  }, [])

  const keys = useMemo(() => (plan ? effective(plan, sel) : []), [plan, sel])
  const counts = plan ? countNew(plan, keys) : null
  const over = !!plan && !!counts && counts.bots > plan.remaining

  const start = async () => {
    if (!plan) return
    setBusy(true)
    setErr(null)
    try {
      const renames = Object.entries(names)
        .filter(([k, v]) => keys.includes(k) && v.trim() && v.trim() !== plan.items.find((it) => it.key === k)?.name)
        .map(([key, name]) => ({ key, name: name.trim() }))
      const r = await importRun({ source: path ? undefined : plan.source.id, path: path || undefined, items: keys, names: renames })
      importRunStore.set(r)
      setShowRun(true)
    } catch (e) {
      setErr(errText(e))
    } finally {
      setBusy(false)
    }
  }

  // 匯入做完：重讀 Bot 清單與首頁卡片。
  const finished = showRun && run && run.state !== 'running'
  useEffect(() => {
    if (finished) {
      void loadBots()
      void loadOnboard()
    }
  }, [finished])

  if (showRun && run)
    return (
      <Progress
        onDone={() => {
          setShowRun(false)
          if (plan) void loadPlan(path ? { path } : { source: plan.source.id })
        }}
      />
    )

  const onToggle = (key: string, on: boolean) => plan && setSel((s) => toggle(plan, s, key, on))
  const botItems = plan?.items.filter((it) => it.kind === 'bot') ?? []
  const orgItems = plan?.items.filter((it) => it.kind === 'team' || it.kind === 'room' || it.kind === 'approvals') ?? []
  const keyItems = plan?.items.filter((it) => it.kind === 'key') ?? []
  const pausedRoutines = plan?.items.filter((it) => it.kind === 'routine' && keys.includes(it.key)).length ?? 0

  return (
    <div className="flex flex-1 flex-col">
      <div className="grid flex-1 grid-cols-[minmax(0,1fr)_380px] content-start items-start gap-5 px-6 pb-4 pt-4 max-[1100px]:grid-cols-1 max-sm:px-4">
        <main aria-label="帶入舊設定" className="flex min-w-0 flex-col gap-3">
          <header className="flex flex-col gap-0.5">
            <h1 className="m-0 flex items-center gap-2 text-[22px] max-md:sr-only-text">
              <Icon icon={FolderInput} size={22} color="var(--color-accent)" />
              帶入舊設定
            </h1>
            <span className="text-sub text-ink-2">從這台電腦上的 LazyBoy 或 OpenMausBot 帶進來。只讀舊資料，不會改動或刪掉它。</span>
          </header>
          {sources && sources.length > 1 && (
            <div role="radiogroup" aria-label="舊資料" className="flex flex-wrap gap-2">
              {sources.map((s) => (
                <button
                  key={s.id}
                  type="button"
                  role="radio"
                  aria-checked={s.id === srcId && !path}
                  onClick={() => choose(s.id)}
                  className={cx('rounded-full border px-3.5 py-1.5 text-sub', s.id === srcId && !path ? 'border-accent bg-accent-soft/30 text-ink' : 'border-line text-ink-2')}
                >
                  {s.label} · {s.bots} 個 Bot
                </button>
              ))}
            </div>
          )}
          {sources && sources.length === 0 && !plan && (
            <p className="m-0 rounded-card bg-card px-6 py-6 text-center text-sub text-ink-2" data-testid="import-none">
              在預設的位置沒有找到 LazyBoy 或 OpenMausBot 的資料。放在別的地方的話，在下面填資料夾。
            </p>
          )}
          {plan && (
            <div className="flex flex-col gap-0.5 rounded-card bg-card px-4 py-2.5" data-testid="import-source">
              <span className="text-ui">{plan.source.summary}</span>
              <span className="truncate text-meta text-ink-3">{plan.source.path}</span>
            </div>
          )}
          {plan && (
            <section aria-label="Bot" className="flex flex-col gap-2">
              <h2 className="m-0 text-body">Bot · {botItems.length}</h2>
              <p className="m-0 text-meta text-ink-2">跟現有 Bot 同名的會自動加編號。對話紀錄不帶。</p>
              <ul className="m-0 flex list-none flex-col overflow-hidden rounded-group bg-card p-0">
                {botItems.map((it) => (
                  <BotCard key={it.key} plan={plan} sel={sel} it={it} name={names[it.key] ?? ''} onName={(v) => setNames((n) => ({ ...n, [it.key]: v }))} onToggle={onToggle} />
                ))}
              </ul>
            </section>
          )}
          <details className="rounded-card bg-card px-4 py-2.5 text-sub">
            <summary className="cursor-pointer text-ink-2">資料放在別的資料夾？</summary>
            <form
              className="mt-2 flex items-end gap-2 max-sm:flex-col max-sm:items-stretch"
              onSubmit={(e) => {
                e.preventDefault()
                if (!other.trim()) return
                setPath(other.trim())
                void loadPlan({ path: other.trim() })
              }}
            >
              <TextInput label="資料夾" placeholder="例：~/LazyBoyHermes/.data 或 ~/.openmausbot" value={other} onChange={(e) => setOther(e.target.value)} className="flex-1" />
              <Button type="submit" disabled={!other.trim()} loading={busy && !!other}>
                讀取
              </Button>
            </form>
          </details>
          {err && (
            <p role="alert" className="m-0 text-meta text-danger">
              {err}
            </p>
          )}
        </main>
        {plan && (
          <aside aria-label="加入前請看一下" className="flex min-w-0 flex-col gap-2">
            <h2 className="m-0 text-body">加入前請看一下</h2>
            <div className="flex flex-col overflow-hidden rounded-group bg-card">
              <Note n={1} title="權限都是「先問我」">
                <span className="inline-flex items-start gap-1.5">
                  <Icon icon={ShieldCheck} size={16} className="mt-0.5 text-success" />
                  不管舊系統怎麼設，帶進來的 Bot 花錢、對外發送、刪除前都會先問你。團隊管理權限要你重新打開。
                </span>
              </Note>
              <Note n={2} title={`${counts?.skills ?? 0} 個技能先關閉`}>
                放進每個 Bot 自己的技能，到 Bot 的「能力」看過內容再逐一打開。
              </Note>
              <Note n={3} title={`${pausedRoutines} 個定時任務先暫停`}>
                你確認時間與內容後，到「定時任務與行事曆」再啟用。
              </Note>
              {orgItems.length > 0 && (
                <Note n={4} title="團隊與群組">
                  <div className="flex flex-col pt-1 text-ink">
                    {orgItems.map((it) => (
                      <ItemCheck key={it.key} plan={plan} sel={sel} it={it} onToggle={onToggle} />
                    ))}
                  </div>
                </Note>
              )}
              {keyItems.length > 0 && (
                <Note n={orgItems.length > 0 ? 5 : 4} title="模型金鑰" tone="warning">
                  <span className="inline-flex items-start gap-1.5">
                    <Icon icon={KeyRound} size={16} className="mt-0.5" />
                    要你勾選才帶。金鑰加密後只放在 XBot 服務，不進 Bot 的電腦；已經設定過的供應商不覆寫。
                  </span>
                  <div className="flex flex-col pt-1 text-ink">
                    {keyItems.map((it) => (
                      <ItemCheck key={it.key} plan={plan} sel={sel} it={it} onToggle={onToggle} />
                    ))}
                  </div>
                </Note>
              )}
              <Note n={4 + (orgItems.length > 0 ? 1 : 0) + (keyItems.length > 0 ? 1 : 0)} title="不帶的東西">
                <ul className="m-0 list-disc pl-4">
                  {plan.not_carried.map((t) => (
                    <li key={t}>{t}</li>
                  ))}
                </ul>
              </Note>
            </div>
            {plan.notes.length > 0 && (
              <ul aria-label="讀舊資料時的小問題" className="m-0 list-disc pl-5 text-meta text-ink-3">
                {plan.notes.map((t) => (
                  <li key={t}>{t}</li>
                ))}
              </ul>
            )}
          </aside>
        )}
      </div>
      {plan && counts && (
        <footer className="sticky bottom-0 flex flex-wrap items-center gap-3 border-t border-line bg-app px-6 py-3 max-sm:px-4" aria-label="匯入">
          <span className={cx('mr-auto text-sub', over ? 'text-danger' : 'text-ink-2')} data-testid="import-summary">
            {over
              ? `還可以建立 ${plan.remaining} 個 Bot；請少勾幾個，或先封存不用的 Bot`
              : counts.bots > 0
                ? `會建立 ${counts.bots} 個 Bot，每個都有自己的電腦，第一次收到工作才開機。`
                : keys.length > 0
                  ? `會帶入 ${keys.length} 項。`
                  : '都帶進來了，或沒有勾選。'}
            {counts.logins > 0 && `瀏覽器登入約 ${human(plan.items.filter((it) => it.kind === 'login' && keys.includes(it.key)).reduce((n, it) => n + (it.bytes ?? 0), 0))}。`}
          </span>
          <Button onClick={() => goBack('/')} disabled={busy}>
            返回
          </Button>
          <Button variant="primary" loading={busy} disabled={keys.length === 0 || over} onClick={() => void start()}>
            開始匯入
          </Button>
        </footer>
      )}
    </div>
  )
}
