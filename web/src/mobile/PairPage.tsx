import { useCallback, useEffect, useRef, useState, type FormEvent } from 'react'
import { Check, Monitor, ShieldCheck, Wifi, MessageCircle, Inbox, ChevronLeft } from 'lucide-react'
import { ApiError, pairLogin } from '../api/generated'
import { Mascot } from '../ui/Avatar'
import { Button } from '../ui/Button'
import { TextInput } from '../ui/Field'
import { Icon } from '../ui/Icon'

// L09 連接這支手機：電腦上的 QR 內容是 <網址>/pair#<一次性配對碼>（xbot.devices.pair_start）。
// 手機掃描後打開這一頁，用配對碼換 session（POST /api/v1/auth/pair）就登入了，不用輸入登入碼。
// 直接打開 /pair（沒有配對碼）時：支援 BarcodeDetector 的瀏覽器在頁面裡開相機掃，否則請你用相機 App 掃，也可以手動輸入配對碼。

type BarcodeDetectorLike = { detect: (src: CanvasImageSource) => Promise<Array<{ rawValue: string }>> }
type BarcodeDetectorCtor = new (opts: { formats: string[] }) => BarcodeDetectorLike

function detectorCtor(): BarcodeDetectorCtor | undefined {
  return (window as unknown as { BarcodeDetector?: BarcodeDetectorCtor }).BarcodeDetector
}

