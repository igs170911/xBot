import { useEffect, useState } from 'react'
import { Check, Copy, ExternalLink, KeyRound, MonitorSmartphone, ShieldCheck, TriangleAlert } from 'lucide-react'
import { appsCancelSignin, appsFinishSignin, appsSigninStatus, type AppsConnectOutput } from '../api/generated'
import { Button } from '../ui/Button'
import { Dialog } from '../ui/Dialog'
import { TextInput } from '../ui/Field'
import { Icon } from '../ui/Icon'
import { Select } from '../ui/Menu'
import { errText } from '../settings/format'
import { AppIcon } from './shared'
import { useCopy } from '../chat/useCopy'

export type Signin = NonNullable<AppsConnectOutput['signin']>

// I03：在這台裝置（或 Bot 的電腦）登入。XBot 自己跑 OAuth，登入完成會回到 XBot；
// 回呼頁打不開時（在別的裝置、或 Bot 的電腦裡登入）把網址列整串貼回來。
export function SigninDialog({
  title,
  icon,
  signin,
  bots,
  onBotSignin,
  onDone,
  onClose,
}: {
  title: string
  icon?: string
  signin: Signin | null
  /** 有給時可以改把登入頁開在某個 Bot 的電腦上（B14 接手）。 */
  bots?: { id: string; name: string }[]
  onBotSignin?: (botId: string) => Promise<void>
  onDone: () => void
  onClose: () => void
}) {
  const [onBot, setOnBot] = useState('')
  const [pasted, setPasted] = useState('')
  const [err, setErr] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const flow = signin?.flow_id

  // 每 2 秒查一次：你在登入頁按了允許，XBot 收到回呼就會完成。
  useEffect(() => {
    if (!flow) return
    let stop = false
    const tick = async () => {
      try {
        const st = await appsSigninStatus({ flow_id: flow })
        if (stop) return
        if (st.status === 'done') onDone()
        else if (st.status === 'error' || st.status === 'gone') setErr(st.error ?? '登入沒有完成')
      } catch {
        /* 暫時連不到服務：下一輪再查 */
      }
    }
    const id = window.setInterval(() => void tick(), 2000)
    return () => {
      stop = true
      window.clearInterval(id)
    }
  }, [flow, onDone])

  const finish = async () => {
    setBusy(true)
    setErr(null)
    try {
      const st = await appsFinishSignin({ url: pasted })
      if (st.status === 'done') onDone()
      else setErr(st.error ?? '登入沒有完成，請重新連接')
    } catch (e) {
      setErr(errText(e))
    } finally {
      setBusy(false)
    }
  }

  const cancel = () => {
    if (flow) void appsCancelSignin({ flow_id: flow }).catch(() => {})
    onClose()
  }

  return (
    <Dialog open={!!signin} onClose={cancel} width={900} title={`登入 ${title}`} description="按「開啟登入頁」，在登入頁按「允許存取」就完成了。">
      <div className="flex gap-4 max-md:flex-col">
        <section aria-label="在這台裝置登入" className="flex min-w-0 flex-1 flex-col rounded-[14px] bg-inset">
          <div className="flex flex-1 flex-col items-center justify-center gap-4 px-6 py-10 text-center">
            <div className="flex items-center gap-3 text-ink-3">
              <AppIcon name="plug" size={44} />
              <span aria-hidden="true">→</span>
              <AppIcon name={icon} size={44} />
            </div>
            <p className="m-0 text-title">XBot 想存取你的 {title}</p>
            <p className="m-0 max-w-[420px] text-sub text-ink-2">
              登入頁會開在新分頁。登入、選好工作區後按「允許存取」，這裡會自動繼續。
              {signin?.bot_id ? ' 登入頁也已經開在 Bot 的電腦上，你可以直接在即時畫面操作。' : ''}
            </p>
            <Button variant="primary" icon={ExternalLink} onClick={() => signin && window.open(signin.auth_url, '_blank', 'noopener')}>
              開啟登入頁
            </Button>
            {onBotSignin && bots && bots.length > 0 && !signin?.bot_id && (
              <div className="flex items-center gap-2">
                <Select
                  label="改在 Bot 的電腦登入"
                  hideLabel
                  look="compact"
                  placeholder="或在 Bot 的電腦登入…"
                  value={onBot}
                  options={bots.map((b) => ({ value: b.id, label: `在${b.name}的電腦登入` }))}
                  onChange={(v) => {
                    setOnBot(v)
                    onBotSignin(v).catch((e) => setErr(errText(e)))
                  }}
                />
              </div>
            )}
          </div>
          <div className="flex items-center justify-between gap-3 border-0 border-t border-solid border-divider px-4 py-3">
            {err ? (
              <span role="alert" className="text-ui-sm text-danger">
                {err}
              </span>
            ) : (
              <span className="flex items-center gap-2 text-ui-sm text-warning-ink" role="status">
                <span aria-hidden="true" className="size-2 animate-pulse rounded-full bg-warning" />
                等你登入…完成後會自動繼續
              </span>
            )}
            <Button size="sm" onClick={cancel}>
              取消登入
            </Button>
          </div>
        </section>
        <aside aria-label="畫面上登入不了" className="flex w-[300px] flex-none flex-col gap-3 rounded-[14px] bg-inset p-4 max-md:w-auto">
          <h3 className="m-0 flex items-center gap-2 text-name font-normal">
            <Icon icon={MonitorSmartphone} size={18} />
            登入頁打不開回來？
          </h3>
          <p className="m-0 text-sub text-ink-2">例如在別的裝置、或在 Bot 的電腦上登入。3 步：</p>
          <ol className="m-0 flex list-none flex-col gap-3 p-0 text-ui-sm">
            <li className="flex gap-2.5">
              <Step n={1} />
              <span>在那台裝置打開登入頁，登入後按「允許存取」。</span>
            </li>
            <li className="flex gap-2.5">
              <Step n={2} />
              <span>最後會停在打不開的頁面（正常），把網址列整串複製下來。</span>
            </li>
            <li className="flex flex-col gap-2">
              <span className="flex gap-2.5">
                <Step n={3} />
                <span>貼到這裡：</span>
              </span>
              <TextInput
                label="登入後的網址"
                hideLabel
                placeholder={signin?.redirect_uri ? `${signin.redirect_uri}?code=…` : 'http://…/callback?code=…'}
                value={pasted}
                onChange={(e) => setPasted(e.target.value)}
                className="font-mono text-meta"
              />
            </li>
          </ol>
          <Button variant="primary" disabled={!pasted.trim() || busy} onClick={() => void finish()}>
            完成登入
          </Button>
          <p className="m-0 mt-auto flex gap-2 border-0 border-t border-solid border-divider pt-3 text-meta text-ink-2">
            <Icon icon={ShieldCheck} size={16} />
            登入資料加密存在這台主機，Bot 和模型都看不到你的密碼或權杖。
          </p>
        </aside>
      </div>
    </Dialog>
  )
}

