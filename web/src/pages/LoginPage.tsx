import { useEffect, useState, type FormEvent } from 'react'
import { ApiError, authStatus, login } from '../api/generated'
import { Mascot } from '../ui/Avatar'
import { Button } from '../ui/Button'
import { TextInput } from '../ui/Field'

// 登入（design/A01-Login 的登入碼部分）：從其他裝置打開 XBot 時要輸入登入碼。
// 掃 QR 允許登入、區網兩步要等手機版（W31）與裝置管理（J05）。
export function LoginPage({ onLoggedIn }: { onLoggedIn: () => void }) {
  const [token, setToken] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  // 經內建 Tailscale 連進來時，提示是哪個 Tailscale 帳號（只是提示；tailnet 來的一樣要登入碼，C35）。
  const [tsUser, setTsUser] = useState<string | null>(null)
  useEffect(() => {
    authStatus()
      .then((s) => setTsUser(s.tailnet ? (s.tailscale_user ?? '') : null))
      .catch(() => undefined)
  }, [])

  const submit = async (e: FormEvent) => {
    e.preventDefault()
    if (!token.trim()) {
      setError('請輸入登入碼')
      return
    }
    setBusy(true)
    setError(null)
    try {
      await login(token.trim())
      onLoggedIn()
    } catch (err) {
      // 服務的錯誤訊息已經是給人看的白話（登入碼不對、試太多次…）
      setError(err instanceof ApiError ? err.message : '連不到服務，請再試一次')
      setBusy(false)
    }
  }

  return (
    <main className="flex min-h-dvh items-center justify-center bg-app px-4 py-10">
      <form onSubmit={submit} aria-label="登入" className="flex w-full max-w-[480px] flex-col gap-4 rounded-card border border-divider bg-panel px-8 pb-8 pt-10 max-sm:px-5">
        <div className="flex flex-col items-center gap-3 text-center">
          <Mascot size={48} />
          <h1 className="m-0 text-page">登入 XBot</h1>
          <p className="m-0 text-sub text-ink-2">在主機上執行 make token（或 xbot token）取得登入碼。</p>
          {tsUser !== null && (
            <p data-testid="tailnet-hint" className="m-0 rounded-field bg-accent-soft px-3 py-1.5 text-meta text-accent-ink">
              {tsUser ? `你經 Tailscale 連進來（${tsUser}）` : '你經 Tailscale 連進來'}；第一次在這台裝置用，還是要輸入登入碼。
            </p>
          )}
        </div>
        <div className="flex items-start gap-2">
          <TextInput
            label="登入碼"
            wrapperClassName="flex-1"
            value={token}
            onChange={(e) => setToken(e.target.value)}
            autoComplete="one-time-code"
            autoFocus
            error={error ?? undefined}
            hint="登入後這台裝置會記住 30 天。"
          />
          <Button variant="primary" type="submit" loading={busy} className="mt-[22px]">
            登入
          </Button>
        </div>
      </form>
    </main>
  )
}
