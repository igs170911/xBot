import { useState } from 'react'
import { ArrowRightLeft, LayoutGrid, LogOut, MessageSquare, Pencil, Settings, Star, Trash2 } from 'lucide-react'
import { botsUpdate } from '../api/generated'
import { loadBots, type Bot } from '../data/bots'
import type { Room } from '../data/rooms'
import { useStore } from '../data/store'
import { moveBot, teamsStore, type Team } from '../data/teams'
import { EditRoomDialog } from '../rooms/EditRoomDialog'
import { navigate } from '../router'
import { useContextMenu, type MenuItem } from '../ui/Menu'
import { useToast } from '../ui/toast-context'
import { DeleteDialog, type DeleteTarget } from './DeleteDialog'
import { EditTeamDialog } from './EditTeamDialog'

/** 接右鍵／長按選單的事件，展開到列上。 */
export type MenuBind = ReturnType<ReturnType<typeof useContextMenu>['bind']>

/** 列上的「⋯」：從按鈕打開同一個選單。 */
export type MoreOpener = (el: HTMLElement) => void

/**
 * Bot、群組、團隊列的選單：右鍵（手機長按），或列上滑過出現的「⋯」。
 * Bot：開對話、設定、換到別的團隊、刪除；群組：開啟、改名換成員、刪除；團隊：團隊地圖、改名換成員、刪除。
 * 側欄和手機的「對話」分頁共用；ui 要放進畫面裡（選單與對話框）。
 */
export function useRowMenu(onNavigate?: () => void) {
  const ctx = useContextMenu()
  const toast = useToast()
  const teams = useStore(teamsStore).teams ?? []
  const [editRoom, setEditRoom] = useState<Room | null>(null)
  const [editTeam, setEditTeam] = useState<Team | null>(null)
  const [deleting, setDeleting] = useState<DeleteTarget | null>(null)
  const go = (to: string) => {
    onNavigate?.()
    navigate(to)
  }
  const move = (b: Bot, to: string) => {
    const where = teams.find((t) => t.id === to)?.name
    moveBot(b, to)
      .then(() => toast.show({ kind: 'done', text: where ? `已把 ${b.name} 移到「${where}」` : `已把 ${b.name} 移出團隊` }))
      .catch((e: unknown) => toast.show({ kind: 'error', text: e instanceof Error ? e.message : String(e) }))
  }
  // 大總管（主要 Bot）：側欄最上面的大卡片；同時只有一個
  const makeChief = (b: Bot) => {
    botsUpdate({ id: b.id, primary: true })
      .then(() => loadBots())
      .then(() => toast.show({ kind: 'done', text: `${b.name} 現在是大總管` }))
      .catch((e: unknown) => toast.show({ kind: 'error', text: e instanceof Error ? e.message : String(e) }))
  }
  const teamTarget = (t: Team): DeleteTarget => ({ kind: 'team', id: t.id, name: t.name, roomId: t.room_id })

  const botItems = (b: Bot): MenuItem[] => {
    const others = teams.filter((t) => t.id !== b.team_id)
    return [
      { label: '開啟對話', icon: MessageSquare, onSelect: () => go(`/bots/${b.id}`) },
      { label: 'Bot 設定', icon: Settings, onSelect: () => go(`/settings/bots/${encodeURIComponent(b.id)}`) },
      ...(b.primary ? [] : [{ label: '設成大總管', icon: Star, onSelect: () => makeChief(b) }]),
      ...others.map((t, i) => ({ label: `移到「${t.name}」`, icon: ArrowRightLeft, separator: i === 0, onSelect: () => move(b, t.id) })),
      ...(b.team_id ? [{ label: '移出團隊', icon: LogOut, separator: others.length === 0, onSelect: () => move(b, '') }] : []),
      { label: '刪除 Bot', icon: Trash2, danger: true, separator: true, onSelect: () => setDeleting({ kind: 'bot', id: b.id, name: b.name }) },
    ]
  }
  const roomItems = (r: Room): MenuItem[] => [
    { label: '開啟群組', icon: MessageSquare, onSelect: () => go(`/rooms/${r.id}`) },
    { label: '改名、換成員', icon: Pencil, onSelect: () => setEditRoom(r) },
    { label: '刪除群組', icon: Trash2, danger: true, separator: true, onSelect: () => setDeleting({ kind: 'room', id: r.id, name: r.name }) },
  ]
  const teamItems = (t: Team): MenuItem[] => [
    { label: '改名、換成員', icon: Pencil, onSelect: () => setEditTeam(t) },
    { label: '團隊地圖', icon: LayoutGrid, onSelect: () => go('/teams') },
    { label: '刪除團隊', icon: Trash2, danger: true, separator: true, onSelect: () => setDeleting(teamTarget(t)) },
  ]

  return {
    bot: (b: Bot) => ctx.bind(b.name, botItems(b)),
    room: (r: Room) => ctx.bind(r.name, roomItems(r)),
    team: (t: Team) => ctx.bind(`團隊 ${t.name}`, teamItems(t)),
    /** 列上「⋯」用的：從按鈕打開同一個選單。 */
    more: {
      bot: (b: Bot): MoreOpener => (el) => ctx.open(el, b.name, botItems(b)),
      room: (r: Room): MoreOpener => (el) => ctx.open(el, r.name, roomItems(r)),
      team: (t: Team): MoreOpener => (el) => ctx.open(el, `團隊 ${t.name}`, teamItems(t)),
    },
    ui: (
      <>
        {ctx.menu}
        {editRoom && <EditRoomDialog key={editRoom.id} room={editRoom} open onClose={() => setEditRoom(null)} onSaved={() => undefined} />}
        {editTeam && (
          <EditTeamDialog
            key={editTeam.id}
            team={editTeam}
            onClose={() => setEditTeam(null)}
            onDelete={() => {
              setDeleting(teamTarget(editTeam))
              setEditTeam(null)
            }}
          />
        )}
        <DeleteDialog target={deleting} onClose={() => setDeleting(null)} />
      </>
    ),
  }
}
