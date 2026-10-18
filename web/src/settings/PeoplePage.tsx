import { useCallback, useEffect, useMemo, useState } from 'react'
import { Bot as BotIcon, ChevronRight, Crown, PlugZap } from 'lucide-react'
import { agentsList, auditList, OPS, type AuditListInput, type OpName } from '../api/generated'
import { auditResult, type Agent, type AuditEntry } from '../data/agents'
import { botsStore, look } from '../data/bots'
import { useStore } from '../data/store'
import { linkClick } from '../router'
import { Avatar } from '../ui/Avatar'
import { Button } from '../ui/Button'
import { Icon } from '../ui/Icon'
import { Select } from '../ui/Menu'
import { StatusChip } from '../ui/Pill'
import { errText } from './format'
import { Block, Group, SectionHeader } from './parts'

// 設定 › 進階 › 成員與紀錄（J08）：目前的成員（v1.0 只有你本人，加上連線的外部代理）與活動紀錄（xbot.audit.list）。
// 和畫板不同：多人帳號、邀請、匯出 CSV 還沒有（v1.0 只有一個使用者）；紀錄依誰、種類、結果、日期篩選。

const DOMAIN: Record<string, string> = {
  bots: 'Bot',
  chat: '對話',
  approvals: '核准',
  inbox: '待我處理',
  computers: '電腦',
  hosts: '主機',
  files: '檔案',
  skills: '技能',
  memory: '記憶',
  models: 'AI 模型',
  usage: '用量與花費',
  engines: '引擎',
  apps: '應用程式',
  logins: '網站登入',
  routines: '定時任務',
  rooms: '群組',
  collab: 'Bot 之間',
  teams: '團隊',
  agenda: '提案與討論',
  agents: '外部代理',
  network: '網路',
  backup: '備份',
  teaching: '教學',
  import: '匯入',
  judge: '快判',
  devices: '裝置',
  push: '推播',
}

const DAYS = [
  { value: '1', label: '今天' },
  { value: '7', label: '最近 7 天' },
  { value: '30', label: '最近 30 天' },
  { value: 'all', label: '全部' },
] as const

const STATUS = [
  { value: '', label: '所有結果' },
  { value: 'ok', label: '完成' },
  { value: 'error', label: '失敗或被擋' },
  { value: 'pending_approval', label: '等你核准' },
  { value: 'reverted', label: '已復原' },
] as const

type Filters = { actor: string; domain: string; status: string; days: string }

/** 篩選條件 → xbot.audit.list 的輸入。 */
export function auditInput(f: Filters, now = Date.now()): AuditListInput {
  const in_: AuditListInput = { limit: 50 }
  if (f.actor) in_.actor = f.actor
  if (f.domain) in_.op = 'xbot.' + f.domain
  if (f.status) in_.status = f.status as AuditListInput['status']
  if (f.days !== 'all') {
    const d = new Date(now)
    d.setHours(0, 0, 0, 0)
    d.setDate(d.getDate() - (Number(f.days) - 1))
    in_.since = d.getTime()
  }
  return in_
}

/** 一筆紀錄做了什麼：操作的白話說明，找不到時用操作名稱。 */
export function opText(op: string): string {
  return OPS[op as OpName]?.summary ?? op
}

function when(ms: number): string {
  const d = new Date(ms)
  return `${d.getMonth() + 1}/${d.getDate()} ${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}`
}

const VIA: Record<string, string> = { http: '網頁', ui: '網頁', mcp: 'MCP', cli: '指令列' }

