import { useCallback, useEffect, useState } from 'react'
import { Download, TriangleAlert } from 'lucide-react'
import {
  modelsPricing,
  modelsSetPrice,
  usageCaps,
  usageExport,
  usageSetCaps,
  usageSummary,
  type ModelsPricingOutput,
  type UsageCapsOutput,
  type UsageSetCapsInput,
  type UsageSummaryOutput,
} from '../api/generated'
import { botsStore } from '../data/bots'
import { useStore } from '../data/store'
import { Button } from '../ui/Button'
import { SegmentedControl } from '../ui/Controls'
import { ConfirmDialog, Dialog } from '../ui/Dialog'
import { TextInput } from '../ui/Field'
import { Icon } from '../ui/Icon'
import { StatusChip } from '../ui/Pill'
import { useToast } from '../ui/toast-context'
import { DailyColumns, DataTable, HBars, Meter } from './charts'
import { Block, Group, Note, Row, SectionHeader } from './parts'
import { errText, fieldError, isPending, pct, tokens, usd } from './format'
import { providerName, useModelData } from './modelData'

// 用量與花費（J04、US-1～US-3）：今天／本月花費、每天花費、各 Bot／各模型、快取命中率；
// 花費上限（全部 Bot 每月、每個供應商、每個 Bot）；CSV 匯出；沒有價格的模型。金額一律美元（Model Proxy 用美元記帳）。

type Summary = UsageSummaryOutput
type Caps = UsageCapsOutput
type Cap = Caps['bots'][number]
type Range = '7' | '30' | '90'

