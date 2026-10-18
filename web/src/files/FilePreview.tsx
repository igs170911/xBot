import { useState, type ReactNode } from 'react'
import { Download, Ellipsis, FileQuestion, X } from 'lucide-react'
import type { FilesReadOutput } from '../api/generated'
import { CodeBlock } from '../chat/markdown/CodeBlock'
import { Markdown } from '../chat/markdown/Markdown'
import type { Bot } from '../data/bots'
import { Button, IconButton } from '../ui/Button'
import { Icon } from '../ui/Icon'
import { MenuButton, type MenuItem } from '../ui/Menu'
import { cx } from '../ui/cx'
import { kindMeta, sizeLabel, tableSummary, whenLabel, type Table } from './data'
import { usePreview } from './usePreview'

/** 檔案的種類圖示（E07：圓角方塊裡的彩色圖示）。 */
export function FileIcon({ kind, size = 36 }: { kind: string; size?: number }) {
  const k = kindMeta(kind)
  return (
    <span aria-hidden="true" className="flex flex-none items-center justify-center rounded-[10px] bg-card" style={{ width: size, height: size, color: k.color }}>
      <Icon icon={k.icon} size={Math.round(size * 0.5)} />
    </span>
  )
}

const NUM = /^[-+]?[\d,]*\.?\d+%?$/

