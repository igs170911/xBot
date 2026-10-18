import { useEffect, useState } from 'react'
import { ChevronRight, Copy, Check, ExternalLink, Globe, KeyRound, Lock, Network, ShieldAlert, ShieldCheck, Smartphone, TriangleAlert } from 'lucide-react'
import { networkTailscaleSet, type NetworkTailscaleSetInput } from '../api/generated'
import { setNetwork, useNetwork, type NetworkStatus } from '../data/network'
import { useFunnelSwitch } from './useFunnelSwitch'
import { linkClick } from '../router'
import { Button } from '../ui/Button'
import { Switch } from '../ui/Controls'
import { TextInput } from '../ui/Field'
import { Icon } from '../ui/Icon'
import { StatusChip, type Tone } from '../ui/Pill'
import { useToast } from '../ui/toast-context'
import { cx } from '../ui/cx'
import { Block, Group, Note, Row, SectionHeader } from './parts'
import { errText, fieldError } from './format'

// 設定 › 進階 › 網路與 Tailscale（C35；design/J05 手機與其他裝置、P02 用 Tailscale 安全連線、J12 外部入口）。
// 內建 Tailscale（tsnet）：開啟、登入（登入網址或登入金鑰）、名字、tailnet 網址與手機 QR、憑證狀態；
// 外部入口（Funnel）：開關（要確認）、開放的路徑、IP 白名單；反向代理模式（XBOT_PUBLIC_URL）只顯示。

type TS = NetworkStatus['tailscale']
type Funnel = NetworkStatus['funnel']

function tsChip(t: TS): { tone: Tone; text: string } {
  if (!t.enabled) return { tone: 'neutral', text: '關閉' }
  switch (t.state) {
    case 'running':
      return { tone: 'success', text: '已連上 tailnet' }
    case 'needs_login':
      return { tone: 'warning', text: '等你登入' }
    case 'error':
      return { tone: 'danger', text: '有問題' }
    default:
      return { tone: 'accent', text: '連線中…' }
  }
}

function funnelChip(f: Funnel): { tone: Tone; text: string } {
  if (!f.enabled) return { tone: 'neutral', text: '關閉' }
  switch (f.state) {
    case 'on':
      return { tone: 'warning', text: '開放中' }
    case 'error':
      return { tone: 'danger', text: '開不起來' }
    case 'starting':
      return { tone: 'accent', text: '開啟中…' }
    default:
      return { tone: 'neutral', text: '等 Tailscale 連上' }
  }
}

function useCopied(): [string, (k: string, text: string) => void] {
  const [copied, setCopied] = useState('')
  useEffect(() => {
    if (!copied) return
    const t = window.setTimeout(() => setCopied(''), 2000)
    return () => window.clearTimeout(t)
  }, [copied])
  return [
    copied,
    (k, text) => {
      void navigator.clipboard?.writeText(text).catch(() => undefined)
      setCopied(k)
    },
  ]
}

/** QR：服務端編好的點陣（每列一個 0／1 字串），畫成 SVG；白底、四格留白，手機相機才掃得到。 */
export function QRCode({ rows, label, size = 164 }: { rows: string[]; label: string; size?: number }) {
  if (rows.length === 0) return null
  const n = rows.length
  const m = 4
  let d = ''
  rows.forEach((row, y) => {
    for (let x = 0; x < row.length; x++) if (row[x] === '1') d += `M${x + m} ${y + m}h1v1h-1z`
  })
  return (
    <svg role="img" aria-label={label} width={size} height={size} viewBox={`0 0 ${n + 2 * m} ${n + 2 * m}`} shapeRendering="crispEdges" className="flex-none rounded-group">
      <rect width="100%" height="100%" fill="#fff" />
      <path d={d} fill="#000" />
    </svg>
  )
}

