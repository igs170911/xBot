import { useState } from 'react'
import { Select } from '../ui/Menu'
import { TextInput } from '../ui/Field'
import { cx } from '../ui/cx'
import { providerName, type ModelRef, type Preset, type Provider } from './modelData'

const NONE = '__none__'

/**
 * 供應商＋模型兩個下拉（G09）。供應商沒有模型清單（還沒探測）時，模型改成可以打字。
 * 選了就呼叫 onCommit（自動儲存）；noneLabel 有給時，供應商可以選「不用」。
 */
export function ModelChooser({
  label,
  value,
  providers,
  presets,
  onCommit,
  noneLabel,
  disabled,
  className,
}: {
  label: string
  value?: ModelRef | null
  providers: Provider[]
  presets: Preset[]
  onCommit: (v: ModelRef | null) => void
  noneLabel?: string
  disabled?: boolean
  className?: string
}) {
  const initial = value?.provider ?? (noneLabel ? NONE : '')
  const src = `${initial}\u0000${value?.model ?? ''}`
  const [prov, setProv] = useState(initial)
  const [typed, setTyped] = useState(value?.model ?? '')
  // 外面的值變了（例：存好後重讀）就跟著換（render 時比對，不用 effect）
  const [seen, setSeen] = useState(src)
  if (seen !== src) {
    setSeen(src)
    setProv(initial)
    setTyped(value?.model ?? '')
  }

  const p = providers.find((x) => x.id === prov)
  const list = [...(p?.models ?? [])]
  if (value && value.provider === prov && value.model && !list.includes(value.model)) list.unshift(value.model)

  const provOptions = [
    ...(noneLabel ? [{ value: NONE, label: noneLabel }] : []),
    ...providers.map((x) => ({ value: x.id, label: providerName(x, presets) })),
  ]

  const pickProvider = (id: string) => {
    setProv(id)
    if (id === NONE) {
      onCommit(null)
      return
    }
    const np = providers.find((x) => x.id === id)
    const preset = presets.find((x) => x.id === (np?.preset || id))
    const first = (preset?.default_model && np?.models?.includes(preset.default_model) ? preset.default_model : undefined) ?? np?.models?.[0] ?? preset?.default_model
    if (first) onCommit({ provider: id, model: first })
    else setTyped('')
  }

  return (
    <div className={cx('grid grid-cols-2 gap-3 max-sm:grid-cols-1', className)}>
      <Select label={`${label}：供應商`} value={prov} options={provOptions} onChange={pickProvider} placeholder="選供應商" disabled={disabled} />
      {prov === NONE ? (
        <span />
      ) : list.length > 0 ? (
        <Select
          label={`${label}：模型`}
          value={value?.provider === prov ? value.model : ''}
          options={list.map((m) => ({ value: m, label: m }))}
          onChange={(m) => onCommit({ provider: prov, model: m })}
          placeholder="選模型"
          disabled={disabled || !prov}
        />
      ) : (
        <TextInput
          label={`${label}：模型`}
          value={typed}
          placeholder="輸入模型名稱，例：gpt-4.1-mini"
          disabled={disabled || !prov}
          onChange={(e) => setTyped(e.target.value)}
          onBlur={() => {
            const m = typed.trim()
            if (m && prov && (m !== value?.model || prov !== value?.provider)) onCommit({ provider: prov, model: m })
          }}
          onKeyDown={(e) => {
            if (e.key !== 'Enter' || e.nativeEvent.isComposing) return
            e.preventDefault()
            ;(e.target as HTMLInputElement).blur()
          }}
        />
      )}
    </div>
  )
}
