import { useState } from 'react'
import { filesMkdir } from '../api/generated'
import { errText } from '../settings/format'
import { Button } from '../ui/Button'
import { Dialog } from '../ui/Dialog'
import { TextInput } from '../ui/Field'

/** 新資料夾。 */
export function NewFolderDialog({ botId, parent, onClose, onCreated }: { botId: string; parent: string; onClose: () => void; onCreated: (p: string) => void }) {
  const [name, setName] = useState('')
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')
  const create = async () => {
    const n = name.trim()
    if (!n) return
    setBusy(true)
    setErr('')
    try {
      const out = await filesMkdir({ bot_id: botId, path: `${parent}/${n}` })
      onCreated(out.path)
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
      title="新資料夾"
      description={`建在 ${parent}`}
      actions={
        <>
          <Button variant="ghost" onClick={onClose} disabled={busy}>
            取消
          </Button>
          <Button variant="primary" onClick={() => void create()} disabled={busy || !name.trim()}>
            建立
          </Button>
        </>
      }
    >
      <TextInput label="名稱" value={name} onChange={(e) => setName(e.target.value)} onKeyDown={(e) => e.key === 'Enter' && void create()} autoFocus />
      {err && (
        <p role="alert" className="m-0 text-sub text-danger">
          {err}
        </p>
      )}
    </Dialog>
  )
}