export function UsageSection() {
  const toast = useToast()
  const [range, setRange] = useState<Range>('30')
  const [sum, setSum] = useState<Summary | null>(null)
  const [caps, setCaps] = useState<Caps | null>(null)
  const [pricing, setPricing] = useState<ModelsPricingOutput | null>(null)
  const [err, setErr] = useState<string | null>(null)
  const [exporting, setExporting] = useState(false)

  const load = useCallback(async () => {
    try {
      const [s, c, p] = await Promise.all([usageSummary({ days: Number(range) }), usageCaps({}), modelsPricing({})])
      setSum(s)
      setCaps(c)
      setPricing(p)
      setErr(null)
    } catch (e) {
      setErr(errText(e))
    }
  }, [range])
  useEffect(() => {
    let live = true
    Promise.all([usageSummary({ days: Number(range) }), usageCaps({}), modelsPricing({})])
      .then(([s, c, p]) => {
        if (!live) return
        setSum(s)
        setCaps(c)
        setPricing(p)
        setErr(null)
      })
      .catch((e: unknown) => live && setErr(errText(e)))
    return () => {
      live = false
    }
  }, [range])

  const download = async () => {
    setExporting(true)
    try {
      const out = await usageExport({ days: Number(range) })
      const blob = new Blob(['﻿' + out.csv], { type: 'text/csv;charset=utf-8' })
      const url = URL.createObjectURL(blob)
      const a = document.createElement('a')
      a.href = url
      a.download = out.filename || 'xbot-usage.csv'
      document.body.appendChild(a)
      a.click()
      a.remove()
      setTimeout(() => URL.revokeObjectURL(url), 1000)
      toast.show({ kind: 'done', text: out.truncated ? `已匯出前 ${out.rows} 筆（太多了，只匯出前面的）` : `已匯出 ${out.rows} 筆` })
    } catch (e) {
      toast.show({ kind: 'error', text: errText(e) })
    } finally {
      setExporting(false)
    }
  }

  const g = caps?.global
  const monthCap = g?.monthly_usd ?? 0
  const t = sum?.total

  return (
    <>
      <SectionHeader
        title="用量與花費"
        sub="每一次模型請求都算，包含 Bot 之間的訊息。用你訂閱的引擎只算額度，不在這裡計費。"
        aside={
          <Button variant="outline" icon={Download} loading={exporting} onClick={download}>
            匯出 CSV
          </Button>
        }
      />
      {err && (
        <p role="alert" className="m-0 text-ui-sm text-danger">
          讀不到用量：{err}
        </p>
      )}
      <div className="grid grid-cols-3 gap-3 max-sm:grid-cols-1">
        <Tile label="今天" value={usd(g?.spent_today_usd ?? 0)} sub={caps ? `${caps.day}` : ''} />
        <Tile
          label="本月"
          value={usd(g?.spent_month_usd ?? 0)}
          sub={monthCap > 0 ? `上限 ${usd(monthCap)} 的 ${pct((g?.spent_month_usd ?? 0) / monthCap)}` : '沒有設上限'}
          meter={monthCap > 0 ? (g?.spent_month_usd ?? 0) / monthCap : undefined}
        />
        <Tile label={`快取命中 · 最近 ${range} 天`} value={t ? pct(t.cache_hit_rate) : '—'} sub={t ? `${t.calls.toLocaleString('en-US')} 次請求 · 輸入 ${tokens(t.input_tokens)} token` : ''} />
      </div>

      {pricing && pricing.unpriced.length > 0 && <Unpriced items={pricing.unpriced} onSaved={load} />}

      <Block
        title="每天花費"
        aside={
          <SegmentedControl
            label="期間"
            value={range}
            onChange={setRange}
            options={[
              { value: '7', label: '7 天' },
              { value: '30', label: '30 天' },
              { value: '90', label: '90 天' },
            ]}
            className="w-[210px]"
          />
        }
      >
        <div className="flex flex-col gap-3 rounded-group bg-card px-4 pb-3 pt-6">
          {sum && sum.by_day.length > 0 ? (
            <>
              <DailyColumns
                label={`最近 ${range} 天每天的花費`}
                format={(v) => usd(v)}
                points={sum.by_day.map((d) => ({ key: d.key, value: d.cost_usd, detail: `${d.calls} 次 · 快取 ${pct(d.cache_hit_rate)}` }))}
              />
              <DataTable
                caption="每天花費"
                head={['日期', '花費', '請求', '輸入 token', '輸出 token', '快取命中']}
                rows={sum.by_day.map((d) => [d.key, usd(d.cost_usd), String(d.calls), tokens(d.input_tokens), tokens(d.output_tokens), pct(d.cache_hit_rate)])}
              />
            </>
          ) : (
            <p className="m-0 pb-3 text-ui-sm text-ink-2">{sum ? '這段期間還沒有用量。' : '讀取中…'}</p>
          )}
        </div>
      </Block>

      {sum && (sum.by_bot.length > 0 || sum.by_model.length > 0) && (
        <div className="grid grid-cols-2 gap-5 max-md:grid-cols-1">
          <Block title="各 Bot">
            <div className="flex flex-col gap-3 rounded-group bg-card px-4 py-3.5">
              <HBars label="各 Bot 的花費" rows={sum.by_bot.map((b) => ({ key: b.key, label: b.label || b.key, value: b.cost_usd, valueText: usd(b.cost_usd), sub: `${b.calls} 次 · 快取 ${pct(b.cache_hit_rate)}` }))} />
              <DataTable caption="各 Bot" head={['Bot', '花費', '請求', '快取命中']} rows={sum.by_bot.map((b) => [b.label || b.key, usd(b.cost_usd), String(b.calls), pct(b.cache_hit_rate)])} />
            </div>
          </Block>
          <Block title="各模型">
            <div className="flex flex-col gap-3 rounded-group bg-card px-4 py-3.5">
              <HBars label="各模型的花費" rows={sum.by_model.map((m) => ({ key: m.key, label: m.key.slice(m.key.indexOf('/') + 1), value: m.cost_usd, valueText: usd(m.cost_usd), sub: `${m.key.split('/')[0]} · 快取 ${pct(m.cache_hit_rate)}` }))} />
              <DataTable caption="各模型" head={['模型', '花費', '請求', '快取命中', '改用備援']} rows={sum.by_model.map((m) => [m.key, usd(m.cost_usd), String(m.calls), pct(m.cache_hit_rate), String(m.fallback_calls)])} />
            </div>
          </Block>
        </div>
      )}

      {caps && <CapsEditor caps={caps} onSaved={load} />}
    </>
  )
}