function TailnetCard({ s }: { s: NetworkStatus }) {
  const t = s.tailscale
  const [copied, copy] = useCopied()
  const chip = tsChip(t)
  return (
    <section aria-label="用 Tailscale 連" className="flex flex-col gap-4 rounded-card bg-card px-4 py-4">
      <div className="flex items-start gap-3">
        <span className="flex size-9 flex-none items-center justify-center rounded-field bg-control text-ink-2">
          <Icon icon={Network} size={18} />
        </span>
        <span className="flex min-w-0 flex-1 flex-col gap-0.5">
          <span className="flex flex-wrap items-center gap-2">
            <span className="text-ui">用 Tailscale 連</span>
            <StatusChip size="sm" tone="accent">
              建議
            </StatusChip>
          </span>
          <span className="text-meta text-ink-2">在家、在外面都能連；不開公開埠，有正式 HTTPS 憑證，不用裝憑證</span>
        </span>
        <span data-testid="ts-state">
          <StatusChip tone={chip.tone}>{chip.text}</StatusChip>
        </span>
      </div>
      {t.state === 'running' && t.url ? (
        <div className="flex gap-5 max-sm:flex-col max-sm:items-center">
          <div className="flex flex-col items-center gap-1.5">
            <QRCode rows={t.qr} label={`tailnet 網址 ${t.url} 的 QR`} />
            <span className="text-[12px] text-ink-3">用手機相機掃</span>
          </div>
          <div className="flex min-w-0 flex-1 flex-col gap-3 max-sm:self-stretch">
            <div className="flex items-center gap-2 rounded-field border border-line bg-inset px-3 py-2">
              <span className="text-success">
                <Icon icon={Lock} size={16} />
              </span>
              <code data-testid="tailnet-url" className="min-w-0 flex-1 truncate font-mono text-[13.5px]">
                {t.url}
              </code>
              <Button size="sm" variant="ghost" icon={copied === 'url' ? Check : Copy} onClick={() => copy('url', t.url!)} aria-label="複製 tailnet 網址">
                {copied === 'url' ? '已複製' : '複製'}
              </Button>
            </div>
            <ol className="m-0 flex list-none flex-col gap-2 p-0 text-ui-sm">
              {['手機裝 Tailscale App，登入同一個帳號', '用相機掃左邊的 QR，會打開 XBot', '第一次輸入登入碼；之後在哪裡都能用，登入維持 30 天'].map((x, i) => (
                <li key={x} className="flex items-center gap-2.5">
                  <span className="flex size-[22px] flex-none items-center justify-center rounded-full bg-control text-[12px] text-ink-2">{i + 1}</span>
                  {x}
                </li>
              ))}
            </ol>
            <span className="flex flex-wrap gap-x-4 gap-y-1 text-meta text-ink-3">
              <span>{t.user ? `帳號 ${t.user}` : 'tailnet 已連上'}</span>
              <span>tailnet 上其他裝置 {t.devices} 台（{t.online} 台在線上）</span>
              <span>{certText(t)}</span>
            </span>
          </div>
        </div>
      ) : (
        <p className="m-0 text-sub text-ink-2">{t.enabled ? (t.message ?? '連線中…') : '還沒開啟。下面打開「內建 Tailscale」，這台 XBot 就會出現在你的 tailnet 上。'}</p>
      )}
      {t.enabled && t.state === 'running' && (t.cert === 'missing' || t.cert === 'error') && (
        <Note tone="warning" icon={<Icon icon={TriangleAlert} size={18} />}
          action={
            <a href="https://login.tailscale.com/admin/dns" target="_blank" rel="noreferrer" className="flex items-center gap-1 whitespace-nowrap text-ui-sm text-accent-ink">
              打開 DNS 設定 <Icon icon={ExternalLink} size={14} />
            </a>
          }
        >
          {t.cert_message}
        </Note>
      )}
    </section>
  )
}

function certText(t: TS): string {
  switch (t.cert) {
    case 'ready':
      return 'HTTPS 正式憑證 ✓'
    case 'pending':
      return '正在拿 HTTPS 憑證…'
    case 'missing':
      return '沒開 HTTPS 憑證'
    case 'error':
      return 'HTTPS 憑證有問題'
    default:
      return ''
  }
}

