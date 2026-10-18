import { useState } from 'react'
import { cx } from '../ui/cx'

// 用量圖表（dataviz 規則）：單一系列只用 accent、細長條、資料端 4px 圓角、基線方角、
// 1px 淡格線、文字只用文字色；每張圖都附表格（螢幕閱讀器與想看數字的人）。

export type DayPoint = { key: string; value: number; detail: string }

function niceMax(v: number): number {
  if (v <= 0) return 1
  const p = Math.pow(10, Math.floor(Math.log10(v)))
  for (const m of [1, 2, 2.5, 5, 10]) if (m * p >= v) return m * p
  return 10 * p
}

/** 每天一根直條；滑過或聚焦顯示那天的明細。 */
export function DailyColumns({ points, format, label }: { points: DayPoint[]; format: (v: number) => string; label: string }) {
  const [hover, setHover] = useState<number | null>(null)
  const max = niceMax(Math.max(0, ...points.map((p) => p.value)))
  const H = 140
  const ticks = [0, max / 2, max]
  const showEvery = points.length > 10 ? Math.ceil(points.length / 5) : 1
  const h = hover !== null ? points[hover] : null
  return (
    <figure className="m-0 flex flex-col gap-2">
      <div className="relative flex gap-2">
        <div aria-hidden="true" className="relative w-12 flex-none text-right text-[11.5px] text-ink-3" style={{ height: H }}>
          {ticks.map((t) => (
            <span key={t} className="absolute right-0 -translate-y-1/2" style={{ top: H - (t / max) * H }}>
              {format(t)}
            </span>
          ))}
        </div>
        <div className="relative min-w-0 flex-1" style={{ height: H }} onMouseLeave={() => setHover(null)}>
          {ticks.map((t) => (
            <span key={t} aria-hidden="true" className="absolute inset-x-0 h-px bg-line/70" style={{ top: H - (t / max) * H }} />
          ))}
          <div role="img" aria-label={label} className="absolute inset-0 flex items-end gap-0.5">
            {points.map((p, i) => (
              <span key={p.key} onMouseEnter={() => setHover(i)} className="flex h-full min-w-0 flex-1 items-end justify-center">
                <span
                  className={cx('block w-full max-w-6 rounded-t-[4px]', hover === i ? 'bg-accent-ink' : 'bg-accent')}
                  style={{ height: p.value > 0 ? Math.max(2, (p.value / max) * H) : 0 }}
                />
              </span>
            ))}
          </div>
          {h && hover !== null && (
            <div
              aria-hidden="true"
              className="pointer-events-none absolute -top-2 z-10 -translate-x-1/2 -translate-y-full whitespace-nowrap rounded-menu bg-card px-3 py-2 text-meta shadow-pop"
              style={{ left: `${((hover + 0.5) / points.length) * 100}%` }}
            >
              <span className="block text-ink-2">{h.key}</span>
              <span className="block text-ui-sm text-ink">{format(h.value)}</span>
              <span className="block text-ink-2">{h.detail}</span>
            </div>
          )}
        </div>
      </div>
      <div aria-hidden="true" className="flex gap-2 pl-14 text-[11.5px] text-ink-3">
        {points.map((p, i) => (
          <span key={p.key} className="min-w-0 flex-1 overflow-visible whitespace-nowrap text-center">
            {i % showEvery === 0 ? p.key.slice(5).replace('-', '/') : ''}
          </span>
        ))}
      </div>
    </figure>
  )
}

export type BarRow = { key: string; label: string; value: number; valueText: string; sub?: string }

/** 橫條：名稱在左、值在條的尾端；最多 limit 列，其餘併成「其他」。 */
export function HBars({ rows, limit = 6, label }: { rows: BarRow[]; limit?: number; label: string }) {
  const shown = rows.slice(0, limit)
  const rest = rows.slice(limit)
  const all = rest.length
    ? [...shown, { key: '__other', label: `其他 ${rest.length} 個`, value: rest.reduce((a, r) => a + r.value, 0), valueText: '', sub: undefined }]
    : shown
  const max = Math.max(0, ...all.map((r) => r.value)) || 1
  return (
    <ul aria-label={label} className="m-0 flex list-none flex-col gap-2.5 p-0">
      {all.map((r) => (
        <li key={r.key} className="grid grid-cols-[minmax(0,11rem)_1fr] items-center gap-3 max-sm:grid-cols-[minmax(0,6.5rem)_1fr]">
          <span className="min-w-0">
            <span className="block truncate text-ui-sm" title={r.label}>
              {r.label}
            </span>
            {r.sub && <span className="block truncate text-[12px] text-ink-3">{r.sub}</span>}
          </span>
          <span className="flex min-w-0 items-center gap-2">
            <span className="h-2.5 rounded-r-[4px] bg-accent" style={{ width: `${Math.max(r.value > 0 ? 1 : 0, (r.value / max) * 78)}%` }} />
            <span className="whitespace-nowrap text-meta text-ink-2">{r.valueText}</span>
          </span>
        </li>
      ))}
    </ul>
  )
}

/** 進度條（上限用）：≥80% 橘色、到上限紅色。 */
export function Meter({ ratio, label }: { ratio: number; label: string }) {
  const r = Math.max(0, Math.min(1, ratio))
  const tone = ratio >= 1 ? 'bg-danger' : ratio >= 0.8 ? 'bg-warning' : 'bg-accent'
  return (
    <span role="meter" aria-label={label} aria-valuemin={0} aria-valuemax={100} aria-valuenow={Math.round(r * 100)} className="block h-1.5 w-full overflow-hidden rounded-full bg-line">
      <span className={cx('block h-full rounded-full', tone)} style={{ width: `${r * 100}%` }} />
    </span>
  )
}

/** 圖表底下的「看表格」：同一份資料的表格版。 */
export function DataTable({ caption, head, rows }: { caption: string; head: string[]; rows: string[][] }) {
  return (
    <details className="text-meta text-ink-2">
      <summary className="cursor-pointer select-none text-accent-ink">看表格</summary>
      <div className="mt-2 overflow-x-auto">
        <table className="w-full border-collapse text-left">
          <caption className="sr-only-text">{caption}</caption>
          <thead>
            <tr>
              {head.map((h) => (
                <th key={h} scope="col" className="border-b border-line px-2 py-1.5 font-normal text-ink-3">
                  {h}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {rows.map((r) => (
              <tr key={r[0]}>
                {r.map((c, i) => (
                  <td key={i} className="border-b border-line/60 px-2 py-1.5 text-ink">
                    {c}
                  </td>
                ))}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </details>
  )
}
