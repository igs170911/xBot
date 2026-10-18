import { useEffect, useState } from 'react'
import { Check, Copy, Download, LoaderCircle, Share, Smartphone, TriangleAlert, Wifi } from 'lucide-react'
import { ApiError, devicesPairStart, devicesPairStatus, type DevicesPairStartOutput } from '../api/generated'
import { Button } from '../ui/Button'
import { Dialog } from '../ui/Dialog'
import { Icon } from '../ui/Icon'
import { cx } from '../ui/cx'
import { promptInstall, useInstall } from './pwa'

// 手機相關設定（W31；A08、L06、L09）：連手機的 QR、推播開關、安裝到主畫面。

function errText(err: unknown): string {
  return err instanceof ApiError || err instanceof Error ? err.message : String(err)
}

/** QR：服務給的 SVG path（xbot.devices.pair_start 的 qr_path），白底黑格。 */
function QR({ size, path, label }: { size: number; path: string; label: string }) {
  return (
    <svg role="img" aria-label={label} viewBox={`0 0 ${size} ${size}`} className="size-[184px] flex-none rounded-menu bg-white" shapeRendering="crispEdges">
      <path d={path} fill="#000" />
    </svg>
  )
}

/** A08／設定的「連手機」：一個 QR（網址＋一次性配對碼），手機掃了就登入；每 2 秒看一次用掉了沒。 */
export function PairPhoneDialog({ open, onClose }: { open: boolean; onClose: () => void }) {
  const [round, setRound] = useState(0)
  return (
    <Dialog open={open} onClose={onClose} title="連手機" width={560}>
      {open && <PairBody key={round} onClose={onClose} onAgain={() => setRound((n) => n + 1)} />}
    </Dialog>
  )
}

function PairBody({ onClose, onAgain }: { onClose: () => void; onAgain: () => void }) {
  const [pair, setPair] = useState<DevicesPairStartOutput | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [status, setStatus] = useState<'pending' | 'paired' | 'expired'>('pending')
  const [copied, setCopied] = useState(false)
  const [wifi, setWifi] = useState(false)

  useEffect(() => {
    devicesPairStart({ origin: window.location.origin })
      .then(setPair)
      .catch((e: unknown) => setError(errText(e)))
  }, [])
  useEffect(() => {
    if (!pair?.code || status !== 'pending') return
    const t = setInterval(() => {
      devicesPairStatus({ code: pair.code ?? '' })
        .then((o) => setStatus(o.status))
        .catch(() => {})
    }, 2000)
    return () => clearInterval(t)
  }, [pair, status])

  const copy = () => {
    if (!pair?.url) return
    void navigator.clipboard?.writeText(pair.url).then(() => {
      setCopied(true)
      setTimeout(() => setCopied(false), 1500)
    })
  }
  const base = pair?.url ? pair.url.replace(/#.*$/, '').replace(/\/pair$/, '') : ''

  return (
    <>
      {error && <p className="m-0 text-sub text-danger">{error}</p>}
      <div className="flex flex-wrap items-center gap-6 pt-2">
        {pair?.qr_path ? <QR size={pair.qr_size} path={pair.qr_path} label="連手機的 QR" /> : <span className="flex size-[184px] items-center justify-center rounded-menu bg-inset text-ink-3">{!error && <Icon icon={LoaderCircle} className="animate-spin" />}</span>}
        <div className="flex min-w-[220px] flex-1 flex-col gap-3">
          {(wifi
            ? ['手機連上和這台電腦同一個 Wi-Fi', '用區網位址打開 XBot（例：http://192.168.1.20:8780）再按一次「連手機」，掃新的 QR']
            : ['手機裝 Tailscale App，用和主機同一個帳號登入', '用手機相機掃這個 QR，開啟後就登入了']
          ).map((t, i) => (
            <div key={t} className="flex gap-2.5 text-ui">
              <span className="flex size-6 flex-none items-center justify-center rounded-seg bg-control text-meta">{i + 1}</span>
              {t}
            </div>
          ))}
          {pair?.url && (
            <div className="flex items-center gap-2 rounded-field border border-line bg-inset py-1.5 pl-3 pr-1.5">
              <code className="min-w-0 flex-1 truncate font-mono text-[13px]">{base}</code>
              <button type="button" aria-label="複製網址" onClick={copy} className="flex size-8 items-center justify-center rounded-seg text-ink-2 hover:bg-control">
                <Icon icon={copied ? Check : Copy} size={16} />
              </button>
            </div>
          )}
          {pair?.code && (
            <p className="m-0 text-meta text-ink-2">
              也可以在手機的登入頁輸入配對碼 <span className="font-mono text-ink">{pair.code}</span>
            </p>
          )}
          {pair?.local_only && (
            <p className="m-0 flex gap-2 rounded-field bg-warning-soft px-3 py-2 text-sub text-warning-ink">
              <Icon icon={TriangleAlert} size={16} className="mt-0.5 flex-none" />
              這個網址是 localhost，手機連不到。請在主機開 tailscale serve（或設 XBOT_PUBLIC_URL），用那個網址打開 XBot 再產生 QR。
            </p>
          )}
          <p role="status" className={cx('m-0 flex items-center gap-2 text-sub', status === 'paired' ? 'text-success' : status === 'expired' ? 'text-warning-ink' : 'text-ink-2')}>
            {status === 'paired' ? (
              <>
                <Icon icon={Check} size={16} />
                手機已連上
              </>
            ) : status === 'expired' ? (
              <>
                QR 過期了
                <Button size="sm" onClick={onAgain}>
                  重新產生
                </Button>
              </>
            ) : (
              <>
                <Icon icon={LoaderCircle} size={16} className="animate-spin" />
                等手機掃描 · 5 分鐘內有效、只能用一次
              </>
            )}
          </p>
        </div>
      </div>
      <div className="mt-2.5 flex flex-wrap items-center gap-2 border-t border-divider pt-3">
        <button type="button" onClick={() => setWifi((v) => !v)} className="mr-auto flex min-h-9 items-center gap-2 text-sub text-ink-2 hover:text-ink">
          <Icon icon={Wifi} size={16} />
          {wifi ? '有 Tailscale：用 Tailscale 網址' : '沒有 Tailscale？改用區網'}
        </button>
        <Button onClick={onClose}>完成</Button>
      </div>
    </>
  )
}

/** 安裝到主畫面的一列（Android 直接跳視窗；iPhone 說明怎麼加）。已安裝就不顯示。 */
export function InstallRow({ className }: { className?: string }) {
  const inst = useInstall()
  const [iosHelp, setIosHelp] = useState(false)
  if (inst.installed || (!inst.canPrompt && !inst.ios)) return null
  return (
    <div className={cx('flex flex-col gap-2', className)}>
      <div className="flex items-center gap-3">
        <Icon icon={Smartphone} size={20} />
        <span className="flex min-w-0 flex-1 flex-col">
          <span className="text-ui">安裝到主畫面</span>
          <span className="text-meta text-ink-2">像 App 一樣全螢幕打開，也能收推播</span>
        </span>
        <Button size="sm" icon={inst.ios ? Share : Download} className="min-h-11" onClick={() => (inst.ios ? setIosHelp((v) => !v) : void promptInstall())}>
          安裝
        </Button>
      </div>
      {iosHelp && <p className="m-0 rounded-field bg-inset px-3 py-2 text-sub text-ink-2">在 Safari 按下方的「分享」，再選「加入主畫面」。</p>}
    </div>
  )
}
