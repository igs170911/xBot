import { useEffect, useRef, useState } from 'react'
import { CircleCheck, Copy, ExternalLink, FlaskConical, TriangleAlert } from 'lucide-react'
import { modelsOauthComplete, modelsOauthStart, type ModelsOauthStartOutput } from '../api/generated'
import { Button } from '../ui/Button'
import { Checkbox } from '../ui/Controls'
import { Dialog } from '../ui/Dialog'
import { Icon } from '../ui/Icon'
import { StatusChip } from '../ui/Pill'
import { Block, Group } from './parts'
import { errText } from './format'
import type { Provider } from './modelData'

// ChatGPT 方案的訂閱登入（E13 原型，實驗性）：裝置代碼流程。
// 服務端拿一組代碼 → 你在自己的瀏覽器打開網址輸入代碼 → 這裡每隔 interval 秒問一次 oauth_complete。
// 權杖只存在 Model Proxy，不會進 Bot 的電腦。

export const OAUTH_RISK =
  '這個方式借用 Codex CLI 的登入呼叫 ChatGPT，不是 OpenAI 公開給第三方的用法，可能違反使用條款，帳號有被限制的風險。能用 API 金鑰的話，建議用金鑰。'

type Phase = { kind: 'consent' } | { kind: 'starting' } | { kind: 'waiting'; login: ModelsOauthStartOutput } | { kind: 'done'; label: string } | { kind: 'error'; message: string }

export function OAuthCard({ providers, onDone }: { providers: Provider[]; onDone: () => Promise<void> }) {
  const [open, setOpen] = useState(false)
  const existing = providers.find((p) => p.auth === 'oauth_codex')
  return (
    <Block title="用 ChatGPT 方案登入" aside={<StatusChip tone="warning" icon={FlaskConical}>實驗性</StatusChip>}>
      <Group>
        <div className="flex items-center gap-4 px-4 py-3 max-sm:flex-col max-sm:items-stretch">
          <span className="min-w-0 flex-1">
            <span className="block text-ui">ChatGPT Plus／Pro／Business</span>
            <span className="block text-[13px] text-ink-2">{existing ? '已登入。重新登入會換掉舊的登入。' : '沒有 API 金鑰時，可以用 ChatGPT 方案的額度。'}</span>
          </span>
          <Button onClick={() => setOpen(true)}>{existing ? '重新登入' : '登入'}</Button>
        </div>
      </Group>
      {open && (
        <OAuthDialog
          onClose={() => setOpen(false)}
          onDone={async () => {
            await onDone()
          }}
        />
      )}
    </Block>
  )
}

export function OAuthDialog({ onClose, onDone }: { onClose: () => void; onDone: () => Promise<void> }) {
  const [agree, setAgree] = useState(false)
  const [phase, setPhase] = useState<Phase>({ kind: 'consent' })
  const [copied, setCopied] = useState(false)
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)
  const alive = useRef(true)
  useEffect(
    () => () => {
      alive.current = false
      clearTimeout(timer.current)
    },
    [],
  )

  const poll = (login: ModelsOauthStartOutput) => {
    timer.current = setTimeout(
      async () => {
        if (!alive.current) return
        if (Date.now() > login.expires_at) {
          setPhase({ kind: 'error', message: '代碼過期了，請重新開始。' })
          return
        }
        try {
          const r = await modelsOauthComplete({ login_id: login.login_id })
          if (!alive.current) return
          if (r.status === 'done') {
            setPhase({ kind: 'done', label: r.provider?.label || r.provider?.id || 'ChatGPT' })
            await onDone()
          } else poll(login)
        } catch (e) {
          if (alive.current) setPhase({ kind: 'error', message: errText(e) })
        }
      },
      Math.max(1, login.interval_seconds) * 1000,
    )
  }

  const start = async () => {
    setPhase({ kind: 'starting' })
    try {
      const login = await modelsOauthStart({ kind: 'openai-codex' })
      if (!alive.current) return
      setPhase({ kind: 'waiting', login })
      poll(login)
    } catch (e) {
      setPhase({ kind: 'error', message: errText(e) })
    }
  }

  const copy = async (code: string) => {
    try {
      await navigator.clipboard.writeText(code)
      setCopied(true)
    } catch {
      setCopied(false)
    }
  }

  const waiting = phase.kind === 'waiting' ? phase.login : null
  return (
    <Dialog
      open
      onClose={onClose}
      title="用 ChatGPT 方案登入（實驗性）"
      width={520}
      actions={
        phase.kind === 'consent' || phase.kind === 'starting' ? (
          <>
            <Button onClick={onClose}>取消</Button>
            <Button variant="primary" disabled={!agree} loading={phase.kind === 'starting'} onClick={start}>
              開始登入
            </Button>
          </>
        ) : phase.kind === 'error' ? (
          <>
            <Button onClick={onClose}>關閉</Button>
            <Button variant="primary" onClick={start}>
              重新開始
            </Button>
          </>
        ) : (
          <Button variant={phase.kind === 'done' ? 'primary' : 'secondary'} onClick={onClose}>
            {phase.kind === 'done' ? '完成' : '取消'}
          </Button>
        )
      }
    >
      {(phase.kind === 'consent' || phase.kind === 'starting') && (
        <div className="flex flex-col gap-3">
          <div className="flex gap-2.5 rounded-group bg-warning-soft px-3.5 py-3 text-ui-sm">
            <span className="pt-0.5 text-warning-ink">
              <Icon icon={TriangleAlert} size={18} />
            </span>
            <span>{OAUTH_RISK}</span>
          </div>
          <p className="m-0 text-ui-sm text-ink-2">登入後的權杖加密存在這台主機的 Model Proxy，不會進 Bot 的電腦。之後可以在供應商清單移除。</p>
          <Checkbox checked={agree} onChange={setAgree}>
            我了解風險，還是要用 ChatGPT 方案登入
          </Checkbox>
        </div>
      )}
      {waiting && (
        <div className="flex flex-col gap-3" aria-live="polite">
          <p className="m-0 text-ui-sm">1. 用你自己的瀏覽器打開登入頁：</p>
          <a href={waiting.verification_url} target="_blank" rel="noreferrer" className="flex items-center gap-1.5 break-all text-ui-sm">
            {waiting.verification_url}
            <Icon icon={ExternalLink} size={14} />
          </a>
          <p className="m-0 text-ui-sm">2. 輸入這組代碼：</p>
          <div className="flex items-center gap-3">
            <code data-testid="oauth-code" className="rounded-field bg-inset px-4 py-2 font-mono text-[22px] tracking-[0.15em]">
              {waiting.user_code}
            </code>
            <Button size="sm" icon={Copy} onClick={() => copy(waiting.user_code)}>
              {copied ? '已複製' : '複製'}
            </Button>
          </div>
          <p role="status" className="m-0 text-meta text-ink-3">
            等你在瀏覽器完成登入…（這個視窗會自己更新）
          </p>
        </div>
      )}
      {phase.kind === 'done' && (
        <p role="status" className="m-0 flex items-center gap-2 text-ui">
          <Icon icon={CircleCheck} color="var(--color-success)" />
          登入完成，已加入 {phase.label}。記得在「預設模型」選用它，或給個別 Bot 用。
        </p>
      )}
      {phase.kind === 'error' && (
        <p role="alert" className="m-0 text-ui-sm text-danger">
          {phase.message}
        </p>
      )}
    </Dialog>
  )
}
