import { useState } from 'react'
import { LayoutGrid, MessageSquare, Pencil, Settings, Trash2 } from 'lucide-react'
import type { Bot } from '../data/bots'
import type { Room } from '../data/rooms'
import { EditRoomDialog } from '../rooms/EditRoomDialog'
import { navigate } from '../router'
import { useContextMenu } from '../ui/Menu'
import { DeleteDialog, type DeleteTarget } from './DeleteDialog'

/** 接右鍵／長按選單的事件，展開到列上。 */
export type MenuBind = ReturnType<ReturnType<typeof useContextMenu>['bind']>

/**
 * Bot、群組、團隊列的右鍵（手機長按）選單：開啟、設定／編輯、刪除（先確認）。
 * 側欄和手機的「對話」分頁共用；ui 要放進畫面裡（選單與對話框）。
 */
export function useRowMenu(onNavigate?: () => void) {
  const ctx = useContextMenu()
  const [editRoom, setEditRoom] = useState<Room | null>(null)
  const [deleting, setDeleting] = useState<DeleteTarget | null>(null)
  const go = (to: string) => {
    onNavigate?.()
    navigate(to)
  }
  return {
    bot: (b: Bot) =>
      ctx.bind(b.name, [
        { label: '開啟對話', icon: MessageSquare, onSelect: () => go(`/bots/${b.id}`) },
        { label: 'Bot 設定', icon: Settings, onSelect: () => go(`/settings/bots/${encodeURIComponent(b.id)}`) },
        { label: '刪除 Bot', icon: Trash2, danger: true, separator: true, onSelect: () => setDeleting({ kind: 'bot', id: b.id, name: b.name }) },
      ]),
    room: (r: Room) =>
      ctx.bind(r.name, [
        { label: '開啟群組', icon: MessageSquare, onSelect: () => go(`/rooms/${r.id}`) },
        { label: '編輯群組', icon: Pencil, onSelect: () => setEditRoom(r) },
        { label: '刪除群組', icon: Trash2, danger: true, separator: true, onSelect: () => setDeleting({ kind: 'room', id: r.id, name: r.name }) },
      ]),
    team: (t: { id: string; name: string }) =>
      ctx.bind(`團隊 ${t.name}`, [
        { label: '團隊地圖', icon: LayoutGrid, onSelect: () => go('/teams') },
        { label: '刪除團隊', icon: Trash2, danger: true, separator: true, onSelect: () => setDeleting({ kind: 'team', id: t.id, name: t.name }) },
      ]),
    ui: (
      <>
        {ctx.menu}
        {editRoom && <EditRoomDialog key={editRoom.id} room={editRoom} open onClose={() => setEditRoom(null)} onSaved={() => undefined} />}
        <DeleteDialog target={deleting} onClose={() => setDeleting(null)} />
      </>
    ),
  }
}
