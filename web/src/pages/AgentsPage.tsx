import { useCallback, useEffect, useRef, useState, type ReactNode } from 'react'
import { Check, Copy, Globe, Hourglass, Info, Lock, Plus, RefreshCw, RotateCcw, Server, ShieldCheck, SlidersHorizontal, SquareTerminal, Undo2 } from 'lucide-react'
import {
  ApiError,
  agentsList,
  agentsPairStart,
  agentsRestore,
  agentsRevoke,
  agentsSetDelegation,
  agentsUpdateScopes,
  auditList,
  type AgentsPairStartOutput,
} from '../api/generated'
import {
  CATEGORIES,
  EXPIRY_DAYS,
  NEVER,
  NEW_DRAFT,
  SCOPES,
  ago,
  auditResult,
  canRestore,
  delegationInput,
  draftOf,
  resourceText,
  toCount,
  untilLabel,
  updateInput,
  usdToMicros,
  validate,
  type Agent,
  type AuditEntry,
  type Draft,
  type ExpiryDays,
  type ResourceScope,
} from '../data/agents'
import { botsStore, look } from '../data/bots'
import { useStore } from '../data/store'
import { useNetwork } from '../data/network'
import { useFunnelSwitch } from '../settings/useFunnelSwitch'
import { linkClick } from '../router'
import { Avatar } from '../ui/Avatar'
import { Button } from '../ui/Button'
import { Checkbox, SegmentedControl, Switch } from '../ui/Controls'
import { Dialog } from '../ui/Dialog'
import { Icon } from '../ui/Icon'
import { StatusChip } from '../ui/Pill'
import { useToast } from '../ui/toast-context'
import { cx } from '../ui/cx'

// 設定 → 已連線的代理（MCP）（design/J12，PRD MCP-6～MCP-13）。
//   - 入口：tailnet 內 HTTP、本機 stdio 可複製；外部入口（Funnel／反向代理）在 C35 才開放，這裡先顯示關閉。
//   - 已連線的代理：權限、資源範圍、今天呼叫數、代為核准標籤；可改權限、撤銷（10 分鐘內可復原）。
//   - 權限對話框：左欄權限範圍、資源範圍、每日上限；右欄代為核准（預設關閉）與「永遠要你本人核准」。
//   - 最近的呼叫：稽核紀錄裡代理的呼叫。

function errText(err: unknown): string {
  if (err instanceof ApiError) return err.message
  return err instanceof Error ? err.message : String(err)
}

function useCopied(): [string, (key: string, text: string) => void] {
  const [copied, setCopied] = useState('')
  const timer = useRef<number | undefined>(undefined)
  useEffect(() => () => window.clearTimeout(timer.current), [])
  const copy = (key: string, text: string) => {
    void navigator.clipboard?.writeText(text).catch(() => undefined)
    setCopied(key)
    window.clearTimeout(timer.current)
    timer.current = window.setTimeout(() => setCopied(''), 2000)
  }
  return [copied, copy]
}

function CopyButton({ k, text, copied, copy, label }: { k: string; text: string; copied: string; copy: (k: string, t: string) => void; label: string }) {
  return (
    <Button size="sm" icon={copied === k ? Check : Copy} onClick={() => copy(k, text)} aria-label={`複製${label}`}>
      {copied === k ? '已複製' : '複製'}
    </Button>
  )
}

const SNIPPET = {
  http: (url: string) => `{ "mcpServers": { "xbot": {\n    "type": "http",\n    "url": "${url}"\n} } }`,
  stdio: () => `{\n  "mcpServers": {\n    "xbot": {\n      "command": "xbot",\n      "args": ["mcp"]\n    }\n  }\n}`,
}

