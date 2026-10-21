import { useEffect, useRef, useState, type FormEvent } from 'react'
import { createPortal } from 'react-dom'
import { Check, ChevronRight, Settings } from 'lucide-react'
import { botsUpdate, enginesStatus, type EnginesStatusOutput, type ModelsGetOutput } from '../api/generated'
import { ENGINE_LABEL, PLAN_NAME } from '../settings/botModes'
import { upsertBot, type Bot } from '../data/bots'
import { navigate } from '../router'
import { Icon } from '../ui/Icon'
import { cx } from '../ui/cx'
import { useFocusTrap } from '../ui/useFocusTrap'
import { modelLabel, parseModel, usesPlan } from './format'

type Provider = ModelsGetOutput['providers'][number]
type EngineStatus = EnginesStatusOutput['bots'][number]

const EFFORTS = [
  { value: 'low', label: '低' },
  { value: 'medium', label: '中' },
  { value: 'high', label: '高' },
] as const

/**
 * 思考強度（bot.effort）：Hermes 和訂閱引擎（Grok、Claude Code、Codex…）都有，改了下一則就生效。
 * 2026-10-21 使用者：「每個模型的思考多久好像功能是壞掉的」——原本只有 Hermes 的模型選單有，而且要等電腦重開才生效。
 */