export function PeoplePage() {
  const bots = useStore(botsStore).bots
  const [agents, setAgents] = useState<Agent[]>([])
  const [filters, setFilters] = useState<Filters>({ actor: '', domain: '', status: '', days: '7' })
  const [entries, setEntries] = useState<AuditEntry[] | null>(null)
  const [next, setNext] = useState(0)
  const [err, setErr] = useState<string | null>(null)
  const [more, setMore] = useState(false)

  useEffect(() => {
    agentsList({})
      .then((o) => setAgents(o.agents.filter((a) => a.status === 'active')))
      .catch(() => setAgents([]))
  }, [])

  const load = useCallback((f: Filters) => {
    auditList(auditInput(f))
      .then((o) => {
        setEntries(o.entries)
        setNext(o.next_before ?? 0)
        setErr(null)
      })
      .catch((e: unknown) => setErr(errText(e)))
  }, [])
  useEffect(() => load(filters), [filters, load])

  const loadMore = async () => {
    setMore(true)
    try {
      const o = await auditList({ ...auditInput(filters), before: next })
      setEntries((cur) => [...(cur ?? []), ...o.entries])
      setNext(o.next_before ?? 0)
    } catch (e) {
      setErr(errText(e))
    } finally {
      setMore(false)
    }
  }

  const who = useMemo(() => {
    const agentName = new Map(agents.map((a) => [`agent:${a.id}`, a.name]))
    return (actor: string): { name: string; kind: 'owner' | 'agent' | 'bot' | 'other'; bot?: NonNullable<typeof bots>[number] } => {
      if (actor === 'owner' || actor === '') return { name: '你', kind: 'owner' }
      if (actor.startsWith('agent:')) return { name: agentName.get(actor) ?? '外部代理', kind: 'agent' }
      if (actor.startsWith('bot:')) {
        const b = bots?.find((x) => 'bot:' + x.id === actor)
        return { name: b?.name ?? 'Bot', kind: 'bot', bot: b }
      }
      return { name: actor, kind: 'other' }
    }
  }, [agents, bots])

  const actorOptions = [
    { value: '', label: '所有人與 Bot' },
    { value: 'owner', label: '你' },
    ...agents.map((a) => ({ value: `agent:${a.id}`, label: `${a.name}（代理）` })),
    ...(bots ?? []).map((b) => ({ value: `bot:${b.id}`, label: b.name })),
  ]
  const domainOptions = [{ value: '', label: '所有種類' }, ...Object.entries(DOMAIN).map(([value, label]) => ({ value, label }))]
  const set = (k: keyof Filters) => (v: string) => {
    setEntries(null)
    setFilters((f) => ({ ...f, [k]: v }))
  }

  return (
    <>
      <SectionHeader title="成員與紀錄" sub="誰核准了什麼、改了哪些設定都會留下紀錄。" />

      <Block title="成員" sub="這一版只有你一個人使用；外部代理用你給的權限操作。">
        <Group label="成員">
          <div className="flex min-h-14 items-center gap-3 px-4 py-2.5" data-testid="member-owner">
            <span className="flex size-9 flex-none items-center justify-center rounded-full bg-accent-soft text-accent-ink">
              <Icon icon={Crown} size={18} />
            </span>
            <span className="min-w-0 flex-1">
              <span className="block text-ui">你（擁有者）</span>
              <span className="block text-[13px] text-ink-2">可以改所有設定、核准任何 Bot 的事</span>
            </span>
            <StatusChip size="sm" tone="accent">
              管理員
            </StatusChip>
          </div>
          <a
            href="/settings/advanced/agents"
            onClick={linkClick('/settings/advanced/agents')}
            className="flex min-h-14 items-center gap-3 border-t border-line px-4 py-2.5 text-ink hover:bg-control/50"
            data-testid="member-agents"
          >
            <span className="flex size-9 flex-none items-center justify-center rounded-full bg-control">
              <Icon icon={PlugZap} size={18} />
            </span>
            <span className="min-w-0 flex-1">
              <span className="block text-ui">已連線的代理 · {agents.length}</span>
              <span className="block truncate text-[13px] text-ink-2">{agents.length ? agents.map((a) => a.name).join('、') : 'Claude Code、Codex 等經 MCP 操作 XBot'}</span>
            </span>
            <span className="text-ink-3">
              <Icon icon={ChevronRight} size={18} />
            </span>
          </a>
        </Group>
        <p className="m-0 text-meta text-ink-3">讓同事用自己的帳號登入、邀請成員，之後的版本開放。</p>
      </Block>

      <Block title="活動紀錄" aside={<span className="text-[13px] text-ink-2">紀錄不能修改或刪除</span>}>
        <div className="flex flex-wrap gap-2" role="group" aria-label="篩選紀錄">
          <Select look="compact" label="誰" hideLabel value={filters.actor} options={actorOptions} onChange={set('actor')} />
          <Select look="compact" label="種類" hideLabel value={filters.domain} options={domainOptions} onChange={set('domain')} />
          <Select look="compact" label="結果" hideLabel value={filters.status} options={[...STATUS]} onChange={set('status')} />
          <Select look="compact" label="日期" hideLabel value={filters.days} options={[...DAYS]} onChange={set('days')} />
        </div>
        {err && <p className="m-0 rounded-group bg-card px-4 py-3 text-ui-sm text-danger">讀不到紀錄：{err}</p>}
        {!entries && !err && <p className="m-0 rounded-group bg-card px-4 py-3 text-ui-sm text-ink-2">讀取中…</p>}
        {entries && entries.length === 0 && <p className="m-0 rounded-group bg-card px-4 py-3 text-ui-sm text-ink-2">這段時間沒有符合的紀錄。</p>}
        {entries && entries.length > 0 && (
          <Group label="活動紀錄">
            {entries.map((e) => {
              const w = who(e.actor)
              const r = auditResult(e)
              return (
                <div key={e.id} className="flex min-h-12 items-center gap-3 border-line px-4 py-2.5 not-first:border-t max-sm:flex-wrap" data-testid="audit-row">
                  <span className="w-[76px] flex-none text-[13px] text-ink-2 tabular-nums">{when(e.created_at)}</span>
                  <span className="flex w-[120px] flex-none items-center gap-2 max-sm:w-auto">
                    {w.bot ? (
                      <Avatar {...look(w.bot)} size={22} blink={false} dot="none" />
                    ) : (
                      <span className="flex size-[22px] flex-none items-center justify-center rounded-full bg-control text-ink-2">
                        <Icon icon={w.kind === 'agent' ? PlugZap : w.kind === 'bot' ? BotIcon : Crown} size={12} />
                      </span>
                    )}
                    <span className="truncate text-ui-sm">{w.name}</span>
                  </span>
                  <span className="min-w-0 flex-1 max-sm:basis-full">
                    <span className="block truncate text-ui-sm">{opText(e.op)}</span>
                    <span className="block truncate text-[12px] text-ink-3">
                      {DOMAIN[e.op.split('.')[1]] ?? e.op}
                      {e.target ? ` · ${e.target}` : ''}
                      {VIA[e.via] ? ` · ${VIA[e.via]}` : ''}
                    </span>
                  </span>
                  <StatusChip size="sm" tone={r.tone}>
                    {r.label}
                  </StatusChip>
                </div>
              )
            })}
          </Group>
        )}
        {entries && next > 0 && (
          <Button size="sm" variant="ghost" className="self-center" loading={more} onClick={() => void loadMore()}>
            載入更早的紀錄
          </Button>
        )}
      </Block>
    </>
  )
}
