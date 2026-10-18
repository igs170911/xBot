import { useEffect, useState } from 'react'
import { Check, FileText, Link2, Send } from 'lucide-react'
import { ApiError, chatSend, filesUploadForm } from '../api/generated'
import { botsStore } from '../data/bots'
import { useStore } from '../data/store'
import { navigate, useSearch } from '../router'
import { Avatar } from '../ui/Avatar'
import { Button } from '../ui/Button'
import { Icon } from '../ui/Icon'
import { cx } from '../ui/cx'
import { PhoneBar } from './Phone'

// Android 分享目標（MB-1）：在別的 App 按「分享 → XBot」，service worker 把文字、網址、檔案先存在快取（xbot-share），
// 帶到這一頁選一個 Bot，檔案上傳到它的 /workspace/uploads，再送一則訊息給它。

type Shared = { title: string; text: string; url: string; files: Array<{ key: string; name: string; type: string; size: number }> }

const SHARE_CACHE = 'xbot-share'

async function readShared(): Promise<{ data: Shared; files: File[] } | null> {
  if (!('caches' in window)) return null
  const cache = await caches.open(SHARE_CACHE)
  const meta = await cache.match('/__share/meta')
  if (!meta) return null
  const data = (await meta.json()) as Shared
  const files: File[] = []
  for (const f of data.files) {
    const r = await cache.match(f.key)
    if (r) files.push(new File([await r.blob()], f.name, { type: f.type }))
  }
  return { data, files }
}

async function clearShared() {
  if ('caches' in window) await caches.delete(SHARE_CACHE)
}

function size(n: number): string {
  return n > 1024 * 1024 ? `${(n / 1024 / 1024).toFixed(1)} MB` : `${Math.max(1, Math.round(n / 1024))} KB`
}

export function SharePage() {
  const q = useSearch()
  const { bots } = useStore(botsStore)
  const live = (bots ?? []).filter((b) => !b.archived)
  const [shared, setShared] = useState<{ data: Shared; files: File[] } | null | undefined>(undefined)
  const [text, setText] = useState('')
  const [picked, setBotId] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const nosw = q.get('nosw') === '1' || q.get('failed') === '1'

  useEffect(() => {
    void readShared()
      .then((s) => {
        setShared(s)
        if (s) setText([s.data.title, s.data.text, s.data.url].filter((x, i, a) => x && a.indexOf(x) === i).join('\n'))
      })
      .catch(() => setShared(null))
  }, [])
  const botId = picked ?? (live.find((b) => b.primary) ?? live[0])?.id ?? null

  const send = async () => {
    if (!botId) return
    setBusy(true)
    setError(null)
    try {
      const files = shared?.files ?? []
      const up = files.length ? await filesUploadForm(botId, files) : []
      const lines = [text.trim(), ...up.map((f) => `（我傳了檔案：${f.path}）`)].filter(Boolean)
      const out = await chatSend({ bot_id: botId, text: lines.join('\n') || '（分享）' })
      await clearShared()
      navigate(`/bots/${botId}/threads/${out.thread_id}`, { replace: true })
    } catch (e) {
      setError(e instanceof ApiError || e instanceof Error ? e.message : String(e))
      setBusy(false)
    }
  }

  return (
    <main className="flex min-h-dvh flex-1 flex-col bg-app" aria-label="分享給 Bot">
      <PhoneBar title="分享給 Bot" back="/chats" />
      <div className="mx-auto flex w-full max-w-[560px] flex-1 flex-col gap-3 px-4 pb-[max(16px,env(safe-area-inset-bottom))]">
        {nosw && <p className="m-0 rounded-field bg-warning-soft px-3.5 py-2.5 text-sub text-warning-ink">剛剛分享的內容沒有收到（XBot 還沒裝好）。請再分享一次，或把內容貼在下面。</p>}
        <label className="flex flex-col gap-1.5">
          <span className="text-sub text-ink-2">要說的話</span>
          <textarea value={text} onChange={(e) => setText(e.target.value)} rows={4} placeholder="例：幫我看看這篇文章的重點" className="w-full resize-none rounded-card border border-line bg-panel px-4 py-3 text-ui text-ink outline-none focus-visible:outline-2 focus-visible:outline-focus" />
        </label>
        {shared?.data.url && (
          <p className="m-0 flex items-center gap-2 truncate text-sub text-accent-ink">
            <Icon icon={Link2} size={16} />
            {shared.data.url}
          </p>
        )}
        {!!shared?.files.length && (
          <ul className="m-0 flex list-none flex-col gap-1 p-0">
            {shared.files.map((f) => (
              <li key={f.name} className="flex items-center gap-2 rounded-field bg-card px-3 py-2 text-sub">
                <Icon icon={FileText} size={16} />
                <span className="min-w-0 flex-1 truncate">{f.name}</span>
                <span className="text-ink-3">{size(f.size)}</span>
              </li>
            ))}
          </ul>
        )}
        <span className="pt-1 text-sub text-ink-2">傳給誰</span>
        <div role="radiogroup" aria-label="傳給誰" className="flex flex-col overflow-hidden rounded-card bg-card">
          {live.map((b) => (
            <button key={b.id} type="button" role="radio" aria-checked={b.id === botId} onClick={() => setBotId(b.id)} className={cx('flex min-h-12 items-center gap-3 border-line px-3.5 py-1.5 text-left not-first:border-t', b.id === botId && 'bg-control')}>
              <Avatar shape={b.shape} color={b.color} dot="none" size={30} blink={false} ring="#262626" />
              <span className="flex min-w-0 flex-1 flex-col">
                <span className="text-ui text-ink">{b.name}</span>
                {b.title && <span className="truncate text-meta text-ink-2">{b.title}</span>}
              </span>
              {b.id === botId && <Icon icon={Check} size={18} color="var(--color-accent)" />}
            </button>
          ))}
          {bots && live.length === 0 && <p className="m-0 px-4 py-3 text-sub text-ink-2">還沒有 Bot，先建一個。</p>}
        </div>
        {error && (
          <p role="alert" className="m-0 text-sub text-danger">
            {error}
          </p>
        )}
        <span className="flex-1" />
        <Button variant="primary" icon={Send} className="min-h-12 w-full" disabled={!botId || (!text.trim() && !shared?.files.length) || shared === undefined} loading={busy} onClick={() => void send()}>
          傳給{live.find((b) => b.id === botId)?.name ?? 'Bot'}
        </Button>
      </div>
    </main>
  )
}
