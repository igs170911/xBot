import { Avatar } from '../../ui/Avatar'
import { COLORS, COLOR_IDS, COLOR_LABELS, EYE_VARIANTS, SHAPE_IDS, SHAPE_LABELS, type AvatarColor, type AvatarShape, type EyeVariant } from '../../ui/avatar-data'
import { cx } from '../../ui/cx'

// 頭像的形狀、眼睛、顏色選擇（A04「換個樣子」、G03 外觀）：點了立刻套用，選中的有 2px #1084FE 外框。

export const EYE_LABELS: Record<EyeVariant, string> = { dot: '圓點', aside: '看旁邊', happy: '開心', wide: '睜大', half: '半閉', closed: '閉眼' }

type Look = { shape: string; color: string; eyes: string; img?: string }

const tile = (on: boolean) =>
  cx('flex flex-col items-center justify-center gap-1 rounded-field border-2 bg-inset text-meta text-ink-2 hover:bg-control', on ? 'border-accent text-ink' : 'border-transparent')

export function ShapePicker({ value, onChange }: { value: Look; onChange: (s: AvatarShape) => void }) {
  return (
    <div role="radiogroup" aria-label="形狀" className="grid grid-cols-5 gap-1.5">
      {SHAPE_IDS.map((s) => (
        <button key={s} type="button" role="radio" aria-checked={value.shape === s} aria-label={SHAPE_LABELS[s]} onClick={() => onChange(s)} className={cx(tile(value.shape === s), 'h-[54px]')}>
          <Avatar shape={s} color={value.color} eyes={value.eyes} img={value.img} dot="none" size={26} blink={false} />
          <span aria-hidden="true">{SHAPE_LABELS[s]}</span>
        </button>
      ))}
    </div>
  )
}

export function EyePicker({ value, onChange, dim }: { value: Look; onChange: (e: EyeVariant) => void; dim?: boolean }) {
  return (
    <div role="radiogroup" aria-label="眼睛" className={cx('grid grid-cols-6 gap-1.5 max-sm:grid-cols-3', dim && 'opacity-45')}>
      {EYE_VARIANTS.map((e) => {
        const on = !dim && (value.eyes || 'dot') === e
        return (
          <button key={e} type="button" role="radio" aria-checked={on} aria-label={EYE_LABELS[e]} onClick={() => onChange(e)} className={cx(tile(on), 'h-[54px]')}>
            <Avatar shape={value.shape} color={value.color} eyes={e} dot="none" size={26} blink={false} />
            <span aria-hidden="true">{EYE_LABELS[e]}</span>
          </button>
        )
      })}
    </div>
  )
}

export function ColorPicker({ value, onChange }: { value: string; onChange: (c: AvatarColor) => void }) {
  return (
    <div role="radiogroup" aria-label="顏色" className="flex flex-wrap gap-2.5">
      {COLOR_IDS.map((c) => (
        <button
          key={c}
          type="button"
          role="radio"
          aria-checked={c === value}
          aria-label={COLOR_LABELS[c]}
          onClick={() => onChange(c)}
          className={cx('relative size-8 rounded-full touch-target', c === value && 'ring-2 ring-accent ring-offset-2 ring-offset-panel')}
          style={{ background: COLORS[c] }}
        />
      ))}
    </div>
  )
}
