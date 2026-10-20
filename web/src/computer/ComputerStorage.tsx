import { useEffect, useState } from 'react'
import { BrushCleaning, HardDrive } from 'lucide-react'
import { ApiError, computersClean, computersStorage, type ComputersStorageOutput } from '../api/generated'
import { sizeLabel } from '../files/data'
import { Button } from '../ui/Button'
import { Icon } from '../ui/Icon'
import { cx } from '../ui/cx'
import { useToast } from '../ui/toast-context'

// 電腦的儲存空間（BX-14，照 LazyBoy 的 storage-panel）：空間花在哪裡，以及「清理快取」。
// 每天會自動清一次（引擎暫存與 /workspace/tmp 放超過 3 天、附件放超過 30 天、瀏覽器快取超過 200 MB）；
// 這個按鈕只清會自己長回來的快取與暫存，工作成果、附件和登入都不動。

export type Usage = ComputersStorageOutput['usage']

/** 依序顯示的分類；說明照 M01「成果不會自動刪除。你傳的附件 30 天後刪除；瀏覽器快取超過 200 MB 每天自動清理」。 */
const ROWS: { key: Exclude<keyof Usage, 'total'>; label: string; note?: string; color: string }[] = [
  { key: 'workspace', label: '工作成果', note: '不會自動刪除', color: 'bg-accent' },
  { key: 'uploads', label: '你傳的附件', note: '30 天後自動刪除', color: 'bg-warning' },
  { key: 'caches', label: '快取與暫存', note: '每天自動清理', color: 'bg-success' },
  { key: 'browser', label: '瀏覽器登入資料', color: 'bg-accent-ink' },
  { key: 'tools', label: '它裝的工具', color: 'bg-[#9f7bff]' },
  { key: 'other', label: '其他', color: 'bg-ink-3' },
]

function errText(err: unknown): string {
  return err instanceof Error ? err.message : String(err)
}

/** 儲存空間：打開時量一次（沒開機也可以，不會叫醒它）。working 時不能清。 */
export function ComputerStorage({ botId, botName, working, className }: { botId: string; botName: string; working?: boolean; className?: string }) {
  const toast = useToast()
  const [view, setView] = useState<ComputersStorageOutput | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [cleaning, setCleaning] = useState(false)

  useEffect(() => {
    let alive = true
    computersStorage({ bot_id: botId })
      .then((v) => {
        if (!alive) return
        setView(v)
        setError(null)
      })
      .catch((e: unknown) => alive && setError(errText(e)))
    return () => {
      alive = false
    }
  }, [botId])

  const clean = async () => {
    setCleaning(true)
    setError(null)
    try {
      const out = await computersClean({ bot_id: botId })
      setView({ bot_id: out.bot_id, has_computer: out.has_computer, usage: out.usage })
      toast.show({ kind: 'done', text: out.result.freed > 0 ? `清出 ${sizeLabel(out.result.freed)}` : '目前沒有可以清的快取' })
    } catch (err) {
      if (err instanceof ApiError && err.code === 'conflict') toast.show({ kind: 'info', text: err.message })
      else setError(errText(err))
    } finally {
      setCleaning(false)
    }
  }

  const v = view?.bot_id === botId ? view : null // 換了 Bot 時先不顯示上一個的
  const u = v?.usage
  const total = u?.total ?? 0
  const none = v && !v.has_computer
  const why = working ? `${botName}正在工作，等它做完再清理` : undefined

  return (
    <section className={cx('flex flex-col gap-2.5 rounded-group px-4 py-3.5', className ?? 'bg-panel')} aria-label="儲存空間" data-testid="computer-storage">
      <div className="flex items-start gap-2">
        <Icon icon={HardDrive} size={16} className="mt-0.5 text-ink-2" />
        <h3 className="m-0 min-w-0 flex-1 text-ui">儲存空間</h3>
        <span className="text-sub text-ink-2" aria-live="polite">
          {v ? (none ? '還沒有電腦' : `共 ${sizeLabel(total)}`) : error ? '' : '計算中…'}
        </span>
      </div>
      {u && !none && (
        <>
          {total > 0 && (
            <div className="flex h-2 gap-0.5 overflow-hidden rounded-full bg-inset" aria-hidden="true">
              {ROWS.map((r) => (u[r.key] > 0 ? <span key={r.key} className={r.color} style={{ flexGrow: u[r.key] }} /> : null))}
            </div>
          )}
          <ul className="m-0 flex list-none flex-col p-0">
            {ROWS.map((r) => (
              <li key={r.key} className="flex min-h-8 items-center gap-2.5 text-ui-sm">
                <span className={cx('size-2 flex-none rounded-full', r.color)} aria-hidden="true" />
                <span className="min-w-0 flex-1">
                  {r.label}
                  {r.note && <span className="ml-2 text-meta text-ink-2">{r.note}</span>}
                </span>
                <span className="text-sub text-ink-2">{sizeLabel(u[r.key])}</span>
              </li>
            ))}
          </ul>
        </>
      )}
      {!none && (
        <div className="flex flex-wrap items-center gap-x-3 gap-y-1.5">
          <Button size="sm" variant="outline" icon={BrushCleaning} loading={cleaning} disabled={!v || !!why} title={why} onClick={() => void clean()}>
            清理快取
          </Button>
          <span className="text-meta text-ink-2">工作成果、附件和登入不會被清掉。</span>
        </div>
      )}
      {error && (
        <p className="m-0 text-meta text-danger" role="alert">
          {error}
        </p>
      )}
    </section>
  )
}