function Tile({ label, value, sub, meter }: { label: string; value: string; sub?: string; meter?: number }) {
  return (
    <div className="flex flex-col gap-1 rounded-group bg-card px-4 py-3">
      <span className="text-[13px] text-ink-2">{label}</span>
      <span className="text-[22px] leading-[1.3]">{value}</span>
      {meter !== undefined && <Meter ratio={meter} label={`${label}用了上限的 ${pct(meter)}`} />}
      {sub && <span className="text-[12.5px] text-ink-2">{sub}</span>}
    </div>
  )
}

// ── 沒有價格的模型 ───────────────────────────────────

function Unpriced({ items, onSaved }: { items: ModelsPricingOutput['unpriced']; onSaved: () => Promise<void> }) {
  const [edit, setEdit] = useState<string | null>(null)
  return (
    <Note tone="warning" icon={<Icon icon={TriangleAlert} />}>
      <span className="block">這些模型沒有價格，花費記成 0，花費上限也管不到它們：</span>
      <span className="mt-1.5 flex flex-wrap gap-2">
        {items.map((u) => {
          const key = `${u.provider}/${u.model}`
          return (
            <button key={key} type="button" onClick={() => setEdit(key)} className="inline-flex h-7 items-center gap-1.5 rounded-full border border-warning/50 bg-transparent px-2.5 text-meta text-ink hover:bg-warning-soft">
              {key} · {u.calls} 次 · <span className="text-accent-ink">設定價格</span>
            </button>
          )
        })}
      </span>
      {edit && <PriceDialog model={edit} onClose={() => setEdit(null)} onSaved={onSaved} />}
    </Note>
  )
}

function PriceDialog({ model, onClose, onSaved }: { model: string; onClose: () => void; onSaved: () => Promise<void> }) {
  const toast = useToast()
  const [input, setInput] = useState('')
  const [output, setOutput] = useState('')
  const [cacheRead, setCacheRead] = useState('')
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<unknown>(null)
  const num = (s: string) => (s.trim() === '' ? undefined : Number(s))
  const valid = [input, output, cacheRead].every((s) => s.trim() === '' || (Number.isFinite(Number(s)) && Number(s) >= 0)) && input.trim() !== '' && output.trim() !== ''
  const save = async () => {
    setBusy(true)
    setErr(null)
    try {
      await modelsSetPrice({ model, input: num(input), output: num(output), cache_read: num(cacheRead) })
      await onSaved()
      toast.show({ kind: 'done', text: `已設定 ${model} 的價格` })
      onClose()
    } catch (e) {
      if (isPending(e)) {
        toast.show({ kind: 'info', text: errText(e) })
        onClose()
      } else setErr(e)
    } finally {
      setBusy(false)
    }
  }
  return (
    <Dialog
      open
      onClose={onClose}
      busy={busy}
      title={`設定 ${model} 的價格`}
      description="每百萬 token 的美元。價格會影響花費上限的計算，之後的用量才照新價格算。"
      actions={
        <>
          <Button onClick={onClose} disabled={busy}>
            取消
          </Button>
          <Button variant="primary" loading={busy} disabled={!valid} onClick={save}>
            儲存
          </Button>
        </>
      }
    >
      <div className="grid grid-cols-3 gap-3 max-sm:grid-cols-1">
        <TextInput label="輸入（US$）" inputMode="decimal" value={input} onChange={(e) => setInput(e.target.value)} placeholder="例：3" error={fieldError(err, 'input')} />
        <TextInput label="輸出（US$）" inputMode="decimal" value={output} onChange={(e) => setOutput(e.target.value)} placeholder="例：15" error={fieldError(err, 'output')} />
        <TextInput label="快取命中（選填）" inputMode="decimal" value={cacheRead} onChange={(e) => setCacheRead(e.target.value)} placeholder="照輸入價" />
      </div>
      {err !== null && !fieldError(err, 'input') && !fieldError(err, 'output') && (
        <p role="alert" className="m-0 text-meta text-danger">
          {errText(err)}
        </p>
      )}
    </Dialog>
  )
}

