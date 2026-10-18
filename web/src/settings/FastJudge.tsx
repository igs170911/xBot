import { useEffect, useMemo, useState } from 'react'
import { ArrowRight, Info, ShieldCheck } from 'lucide-react'
import {
  judgeSettingsGet,
  judgeSettingsSet,
  judgeStats,
  type JudgeSettingsGetOutput,
  type JudgeSettingsSetInput,
  type JudgeStatsOutput,
} from '../api/generated'
import { Button } from '../ui/Button'
import { SegmentedControl } from '../ui/Controls'
import { TextInput } from '../ui/Field'
import { Icon } from '../ui/Icon'
import { Select } from '../ui/Menu'
import { StatusChip } from '../ui/Pill'
import { useToast } from '../ui/toast-context'
import { cx } from '../ui/cx'
import { Block, Group, Note, Row } from './parts'
import { errText } from './format'
import type { Provider } from './modelData'

// 快判模型（J10，PRD §20）：第一層選擇題（OpenCode Zen 的 Jev）、第二層短文字（OpenCode Go flash）、本機小模型 → 規則；
// 每個用途 關／影子／生效；每日呼叫上限；今天的用量與花費；影子模式和規則、和你的改正一致多少。

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
  if (u.tier === 'tier2') return 'Go'
  return 'Jev'
}