/** CSV、Excel 的表格：數字靠右；rows 可以限制顯示幾列（手機預覽前 5 列）。 */
export function DataTable({ t, rows, label }: { t: Table; rows?: number; label: string }) {
  const shown = rows ? t.rows.slice(0, rows) : t.rows
  const width = Math.max(t.columns.length, ...shown.map((r) => r.length))
  const numeric = Array.from({ length: width }, (_, i) => shown.length > 0 && shown.every((r) => !r[i] || NUM.test(r[i].trim())))
  return (
    <div tabIndex={0} role="region" aria-label="表格內容" className="overflow-auto rounded-group border border-divider bg-inset" data-testid="file-table">
      <table aria-label={label} className="w-full border-collapse text-ui-sm">
        <thead className="sticky top-0 bg-inset">
          <tr>
            {Array.from({ length: width }, (_, i) => (
              <th key={i} scope="col" className={cx('whitespace-nowrap border-b border-divider px-3 py-2.5 font-normal text-ink-2', numeric[i] ? 'text-right' : 'text-left')}>
                {t.columns[i] ?? ''}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {shown.map((r, ri) => (
            <tr key={ri} className="border-b border-divider last:border-0">
              {Array.from({ length: width }, (_, i) => (
                <td key={i} className={cx('max-w-[320px] truncate whitespace-nowrap px-3 py-2.5', numeric[i] ? 'text-right tabular-nums' : 'text-left')}>
                  {r[i] ?? ''}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

function Body({ d, raw, onSheet, sheet }: { d: FilesReadOutput; raw: boolean; onSheet: (s: string) => void; sheet: string }) {
  if (d.table && !raw) {
    const t = d.table
    return (
      <div className="flex flex-col gap-2">
        {t.sheets && t.sheets.length > 1 && (
          <div role="tablist" aria-label="工作表" className="flex flex-wrap gap-1">
            {t.sheets.map((s) => (
              <button
                key={s}
                type="button"
                role="tab"
                aria-selected={(sheet || t.sheet) === s}
                onClick={() => onSheet(s)}
                className={cx('h-8 rounded-seg px-3 text-ui-sm', (sheet || t.sheet) === s ? 'bg-control text-ink' : 'text-ink-2 hover:text-ink')}
              >
                {s}
              </button>
            ))}
          </div>
        )}
        <DataTable t={t} label={d.name} />
        <p className="m-0 text-sub text-ink-2">{tableSummary(t)}</p>
      </div>
    )
  }
  if (d.kind === 'image' && d.inline_url) {
    return (
      <div className="flex justify-center rounded-group bg-inset p-3">
        <img src={d.inline_url} alt={d.name} className="max-h-[70vh] max-w-full rounded-seg object-contain" />
      </div>
    )
  }
  if (d.kind === 'pdf' && d.inline_url) {
    return <iframe src={d.inline_url} title={d.name} className="h-[70vh] w-full rounded-group border-0 bg-white" />
  }
  if (d.kind === 'audio' && d.inline_url) return <audio controls src={d.inline_url} className="w-full" />
  if (d.kind === 'video' && d.inline_url) return <video controls src={d.inline_url} className="max-h-[70vh] w-full rounded-group bg-black" />
  if (d.archive) {
    return (
      <div className="flex flex-col gap-2">
        <ul className="m-0 flex max-h-[60vh] list-none flex-col overflow-auto rounded-group bg-inset p-0">
          {d.archive.entries.map((e) => (
            <li key={e.name} className="flex justify-between gap-3 border-b border-divider px-3 py-2 text-ui-sm last:border-0">
              <span className="min-w-0 truncate font-mono text-[13px]">{e.name}</span>
              {!e.dir && <span className="flex-none text-ink-3">{sizeLabel(e.size)}</span>}
            </li>
          ))}
        </ul>
        <p className="m-0 text-sub text-ink-2">{d.archive.truncated ? `共 ${d.archive.total.toLocaleString()} 項，這裡只列前面` : `${d.archive.total} 項`}</p>
      </div>
    )
  }
  if (d.text !== undefined && d.text !== '' && !d.binary) {
    const more = d.truncated ? <p className="m-0 text-sub text-ink-2">檔案比較大，這裡只顯示前面。完整內容請下載。</p> : null
    if (d.kind === 'markdown' && !raw) {
      return (
        <div className="flex flex-col gap-2">
          <div className="rounded-group bg-inset px-5 py-4">
            <Markdown text={d.text} />
          </div>
          {more}
        </div>
      )
    }
    if (d.kind === 'code') {
      return (
        <div className="flex flex-col gap-2">
          <CodeBlock code={d.text} lang={d.language} />
          {more}
        </div>
      )
    }
    return (
      <div className="flex flex-col gap-2">
        <pre tabIndex={0} className="m-0 max-h-[70vh] overflow-auto whitespace-pre-wrap break-words rounded-group bg-inset px-4 py-3 font-mono text-[13px] leading-relaxed">{d.text}</pre>
        {more}
      </div>
    )
  }
  return (
    <div className="flex flex-col items-center gap-3 rounded-group bg-inset px-6 py-10 text-center">
      <FileIcon kind={d.kind} size={56} />
      <p className="m-0 text-ui text-ink-2">{d.note || (d.size === 0 ? '這是空的檔案。' : '這種檔案沒有預覽，請下載後開啟。')}</p>
      <Button variant="secondary" icon={Download} onClick={() => window.open(d.download_url, '_self')}>
        下載
      </Button>
    </div>
  )
}

export type FilePreviewProps = {
  botId: string
  bot?: Bot
  path: string
  /** 動作選單（傳給其他 Bot、改名、刪除…）。 */
  actions?: MenuItem[]
  onClose?: () => void
  /** 手機 sheet：表格只顯示前 5 列。 */
  compact?: boolean
  /** 底部多放的東西（手機 sheet 的按鈕列）。 */
  footer?: ReactNode
}

// 檔案預覽（E07 右側）：標題列（名稱、大小・Bot・時間）、表格／原始內容切換、下載、更多；內容依種類畫。
export function FilePreview({ botId, bot, path, actions, onClose, compact, footer }: FilePreviewProps) {
  const [state, setSheet] = usePreview(botId, path)
  const [raw, setRaw] = useState(false)
  const d = state?.data
  const name = d?.name ?? path.slice(path.lastIndexOf('/') + 1)
  const meta = d ? [sizeLabel(d.size), bot?.name, whenLabel(d.modified)].filter(Boolean).join(' · ') : ''
  const canRaw = d && d.table && d.kind === 'csv'
  const canRawMd = d && d.kind === 'markdown'
  return (
    <section aria-label={`預覽：${name}`} className="flex min-h-0 min-w-0 flex-1 flex-col" data-testid="file-preview">
      <header className="flex min-h-14 flex-none items-center gap-2.5 border-b border-divider px-4 py-2">
        {compact && d && <FileIcon kind={d.kind} size={40} />}
        <div className="flex min-w-0 flex-1 flex-col">
          <span className="truncate text-name" title={name}>
            {name}
          </span>
          <span className="truncate text-meta text-ink-3">{meta || ' '}</span>
          {compact && <span className="truncate font-mono text-[12px] text-ink-3">{path}</span>}
        </div>
        {(canRaw || canRawMd) && !compact && (
          <div role="radiogroup" aria-label="顯示方式" className="flex rounded-field bg-inset p-[3px]">
            {[
              [false, canRaw ? '表格' : '排版'],
              [true, '原始內容'],
            ].map(([v, label]) => (
              <button
                key={String(v)}
                type="button"
                role="radio"
                aria-checked={raw === v}
                onClick={() => setRaw(v as boolean)}
                className={cx('h-8 whitespace-nowrap rounded-seg px-3 text-ui-sm', raw === v ? 'bg-control text-ink' : 'text-ink-2 hover:text-ink')}
              >
                {label as string}
              </button>
            ))}
          </div>
        )}
        {d && !compact && (
          <a
            href={d.download_url}
            download={d.name}
            aria-label="下載"
            title="下載"
            className="flex size-9 flex-none items-center justify-center rounded-[10px] text-ink-2 hover:bg-control hover:text-ink"
          >
            <Icon icon={Download} />
          </a>
        )}
        {actions && actions.length > 0 && (
          <MenuButton label="檔案動作" align="end" items={actions} trigger={(p) => <IconButton {...p} icon={Ellipsis} label="更多動作" />} />
        )}
        {onClose && <IconButton icon={X} label="關閉預覽" onClick={onClose} />}
      </header>
      <div className="min-h-0 flex-1 overflow-auto px-4 py-4">
        {!state && <p className="m-0 text-ui-sm text-ink-3">讀取中…</p>}
        {state?.error && (
          <div className="flex flex-col items-center gap-2 rounded-group bg-inset px-6 py-10 text-center" role="alert">
            <span className="text-ink-3">
              <Icon icon={FileQuestion} size={40} />
            </span>
            <p className="m-0 text-ui">{state.error.missing ? '這個檔案不在這台電腦' : '打不開這個檔案'}</p>
            <p className="m-0 text-sub text-ink-2">{state.error.missing ? `${bot?.name ?? 'Bot'}的電腦裡找不到 ${path}，可能已經搬走或刪掉，或在隊友的電腦裡。` : state.error.text}</p>
          </div>
        )}
        {d && compact && d.table ? (
          <div className="flex flex-col gap-2">
            <DataTable t={d.table} rows={5} label={d.name} />
            <p className="m-0 text-sub text-ink-2">{tableSummary(d.table, Math.min(5, d.table.rows.length))}</p>
          </div>
        ) : (
          d && <Body d={d} raw={raw} onSheet={setSheet} sheet={state?.sheet ?? ''} />
        )}
        {d && !compact && (
          <dl className="mt-4 grid grid-cols-[72px_1fr] gap-x-4 gap-y-2 rounded-group bg-inset px-4 py-3 text-ui-sm">
            <dt className="text-ink-3">位置</dt>
            <dd className="m-0 break-all font-mono text-[13px]">{d.path}</dd>
            <dt className="text-ink-3">種類</dt>
            <dd className="m-0">{kindMeta(d.kind).label}</dd>
            <dt className="text-ink-3">大小</dt>
            <dd className="m-0">{sizeLabel(d.size)}</dd>
            {d.modified > 0 && (
              <>
                <dt className="text-ink-3">修改</dt>
                <dd className="m-0">{whenLabel(d.modified)}</dd>
              </>
            )}
          </dl>
        )}
      </div>
      {footer}
    </section>
  )
}