function Entrances() {
  const [copied, copy] = useCopied()
  const [snip, setSnip] = useState<'http' | 'stdio'>('http')
  const net = useNetwork().status
  const funnel = useFunnelSwitch()
  // tailnet 網址有的話給它（其他電腦上的代理用）；沒有就用目前這個網站（C35）。
  const url = `${net?.tailscale.url || window.location.origin}/mcp`
  const ext = net?.funnel
  const extURL = net?.public_url ? `${net.public_url}/mcp` : ''
  const snippet = snip === 'http' ? SNIPPET.http(url) : SNIPPET.stdio()
  const tsOn = !!net?.tailscale.enabled
  const extState = !ext || !ext.enabled ? (net?.proxy.enabled ? '反向代理' : '關閉') : ext.state === 'on' ? '開放中' : ext.state === 'error' ? '開不起來' : '等 Tailscale'
  const extOn = !!ext?.enabled || !!net?.proxy.enabled
  return (
    <section aria-label="XBot MCP 入口" className="flex flex-col gap-3 rounded-card bg-panel px-5 py-4">
      <div className="flex items-center gap-3">
        <span className="flex size-9 flex-none items-center justify-center rounded-field bg-accent-soft text-accent-ink">
          <Icon icon={Server} size={18} />
        </span>
        <span className="flex min-w-0 flex-1 flex-col">
          <span className="text-body">XBot MCP</span>
          <span className="text-meta text-ink-3">
            一個伺服器：tailnet、本機{extOn ? '、外部' : ''}；工具名稱 <code className="font-mono">xbot.領域.動作</code>，這一版是 <code className="font-mono">xbot.v1</code>
          </span>
        </span>
        <StatusChip tone="success">運作中</StatusChip>
      </div>
      <div className="grid gap-3 min-[900px]:grid-cols-2">
        <div className="flex flex-col gap-2">
          <EntranceRow title="Streamable HTTP" tag={net?.tailscale.url ? 'tailnet 內' : '這個網址'} sub="其他電腦上的代理" value={url} action={<CopyButton k="http" text={url} copied={copied} copy={copy} label="Streamable HTTP" />} />
          <EntranceRow title="stdio（這台電腦）" tag="本機" sub="裝在這台電腦的代理" value="xbot mcp" action={<CopyButton k="stdio" text="xbot mcp" copied={copied} copy={copy} label="stdio" />} />
          <div data-testid="external-entry" className={'flex flex-col gap-1.5 rounded-group bg-inset px-3.5 py-2.5'}>
            <span className="flex flex-wrap items-center gap-2">
              <span className="text-ui-sm">外部入口</span>
              <StatusChip size="sm" tone={extOn ? 'warning' : 'neutral'}>
                {extState}
              </StatusChip>
              <span className="text-meta text-ink-3">tailnet 外的代理</span>
              <Switch className="ml-auto" checked={!!ext?.enabled} onChange={funnel.toggle} label="開啟外部入口" disabled={!tsOn || funnel.busy} />
            </span>
            {extURL ? (
              <span className="flex items-center gap-2">
                <code className="min-w-0 flex-1 truncate font-mono text-[13px] text-ink-2">{extURL}</code>
                <CopyButton k="ext" text={extURL} copied={copied} copy={copy} label="外部入口" />
              </span>
            ) : null}
            <span className="flex items-center gap-1.5 text-meta text-ink-3">
              <Icon icon={Globe} size={14} />
              <span className="min-w-0 flex-1">
                {tsOn ? 'Tailscale Funnel 或反向代理' : '先開內建 Tailscale，或用反向代理'}；一律要權杖，每分鐘最多 {net?.external_rate_per_min ?? 20} 次，比 tailnet 內嚴
                {ext && ext.allow_ips.length > 0 ? ` · 白名單 ${ext.allow_ips.length} 筆` : ''}
              </span>
              <a href="/settings/advanced/network" onClick={linkClick('/settings/advanced/network')} className="whitespace-nowrap text-accent-ink">
                設定
              </a>
            </span>
          </div>
        </div>
        <div className="flex min-w-0 flex-col gap-2 rounded-group bg-inset px-3.5 py-2.5">
          <div className="flex flex-wrap items-center gap-2">
            <span className="flex-1 text-ui-sm">貼進 MCP 用戶端</span>
            <SegmentedControl
              label="設定檔格式"
              value={snip}
              onChange={setSnip}
              options={[
                { value: 'http', label: 'HTTP' },
                { value: 'stdio', label: 'stdio' },
              ]}
            />
            <CopyButton k="json" text={snippet} copied={copied} copy={copy} label="設定" />
          </div>
          <pre tabIndex={0} className="m-0 overflow-x-auto whitespace-pre rounded-field bg-app px-3 py-2 font-mono text-[12.5px] leading-[1.5] text-ink-2">{snippet}</pre>
        </div>
      </div>
      {funnel.dialog}
    </section>
  )
}