export function FastJudge({ providers }: { providers: Provider[] }) {
  const toast = useToast()
  const [data, setData] = useState<JudgeSettingsGetOutput | null>(null)
  const [stats, setStats] = useState<JudgeStatsOutput | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [cap, setCap] = useState('')

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
        讀不到快判設定：{error}
      </p>
    )
  }
  if (!data) return <p className="m-0 text-ui-sm text-ink-2">讀取中…</p>

  const s = data.settings
  const t1On = !!s.tier1.provider
  const zen = providers.find((p) => p.id === 'opencode-zen')
  const go = providers.find((p) => p.id === 'opencode-go')
  const today = data.today
  // Jev 用不了時（例：Zen 沒儲值），選擇題改用第二層的 Go flash（Go 訂閱額度）。
  const goFlash: Tier = s.tier2.provider ? s.tier2 : TIER2_SUGGEST[0]
  const capNum = Number(cap)
  const capValid = Number.isInteger(capNum) && capNum >= 1 && capNum <= 100000

  return (
    <>
      <Note icon={<Icon icon={ShieldCheck} />}>快判先用 OpenCode 的 Jev 做選擇題，只判斷、給建議；永遠不能自己刪除、花錢、對外發送或核准。</Note>
      {data.problems.map((p) => (
        <Note
          key={p.provider + p.kind}
          tone="warning"
          icon={<Icon icon={Info} />}
          action={
            p.provider === s.tier1.provider && goFlash.provider ? (
              <Button size="sm" variant="secondary" onClick={() => void save({ tier1: goFlash }, '選擇題改用 ' + ref(goFlash))}>
                改用 Go flash 做選擇題
              </Button>
            ) : undefined
          }
        >
          {p.message}；這段時間快判先照規則。
        </Note>
      ))}
      {today.capped && (
        <Note tone="warning" icon={<Icon icon={Info} />}>
          今天已經到每日上限（{today.daily_cap} 次），全部先照規則；明天自動恢復。
        </Note>
      )}

      <Block
        title="用 OpenCode"
        aside={
          <span className="flex flex-wrap gap-1.5">
            <StatusChip tone={zen?.api_key_set ? 'success' : 'neutral'}>{zen?.api_key_set ? 'Zen 已設定' : 'Zen 未設定'}</StatusChip>
            <StatusChip tone={go?.api_key_set ? 'success' : 'neutral'}>{go?.api_key_set ? 'Go 已設定' : 'Go 未設定'}</StatusChip>
          </span>
        }
      >
        <Group label="快判模型">
          <Row
            title="今天"
            sub={`快判 ${today.calls}／${today.daily_cap} 次 · 第二層 ${today.tier2_calls}／${today.tier2_cap} 次 · 花費 ${usd(today.cost_micros)}`}
          />
          <Row title="第一層 · 選擇題" sub={`所有「從幾個選項挑一個」的判斷；回傳機率，低於 ${data.min_p} 就照規則`} wrap>
            <Select
              label="第一層模型"
              hideLabel
              look="compact"
              className="min-w-[240px]"
              value={ref(s.tier1)}
              options={tierOptions(providers, TIER1_SUGGEST, s.tier1, '不用快判（只用規則）', (m) => !m.endsWith('-free'))}
              onChange={(v) => void save({ tier1: split(v) }, v ? '第一層已更新' : '已改成只用規則')}
            />
          </Row>
          <Row title="第二層 · 短文字" sub={`只用在摘要、命名、建 Bot 建議；每日上限 ${s.tier2_cap} 次，到了往下一層退`} wrap>
            <Select
              label="第二層模型"
              hideLabel
              look="compact"
              className="min-w-[240px]"
              value={ref(s.tier2)}
              options={tierOptions(providers, TIER2_SUGGEST, s.tier2, '不用', (m) => /flash|mini|nano|lite/.test(m))}
              onChange={(v) => void save({ tier2: split(v) }, '第二層已更新')}
            />
          </Row>
          <Row title="本機小模型" sub="LM Studio／Ollama 等（先在「主要模型」加一個供應商）；完全離線時用" wrap>
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
          <Row title="退路順序" sub="逾時、出錯、額度用完都往下一個">
            <span className="flex flex-wrap items-center gap-1 text-meta text-ink-2" aria-label="退路順序">
              {['OpenCode Jev', 'OpenCode Go flash', '本機小模型', '規則'].map((x, i) => (
                <span key={x} className="flex items-center gap-1">
                  {i > 0 && <Icon icon={ArrowRight} size={12} />}
                  <span className="rounded-full border border-line px-2 py-0.5">{x}</span>
                </span>
              ))}
            </span>
          </Row>
          <Row title="每日呼叫上限" sub="到了全部退回規則並通知一次；預設 300 次" wrap>
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
      </Block>

      <Block title="用途" sub="11–15 是工作開始前的「先判斷」；先用影子模式試，和規則、和你的改正一致 ≥ 95% 再生效">
        <Group label="快判用途">
          {data.uses.map((u) => {
            const st = byUse.get(u.id)
            const onlyRules = u.tier === 'tier1' && !t1On
            const acc = st && st.corrected > 0 ? `和你一致 ${pct(st.corrected_agreement)}` : st && st.answered > 0 ? `和規則一致 ${pct(st.agreement)}` : '還沒有資料'
            return (
              <div key={u.id} className="flex items-center gap-3 border-line px-4 py-3 not-first:border-t max-sm:flex-col max-sm:items-stretch" data-testid={`judge-use-${u.id}`}>
                <span className="w-6 flex-none text-meta text-ink-3">{u.n}</span>
                <span className="min-w-0 flex-1">
                  <span className="flex flex-wrap items-center gap-1.5 text-ui leading-[1.45]">
                    {u.name}
                    <span className={cx('rounded-full px-1.5 text-[11px] leading-[18px]', u.tier === 'tier2' ? 'bg-warning-soft text-warning-ink' : 'bg-accent-soft text-accent-ink')}>{tierTag(u)}</span>
                    {!u.wired && <span className="text-[11px] text-ink-3">尚未接上</span>}
                    {st?.ready_for_live && u.mode === 'shadow' && <StatusChip tone="success">可以生效</StatusChip>}
                  </span>
                  <span className="mt-0.5 block text-[13px] leading-normal text-ink-2">
                    {u.desc} · 等 {(u.wait_ms / 1000).toFixed(1)} 秒 · {acc}
                    {st && st.calls > 0 ? ` · ${st.calls} 次` : ''}
                  </span>
                </span>
                {onlyRules ? (
                  <span className="text-meta text-ink-3">只用規則</span>
                ) : (
                  <SegmentedControl
                    label={`${u.name}的開關`}
                    className="w-[168px] flex-none max-sm:w-full"
                    options={MODE_OPTIONS}
                    value={u.mode as Mode}
                    onChange={(m) => void save({ modes: { [u.id]: m } })}
                  />
                )}
              </div>
            )
          })}
        </Group>
      </Block>
    </>
  )
}
