import { useState } from 'react'
import { botsDelete, roomsDelete, teamsDelete } from '../api/generated'
import { Checkbox } from '../ui/Controls'
import { loadBots } from '../data/bots'
import { loadRooms, roomsStore } from '../data/rooms'
import { loadTeams } from '../data/teams'
import { useStore } from '../data/store'
import { navigate } from '../router'
import { ConfirmDialog } from '../ui/Dialog'
import { useToast } from '../ui/toast-context'

// 從側欄右鍵刪 Bot／團隊／群組：一律先確認（你本人在畫面上按，就是核准）。
// 正在看被刪掉的那一頁就回待我處理。

/** roomId：團隊建立時一起開的群組（刪團隊時問要不要一起刪）。 */
export type DeleteTarget = { kind: 'bot' | 'team' | 'room'; id: string; name: string; roomId?: string }

const TEXT: Record<DeleteTarget['kind'], { what: string; description: string }> = {
  bot: { what: ' Bot', description: '連同它的電腦（含檔案）、對話、任務一起刪除，無法復原。' },
  team: { what: '團隊', description: '成員回到未分區，Bot、對話和檔案都保留；團隊記憶會一起刪掉。' },
  room: { what: '群組', description: '群組裡的訊息會一起刪掉，無法復原。Bot 本身和它們的對話、檔案不受影響。' },
}

export function DeleteDialog({ target, onClose }: { target: DeleteTarget | null; onClose: () => void }) {
  const toast = useToast()
  const [busy, setBusy] = useState(false)
  const t = target ? TEXT[target.kind] : null
  // 刪團隊時，它的群組還在就問要不要一起刪（預設一起刪：團隊沒了，它的群組通常也不要了）
  const room = useStore(roomsStore).rooms?.find((r) => target?.kind === 'team' && r.id === target.roomId)
  const [withRoom, setWithRoom] = useState(true)
  const remove = async () => {
    if (!target || !t) return
    setBusy(true)
    try {
      if (target.kind === 'bot') await botsDelete({ id: target.id })
      else if (target.kind === 'team') {
        await teamsDelete({ id: target.id })
        if (room && withRoom) await roomsDelete({ room_id: room.id })
      } else await roomsDelete({ room_id: target.id })
      void (target.kind === 'room' ? loadRooms() : target.kind === 'team' ? Promise.all([loadTeams(), loadRooms(), loadBots()]) : loadBots())
      toast.show({ kind: 'done', text: `已刪除${t.what}「${target.name}」${room && withRoom ? `和群組「${room.name}」` : ''}` })
      if (room && withRoom && window.location.pathname === `/rooms/${room.id}`) navigate('/', { replace: true })
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
      description={
        room ? (
          <>
            {t?.description}
            <Checkbox checked={withRoom} onChange={() => setWithRoom((v) => !v)} className="mt-3">
              一起刪除團隊的群組「{room.name}」（群組訊息無法復原）
            </Checkbox>
          </>
        ) : (
          t?.description
        )
      }
      confirmText="刪除"
      onConfirm={() => void remove()}
      onCancel={onClose}
    />
  )
}