function Step({ n }: { n: number }) {
  return (
    <span aria-hidden="true" className="inline-flex size-6 flex-none items-center justify-center rounded-full bg-control text-meta">
      {n}
    </span>
  )
}

// 要貼金鑰（GitHub）或自己的 OAuth 用戶端 ID（Slack）的應用程式。
export function KeyDialog({
  open,
  title,
  kind,
  hint,
  link,
  redirectUri,
  onSubmit,
  onClose,
}: {
  open: boolean
  title: string
  kind: 'api_key' | 'client'
  hint?: string
  link?: string
  /** 自己的 OAuth 用戶端要登記的「已授權的重新導向 URI」（XBot 的回呼網址）。 */
  redirectUri?: string
  onSubmit: (a: string, b: string) => Promise<void>
  onClose: () => void
}) {
  const [copied, copy] = useCopy()
  // Google 等服務只接受 https 或 localhost 的回呼網址：用區網 IP 的 http 打開 XBot 會被拒絕
  const plainHttp = !!redirectUri && /^http:\/\/(?!localhost[:/]|127\.0\.0\.1[:/])/.test(redirectUri)
  const [a, setA] = useState('')
  const [b, setB] = useState('')
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string | null>(null)
  const submit = async () => {
    setBusy(true)
    setErr(null)
    try {
      await onSubmit(a.trim(), b.trim())
      setA('')
      setB('')
    } catch (e) {
      setErr(errText(e))
    } finally {
      setBusy(false)
    }
  }
  return (
    <Dialog
      open={open}
      onClose={onClose}
      busy={busy}
      title={`連接 ${title}`}
      description={kind === 'api_key' ? `貼上 ${hint ?? '金鑰'}。金鑰加密存在這台主機，不會進 Bot 的電腦。` : `${title} 不開放自動註冊，要你自己的 ${hint ?? 'OAuth 用戶端'}。`}
      actions={
        <>
          <Button onClick={onClose}>取消</Button>
          <Button variant="primary" icon={KeyRound} disabled={!a || busy} onClick={() => void submit()}>
            {kind === 'api_key' ? '連接' : '下一步：登入'}
          </Button>
        </>
      }
    >
      <div className="flex flex-col gap-3">
        <TextInput
          label={kind === 'api_key' ? (hint ?? '金鑰') : 'Client ID'}
          type={kind === 'api_key' ? 'password' : 'text'}
          autoComplete="off"
          value={a}
          onChange={(e) => setA(e.target.value)}
          error={err ?? undefined}
        />
        {kind === 'client' && <TextInput label="Client Secret（有的話）" type="password" autoComplete="off" value={b} onChange={(e) => setB(e.target.value)} />}
        {kind === 'client' && redirectUri && (
          <div className="flex flex-col gap-1.5">
            <span className="text-meta text-ink-2">建立用戶端時，「已授權的重新導向 URI」填這個：</span>
            <div className="flex items-center gap-2 rounded-field bg-inset px-3 py-2">
              <code className="min-w-0 flex-1 break-all font-mono text-[12.5px] text-ink">{redirectUri}</code>
              <Button size="sm" variant="ghost" icon={copied ? Check : Copy} onClick={() => void copy(redirectUri)}>
                {copied ? '已複製' : '複製'}
              </Button>
            </div>
            {plainHttp && (
              <p className="m-0 flex items-start gap-2 text-meta text-warning-ink">
                <Icon icon={TriangleAlert} size={14} className="mt-0.5 flex-none" />
                Google 等服務只接受 https 或 localhost 的網址。請在架 XBot 的那台電腦用 http://localhost 打開 XBot 再連接，或改用「設定 › 進階 › 網路與 Tailscale」的 https 網址。
              </p>
            )}
          </div>
        )}
        {link && (
          <a href={link} target="_blank" rel="noreferrer" className="text-ui-sm">
            去哪裡拿？
          </a>
        )}
      </div>
    </Dialog>
  )
}