// ── 花費上限 ─────────────────────────────────────────

type Target = { kind: 'global' } | { kind: 'provider'; id: string } | { kind: 'bot'; id: string }
type Pending = { target: Target; name: string; field: 'daily_usd' | 'monthly_usd'; from: number; to: number }

function CapsEditor({ caps, onSaved }: { caps: Caps; onSaved: () => Promise<void> }) {
  const toast = useToast()
  const bots = useStore(botsStore).bots ?? []
  const { models, presets } = useModelData()
  const [confirm, setConfirm] = useState<Pending | null>(null)
  const [busy, setBusy] = useState(false)
  const [showAll, setShowAll] = useState(false)
  // 取消確認時把欄位還原（重新掛上）
  const [ver, setVer] = useState(0)

  const apply = async (p: Pending) => {
    const input: UsageSetCapsInput = { [p.field]: p.to }
    if (p.target.kind === 'provider') input.provider = p.target.id
    if (p.target.kind === 'bot') input.bot_id = p.target.id
    setBusy(true)
    try {
      await usageSetCaps(input)
      await onSaved()
      toast.show({ kind: 'done', text: `${p.name}的${p.field === 'daily_usd' ? '每日' : '每月'}上限改成 ${p.to ? usd(p.to) : '不限'}` })
    } catch (e) {
      toast.show({ kind: isPending(e) ? 'info' : 'error', text: errText(e) })
    } finally {
      setBusy(false)
      setConfirm(null)
    }
  }

  // 降低直接生效；提高或取消（0＝不限）要再確認一次（US-2）。
  const change = (p: Pending) => {
    if (p.to === p.from) return
    const loosen = p.to === 0 || (p.from !== 0 && p.to > p.from)
    if (loosen) setConfirm(p)
    else void apply(p)
  }

  const providerCaps = (models?.providers ?? []).map((pv) => caps.providers.find((c) => c.key === pv.id) ?? emptyCap(pv.id, providerName(pv, presets)))
  for (const c of caps.providers) if (!providerCaps.some((x) => x.key === c.key)) providerCaps.push(c)
  const botCaps = bots
    .filter((b) => !b.archived)
    .map((b) => ({ ...(caps.bots.find((c) => c.key === b.id) ?? emptyCap(b.id, b.name)), label: b.name }))
    .sort((a, b) => b.spent_month_usd - a.spent_month_usd)
  const visibleBots = showAll ? botCaps : botCaps.slice(0, 4)

  return (
    <div key={ver} className="contents">
      <Block title="花費上限" sub="到上限時，模型請求會被擋下、正在做的工作停在原地，並在「待我處理」問你要不要追加。0 或空白表示不限。">
        <Group label="全部 Bot">
          <Row title="全部 Bot · 每月" sub={`本月已花 ${usd(caps.global?.spent_month_usd ?? 0)} · 預設 $20，可以改`} wrap>
            <CapInput
              label="全部 Bot 每月上限"
              value={caps.global?.monthly_usd ?? 0}
              onCommit={(to) => change({ target: { kind: 'global' }, name: '全部 Bot ', field: 'monthly_usd', from: caps.global?.monthly_usd ?? 0, to })}
            />
          </Row>
        </Group>
      </Block>
      <Block title="每個供應商" sub="所有 Bot 合計；用來省著用某把金鑰的額度（例：OpenCode Go 每日上限）。">
        {providerCaps.length === 0 ? (
          <p className="m-0 rounded-group bg-card px-4 py-3 text-ui-sm text-ink-2">還沒有模型供應商。</p>
        ) : (
          <Group label="每個供應商的上限">
            {providerCaps.map((c) => (
              <CapRow key={c.key} cap={c} name={c.label || c.key} onChange={(field, from, to) => change({ target: { kind: 'provider', id: c.key }, name: (c.label || c.key) + ' ', field, from, to })} />
            ))}
          </Group>
        )}
      </Block>
      <Block title="每個 Bot">
        {botCaps.length === 0 ? (
          <p className="m-0 rounded-group bg-card px-4 py-3 text-ui-sm text-ink-2">還沒有 Bot。</p>
        ) : (
          <Group label="每個 Bot 的上限">
            {visibleBots.map((c) => (
              <CapRow key={c.key} cap={c} name={c.label || c.key} onChange={(field, from, to) => change({ target: { kind: 'bot', id: c.key }, name: c.label || c.key, field, from, to })} />
            ))}
            {botCaps.length > 4 && (
              <button type="button" onClick={() => setShowAll((s) => !s)} aria-expanded={showAll} className="h-11 border-t border-line bg-transparent text-ui-sm text-ink-2 hover:text-ink">
                {showAll ? '收起' : `顯示全部 ${botCaps.length} 個 Bot`}
              </button>
            )}
          </Group>
        )}
      </Block>
      <ConfirmDialog
        open={!!confirm}
        busy={busy}
        title={confirm?.to === 0 ? '取消這個上限？' : '提高上限？'}
        description={
          confirm
            ? `${confirm.name}的${confirm.field === 'daily_usd' ? '每日' : '每月'}上限從 ${confirm.from ? usd(confirm.from) : '不限'} 改成 ${confirm.to ? usd(confirm.to) : '不限'}。放寬上限只有你本人能決定，不能交給別人代為核准。`
            : ''
        }
        confirmText={confirm?.to === 0 ? '取消上限' : '提高'}
        onConfirm={() => confirm && void apply(confirm)}
        onCancel={() => {
          setConfirm(null)
          setVer((v) => v + 1)
        }}
      />
    </div>
  )
}