function TailscaleSettings({ s }: { s: NetworkStatus }) {
  const t = s.tailscale
  const toast = useToast()
  const [hostname, setHostname] = useState(t.hostname)
  const [key, setKey] = useState('')
  const [busy, setBusy] = useState('')
  const [err, setErr] = useState<unknown>(null)
  const save = async (k: string, input: NetworkTailscaleSetInput, done: string) => {
    setBusy(k)
    setErr(null)
    try {
      setNetwork(await networkTailscaleSet(input))
      toast.show({ kind: 'done', text: done })
      if (input.auth_key) setKey('')
    } catch (e) {
      setErr(e)
      toast.show({ kind: 'error', text: errText(e) })
    } finally {
      setBusy('')
    }
  }
  const nameChanged = hostname.trim() !== t.hostname
  return (
    <Group label="內建 Tailscale">
      <Row title="內建 Tailscale" sub="XBot 自己就是 tailnet 上的一台機器；tailnet 來的連線一樣要登入碼或權杖，不會當成本機的你">
        <Switch
          label="內建 Tailscale"
          checked={t.enabled}
          disabled={!!busy}
          onChange={(v) => void save('enable', { enabled: v }, v ? '內建 Tailscale 已開啟' : '內建 Tailscale 已關閉')}
        />
      </Row>
      {t.enabled && t.state === 'needs_login' && (
        <Row title="登入 tailnet" sub={t.login_url ? '打開這個網址，用你的 Tailscale 帳號核准這台 XBot（只給你自己用，不要傳給別人）' : (t.message ?? '等 Tailscale 給登入網址…')} wrap>
          {t.login_url && (
            <a data-testid="ts-login" href={t.login_url} target="_blank" rel="noreferrer" className="flex h-[30px] items-center gap-1.5 rounded-full bg-ink px-3 text-sub text-on-ink">
              打開登入網址 <Icon icon={ExternalLink} size={14} />
            </a>
          )}
        </Row>
      )}
      <Row title="在 tailnet 上的名字" sub={`網址會是 https://${hostname || 'xbot'}.<你的 tailnet>.ts.net；改了會重新連線`} wrap>
        <span className="flex items-start gap-2">
          <TextInput
            label="在 tailnet 上的名字"
            hideLabel
            value={hostname}
            onChange={(e) => setHostname(e.target.value.toLowerCase())}
            error={fieldError(err, 'hostname')}
            className="w-[160px] font-mono"
            maxLength={63}
          />
          <Button size="sm" variant="secondary" disabled={!nameChanged || !hostname.trim()} loading={busy === 'name'} onClick={() => void save('name', { enabled: t.enabled, hostname: hostname.trim() }, '名字已更新')}>
            儲存
          </Button>
        </span>
      </Row>
      <Row
        title="登入金鑰（選用）"
        sub={
          t.has_auth_key
            ? '已設定（加密存在這台 XBot）。換金鑰會重新連線。'
            : '不想點登入網址時用：到 Tailscale 後台 Settings → Keys 產生 tskey-auth-…。只能寫入，不會再顯示。'
        }
        wrap
      >
        <span className="flex items-start gap-2">
          <TextInput label="Tailscale 登入金鑰" hideLabel type="password" autoComplete="off" placeholder="tskey-auth-…" value={key} onChange={(e) => setKey(e.target.value)} className="w-[200px] font-mono" icon={KeyRound} />
          <Button size="sm" variant="secondary" disabled={!key.trim()} loading={busy === 'key'} onClick={() => void save('key', { enabled: t.enabled, auth_key: key.trim() }, '登入金鑰已儲存')}>
            儲存
          </Button>
          {t.has_auth_key && (
            <Button size="sm" variant="danger" loading={busy === 'clear'} onClick={() => void save('clear', { enabled: t.enabled, clear_auth_key: true }, '登入金鑰已刪除')}>
              刪除
            </Button>
          )}
        </span>
      </Row>
    </Group>
  )
}

function FunnelBlock({ s }: { s: NetworkStatus }) {
  const f = s.funnel
  const funnel = useFunnelSwitch()
  const [ips, setIps] = useState(f.allow_ips.join('\n'))
  const [err, setErr] = useState('')
  const chip = funnelChip(f)
  const tsOn = s.tailscale.enabled
  const list = ips
    .split(/[\s,]+/)
    .map((x) => x.trim())
    .filter(Boolean)
  const changed = list.join(',') !== f.allow_ips.join(',')
  const saveIPs = async () => {
    setErr('')
    try {
      await funnel.set(f.enabled, list)
    } catch (e) {
      setErr(errText(e))
    }
  }
  return (
    <Block title="外部入口（Tailscale Funnel）" sub="讓 tailnet 外的代理、GitHub 等網址觸發連得到 XBot。預設關閉。" aside={<span data-testid="funnel-state"><StatusChip tone={chip.tone}>{chip.text}</StatusChip></span>}>
      <Note tone="warning" icon={<Icon icon={ShieldAlert} size={18} />}>
        開啟後，<b className="font-normal text-ink">網際網路上任何人</b>都連得到下面三個路徑；網頁、對話和其他 API 一律回「找不到」。/mcp 一律要代理權杖（登入碼也不收），每分鐘最多 {s.external_rate_per_min} 次呼叫，比 tailnet 內嚴。
      </Note>
      <Group label="外部入口設定">
        <Row title="開放外部入口" sub={tsOn ? (f.message ?? (f.url ? `網址：${f.url}` : '開啟前會再問你一次')) : '要先開啟上面的內建 Tailscale'}>
          <Switch label="開放外部入口" checked={f.enabled} disabled={!tsOn || funnel.busy} onChange={funnel.toggle} />
        </Row>
        <Row title="開放的路徑" sub="其他路徑一律 404" wrap>
          <span className="flex flex-wrap justify-end gap-1.5 max-sm:justify-start" data-testid="external-paths">
            {s.external_paths.map((p) => (
              <code key={p} className="rounded-field bg-inset px-2 py-0.5 font-mono text-[12.5px] text-ink-2">
                {p}
              </code>
            ))}
          </span>
        </Row>
        <Row title="IP 白名單" sub="一行一個 IP 或網段（例：203.0.113.7、198.51.100.0/24）；空白表示不限。反向代理也適用。" wrap>
          <span className="flex flex-col items-end gap-2 max-sm:items-stretch">
            <textarea
              aria-label="IP 白名單"
              value={ips}
              onChange={(e) => setIps(e.target.value)}
              rows={3}
              placeholder="不限"
              className="w-[240px] resize-y rounded-field border border-line bg-inset px-3 py-2 font-mono text-[13px] text-ink outline-none focus:border-accent max-sm:w-full"
            />
            {err && <span className="text-meta text-danger">{err}</span>}
            <Button size="sm" variant="secondary" disabled={!changed} loading={funnel.busy} onClick={() => void saveIPs()}>
              儲存白名單
            </Button>
          </span>
        </Row>
      </Group>
      {funnel.dialog}
    </Block>
  )
}

