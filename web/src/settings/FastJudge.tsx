import { useEffect, useMemo, useState } from 'react'
import { ChevronDown, Info } from 'lucide-react'
import {
  judgeSettingsGet,
  judgeSettingsSet,
  judgeStats,
  type JudgeSettingsGetOutput,
  type JudgeSettingsSetInput,
  type JudgeStatsOutput,
} from '../api/generated'
import { Button } from '../ui/Button'
import { SegmentedControl, Switch } from '../ui/Controls'
import { TextInput } from '../ui/Field'
import { Icon } from '../ui/Icon'
import { Select } from '../ui/Menu'
import { StatusChip } from '../ui/Pill'
import { useToast } from '../ui/toast-context'
import { cx } from '../ui/cx'
import { Block, Group, Note, Row } from './parts'
import { errText } from './format'
import type { Provider } from './modelData'

// 快判模型（J10，PRD §20）：一個開關＋一個模型；細節（第二層、本機小模型、上限、各用途）收在「進階」。
// 關掉＝全部照規則。設定的供應商不存在時（例：選了 Zen 的 Jev 但沒有 Zen 金鑰）提醒，並一鍵改用你的預設模型。

type Mode = 'off' | 'shadow' | 'live'
type Tier = { provider: string; model: string }
type UseRow = JudgeSettingsGetOutput['uses'][number]

const MODE_OPTIONS: { value: Mode; label: string }[] = [
  { value: 'off', label: '關' },
  { value: 'shadow', label: '影子' },
  { value: 'live', label: '生效' },
]

// 建議的模型（範本的清單裡沒有 Jev）：第一層 Jev 付費版（免費版只給 OpenCode 自己用）；第二層 Go 的 flash。
const TIER1_SUGGEST: Tier[] = [{ provider: 'opencode-zen', model: 'jev-1.13' }]
const TIER2_SUGGEST: Tier[] = [
  { provider: 'opencode-go', model: 'deepseek-v4-flash' },
  { provider: 'opencode-go', model: 'glm-5.3-flash' },
]

const ref = (t: Tier) => (t.provider ? `${t.provider}/${t.model}` : '')
const split = (v: string): Tier => {
  const i = v.indexOf('/')
  return i < 0 ? { provider: '', model: '' } : { provider: v.slice(0, i), model: v.slice(i + 1) }
}

function tierOptions(providers: Provider[], suggest: Tier[], current: Tier, none: string, filter?: (m: string) => boolean) {
  const seen = new Set<string>()
  const out: { value: string; label: string }[] = [{ value: '', label: none }]
  const add = (t: Tier, note?: string) => {
    const v = ref(t)
    if (!v || seen.has(v)) return
    seen.add(v)
    out.push({ value: v, label: note ? `${v} · ${note}` : v })
  }
  const have = new Set(providers.map((p) => p.id))
  for (const s of suggest) add(s, have.has(s.provider) ? '建議' : '還沒設定這個供應商')
  add(current)
  for (const p of providers) for (const m of p.models ?? []) if (!filter || filter(m)) add({ provider: p.id, model: m })
  return out
}

function usd(micros: number): string {
  const v = micros / 1e6
  return v === 0 ? '$0' : v < 0.01 ? `$${v.toFixed(4)}` : `$${v.toFixed(2)}`
}

function pct(v: number): string {
  return v < 0 ? '—' : `${Math.round(v * 100)}%`
}

function tierTag(u: UseRow): string {
  return u.tier === 'tier2' ? '短文字' : '選擇題'
}

