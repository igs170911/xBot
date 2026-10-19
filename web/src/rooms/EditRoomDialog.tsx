import { useState } from 'react'
import { Trash2 } from 'lucide-react'
import { roomsDelete, roomsUpdate, type CollabRoomReadOutput } from '../api/generated'
import { botsStore, look } from '../data/bots'
import { loadRooms } from '../data/rooms'
import { useStore } from '../data/store'
import { navigate } from '../router'
import { Avatar } from '../ui/Avatar'
import { Button } from '../ui/Button'
import { Checkbox } from '../ui/Controls'
import { ConfirmDialog, Dialog } from '../ui/Dialog'
import { Textarea, TextInput } from '../ui/Field'
import { Select } from '../ui/Menu'
import { useToast } from '../ui/toast-context'

// 編輯群組（xbot.rooms.update／delete）：名稱、成員（2–10 個 Bot）、主責、群組指示；最下面刪除群組（要確認）。

type Room = CollabRoomReadOutput['room']

const MIN = 2
const MAX = 10

export function EditRoomDialog({ room, open, onClose, onSaved }: { room: Room; open: boolean; onClose: () => void; onSaved: () => void }) {
  const toast = useToast()
  const bots = (useStore(botsStore).bots ?? []).filter((b) => !b.archived)
  const [name, setName] = useState(room.name)
  const [members, setMembers] = useState<string[]>(room.members.map((m) => m.bot_id))
  const [lead, setLead] = useState(room.members.find((m) => m.lead)?.bot_id ?? '')
  const [bulletin, setBulletin] = useState(room.bulletin ?? '')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [deleting, setDeleting] = useState(false)

  const toggle = (id: string) => setMembers((cur) => (cur.includes(id) ? cur.filter((x) => x !== id) : [...cur, id]))
  const count = members.length
  const valid = count >= MIN && count <= MAX
  const leadOK = lead && members.includes(lead) ? lead : members[0]

  const save = async () => {
    setBusy(true)
    setError(null)
    try {
      await roomsUpdate({ room_id: room.id, name: name.trim(), member_ids: members, lead_id: leadOK, bulletin: bulletin.trim() })
      void loadRooms()
      onSaved()
      onClose()
      toast.show({ kind: 'done', text: '群組已更新' })
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }
  const remove = async () => {
    setBusy(true)
    try {
      await roomsDelete({ room_id: room.id })
      void loadRooms()
      setDeleting(false)
      onClose()
      toast.show({ kind: 'done', text: `已刪除群組「${room.name}」` })
      if (window.location.pathname === `/rooms/${room.id}`) navigate('/', { replace: true })
    } catch (e) {
      setDeleting(false)
      toast.show({ kind: 'error', text: e instanceof Error ? e.message : String(e) })
    } finally {
      setBusy(false)
    }
  }

  return (
    <>
      <Dialog
        open={open && !deleting}
        onClose={onClose}
        busy={busy}
        width={560}
        title="編輯群組"
        actions={
          <>
            <Button variant="danger" icon={Trash2} onClick={() => setDeleting(true)} disabled={busy} className="mr-auto">
              刪除群組
            </Button>
            <Button onClick={onClose} disabled={busy}>
              取消
            </Button>
            <Button variant="primary" loading={busy} disabled={!valid} onClick={() => void save()}>
              儲存
            </Button>
          </>
        }
      >
        <div className="flex flex-col gap-4">
          <TextInput label="名稱" value={name} onChange={(e) => setName(e.target.value)} placeholder="留空用成員名字自動命名" />
          <div className="flex flex-col gap-1.5">
            <span className="text-meta text-ink-3">
              成員（{count} 位，{MIN}–{MAX} 位）
            </span>
            <div className="flex max-h-[240px] flex-col overflow-y-auto rounded-group bg-inset" role="group" aria-label="成員">
              {bots.map((b) => (
                <Checkbox key={b.id} checked={members.includes(b.id)} onChange={() => toggle(b.id)} className="min-h-11 border-line px-3 not-first:border-t">
                  <Avatar {...look(b)} dot="none" size={24} blink={false} />
                  <span className="min-w-0 flex-1 truncate">{b.name}</span>
                  {b.title && <span className="truncate text-meta text-ink-3">{b.title}</span>}
                </Checkbox>
              ))}
            </div>
            {!valid && <span className="text-meta text-danger">群組要有 {MIN} 到 {MAX} 位 Bot</span>}
          </div>
          <Select
            label="主責（沒點名時由它回答）"
            value={leadOK ?? ''}
            options={members.map((id) => ({ value: id, label: bots.find((b) => b.id === id)?.name ?? id }))}
            onChange={setLead}
          />
          <Textarea label="群組指示（每位成員都會看到）" value={bulletin} onChange={(e) => setBulletin(e.target.value)} rows={3} placeholder="例：每天下午 5 點前回報一次進度" />
          {error && (
            <p role="alert" className="m-0 text-meta text-danger">
              {error}
            </p>
          )}
        </div>
      </Dialog>
      <ConfirmDialog
        open={deleting}
        danger
        busy={busy}
        title={`刪除群組「${room.name}」？`}
        description="群組裡的訊息會一起刪掉，無法復原。Bot 本身和它們的對話、檔案不受影響。"
        confirmText="刪除"
        onConfirm={() => void remove()}
        onCancel={() => setDeleting(false)}
      />
    </>
  )
}