export function EffortPicker({ bot, onSaved, className }: { bot: { id: string; effort?: string }; onSaved: (b: Bot) => void; className?: string }) {
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')
  const save = async (effort: 'low' | 'medium' | 'high') => {
    setBusy(true)
    setErr('')
    try {
      onSaved(await botsUpdate({ id: bot.id, effort }))
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }
  return (
    <div className={cx('mt-3 border-t border-line pt-3', className)}>
      <div className="flex items-baseline justify-between">
        <span id={`effort-${bot.id}`} className="text-ui">
          思考強度
        </span>
        <span className="text-meta text-ink-3">越高越仔細，也越慢、越花錢；下一則就生效</span>
      </div>
      <div role="radiogroup" aria-labelledby={`effort-${bot.id}`} className="mt-2 flex gap-0.5 rounded-field bg-inset p-1">
        {EFFORTS.map((e) => {
          const on = (bot.effort || 'medium') === e.value
          return (
            <button
              key={e.value}
              type="button"
              role="radio"
              aria-checked={on}
              disabled={busy}
              onClick={() => !on && void save(e.value)}
              className={cx('h-8 flex-1 rounded-seg text-ui-sm max-sm:h-11', on ? 'bg-hover text-ink' : 'bg-transparent text-ink-2 hover:text-ink')}
            >
              {e.label}
            </button>
          )
        })}
      </div>
      {err && (
        <span role="alert" className="pt-1 text-sub text-danger">
          {err}
        </span>
      )}
    </div>
  )
}

function abbr(id: string): string {
  const parts = id.split(/[-_\s]+/).filter(Boolean)
  return (parts.length > 1 ? parts[0][0] + parts[1][0] : id.slice(0, 2)).toUpperCase()
}

function host(url: string): string {
  try {
    return new URL(url).host
  } catch {
    return url
  }
}

function Row({ label, tag, note, on, onClick, disabled }: { label: string; tag?: string; note?: string; on: boolean; onClick: () => void; disabled?: boolean }) {
  return (
    <button
      type="button"
      role="menuitemradio"
      aria-checked={on}
      disabled={disabled}
      onClick={onClick}
      className={cx('flex min-h-[38px] w-full items-center gap-2 rounded-seg px-2.5 text-left text-ui text-ink outline-none hover:bg-control focus-visible:bg-control disabled:opacity-50', on ? 'bg-control' : 'bg-transparent')}
    >
      <span className="min-w-0 truncate">{label}</span>
      {tag && <span className="flex h-5 items-center rounded-[6px] bg-inset px-1.5 text-[11.5px] text-ink-2">{tag}</span>}
      <span className="flex-1" />
      {note && <span className="text-meta text-ink-3">{note}</span>}
      {on && <Icon icon={Check} size={18} color="var(--color-accent)" />}
    </button>
  )
}

function Body({ bot, models, close }: { bot: Bot; models: ModelsGetOutput; close: () => void }) {
  const cur = parseModel(bot.model, models)
  const [pid, setPid] = useState(cur.provider ?? models.providers[0]?.id ?? '')
  const [other, setOther] = useState('')
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')
  const p: Provider | undefined = models.providers.find((x) => x.id === pid)
  const isDefaultProvider = models.default?.provider === pid

  const save = async (patch: { model?: string; effort?: 'low' | 'medium' | 'high' }) => {
    setBusy(true)
    setErr('')
    try {
      const out = await botsUpdate({ id: bot.id, ...patch })
      upsertBot(out)
      return true
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e))
      return false
    } finally {
      setBusy(false)
    }
  }

  const pick = async (model: string) => {
    if (await save({ model })) close()
  }
  const submitOther = async (e: FormEvent) => {
    e.preventDefault()
    const name = other.trim()
    if (!name) return
    if (await save({ model: `${pid}/${name}` })) close()
  }

  // 這個供應商目前選的模型（不是預設模型的那一列）
  const custom = cur.provider === pid && bot.model && !(isDefaultProvider && cur.model === models.default?.model) ? cur.model : ''

  return (
    <div role="dialog" aria-label="選擇模型" className="flex w-[540px] max-w-full flex-col max-sm:w-full overflow-hidden rounded-menu bg-card shadow-pop">
      <div className="flex min-h-[300px] max-sm:flex-col">
        <div role="tablist" aria-label="供應商" aria-orientation="vertical" className="flex flex-none flex-col gap-1.5 bg-panel/60 p-2.5 max-sm:flex-row max-sm:overflow-x-auto">
          {models.providers.map((x) => (
            <button
              key={x.id}
              type="button"
              role="tab"
              aria-selected={x.id === pid}
              aria-label={x.id}
              title={x.id}
              onClick={() => setPid(x.id)}
              className={cx('relative flex size-[42px] flex-none items-center justify-center rounded-field text-[13px] tracking-wide', x.id === pid ? 'bg-hover text-ink' : 'bg-transparent text-ink-2 hover:bg-control')}
            >
              {abbr(x.id)}
              {!x.api_key_set && <span className="absolute bottom-2 right-2 size-1.5 rounded-full bg-warning" aria-hidden="true" />}
            </button>
          ))}
        </div>
        <div role="tabpanel" aria-label={pid} className="flex min-w-0 flex-1 flex-col gap-1 px-4 pb-3 pt-3.5">
          {p && (
            <>
              <div className="flex items-start gap-2">
                <div className="flex min-w-0 flex-1 flex-col">
                  <span className="text-title">{p.id}</span>
                  <span className="truncate text-sub text-ink-2">{host(p.base_url)} · API 金鑰</span>
                </div>
                {p.api_key_set ? (
                  <span className="flex h-6 flex-none items-center rounded-full bg-success-soft px-2.5 text-[12px] text-success">金鑰已設定</span>
                ) : (
                  <button type="button" onClick={() => navigate('/settings/models')} className="flex h-7 flex-none items-center rounded-full bg-ink px-3 text-[12.5px] text-on-ink">
                    設定金鑰
                  </button>
                )}
              </div>
              <div role="menu" aria-label="模型" className="mt-2 flex max-h-[min(260px,40dvh)] flex-col gap-0.5 overflow-y-auto">
                {isDefaultProvider && models.default && <Row label={models.default.model} tag="預設" on={!bot.model} disabled={busy} onClick={() => void pick('')} />}
                {custom && <Row label={custom} on onClick={close} />}
                {(p.models ?? [])
                  .filter((m) => m !== custom && !(isDefaultProvider && m === models.default?.model))
                  .map((m) => (
                    <Row key={m} label={m} on={false} disabled={busy} onClick={() => void pick(`${pid}/${m}`)} />
                  ))}
                {!isDefaultProvider && !custom && !p.models?.length && <span className="px-2.5 py-1.5 text-sub text-ink-3">按「探測」後會列出模型，或在下面輸入名稱。</span>}
              </div>
              <form onSubmit={submitOther} className="flex items-center gap-2 px-1">
                <label className="flex min-w-0 flex-1">
                  <span className="sr-only-text">其他模型名稱</span>
                  <input
                    value={other}
                    onChange={(e) => setOther(e.target.value)}
                    placeholder="模型名稱，例：gpt-4.1"
                    className="h-9 w-full min-w-0 rounded-field border border-line bg-inset px-3 text-ui-sm text-ink outline-none focus-visible:border-focus"
                  />
                </label>
                <button type="submit" disabled={!other.trim() || busy} className="h-9 flex-none rounded-full bg-control px-3.5 text-ui-sm text-ink hover:bg-hover disabled:opacity-40">
                  使用
                </button>
              </form>
              {err && (
                <span role="alert" className="px-1 pt-1 text-sub text-danger">
                  {err}
                </span>
              )}
              <span className="flex-1" />
              <EffortPicker bot={bot} onSaved={upsertBot} />
            </>
          )}
        </div>
      </div>
      <button type="button" onClick={() => navigate('/settings/models')} className="flex h-12 items-center gap-2.5 border-t border-line bg-transparent px-4 text-left text-ui text-ink hover:bg-control">
        <Icon icon={Settings} size={18} className="text-ink-2" />
        <span className="flex-1">管理 AI 模型與金鑰</span>
        <Icon icon={ChevronRight} size={16} className="text-ink-2" />
      </button>
    </div>
  )
}