function ProxyBlock({ s }: { s: NetworkStatus }) {
  const p = s.proxy
  return (
    <Block title="自己的反向代理（進階）" sub="不用 Funnel，改用你自己的網域＋HTTPS（nginx、Caddy、Cloudflare Tunnel…）。">
      <Group>
        <Row
          wrap
          title={p.enabled ? '反向代理模式：開' : '反向代理模式：關'}
          sub={
            p.enabled
              ? `對外網址 ${p.url}；從反向代理進來的請求和 Funnel 一樣只開放上面三個路徑、要權杖、套白名單。信任的代理：${p.trusted_proxies.join('、')}`
              : '啟動服務時設 XBOT_PUBLIC_URL=https://你的網址（反向代理要帶 X-Forwarded-For；代理不在這台時再設 XBOT_TRUSTED_PROXIES）'
          }
        >
          <StatusChip tone={p.enabled ? 'warning' : 'neutral'}>{p.enabled ? '開放中' : '沒有設定'}</StatusChip>
        </Row>
      </Group>
    </Block>
  )
}

/** /settings/advanced/network */
export function NetworkPage() {
  const { status, error } = useNetwork(3000)
  return (
    <>
      <SectionHeader title="網路與 Tailscale" sub="手機、筆電和外部代理怎麼連到這台 XBot。" />
      {error && !status && <p className="m-0 rounded-group bg-card px-4 py-3 text-ui-sm text-danger">讀不到網路狀態：{error}</p>}
      {!status && !error && <p className="m-0 rounded-group bg-card px-4 py-3 text-ui-sm text-ink-2">讀取中…</p>}
      {status && (
        <>
          <TailnetCard s={status} />
          <TailscaleSettings key={status.tailscale.hostname} s={status} />
          <FunnelBlock key={status.funnel.allow_ips.join(',')} s={status} />
          <ProxyBlock s={status} />
          <p className="m-0 flex items-center gap-1.5 text-meta text-ink-3">
            <Icon icon={ShieldCheck} size={14} />
            本機網址 {status.local_url}：只有這台電腦連得到，不用登入。
          </p>
        </>
      )}
    </>
  )
}

/** 「電腦與裝置」上的入口列（J05）：Tailscale 狀態＋網址，點進網路頁。 */
export function DevicesEntry() {
  const { status } = useNetwork()
  const t = status?.tailscale
  const chip = t ? tsChip(t) : null
  return (
    <Block title="手機與其他裝置">
      <Group>
        <a
          href="/settings/advanced/network"
          onClick={linkClick('/settings/advanced/network')}
          className="flex min-h-14 items-center gap-3 px-4 py-3 text-ink hover:bg-control/50"
        >
          <span className="flex size-9 flex-none items-center justify-center rounded-field bg-control text-ink-2">
            <Icon icon={Smartphone} size={18} />
          </span>
          <span className="flex min-w-0 flex-1 flex-col gap-0.5">
            <span className="text-ui">用 Tailscale 連手機</span>
            <span className={cx('truncate text-[13px] text-ink-2', t?.url && 'font-mono')}>{t?.url ?? '在外面也能連，不開公開埠、不用裝憑證'}</span>
          </span>
          {chip && <StatusChip tone={chip.tone}>{chip.text}</StatusChip>}
          {status?.funnel.state === 'on' && (
            <StatusChip tone="warning" icon={Globe}>
              外部入口開著
            </StatusChip>
          )}
          <span className="text-ink-3">
            <Icon icon={ChevronRight} size={18} />
          </span>
        </a>
      </Group>
    </Block>
  )
}
