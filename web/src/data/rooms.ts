import { roomsList, type RoomsListOutput, type XBotEvent } from '../api/generated'
import { onEvent, onReconnect } from './events'
import { createStore } from './store'

export type Room = RoomsListOutput['rooms'][number]
export type RoomMember = Room['members'][number]

export type RoomsState = { rooms: Room[] | null; error?: string }

export const roomsStore = createStore<RoomsState>({ rooms: null })

/** 「新增群組」對話框開著沒（側欄與空白頁都能打開）。 */
export const newRoomDialog = createStore(false)
export const openNewRoom = () => newRoomDialog.set(true)

let inflight: Promise<void> | null = null
let again = false

/** 重讀群組清單（讀的途中又被叫，讀完再讀一次）。 */
export function loadRooms(): Promise<void> {
  if (inflight) {
    again = true
    return inflight
  }
  inflight = roomsList({})
    .then((out) => roomsStore.set({ rooms: out.rooms }))
    .catch((err: unknown) => roomsStore.set((s) => ({ ...s, error: err instanceof Error ? err.message : String(err) })))
    .finally(() => {
      inflight = null
      if (again) {
        again = false
        void loadRooms()
      }
    })
  return inflight
}

export function upsertRoom(r: Room) {
  roomsStore.set((s) => {
    const list = s.rooms ?? []
    const i = list.findIndex((x) => x.id === r.id)
    return { ...s, rooms: i < 0 ? [r, ...list] : list.map((x) => (x.id === r.id ? r : x)) }
  })
}

/** 群組事件（room.*）或群組操作：要重讀清單（預覽、成員狀態）。 */
export function isRoomEvent(e: XBotEvent): boolean {
  if (e.kind.startsWith('room.')) return true
  if (e.kind !== 'op') return false
  const p = (e.payload ?? {}) as { op?: string; status?: string }
  return typeof p.op === 'string' && (p.op.startsWith('xbot.rooms.') || p.op === 'xbot.collab.start_group') && p.status === 'ok'
}

let unsubs: Array<() => void> | null = null
let timer: ReturnType<typeof setTimeout> | null = null

export function startRooms() {
  if (unsubs) return
  void loadRooms()
  unsubs = [
    onEvent((e) => {
      if (!isRoomEvent(e)) return
      // 接力時事件很密：合併成一次重讀
      if (timer) return
      timer = setTimeout(() => {
        timer = null
        void loadRooms()
      }, 150)
    }),
    onReconnect(() => void loadRooms()),
  ]
}

export function resetRooms() {
  unsubs?.forEach((u) => u())
  unsubs = null
  roomsStore.set({ rooms: null })
}
