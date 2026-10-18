import { useState, type FormEvent } from 'react'
import { Check, ChevronDown, ChevronRight, Settings } from 'lucide-react'
import { botsUpdate, type ModelsGetOutput } from '../api/generated'
import { upsertBot, type Bot } from '../data/bots'
import { navigate } from '../router'
import { Icon } from '../ui/Icon'
import { cx } from '../ui/cx'
import { modelLabel, parseModel } from './format'
import { Popover } from './Popover'

type Provider = ModelsGetOutput['providers'][number]

const EFFORTS = [
  { value: 'low', label: '低' },
  { value: 'medium', label: '中' },
  { value: 'high', label: '高' },
] as const

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
                  <span className="truncate text-sub text-ink-2">{host(p.base_url)} · 用你的 API 金鑰，用量算進花費上限</span>
                </div>
                {p.api_key_set ? (
                  <span className="flex h-6 flex-none items-center rounded-full bg-success-soft px-2.5 text-[12px] text-success">金鑰已設定</span>
                ) : (
                  <button type="button" onClick={() => navigate('/settings/models')} className="flex h-7 flex-none items-center rounded-full bg-ink px-3 text-[12.5px] text-on-ink">
                    設定金鑰
                  </button>
                )}
              </div>
              <span className="mt-2 px-2.5 text-meta text-ink-3">建議</span>
              <div role="menu" aria-label="模型" className="flex flex-col gap-0.5">
                {isDefaultProvider && models.default && <Row label={models.default.model} tag="預設" on={!bot.model} disabled={busy} onClick={() => void pick('')} />}
                {custom && <Row label={custom} on onClick={close} />}
                {!isDefaultProvider && !custom && <span className="px-2.5 py-1.5 text-sub text-ink-3">在下面輸入要用的模型名稱。</span>}
              </div>
              <span className="mt-2 px-2.5 text-meta text-ink-3">其他</span>
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
              <div className="mt-3 flex items-baseline justify-between border-t border-line pt-3">
                <span id="effort-l" className="text-ui">
                  推理強度
                </span>
                <span className="text-meta text-ink-3">越高越仔細，也越慢、越花錢</span>
              </div>
              <div role="radiogroup" aria-labelledby="effort-l" className="mt-2 flex gap-0.5 rounded-field bg-inset p-1">
                {EFFORTS.map((e) => {
                  const on = (bot.effort || 'medium') === e.value
                  return (
                    <button
                      key={e.value}
                      type="button"
                      role="radio"
                      aria-checked={on}
                      disabled={busy}
                      onClick={() => !on && void save({ effort: e.value })}
                      className={cx('h-8 flex-1 rounded-seg text-ui-sm', on ? 'bg-hover text-ink' : 'bg-transparent text-ink-2 hover:text-ink')}
                    >
                      {e.label}
                    </button>
                  )
                })}
              </div>
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

// 模型選擇器（design/C06）：標題列的模型膠囊打開；左邊供應商、右邊模型與推理強度。
// 只影響這個 Bot（xbot.bots.update 的 model、effort）。還沒有任何供應商時，按膠囊直接打開模型設定。
export function ModelPicker({ bot, models, onSetup, className }: { bot: Bot; models: ModelsGetOutput | null; onSetup: () => void; className?: string }) {
  const label = modelLabel(bot, models)
  const empty = !models?.providers.length
  return (
    <Popover
      kind="dialog"
      align="end"
      className="max-sm:fixed max-sm:inset-x-3 max-sm:top-[112px] max-sm:mt-0"
      trigger={(p) => (
        <button
          {...p}
          onClick={empty ? onSetup : p.onClick}
          type="button"
          aria-label={`模型：${label}（選擇模型）`}
          className={cx(
            'relative flex h-8 min-w-0 max-w-[220px] items-center gap-1.5 whitespace-nowrap rounded-full bg-card pl-3.5 pr-2.5 text-sub text-ink touch-target hover:bg-control aria-expanded:outline aria-expanded:outline-2 aria-expanded:outline-accent',
            className,
          )}
        >
          <span className="truncate">{label}</span>
          <Icon icon={ChevronDown} size={16} className="text-ink-2" />
        </button>
      )}
    >
      {(close) => (models ? <Body bot={bot} models={models} close={close} /> : null)}
    </Popover>
  )
}