function emptyCap(key: string, label: string): Cap {
  return { key, label, daily_usd: 0, monthly_usd: 0, spent_today_usd: 0, spent_month_usd: 0, exceeded: false, daily_micros: 0, monthly_micros: 0, spent_day_micros: 0, spent_month_micros: 0 }
}

function CapRow({ cap, name, onChange }: { cap: Cap; name: string; onChange: (field: 'daily_usd' | 'monthly_usd', from: number, to: number) => void }) {
  const ratio = Math.max(cap.daily_usd ? cap.spent_today_usd / cap.daily_usd : 0, cap.monthly_usd ? cap.spent_month_usd / cap.monthly_usd : 0)
  return (
    <div className="flex items-center gap-4 border-line px-4 py-3 not-first:border-t max-sm:flex-col max-sm:items-stretch max-sm:gap-2.5">
      <span className="min-w-0 flex-1">
        <span className="flex items-center gap-2">
          <span className="truncate text-ui">{name}</span>
          {cap.exceeded ? (
            <StatusChip tone="danger" size="sm">已達上限</StatusChip>
          ) : ratio >= 0.8 ? (
            <StatusChip tone="warning" size="sm">接近上限</StatusChip>
          ) : null}
        </span>
        <span className="block text-[13px] text-ink-2">
          今天 {usd(cap.spent_today_usd)} · 本月 {usd(cap.spent_month_usd)}
        </span>
        {ratio > 0 && (
          <span className="mt-1.5 block max-w-[240px]">
            <Meter ratio={ratio} label={`${name}用了上限的 ${pct(ratio)}`} />
          </span>
        )}
      </span>
      <span className="flex gap-2">
        <CapInput label={`${name}每日上限`} prefix="每日" value={cap.daily_usd} onCommit={(to) => onChange('daily_usd', cap.daily_usd, to)} />
        <CapInput label={`${name}每月上限`} prefix="每月" value={cap.monthly_usd} onCommit={(to) => onChange('monthly_usd', cap.monthly_usd, to)} />
      </span>
    </div>
  )
}