function EntranceRow({ title, tag, sub, value, action }: { title: string; tag: string; sub: string; value: string; action: ReactNode }) {
  return (
    <div className="flex flex-col gap-1.5 rounded-group bg-inset px-3.5 py-2.5">
      <span className="flex flex-wrap items-center gap-2">
        <span className="text-ui-sm">{title}</span>
        <StatusChip size="sm" tone="accent">
          {tag}
        </StatusChip>
        <span className="text-meta text-ink-3">{sub}</span>
      </span>
      <span className="flex items-center gap-2">
        <code className="min-w-0 flex-1 truncate font-mono text-[13px] text-ink-2">{value}</code>
        {action}
      </span>
    </div>
  )
}

// ── 代理清單 ─────────────────────────────────────────

function AgentRow({ agent, botName, onEdit, onChanged }: { agent: Agent; botName: (id: string) => string | undefined; onEdit: () => void; onChanged: () => void }) {
  const toast = useToast()
  const [busy, setBusy] = useState(false)
  const revoked = agent.status === 'revoked'
  const run = async (fn: () => Promise<unknown>, done?: string) => {
    setBusy(true)
    try {
      await fn()
      if (done) toast.show({ kind: 'done', text: done })
      onChanged()
    } catch (err) {
      toast.show({ kind: 'error', text: errText(err) })
    } finally {
      setBusy(false)
    }
  }
  const pct = agent.daily_calls ? Math.min(100, Math.round((agent.calls_today / agent.daily_calls) * 100)) : 0
  return (
    <div data-testid="agent-row" data-agent={agent.name} className={cx('flex flex-wrap items-center gap-x-3 gap-y-2 px-4 py-3', revoked && 'opacity-60')}>
      <span className="flex size-9 flex-none items-center justify-center rounded-field bg-control text-ink-2">
        <Icon icon={SquareTerminal} size={18} />
      </span>
      <span className="flex min-w-0 flex-[999_1_260px] flex-col gap-0.5">
        <span className="flex flex-wrap items-center gap-1.5">
          <span className="text-ui">{agent.name}</span>
          {agent.scopes.map((s) => (
            <StatusChip key={s} size="sm">
              {s}
            </StatusChip>
          ))}
          {agent.delegation.enabled && (
            <StatusChip size="sm" tone="accent" icon={ShieldCheck}>
              代為核准
            </StatusChip>
          )}
        </span>
        <span className="text-meta text-ink-3">
          {resourceText(agent, botName)} · 最近 {ago(agent.last_used_at)} · {agent.calls_today ? `今天 ${agent.calls_today} 次呼叫` : '今天沒有呼叫'}
        </span>
      </span>
      {revoked ? (
        <span className="flex flex-none items-center gap-2">
          <span className="text-sub text-ink-2">已撤銷，權杖立即失效</span>
          {canRestore(agent) && (
            <Button size="sm" icon={Undo2} loading={busy} onClick={() => void run(() => agentsRestore({ id: agent.id }), `已復原：${agent.name}`)}>
              復原
            </Button>
          )}
        </span>
      ) : (
        <span className="flex flex-none flex-wrap items-center gap-2">
          {agent.daily_calls > 0 && (
            <span className="flex w-[120px] flex-col gap-1" title="每日上限">
              <span className="text-meta text-ink-2">
                今天 <span className="text-ink">{agent.calls_today}</span> / {agent.daily_calls} 次
              </span>
              <span className="block h-1 rounded-full bg-line">
                <span className="block h-1 rounded-full bg-accent" style={{ width: `${pct}%` }} />
              </span>
            </span>
          )}
          <Button size="sm" icon={SlidersHorizontal} onClick={onEdit}>
            權限
          </Button>
          <Button size="sm" variant="danger" loading={busy} onClick={() => void run(() => agentsRevoke({ id: agent.id }))}>
            撤銷
          </Button>
        </span>
      )}
    </div>
  )
}

