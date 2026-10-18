import { useState } from 'react'
import { Download, Maximize2, Search } from 'lucide-react'
import { Dialog } from '../../ui/Dialog'
import { Icon } from '../../ui/Icon'
import { cx } from '../../ui/cx'
import { TABLE_PREVIEW_ROWS, toCSV, type TableCell, type TableData } from './parse'

function download(name: string, text: string) {
  const url = URL.createObjectURL(new Blob([text], { type: 'text/csv;charset=utf-8' }))
  const a = document.createElement('a')
  a.href = url
  a.download = name
  document.body.appendChild(a)
  a.click()
  a.remove()
  setTimeout(() => URL.revokeObjectURL(url), 1000)
}

const ALIGN = { left: 'text-left', right: 'text-right', center: 'text-center' } as const

function Grid({ t, rows }: { t: TableData; rows: TableCell[][] }) {
  // 短的格子不換行（數字、價錢），長的文字給它寬一點再換行
  const cellClass = (c: TableCell, i: number) => cx('px-3 py-[9px] align-top', ALIGN[t.align[i] ?? 'left'], c.text.length <= 24 ? 'whitespace-nowrap' : 'min-w-[14em]')
  return (
    <div className="overflow-x-auto" tabIndex={0} role="region" aria-label="表格內容（可橫向捲動）">
      <table className="w-full border-collapse text-[14px] leading-[1.4]">
        <thead>
          <tr className="text-meta text-ink-2">
            {t.header.map((c, i) => (
              <th key={i} scope="col" className={cx('whitespace-nowrap px-3 py-2.5 font-normal', ALIGN[t.align[i] ?? 'left'])}>
                {c.node}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.map((r, j) => (
            <tr key={j} className="border-t border-line">
              {t.header.map((_, i) => (
                <td key={i} className={cellClass(r[i] ?? { node: null, text: '' }, i)}>
                  {r[i]?.node}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

// 表格（design/C07、C02）：#191919 圓角 12，寬表格在框內橫向捲動。
// 超過 5 列時加上工具列：搜尋、列數、下載 CSV、放大；先顯示 5 列，「顯示全部」展開。
export function Table({ t }: { t: TableData }) {
  const [q, setQ] = useState('')
  const [all, setAll] = useState(false)
  const [big, setBig] = useState(false)
  const long = t.rows.length > TABLE_PREVIEW_ROWS
  const needle = q.trim().toLowerCase()
  const matched = needle ? t.rows.filter((r) => r.some((c) => c.text.toLowerCase().includes(needle))) : t.rows
  const shown = all || needle ? matched : matched.slice(0, TABLE_PREVIEW_ROWS)
  const csv = () => download('表格.csv', toCSV(t))

  if (!long) {
    return (
      <div className="min-w-0 overflow-hidden rounded-menu bg-inset" data-testid="md-table">
        <Grid t={t} rows={t.rows} />
      </div>
    )
  }

  return (
    <div className="flex min-w-0 flex-col overflow-hidden rounded-menu bg-inset" data-testid="md-table">
      <div className="flex flex-wrap items-center gap-2 border-b border-line py-2.5 pl-3 pr-2.5">
        <label className="relative flex min-w-0 flex-[1_1_180px] items-center">
          <span className="sr-only-text">搜尋表格</span>
          <span className="pointer-events-none absolute left-2.5 flex text-ink-3">
            <Icon icon={Search} size={16} />
          </span>
          <input
            type="search"
            value={q}
            onChange={(e) => setQ(e.target.value)}
            placeholder="搜尋表格…"
            className="h-[34px] w-full rounded-field border border-line bg-panel pl-[34px] pr-2.5 text-sub text-ink outline-none focus-visible:border-focus"
          />
        </label>
        <span className="px-1 text-meta text-ink-3">
          {t.rows.length} 列 · {t.header.length} 欄
        </span>
        <button
          type="button"
          onClick={csv}
          className="relative flex h-[30px] items-center gap-1.5 rounded-full border border-line bg-transparent px-3 text-[13px] text-ink touch-target hover:bg-control"
        >
          <Icon icon={Download} size={15} />
          下載 CSV
        </button>
        <button type="button" aria-label="放大表格" onClick={() => setBig(true)} className="relative flex size-[30px] items-center justify-center rounded-seg bg-transparent text-ink-2 touch-target hover:bg-control">
          <Icon icon={Maximize2} size={16} />
        </button>
      </div>
      <Grid t={t} rows={shown} />
      <div className="flex items-center justify-between border-t border-line px-3 py-2 text-meta text-ink-3">
        <span>
          {needle ? `找到 ${matched.length} 列` : `顯示 ${shown.length}／${t.rows.length} 列`}
        </span>
        {!needle && (
          <button type="button" onClick={() => setAll((a) => !a)} className="relative h-[26px] rounded-seg bg-transparent px-2 text-meta text-accent-ink touch-target hover:bg-control">
            {all ? '收起' : '顯示全部'}
          </button>
        )}
      </div>
      <Dialog open={big} onClose={() => setBig(false)} title={`表格 · ${t.rows.length} 列`} width={980}>
        <div className="-mx-1 overflow-hidden rounded-menu bg-inset">
          <Grid t={t} rows={t.rows} />
        </div>
      </Dialog>
    </div>
  )
}