// 用訂閱 CLI 的 Bot：列出這個方案能用的模型（CLI 上次開對話時回報的，engines.status）。
function PlanBody({ bot, st, close }: { bot: Bot; st: EngineStatus | null; close: () => void }) {
  const [other, setOther] = useState('')
  const [filter, setFilter] = useState('')
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')
  const plan = PLAN_NAME[bot.engine] ?? ENGINE_LABEL[bot.engine] ?? bot.engine
  const models = st?.models ?? []
  const shown = filter ? models.filter((m) => (m.value + ' ' + (m.name ?? '')).toLowerCase().includes(filter.toLowerCase())) : models
  const settings = `/settings/bots/${encodeURIComponent(bot.id)}/advanced`
  const pick = async (model: string) => {
    setBusy(true)
    setErr('')
    try {
      upsertBot(await botsUpdate({ id: bot.id, model }))
      close()
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }
  return (
    <div role="dialog" aria-label="選擇模型" className="flex w-[400px] max-w-full flex-col overflow-hidden rounded-menu bg-card shadow-pop max-sm:w-full">
      <div className="flex flex-col gap-1 px-4 pb-3 pt-3.5">
        <span className="text-title">{plan} 訂閱</span>
        {st?.needs_link && !st.linked && (
          <button type="button" onClick={() => navigate(settings)} className="mt-1 self-start rounded-full bg-warning-soft px-2.5 py-1 text-[12.5px] text-warning-ink">
            還沒登入這個帳號，去設定
          </button>
        )}
        {models.length > 8 && (
          <input
            aria-label="找模型"
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
            placeholder="找模型"
            className="mt-2 h-9 w-full rounded-field border border-line bg-inset px-3 text-ui-sm text-ink outline-none focus-visible:border-focus"
          />
        )}
        <div role="menu" aria-label="模型" className="mt-1 flex max-h-[min(320px,45dvh)] flex-col gap-0.5 overflow-y-auto">
          {!filter && <Row label={st?.agent_default ? `預設（${st.agent_default}）` : '預設'} on={!bot.model} disabled={busy} onClick={() => void pick('')} />}
          {shown.map((m) => (
            <Row key={m.value} label={m.name && m.name !== m.value ? m.name : m.value} note={m.name && m.name !== m.value ? m.value : undefined} on={bot.model === m.value} disabled={busy} onClick={() => void pick(m.value)} />
          ))}
          {bot.model && !models.some((m) => m.value === bot.model) && <Row label={bot.model} on onClick={close} />}
        </div>
        {st && !models.length && <span className="px-2.5 pt-1 text-sub text-ink-3">聊過一次之後，這裡會列出能用的模型。</span>}
        {!models.length && (
          <form
            onSubmit={(e: FormEvent) => {
              e.preventDefault()
              if (other.trim()) void pick(other.trim())
            }}
            className="mt-2 flex items-center gap-2 px-1"
          >
            <label className="flex min-w-0 flex-1">
              <span className="sr-only-text">其他模型名稱</span>
              <input value={other} onChange={(e) => setOther(e.target.value)} placeholder="模型名稱" className="h-9 w-full min-w-0 rounded-field border border-line bg-inset px-3 text-ui-sm text-ink outline-none focus-visible:border-focus" />
            </label>
            <button type="submit" disabled={!other.trim() || busy} className="h-9 flex-none rounded-full bg-control px-3.5 text-ui-sm text-ink hover:bg-hover disabled:opacity-40">
              使用
            </button>
          </form>
        )}
        {err && (
          <span role="alert" className="px-1 pt-1 text-sub text-danger">
            {err}
          </span>
        )}
        <EffortPicker bot={bot} onSaved={upsertBot} />
      </div>
      <button type="button" onClick={() => navigate(settings)} className="flex h-12 items-center gap-2.5 border-t border-line bg-transparent px-4 text-left text-ui text-ink hover:bg-control">
        <Icon icon={Settings} size={18} className="text-ink-2" />
        <span className="flex-1">換帳號或模型</span>
        <Icon icon={ChevronRight} size={16} className="text-ink-2" />
      </button>
    </div>
  )
}

/** 訂閱 CLI 的狀態（模型清單）；不是訂閱的 Bot 不讀。 */
function usePlanStatus(bot: Bot): EngineStatus | null {
  const [st, setSt] = useState<EngineStatus | null>(null)
  const plan = usesPlan(bot)
  useEffect(() => {
    if (!plan) return
    let live = true
    enginesStatus({ bot_id: bot.id })
      .then((o) => live && setSt(o.bots[0] ?? null))
      .catch(() => {})
    return () => {
      live = false
    }
  }, [bot.id, bot.engine, plan])
  return plan ? st : null
}

// 模型選擇器（design/C06）：對話頁「⋯」→「模型：…」打開（原本是標題列的膠囊）。只影響這個 Bot（xbot.bots.update 的 model、effort）。
// 用 API 金鑰（Hermes）：左邊供應商、右邊模型與推理強度；還沒有任何供應商時，直接打開模型設定。
// 用訂閱 CLI：列出這個方案能用的模型。
/** 這個 Bot 現在用的模型（給「⋯」選單那一項寫出來）；empty：還沒設定任何模型。 */
export function useModelLabel(bot: Bot, models: ModelsGetOutput | null): { label: string; empty: boolean } {
  const plan = usesPlan(bot)
  const st = usePlanStatus(bot)
  return {
    label: plan ? `${ENGINE_LABEL[bot.engine] ?? bot.engine} · ${bot.model || st?.agent_default || '預設'}` : modelLabel(bot, models),
    empty: !plan && !models?.providers.length,
  }
}

/**
 * 選模型的視窗（從對話頁「⋯」→「模型：…」打開；2026-10-20 頁首精簡：換模型很少用，不再一直擺在頁首）。
 * 內容和原本模型膠囊的彈出框一樣。
 */
export function ModelDialog({ bot, models, onClose }: { bot: Bot; models: ModelsGetOutput | null; onClose: () => void }) {
  const plan = usesPlan(bot)
  const st = usePlanStatus(bot)
  const box = useRef<HTMLDivElement>(null)
  const onKeyDown = useFocusTrap(box, true, onClose)
  return createPortal(
    <div className="fixed-screen z-50 flex items-start justify-center bg-black/60 px-3 pt-[12vh]" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <div ref={box} onKeyDown={onKeyDown} className="flex max-w-full justify-center">
        {plan ? <PlanBody bot={bot} st={st} close={onClose} /> : models ? <Body bot={bot} models={models} close={onClose} /> : null}
      </div>
    </div>,
    document.body,
  )
}
