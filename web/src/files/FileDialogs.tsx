import { useState } from 'react'
import { Send } from 'lucide-react'
import { filesDelete, filesMove, filesMoveBetweenBots } from '../api/generated'
import { botsStore } from '../data/bots'
import { useStore } from '../data/store'
import { errText } from '../settings/format'
import { Avatar } from '../ui/Avatar'
import { Button } from '../ui/Button'
import { ConfirmDialog, Dialog } from '../ui/Dialog'
import { TextInput } from '../ui/Field'
import { useToast } from '../ui/toast-context'
import { cx } from '../ui/cx'
import { parentOf, type FileEntry } from './data'
import type { FileDialog } from './menu'

// 檔案的動作：在對話中提到、傳給其他 Bot、改名或搬移、刪除（C33；E07、L19）。

/** 檔案動作的對話框（fileMenu 打開哪一個就畫哪一個）。onChanged：搬移或刪除之後（新的路徑；刪除時是 null）。 */
export function FileDialogs({ botId, entry, dlg, onClose, onChanged }: { botId: string; entry: FileEntry | null; dlg: FileDialog; onClose: () => void; onChanged: (path: string | null) => void }) {
  if (!entry || !dlg) return null
  if (dlg === 'send') return <SendDialog botId={botId} entry={entry} onClose={onClose} />
  if (dlg === 'move')
    return (
      <MoveDialog
        botId={botId}
        entry={entry}
        onClose={onClose}
        onMoved={(p) => {
          onClose()
          onChanged(p)
        }}
      />
    )
  return (
    <DeleteDialog
      botId={botId}
      entry={entry}
      onClose={onClose}
      onDeleted={() => {
        onClose()
        onChanged(null)
      }}
    />
  )
}

/** 傳給其他 Bot：放到對方電腦的 /workspace/from/<你這個 Bot>/（和 Bot 之間帶檔相同）。 */
function SendDialog({ botId, entry, onClose }: { botId: string; entry: FileEntry; onClose: () => void }) {
  const bots = useStore(botsStore).bots ?? []
  const others = bots.filter((b) => b.id !== botId && !b.archived)
  const from = bots.find((b) => b.id === botId)
  const [to, setTo] = useState('')
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')
  const toast = useToast()
  const send = async () => {
    setBusy(true)
    setErr('')
    try {
      const out = await filesMoveBetweenBots({ bot_id: botId, to_bot_id: to, paths: [entry.path] })
      const name = bots.find((b) => b.id === to)?.name ?? '對方'
      if (out.files.length) {
        toast.show({ kind: 'done', text: out.files[0].reused ? `${name}已經有一樣的檔案，不用再傳` : `已傳給${name}：${out.files[0].to}` })
        onClose()
      } else setErr(out.skipped[0]?.reason ?? '沒有傳過去')
    } catch (e) {
      setErr(errText(e))
    } finally {
      setBusy(false)
    }
  }
  return (
    <Dialog
      open
      onClose={onClose}
      busy={busy}
      title="傳給其他 Bot"
      description={`複製「${entry.name}」到對方電腦的 /workspace/from/${from?.name ?? '寄件者'}/，原本的檔案留著。`}
      actions={
        <>
          <Button variant="ghost" onClick={onClose} disabled={busy}>
            取消
          </Button>
          <Button variant="primary" icon={Send} onClick={() => void send()} disabled={!to || busy}>
            傳過去
          </Button>
        </>
      }
    >
      {others.length === 0 ? (
        <p className="m-0 text-ui-sm text-ink-2">還沒有其他 Bot。</p>
      ) : (
        <div role="radiogroup" aria-label="傳給哪個 Bot" className="flex max-h-72 flex-col gap-1 overflow-auto">
          {others.map((b) => (
            <button
              key={b.id}
              type="button"
              role="radio"
              aria-checked={to === b.id}
              onClick={() => setTo(b.id)}
              className={cx('flex min-h-12 items-center gap-2.5 rounded-menu px-2.5 text-left text-ink', to === b.id ? 'bg-control' : 'hover:bg-control/60')}
            >
              <Avatar shape={b.shape} color={b.color} dot="none" size={30} ring="#111111" />
              <span className="flex min-w-0 flex-col">
                <span className="truncate text-body">{b.name}</span>
                {b.title && <span className="truncate text-sub text-ink-2">{b.title}</span>}
              </span>
            </button>
          ))}
        </div>
      )}
      {err && (
        <p role="alert" className="m-0 text-sub text-danger">
          {err}
        </p>
      )}
    </Dialog>
  )
}

function MoveDialog({ botId, entry, onClose, onMoved }: { botId: string; entry: FileEntry; onClose: () => void; onMoved: (p: string) => void }) {
  const [to, setTo] = useState(entry.path)
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')
  const move = async () => {
    setBusy(true)
    setErr('')
    try {
      const target = to.startsWith('/') ? to : `${parentOf(entry.path)}/${to}`
      const out = await filesMove({ bot_id: botId, from: entry.path, to: target })
      onMoved(out.path)
    } catch (e) {
      setErr(errText(e))
    } finally {
      setBusy(false)
    }
  }
  return (
    <Dialog
      open
      onClose={onClose}
      busy={busy}
      title="改名或搬移"
      description="改最後的名稱就是改名；改前面的資料夾就是搬過去（沒有的資料夾會建立）。"
      actions={
        <>
          <Button variant="ghost" onClick={onClose} disabled={busy}>
            取消
          </Button>
          <Button variant="primary" onClick={() => void move()} disabled={busy || !to.trim() || to === entry.path}>
            確定
          </Button>
        </>
      }
    >
      <TextInput label="新的位置" value={to} onChange={(e) => setTo(e.target.value)} onKeyDown={(e) => e.key === 'Enter' && void move()} autoFocus />
      {err && (
        <p role="alert" className="m-0 text-sub text-danger">
          {err}
        </p>
      )}
    </Dialog>
  )
}

function DeleteDialog({ botId, entry, onClose, onDeleted }: { botId: string; entry: FileEntry; onClose: () => void; onDeleted: () => void }) {
  const [busy, setBusy] = useState(false)
  const toast = useToast()
  const del = async () => {
    setBusy(true)
    try {
      await filesDelete({ bot_id: botId, path: entry.path })
      toast.show({ kind: 'done', text: `已刪除「${entry.name}」` })
      onDeleted()
    } catch (e) {
      toast.show({ kind: 'error', text: errText(e) })
      onClose()
    } finally {
      setBusy(false)
    }
  }
  return (
    <ConfirmDialog
      open
      danger
      busy={busy}
      title={`刪除「${entry.name}」？`}
      description={entry.dir ? '整個資料夾和裡面的檔案都會刪掉，不能復原。' : '刪掉就不能復原。'}
      confirmText="刪除"
      onConfirm={() => void del()}
      onCancel={onClose}
    />
  )
}