function RecentCalls({ entries, agents, botName }: { entries: AuditEntry[]; agents: Agent[]; botName: (id: string) => string | undefined }) {
  const name = (actor: string) => agents.find((a) => 'agent:' + a.id === actor)?.name ?? actor
  return (
    <section aria-label="最近的呼叫" className="flex flex-col gap-2">
      <div className="flex flex-wrap items-baseline gap-3">
        <h2 className="m-0 flex-1 text-body">最近的呼叫</h2>
        <span className="inline-flex items-center gap-1 text-meta text-ink-3">
          <Icon icon={Lock} size={13} />
          參數裡的密鑰一律遮蔽
        </span>
      </div>
      <div className="overflow-hidden rounded-card bg-panel">
        {entries.length === 0 ? (
          <p className="m-0 px-4 py-4 text-sub text-ink-3">還沒有代理呼叫過。</p>
        ) : (
          <table className="w-full table-fixed border-collapse text-left text-sub">
            <colgroup>
              <col className="w-[72px]" />
              <col className="w-[30%]" />
              <col />
              <col className="w-[130px]" />
            </colgroup>
            <thead className="text-meta text-ink-3">
              <tr>
                <th className="px-4 py-2 font-normal">時間</th>
                <th className="px-2 py-2 font-normal">代理</th>
                <th className="px-2 py-2 font-normal">工具與對象</th>
                <th className="px-4 py-2 font-normal">結果</th>
              </tr>
            </thead>
            <tbody>
              {entries.map((e) => {
                const r = auditResult(e)
                const t = new Date(e.created_at)
                return (
                  <tr key={e.id} data-testid="audit-row" className="border-t border-line">
                    <td className="whitespace-nowrap px-4 py-2 text-ink-3">{`${String(t.getHours()).padStart(2, '0')}:${String(t.getMinutes()).padStart(2, '0')}`}</td>
                    <td className="truncate px-2 py-2">{name(e.actor)}</td>
                    <td className="max-w-0 px-2 py-2">
                      <span className="block truncate">
                        <code className="font-mono text-accent-ink">{e.op}</code>
                        {e.target && <span className="ml-2 text-ink-3">{botName(e.target) ?? e.target}</span>}
                      </span>
                    </td>
                    <td className="px-4 py-2">
                      <StatusChip size="sm" tone={r.tone}>
                        {r.label}
                      </StatusChip>
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        )}
      </div>
    </section>
  )
}

// ── 權限對話框 ─────────────────────────────────────────

function NumberBox({ label, value, onChange, prefix, suffix, error }: { label: string; value: string; onChange: (v: string) => void; prefix?: string; suffix: string; error?: string }) {
  return (
    <label className="flex flex-col gap-1">
      <span
        className={cx(
          'inline-flex h-9 items-center gap-1.5 rounded-field border bg-inset px-3 text-ui-sm focus-within:border-focus max-sm:h-11',
          error ? 'border-danger' : 'border-line',
        )}
      >
        {prefix && <span className="text-ink-3">{prefix}</span>}
        <input
          aria-label={label}
          aria-invalid={error ? true : undefined}
          inputMode="decimal"
          value={value}
          onChange={(e) => onChange(e.target.value)}
          placeholder="不限"
          className="w-16 min-w-0 bg-transparent text-ink outline-none placeholder:text-ink-3"
        />
        <span className="text-ink-3">{suffix}</span>
      </span>
      {error && <span className="text-meta text-danger">{error}</span>}
    </label>
  )
}

function Editor({ draft, set, bots, locked }: { draft: Draft; set: (d: Partial<Draft>) => void; bots: { id: string; name: string; shape?: string; color?: string }[]; locked?: boolean }) {
  const errs = validate(draft)
  const dg = draft.dg
  const setDg = (p: Partial<Draft['dg']>) => set({ dg: { ...dg, ...p } })
  const resOptions: { value: ResourceScope; label: string }[] = [
    { value: 'own', label: '它建立的' },
    { value: 'all', label: '全部 Bot' },
    { value: 'custom', label: '只有這些 Bot' },
  ]
  return (
    <fieldset disabled={locked} className="m-0 grid min-w-0 gap-5 border-0 p-0 min-[820px]:grid-cols-2">
      <div className="flex min-w-0 flex-col gap-4">
        <div className="flex flex-col gap-2">
          <span className="text-ui-sm">
            可以做什麼 <span className="text-meta text-ink-3">可以疊加；之後隨時改</span>
          </span>
          <div className="flex flex-col rounded-group bg-inset px-3.5 py-1">
            {SCOPES.map((s) => (
              <Checkbox
                key={s.key}
                className="border-line py-2 not-first:border-t"
                checked={draft.scopes.includes(s.key)}
                onChange={(on) => set({ scopes: on ? SCOPES.map((x) => x.key).filter((k) => k === s.key || draft.scopes.includes(k)) : draft.scopes.filter((k) => k !== s.key) })}
                note={s.desc}
              >
                <code className="mr-2 font-mono">{s.key}</code>
                {s.name}
              </Checkbox>
            ))}
          </div>
          {errs.scopes && <span className="text-meta text-danger">{errs.scopes}</span>}
        </div>
        <div className="flex flex-col gap-2">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <span className="text-ui-sm">可以碰哪些 Bot</span>
            <SegmentedControl label="資源範圍" value={draft.resourceScope} onChange={(v) => set({ resourceScope: v })} options={resOptions} />
          </div>
          {draft.resourceScope === 'custom' && (
            <div className="flex flex-wrap gap-1.5">
              {bots.length === 0 && <span className="text-meta text-ink-3">還沒有 Bot</span>}
              {bots.map((b) => {
                const on = draft.bots.includes(b.id)
                return (
                  <button
                    key={b.id}
                    type="button"
                    aria-pressed={on}
                    onClick={() => set({ bots: on ? draft.bots.filter((x) => x !== b.id) : [...draft.bots, b.id] })}
                    className={cx('inline-flex h-8 items-center gap-1.5 rounded-full border pl-1 pr-3 text-sub', on ? 'border-accent bg-accent-soft' : 'border-line bg-inset')}
                  >
                    <Avatar {...look(b)} state="idle" dot="none" size={22} />
                    {b.name}
                  </button>
                )
              })}
            </div>
          )}
          <span className="text-meta text-ink-3">它自己建立的 Bot 一定可以碰；團隊和主機照同樣的範圍</span>
        </div>
        <div className="flex flex-wrap items-start justify-between gap-2">
          <span className="pt-2 text-ui-sm">每日上限</span>
          <span className="flex flex-wrap gap-2">
            <NumberBox label="每日呼叫上限" value={draft.dailyCalls} onChange={(v) => set({ dailyCalls: v })} suffix="次呼叫" error={errs.dailyCalls} />
            <NumberBox label="每日花費上限" value={draft.dailySpend} onChange={(v) => set({ dailySpend: v })} prefix="US$" suffix="／天" error={errs.dailySpend} />
          </span>
        </div>
      </div>

      <div className="flex min-w-0 flex-col gap-3 border-line min-[820px]:border-l min-[820px]:pl-5">
        <div className="flex items-center gap-3">
          <span className="flex flex-1 flex-col">
            <span className="text-ui-sm">代為核准</span>
            <span className="text-meta text-ink-3">讓它代你按「允許」，預設關閉</span>
          </span>
          <Switch checked={dg.on} onChange={(on) => setDg({ on })} label="允許代為核准" />
        </div>
        <div className={cx('flex flex-col gap-3', !dg.on && 'pointer-events-none opacity-40')} aria-disabled={!dg.on}>
          <div className="flex flex-col gap-1.5">
            <span className="text-meta text-ink-2">哪些類別</span>
            <div className="grid grid-cols-2 gap-1.5">
              {CATEGORIES.map((c) => (
                <Checkbox
                  key={c.key}
                  disabled={!dg.on}
                  className="rounded-field bg-inset px-3 py-2"
                  checked={dg.cats.includes(c.key)}
                  onChange={(on) => setDg({ cats: on ? CATEGORIES.map((x) => x.key).filter((k) => k === c.key || dg.cats.includes(k)) : dg.cats.filter((k) => k !== c.key) })}
                >
                  {c.name}
                </Checkbox>
              ))}
            </div>
            {dg.on && errs.dgCats && <span className="text-meta text-danger">{errs.dgCats}</span>}
          </div>
          <div className="flex flex-wrap items-start justify-between gap-2">
            <span className="pt-2 text-meta text-ink-2">每日上限</span>
            <span className="flex flex-wrap gap-2">
              <NumberBox label="每日代為核准次數" value={dg.count} onChange={(v) => setDg({ count: v })} suffix="次" error={dg.on ? errs.dgCount : undefined} />
              <NumberBox label="每日代為核准金額" value={dg.amount} onChange={(v) => setDg({ amount: v })} prefix="US$" suffix="／天" error={dg.on ? errs.dgAmount : undefined} />
            </span>
          </div>
          <div className="flex flex-wrap items-center justify-between gap-2">
            <span className="text-meta text-ink-2">有效期限</span>
            <span className="flex items-center gap-2">
              <span className="text-meta text-ink-3">{untilLabel(dg.days)}</span>
              <SegmentedControl<string>
                label="有效期限"
                value={String(dg.days)}
                onChange={(v) => setDg({ days: Number(v) as ExpiryDays })}
                options={EXPIRY_DAYS.map((d) => ({ value: String(d), label: `${d} 天` }))}
              />
            </span>
          </div>
        </div>
        <div data-testid="never-delegate" className="flex flex-col gap-1.5 rounded-group border border-danger/40 bg-danger-soft px-3.5 py-2.5">
          <span className="inline-flex items-center gap-1.5 text-ui-sm">
            <Icon icon={Lock} size={15} color="var(--color-danger)" />
            永遠要你本人核准
          </span>
          <span className="grid grid-cols-2 gap-x-3 gap-y-0.5 text-meta text-ink-2">
            {NEVER.map((n) => (
              <span key={n}>· {n}</span>
            ))}
          </span>
        </div>
        <span className="inline-flex items-start gap-1.5 text-meta text-ink-3">
          <Icon icon={RotateCcw} size={14} />
          每次代為核准都在待我處理留一張紀錄，可復原的 10 分鐘內能復原
        </span>
      </div>
    </fieldset>
  )
}

function useBotsForPicker() {
  const bots = useStore(botsStore).bots ?? []
  return bots.filter((b) => !b.archived).map((b) => ({ id: b.id, name: b.name, shape: b.shape, color: b.color }))
}

/** 改一個代理的權限：改了就自動儲存（design/J12「變更會自動儲存，下一次呼叫就生效」）。 */
function EditDialog({ agent, onClose, onSaved }: { agent: Agent; onClose: () => void; onSaved: () => void }) {
  const bots = useBotsForPicker()
  const [draft, setDraft] = useState<Draft>(() => draftOf(agent))
  const [dirty, setDirty] = useState(false)
  const [state, setState] = useState<'idle' | 'saving' | 'saved' | 'error'>('idle')
  const [error, setError] = useState('')
  const saving = useRef<Promise<void> | null>(null)

  const save = useCallback(
    (d: Draft) => {
      if (Object.keys(validate(d)).length) return Promise.resolve()
      setState('saving')
      const p = Promise.all([agentsUpdateScopes(updateInput(agent.id, d)), agentsSetDelegation(delegationInput(agent.id, d))])
        .then(() => {
          setState('saved')
          setError('')
          onSaved()
        })
        .catch((err: unknown) => {
          setState('error')
          setError(errText(err))
        })
      saving.current = p
      return p
    },
    [agent.id, onSaved],
  )

  useEffect(() => {
    if (!dirty) return
    const t = window.setTimeout(() => void save(draft), 500)
    return () => window.clearTimeout(t)
  }, [draft, dirty, save])

  const done = async () => {
    if (dirty) await save(draft)
    else await saving.current
    onClose()
  }

  return (
    <Dialog
      open
      onClose={() => void done()}
      width={900}
      title={`${agent.name} 的權限`}
      description="改了下一次呼叫就生效；要讓它停用請按撤銷"
      actions={
        <span className="flex w-full flex-wrap items-center justify-end gap-3">
          <span role="status" className={cx('flex-1 text-meta', state === 'error' ? 'text-danger' : 'text-ink-3')}>
            {state === 'saving' ? '儲存中…' : state === 'saved' ? '已儲存，下一次呼叫就生效' : state === 'error' ? error : '變更會自動儲存，下一次呼叫就生效'}
          </span>
          <Button variant="primary" onClick={() => void done()}>
            完成
          </Button>
        </span>
      }
    >
      <div className="pt-2">
        <Editor
          draft={draft}
          bots={bots}
          set={(p) => {
            setDraft((d) => ({ ...d, ...p }))
            setDirty(true)
          }}
        />
      </div>
    </Dialog>
  )
}

function useCountdown(until: number): string {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    const t = window.setInterval(() => setNow(Date.now()), 1000)
    return () => window.clearInterval(t)
  }, [])
  const left = Math.max(0, Math.round((until - now) / 1000))
  return `${Math.floor(left / 60)}:${String(left % 60).padStart(2, '0')}`
}

function PairCode({ pair, copied, copy, expired }: { pair: AgentsPairStartOutput; copied: string; copy: (k: string, t: string) => void; expired: boolean }) {
  const left = useCountdown(pair.expires_at)
  return (
    <div data-testid="pair-code" className="flex flex-wrap items-center gap-3 rounded-group bg-inset px-4 py-3">
      <span className="flex min-w-0 flex-1 flex-col gap-0.5">
        <span className="text-meta text-ink-3">配對碼</span>
        <span className={cx('font-mono text-[30px] tracking-[0.12em]', expired && 'text-ink-3 line-through')}>{pair.code}</span>
        <span className="text-meta text-ink-3">
          在代理裡輸入，或執行 <code className="font-mono">xbot mcp pair {pair.code}</code>
        </span>
      </span>
      <span className="flex flex-col items-end gap-2">
        <StatusChip tone={expired ? 'danger' : 'warning'} icon={Hourglass}>
          {expired ? '已過期，請重新產生' : `${left} 後失效 · 只能用一次`}
        </StatusChip>
        <CopyButton k="code" text={pair.code ?? ''} copied={copied} copy={copy} label="配對碼" />
      </span>
    </div>
  )
}

/** 連接新的代理：先選權限，產生配對碼；代理換到權杖、出現在清單後，自動套用代為核准設定。 */
function NewDialog({ known, onClose, onPaired }: { known: string[]; onClose: () => void; onPaired: (a: Agent) => void }) {
  const bots = useBotsForPicker()
  const toast = useToast()
  const [draft, setDraft] = useState<Draft>(NEW_DRAFT)
  const [pair, setPair] = useState<AgentsPairStartOutput | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [now, setNow] = useState(() => Date.now())
  const errs = validate(draft)
  const expired = !!pair && pair.expires_at <= now
  const [copied, copy] = useCopied()

  const start = async () => {
    setBusy(true)
    setError('')
    try {
      const out = await agentsPairStart({
        scopes: draft.scopes,
        resource_scope: draft.resourceScope,
        ...(draft.resourceScope === 'custom' ? { resources: { bots: draft.bots } } : {}),
        daily_calls: toCount(draft.dailyCalls) ?? 0,
        daily_spend_micros: usdToMicros(draft.dailySpend) ?? 0,
      })
      setPair(out)
      setNow(Date.now())
    } catch (err) {
      setError(errText(err))
    } finally {
      setBusy(false)
    }
  }

  // 等代理配對：清單多了一個新的代理就是它（只在對話框開著時檢查）。
  const knownRef = useRef(known)
  useEffect(() => {
    if (!pair) return
    const t = window.setInterval(() => {
      setNow(Date.now())
      agentsList({})
        .then(async (out) => {
          const fresh = out.agents.find((a) => !knownRef.current.includes(a.id))
          if (!fresh) return
          window.clearInterval(t)
          if (draft.dg.on) {
            try {
              await agentsSetDelegation(delegationInput(fresh.id, draft))
            } catch (err) {
              toast.show({ kind: 'error', text: `代為核准沒有設定成功：${errText(err)}` })
            }
          }
          toast.show({ kind: 'done', text: `已連上：${fresh.name}` })
          onPaired(fresh)
        })
        .catch(() => undefined)
    }, 2000)
    return () => window.clearInterval(t)
  }, [pair, draft, onPaired, toast])

  return (
    <Dialog
      open
      onClose={onClose}
      width={900}
      title="連接新的代理"
      description="配對碼 5 分鐘內有效、只能用一次；換到的權杖可以隨時撤銷"
      actions={
        <span className="flex w-full flex-wrap items-center justify-end gap-2">
          {pair && !expired && (
            <span role="status" className="inline-flex flex-1 items-center gap-1.5 text-meta text-ink-3">
              <Icon icon={RefreshCw} size={14} className="animate-spin" />
              等代理輸入配對碼，連上就會出現在清單
            </span>
          )}
          {error && (
            <span role="alert" className="flex-1 text-meta text-danger">
              {error}
            </span>
          )}
          <Button onClick={onClose}>取消</Button>
          {pair ? (
            <Button icon={RefreshCw} loading={busy} onClick={() => void start()}>
              重新產生
            </Button>
          ) : (
            <Button variant="primary" loading={busy} disabled={Object.keys(errs).length > 0} onClick={() => void start()}>
              產生配對碼
            </Button>
          )}
        </span>
      }
    >
      <div className="flex flex-col gap-4 pt-2">
        {pair && <PairCode pair={pair} copied={copied} copy={copy} expired={expired} />}
        {pair && (
          <span className="inline-flex items-center gap-1.5 text-meta text-ink-3">
            <Icon icon={Info} size={14} />
            要改權限請按「重新產生」；連上之後也能隨時改
          </span>
        )}
        <Editor draft={draft} bots={bots} locked={!!pair && !expired} set={(p) => setDraft((d) => ({ ...d, ...p }))} />
      </div>
    </Dialog>
  )
}

// ── 頁面 ─────────────────────────────────────────────

export function AgentsPage() {
  const [agents, setAgents] = useState<Agent[] | null>(null)
  const [audit, setAudit] = useState<AuditEntry[]>([])
  const [error, setError] = useState('')
  const [dialog, setDialog] = useState<{ kind: 'new' } | { kind: 'edit'; id: string } | null>(null)
  const bots = useStore(botsStore).bots
  const botName = (id: string) => bots?.find((b) => b.id === id)?.name

  const load = useCallback(() => {
    agentsList({ include_revoked: true })
      .then((out) => {
        setAgents(out.agents.filter((a) => a.status === 'active' || canRestore(a)))
        setError('')
      })
      .catch((err: unknown) => setError(errText(err)))
    auditList({ limit: 100 })
      .then((out) => setAudit(out.entries.filter((e) => e.actor.startsWith('agent:')).slice(0, 8)))
      .catch(() => undefined)
  }, [])
  useEffect(load, [load])

  const live = agents?.filter((a) => a.status === 'active') ?? []
  const editing = dialog?.kind === 'edit' ? agents?.find((a) => a.id === dialog.id) : undefined

  return (
    // 放在設定頁的 <main> 裡，自己不再是 main（v1.0 無障礙：landmark 不重複）
    <div className="flex flex-1 justify-center px-8 py-6 max-sm:px-4">
      <div className="flex w-full max-w-[1000px] flex-col gap-5">
        <header className="flex flex-wrap items-end justify-between gap-3">
          <div className="flex flex-col gap-0.5">
            <h1 className="m-0 text-page">已連線的代理（MCP）</h1>
            <p className="m-0 text-sub text-ink-2">Claude Code、Codex、Cursor 或另一台 XBot 用 MCP 操作這台 XBot，只在你給的權限內。</p>
          </div>
          <Button variant="primary" icon={Plus} onClick={() => setDialog({ kind: 'new' })}>
            連接新的代理
          </Button>
        </header>

        <Entrances />

        <section aria-label="已連線的代理" className="flex flex-col gap-2">
          <div className="flex flex-wrap items-baseline gap-3">
            <h2 className="m-0 flex-1 text-body">已連線的代理 · {live.length}</h2>
            <span className="text-meta text-ink-3">Bot 電腦裡的 Bot 也走同一個 MCP，身分就是那個 Bot</span>
          </div>
          {error && (
            <p role="alert" className="m-0 text-sub text-danger">
              {error}
            </p>
          )}
          <div className="flex flex-col divide-y divide-line overflow-hidden rounded-card bg-panel">
            {agents === null && !error && <p className="m-0 px-4 py-4 text-sub text-ink-3">讀取中…</p>}
            {agents?.length === 0 && (
              <div data-testid="agents-empty" className="flex flex-col items-center gap-2 px-6 py-8 text-center">
                <Icon icon={SquareTerminal} size={26} color="var(--color-ink-3)" />
                <span className="text-body">還沒有連線的代理</span>
                <span className="text-sub text-ink-2">按「連接新的代理」產生配對碼，貼給 Claude Code 或 Codex。</span>
              </div>
            )}
            {agents?.map((a) => (
              <AgentRow key={a.id} agent={a} botName={botName} onEdit={() => setDialog({ kind: 'edit', id: a.id })} onChanged={load} />
            ))}
          </div>
        </section>

        <div className="flex flex-wrap items-center gap-2 rounded-group border border-warning/40 bg-warning-soft px-4 py-3 text-sub">
          <Icon icon={ShieldCheck} size={16} color="var(--color-warning-ink)" />
          <span className="min-w-0 flex-1">需要核准的動作一律送到待我處理。只有你授權「代為核准」的代理能代你按，刪除、移除主機等永遠要你本人。</span>
          <a href="/" onClick={linkClick('/')}>
            待我處理
          </a>
        </div>

        <RecentCalls entries={audit} agents={agents ?? []} botName={botName} />
      </div>

      {dialog?.kind === 'new' && (
        <NewDialog
          known={(agents ?? []).map((a) => a.id)}
          onClose={() => setDialog(null)}
          onPaired={() => {
            setDialog(null)
            load()
          }}
        />
      )}
      {editing && <EditDialog key={editing.id} agent={editing} onClose={() => setDialog(null)} onSaved={load} />}
    </div>
  )
}

