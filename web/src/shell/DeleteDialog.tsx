import { useState } from 'react'
import { botsDelete, roomsDelete, teamsDelete } from '../api/generated'
import { loadBots } from '../data/bots'
import { loadRooms } from '../data/rooms'
import { loadTeams } from '../data/teams'
import { navigate } from '../router'
import { ConfirmDialog } from '../ui/Dialog'
import { useToast } from '../ui/toast-context'

// 從側欄右鍵刪 Bot／團隊／群組：一律先確認（你本人在畫面上按，就是核准）。
// 正在看被刪掉的那一頁就回待我處理。

export type DeleteTarget = { kind: 'bot' | 'team' | 'room'; id: string; name: string }

const TEXT: Record<DeleteTarget['kind'], { what: string; description: string }> = {
  bot: { what: ' Bot', description: '連同它的電腦（含檔案）、對話、任務一起刪除，無法復原。' },
  team: { what: '團隊', description: '成員回到未分區，Bot、對話和檔案都保留；團隊記憶會一起刪掉。' },
  room: { what: '群組', description: '群組裡的訊息會一起刪掉，無法復原。Bot 本身和它們的對話、檔案不受影響。' },
}

export function DeleteDialog({ target, onClose }: { target: DeleteTarget | null; onClose: () => void }) {
  const toast = useToast()
  const [busy, setBusy] = useState(false)
  const t = target ? TEXT[target.kind] : null
  const remove = async () => {
    if (!target || !t) return
    setBusy(true)
    try {
      if (target.kind === 'bot') await botsDelete({ id: target.id })
      else if (target.kind === 'team') await teamsDelete({ id: target.id })
      else await roomsDelete({ room_id: target.id })
      void (target.kind === 'room' ? loadRooms() : target.kind === 'team' ? loadTeams() : loadBots())
      toast.show({ kind: 'done', text: `已刪除${t.what}「${target.name}」` })
      const here = window.location.pathname
      const page = target.kind === 'bot' ? `/bots/${target.id}` : target.kind === 'room' ? `/rooms/${target.id}` : null
      if (page && (here === page || here.startsWith(page + '/'))) navigate('/', { replace: true })
      onClose()
    } catch (e) {
      toast.show({ kind: 'error', text: e instanceof Error ? e.message : String(e) })
      onClose()
    } finally {
      setBusy(false)
    }
  }
  return (
    <ConfirmDialog
      open={!!target}
      danger
      busy={busy}
      title={target && t ? `刪除${t.what}「${target.name}」？` : ''}
      description={t?.description}
      confirmText="刪除"
      onConfirm={() => void remove()}
      onCancel={onClose}
    />
  )
}