export function FastJudge({ providers, defaultModel }: { providers: Provider[]; defaultModel?: Tier | null }) {
  const toast = useToast()
  const [data, setData] = useState<JudgeSettingsGetOutput | null>(null)
  const [stats, setStats] = useState<JudgeStatsOutput | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [cap, setCap] = useState('')
  const [more, setMore] = useState(false)

  useEffect(() => {
    Promise.all([judgeSettingsGet({}), judgeStats({})])
      .then(([d, st]) => {
        setData(d)
        setStats(st)
        setCap(String(d.settings.daily_cap))
      })
      .catch((e: unknown) => setError(errText(e)))
  }, [])

  const save = async (patch: JudgeSettingsSetInput, done?: string) => {
    try {
      const d = await judgeSettingsSet(patch)
      setData(d)
      setCap(String(d.settings.daily_cap))
      if (done) toast.show({ kind: 'done', text: done })
    } catch (e) {
      toast.show({ kind: 'error', text: errText(e) })
    }
  }

  const byUse = useMemo(() => new Map((stats?.uses ?? []).map((u) => [u.use, u])), [stats])
  if (error) {
    return (
      <p role="alert" className="m-0 text-ui-sm text-danger">
        讀不到快速判斷設定：{error}
      </p>
    )
  }
  if (!data) return <p className="m-0 text-ui-sm text-ink-2">讀取中…</p>

  const s = data.settings
  const on = !!s.tier1.provider
  const today = data.today
  const have = new Set(providers.map((p) => p.id))
  // 設定的層用了不存在的供應商：快判其實一直在照規則。
  const missing = [s.tier1, s.tier2].filter((t) => t.provider && !have.has(t.provider))
  const fallback: Tier | null = defaultModel?.provider && have.has(defaultModel.provider) ? defaultModel : null
  const turnOn = () => {
    if (!fallback) {
      toast.show({ kind: 'error', text: '先在「方案」設定一個預設模型' })
      return
    }
    void save({ tier1: fallback, tier2: fallback }, '快速判斷已開啟')
  }
  const turnOff = () => void save({ tier1: { provider: '', model: '' }, tier2: { provider: '', model: '' } }, '快速判斷已關閉，全部照規則')
  const capNum = Number(cap)
  const capValid = Number.isInteger(capNum) && capNum >= 1 && capNum <= 100000

  return (
    <>
      {missing.length > 0 && (
        <Note
          tone="warning"
          icon={<Icon icon={Info} />}
          action={
            <span className="flex gap-2">
              {fallback && (
                <Button size="sm" variant="secondary" onClick={() => void save({ tier1: fallback, tier2: fallback }, '快速判斷改用 ' + ref(fallback))}>
                  改用 {fallback.model}
                </Button>
              )}
              <Button size="sm" variant="ghost" onClick={turnOff}>
                關掉
              </Button>
            </span>
          }
        >
          {missing[0].provider} 還沒設定，快速判斷現在只照規則。
        </Note>
      )}
      {data.problems.map((p) => (
        <Note
          key={p.provider + p.kind}
          tone="warning"
          icon={<Icon icon={Info} />}
          action={
            fallback && p.provider === s.tier1.provider && fallback.provider !== p.provider ? (
              <Button size="sm" variant="secondary" onClick={() => void save({ tier1: fallback }, '快速判斷改用 ' + ref(fallback))}>
                改用 {fallback.model}
              </Button>
            ) : undefined
          }
        >
          {p.message}；先照規則。
        </Note>
      ))}
      {today.capped && (
        <Note tone="warning" icon={<Icon icon={Info} />}>
          今天到上限了，先照規則，明天恢復。
        </Note>
      )}

      <Group label="快速判斷">
        <Row title="快速判斷" sub="用小模型做簡單判斷（做完了沒、要不要問你）。只給建議，不會自己花錢或刪東西。">
          <Switch label="快速判斷" checked={on} onChange={(v) => (v ? turnOn() : turnOff())} />
        </Row>
        {on && (
          <Row title="模型" sub={`今天 ${today.calls}／${today.daily_cap} 次 · ${usd(today.cost_micros)}`} wrap>
            <Select
              label="快速判斷模型"
              hideLabel
              look="compact"
              className="min-w-[240px]"
              value={ref(s.tier1)}
              options={tierOptions(providers, TIER1_SUGGEST.filter((t) => have.has(t.provider)), s.tier1, '關掉（只用規則）', (m) => !m.endsWith('-free'))}
              onChange={(v) => void save({ tier1: split(v) }, v ? '快速判斷模型已更新' : '已改成只用規則')}
            />
          </Row>
        )}
      </Group>

      {on && (
        <button type="button" aria-expanded={more} onClick={() => setMore((m) => !m)} className="flex items-center gap-1 self-start bg-transparent p-0 text-meta text-accent-ink">
          進階
          <Icon icon={ChevronDown} size={14} className={cx('transition-transform', more && 'rotate-180')} />
        </button>
      )}
      {on && more && (
        <>
          <Group label="快速判斷進階">
            <Row title="短文字模型" sub="摘要、命名用" wrap>
              <Select
                label="第二層模型"
                hideLabel
                look="compact"
                className="min-w-[240px]"
                value={ref(s.tier2)}
                options={tierOptions(providers, TIER2_SUGGEST.filter((t) => have.has(t.provider)), s.tier2, '不用')}
                onChange={(v) => void save({ tier2: split(v) }, '短文字模型已更新')}
              />
            </Row>
            <Row title="本機小模型" sub="離線時用" wrap>
              <Select
                label="本機小模型"
                hideLabel
                look="compact"
                className="min-w-[240px]"
                value={ref(s.local)}
                options={tierOptions(providers, [], s.local, '不用')}
                onChange={(v) => void save({ local: split(v) }, '本機小模型已更新')}
              />
            </Row>
            <Row title="每日上限" wrap>
              <span className="flex items-center gap-2">
                <TextInput
                  label="每日呼叫上限"
                  hideLabel
                  inputMode="numeric"
                  wrapperClassName="w-24"
                  value={cap}
                  onChange={(e) => setCap(e.target.value.replace(/[^0-9]/g, ''))}
                />
                <Button size="sm" variant="secondary" disabled={!capValid || capNum === s.daily_cap} onClick={() => void save({ daily_cap: capNum }, '每日上限已更新')}>
                  儲存
                </Button>
              </span>
            </Row>
          </Group>

          <Block title="用途" sub="影子＝只記錄不採用；和你一致夠高再改成生效">
            <Group label="快速判斷用途">
              {data.uses.map((u) => {
                const st = byUse.get(u.id)
                const acc = st && st.corrected > 0 ? `和你一致 ${pct(st.corrected_agreement)}` : st && st.answered > 0 ? `和規則一致 ${pct(st.agreement)}` : ''
                return (
                  <div key={u.id} className="flex items-center gap-3 border-line px-4 py-3 not-first:border-t max-sm:flex-col max-sm:items-stretch" data-testid={`judge-use-${u.id}`}>
                    <span className="min-w-0 flex-1">
                      <span className="flex flex-wrap items-center gap-1.5 text-ui leading-[1.45]">
                        {u.name}
                        <span className={cx('rounded-full px-1.5 text-[11px] leading-[18px]', u.tier === 'tier2' ? 'bg-warning-soft text-warning-ink' : 'bg-accent-soft text-accent-ink')}>{tierTag(u)}</span>
                        {!u.wired && <span className="text-[11px] text-ink-3">尚未接上</span>}
                        {st?.ready_for_live && u.mode === 'shadow' && <StatusChip tone="success">可以生效</StatusChip>}
                      </span>
                      {acc && <span className="mt-0.5 block text-[13px] leading-normal text-ink-2">{acc}</span>}
                    </span>
                    <SegmentedControl
                      label={`${u.name}的開關`}
                      className="w-[168px] flex-none max-sm:w-full"
                      options={MODE_OPTIONS}
                      value={u.mode as Mode}
                      onChange={(m) => void save({ modes: { [u.id]: m } })}
                    />
                  </div>
                )
              })}
            </Group>
          </Block>
        </>
      )}
    </>
  )
}