/** 金額欄：按 Enter 或離開欄位才存。 */
function CapInput({ label, value, onCommit, prefix }: { label: string; value: number; onCommit: (v: number) => void; prefix?: string }) {
  const [text, setText] = useState(value ? String(value) : '')
  const [seen, setSeen] = useState(value)
  if (seen !== value) {
    setSeen(value)
    setText(value ? String(value) : '')
  }
  const bad = text.trim() !== '' && !(Number.isFinite(Number(text)) && Number(text) >= 0)
  const commit = () => {
    if (bad) return
    const v = text.trim() === '' ? 0 : Number(text)
    if (v !== value) onCommit(v)
  }
  return (
    <label className="flex h-10 w-[150px] items-center gap-1.5 rounded-field border border-line bg-inset px-3 text-ui focus-within:border-focus max-sm:h-11 max-sm:flex-1 has-aria-invalid:border-danger">
      <span className="whitespace-nowrap text-meta text-ink-3">{prefix ? `${prefix} US$` : 'US$'}</span>
      <input
        aria-label={label}
        aria-invalid={bad || undefined}
        inputMode="decimal"
        value={text}
        placeholder="不限"
        onChange={(e) => setText(e.target.value)}
        onBlur={commit}
        onKeyDown={(e) => {
          if (e.key !== 'Enter' || e.nativeEvent.isComposing) return
          // 不讓這個 Enter 接著按到確認對話框裡的按鈕
          e.preventDefault()
          ;(e.target as HTMLInputElement).blur()
        }}
        className="w-full min-w-0 border-0 bg-transparent text-ui text-ink outline-none"
      />
    </label>
  )
}

/** 一個 Bot 的花費上限（G08「權限與花費」）：每日、每月與用量條；降低直接生效，提高或取消要再確認（US-2）。 */
export function BotCaps({ botId, name }: { botId: string; name: string }) {
  const toast = useToast()
  const [cap, setCap] = useState<Cap | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [confirm, setConfirm] = useState<{ field: 'daily_usd' | 'monthly_usd'; from: number; to: number } | null>(null)
  const [busy, setBusy] = useState(false)
  const [ver, setVer] = useState(0)
  const load = useCallback(
    () =>
      usageCaps({ bot_id: botId })
        .then((c) => {
          setCap(c.bots.find((x) => x.key === botId) ?? emptyCap(botId, name))
          setError(null)
        })
        .catch((e: unknown) => setError(errText(e))),
    [botId, name],
  )
  useEffect(() => {
    void load()
  }, [load])

  const apply = async (field: 'daily_usd' | 'monthly_usd', to: number) => {
    setBusy(true)
    try {
      await usageSetCaps({ bot_id: botId, [field]: to })
      await load()
      toast.show({ kind: 'done', text: `${name}的${field === 'daily_usd' ? '每日' : '每月'}上限改成 ${to ? usd(to) : '不限'}` })
    } catch (e) {
      toast.show({ kind: isPending(e) ? 'info' : 'error', text: errText(e) })
    } finally {
      setBusy(false)
      setConfirm(null)
    }
  }
  const change = (field: 'daily_usd' | 'monthly_usd', from: number, to: number) => {
    if (to === from) return
    if (to === 0 || (from !== 0 && to > from)) setConfirm({ field, from, to })
    else void apply(field, to)
  }

  return (
    <Block title="花費上限" sub="用到 80% 提醒你，到上限就暫停並在「待我處理」問你。0 或空白表示不限。">
      {error && <p className="m-0 rounded-group bg-card px-4 py-3 text-ui-sm text-danger">讀不到花費上限：{error}</p>}
      {cap && (
        <Group label={`${name}的花費上限`}>
          <div key={ver} className="contents">
            <CapRow cap={cap} name={name} onChange={change} />
          </div>
        </Group>
      )}
      <ConfirmDialog
        open={!!confirm}
        busy={busy}
        title={confirm?.to === 0 ? '取消這個上限？' : '提高上限？'}
        description={confirm ? `${name}的${confirm.field === 'daily_usd' ? '每日' : '每月'}上限從 ${confirm.from ? usd(confirm.from) : '不限'} 改成 ${confirm.to ? usd(confirm.to) : '不限'}。放寬上限只有你本人能決定，不能交給別人代為核准。` : ''}
        confirmText={confirm?.to === 0 ? '取消上限' : '提高'}
        onConfirm={() => confirm && void apply(confirm.field, confirm.to)}
        onCancel={() => {
          setConfirm(null)
          setVer((v) => v + 1)
        }}
      />
    </Block>
  )
}