function codeFromHash(): string {
  return decodeURIComponent(window.location.hash.replace(/^#/, '')).trim()
}

function errText(err: unknown): string {
  return err instanceof ApiError ? err.message : '連不到 XBot：手機要先裝 Tailscale 並登入和電腦同一個帳號'
}

/** 開相機找 QR（只有支援 BarcodeDetector 的瀏覽器）。找到網址就交給 onFound。 */
function Scanner({ onFound }: { onFound: (text: string) => void }) {
  const video = useRef<HTMLVideoElement>(null)
  const [state, setState] = useState<'idle' | 'on' | 'unsupported' | 'denied'>(() => (detectorCtor() && typeof navigator.mediaDevices?.getUserMedia === 'function' ? 'idle' : 'unsupported'))
  useEffect(() => {
    const Ctor = detectorCtor()
    if (!Ctor || !navigator.mediaDevices?.getUserMedia) return
    let stream: MediaStream | null = null
    let timer: ReturnType<typeof setInterval> | null = null
    let alive = true
    const det = new Ctor({ formats: ['qr_code'] })
    navigator.mediaDevices
      .getUserMedia({ video: { facingMode: 'environment' }, audio: false })
      .then((s) => {
        if (!alive) {
          s.getTracks().forEach((t) => t.stop())
          return
        }
        stream = s
        if (video.current) {
          video.current.srcObject = s
          void video.current.play().catch(() => {})
        }
        setState('on')
        timer = setInterval(() => {
          const v = video.current
          if (!v || v.readyState < 2) return
          det
            .detect(v)
            .then((codes) => {
              const hit = codes.find((c) => c.rawValue)
              if (hit) onFound(hit.rawValue)
            })
            .catch(() => {})
        }, 400)
      })
      .catch(() => setState('denied'))
    return () => {
      alive = false
      if (timer) clearInterval(timer)
      stream?.getTracks().forEach((t) => t.stop())
    }
  }, [onFound])
  return (
    <div className="relative flex aspect-square w-full items-center justify-center overflow-hidden rounded-card bg-panel">
      <video ref={video} muted playsInline className={state === 'on' ? 'absolute inset-0 size-full object-cover' : 'hidden'} />
      {/* 取景框與掃描線 */}
      <span aria-hidden="true" className="relative size-[200px]">
        {['left-0 top-0 border-l-4 border-t-4 rounded-tl-[18px]', 'right-0 top-0 border-r-4 border-t-4 rounded-tr-[18px]', 'bottom-0 left-0 border-b-4 border-l-4 rounded-bl-[18px]', 'bottom-0 right-0 border-b-4 border-r-4 rounded-br-[18px]'].map((c) => (
          <span key={c} className={`absolute size-10 border-white ${c}`} />
        ))}
        <span className="absolute inset-x-3 top-1/2 h-0.5 animate-pulse bg-accent" />
      </span>
      <span className="absolute bottom-4 rounded-full bg-black/70 px-3 py-1.5 text-sub text-white">
        {state === 'on' ? '對準 QR，會自動掃描' : state === 'denied' ? '沒有相機權限：改用相機 App 掃 QR' : state === 'unsupported' ? '用手機的相機 App 掃電腦上的 QR' : '開啟相機中…'}
      </span>
    </div>
  )
}

export function PairPage() {
  const [code, setCode] = useState(codeFromHash)
  const [state, setState] = useState<'idle' | 'working' | 'ok' | 'error'>(() => (codeFromHash() ? 'working' : 'idle'))
  const [error, setError] = useState<string | null>(null)
  const [wifi, setWifi] = useState(false)
  const tried = useRef<string | null>(null)

  const redeem = async (c: string) => {
    const v = c.trim()
    if (!v || tried.current === v) return
    tried.current = v
    setState('working')
    setError(null)
    try {
      await pairLogin(v)
      // 配對碼用過就沒用了，從網址拿掉
      window.history.replaceState(null, '', '/pair')
      setState('ok')
    } catch (err) {
      tried.current = null
      setError(errText(err))
      setState('error')
    }
  }

  const redeemRef = useRef(redeem)
  useEffect(() => {
    redeemRef.current = redeem
  })
  useEffect(() => {
    // 只在打開時用網址裡的配對碼換一次
    const c = codeFromHash()
    if (c) void Promise.resolve().then(() => redeemRef.current(c))
  }, [])

  const onFound = useCallback((text: string) => {
    try {
      const u = new URL(text)
      if (u.origin !== window.location.origin) {
        window.location.href = text // 另一台 XBot（例：Tailscale 網址）：直接打開
        return
      }
      const c = decodeURIComponent(u.hash.replace(/^#/, ''))
      if (c) {
        setCode(c)
        void redeemRef.current(c)
      }
    } catch {
      // 不是網址的 QR：不理它
    }
  }, [])

  const submit = (e: FormEvent) => {
    e.preventDefault()
    void redeem(code)
  }

  if (state === 'ok')
    return (
      <main className="flex min-h-dvh flex-col gap-5 bg-app px-4 pb-[max(24px,env(safe-area-inset-bottom))] pt-[max(48px,env(safe-area-inset-top))]">
        <div className="flex flex-col items-center gap-3 text-center">
          <span className="flex size-16 items-center justify-center rounded-full bg-success-soft text-success">
            <Icon icon={Check} size={32} strokeWidth={2.25} />
          </span>
          <h1 className="m-0 text-page">已連上 {window.location.host}</h1>
          <p className="m-0 text-sub text-ink-2">這支手機會記住 30 天，之後打開就能用。</p>
        </div>
        <ul className="m-0 flex list-none flex-col overflow-hidden rounded-card bg-card p-0">
          {[
            { icon: MessageCircle, t: '聊天', s: '交代 Bot 做事，看它們之間的往來' },
            { icon: Inbox, t: '核准', s: '付款、登入、刪除這些事，一按就決定' },
            { icon: Monitor, t: '看電腦', s: '看 Bot 的電腦畫面，需要時接手' },
          ].map((x) => (
            <li key={x.t} className="flex items-center gap-3 border-divider px-4 py-3 not-first:border-t">
              <Icon icon={x.icon} size={22} />
              <span className="flex flex-col">
                <span className="text-ui">{x.t}</span>
                <span className="text-sub text-ink-2">{x.s}</span>
              </span>
            </li>
          ))}
        </ul>
        <span className="flex-1" />
        <Button variant="primary" className="min-h-12 w-full" onClick={() => window.location.replace('/')}>
          開始使用
        </Button>
      </main>
    )

  return (
    <main className="flex min-h-dvh flex-col gap-4 bg-app px-4 pb-[max(16px,env(safe-area-inset-bottom))]" aria-label="連接這支手機">
      <header className="flex min-h-[60px] items-center gap-3 pt-[max(8px,env(safe-area-inset-top))]">
        {wifi ? (
          <button type="button" aria-label="返回" onClick={() => setWifi(false)} className="flex size-11 items-center justify-center rounded-full border border-divider bg-panel text-ink">
            <Icon icon={ChevronLeft} size={22} />
          </button>
        ) : (
          <Mascot size={28} />
        )}
        <h1 className="m-0 text-title">{wifi ? '同一個 Wi-Fi（兩步）' : '連接這支手機'}</h1>
      </header>
      {wifi ? (
        <div className="flex flex-col gap-3">
          <p className="m-0 rounded-field bg-warning-soft px-3.5 py-2.5 text-sub text-warning-ink">只在同一個 Wi-Fi 有效；離開家裡或辦公室就連不到。</p>
          <ol className="m-0 flex flex-col gap-3 pl-5 text-ui text-ink">
            <li>手機連上和電腦同一個 Wi-Fi。</li>
            <li>在電腦上用區網位址打開 XBot（例：http://192.168.1.20:8780），到「設定 › 電腦與裝置」按「連手機」。</li>
            <li>用手機相機掃電腦上的 QR，或在下面輸入電腦上顯示的配對碼。</li>
          </ol>
          <form onSubmit={submit} className="flex items-start gap-2 pt-2">
            <TextInput label="配對碼" wrapperClassName="flex-1" value={code} onChange={(e) => setCode(e.target.value)} placeholder="XXXX-XXXX" autoComplete="one-time-code" error={error ?? undefined} />
            <Button variant="primary" type="submit" className="mt-[22px] min-h-11" loading={state === 'working'}>
              連接
            </Button>
          </form>
        </div>
      ) : (
        <>
          <p className="m-0 text-ui text-ink-2">在電腦上打開「設定 › 電腦與裝置 › 手機」，把畫面上的 QR 放進框裡。</p>
          {state === 'working' ? (
            <div className="flex aspect-square w-full flex-col items-center justify-center gap-3 rounded-card bg-panel text-ink-2" role="status">
              <Mascot size={48} pose="working" />
              正在連線…
            </div>
          ) : (
            <Scanner onFound={onFound} />
          )}
          {error && (
            <p role="alert" className="m-0 rounded-field bg-danger-soft px-3.5 py-2.5 text-sub text-danger">
              {error}
            </p>
          )}
          <p className="m-0 flex items-start gap-2.5 rounded-card bg-card px-3.5 py-3 text-sub text-ink-2">
            <Icon icon={ShieldCheck} size={18} className="mt-0.5 flex-none text-success" />
            手機先裝 Tailscale，登入和電腦同一個帳號。走正式 HTTPS 憑證，不用另外安裝任何憑證。
          </p>
          <form onSubmit={submit} className="flex items-start gap-2">
            <TextInput label="或輸入配對碼" wrapperClassName="flex-1" value={code} onChange={(e) => setCode(e.target.value)} placeholder="XXXX-XXXX" autoComplete="one-time-code" />
            <Button type="submit" className="mt-[22px] min-h-11" disabled={!code.trim()}>
              連接
            </Button>
          </form>
          <span className="flex-1" />
          <button type="button" onClick={() => setWifi(true)} className="flex min-h-11 items-center justify-center gap-2 text-ui text-accent-ink">
            <Icon icon={Wifi} size={18} />
            沒有 Tailscale？改用同一個 Wi-Fi
          </button>
        </>
      )}
    </main>
  )
}
