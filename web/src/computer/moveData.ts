import { useEffect } from 'react'
import { computersMoveStatus, type ComputersMoveStatusOutput } from '../api/generated'
import { onEvent, onReconnect } from '../data/events'
import { createStore, useStore } from '../data/store'
import type { HostView } from '../settings/hostsData'

// 搬家（CL-5，B31）：xbot.computers.move_status ＋ computers.move 事件（payload 就是整份進度）。

export type MoveStatus = ComputersMoveStatusOutput
export type MoveJob = MoveStatus['jobs'][number]

export const movesStore = createStore<MoveStatus | null>(null)

export function loadMoves(): Promise<void> {
  return computersMoveStatus({})
    .then((s) => movesStore.set(s))
    .catch(() => movesStore.set((s) => s ?? { running: false, jobs: [] }))
}

let users = 0
let unsub: (() => void) | null = null

/** 所有搬家的進度（元件掛著時跟著事件更新）。 */
export function useMoves(): MoveStatus | null {
  useEffect(() => {
    users++
    if (users === 1) {
      const off = onEvent((e) => {
        if (e.kind === 'computers.move' && e.payload) movesStore.set(e.payload as MoveStatus)
      })
      const offRe = onReconnect(() => void loadMoves())
      unsub = () => {
        off()
        offRe()
      }
    }
    void loadMoves()
    return () => {
      users--
      if (users === 0 && unsub) {
        unsub()
        unsub = null
      }
    }
  }, [])
  return useStore(movesStore)
}

/** 主機在分段控制上的名字：這台主機／cloud-01（雲端）。 */
export function hostLabel(h: HostView): string {
  return h.kind === 'local' ? '這台主機' : `${h.name}（雲端）`
}

/** 能搬去的主機：別台、在線上的。 */
export function moveTargets(from: HostView, hosts: HostView[]): HostView[] {
  return hosts.filter((h) => h.id !== from.id && (h.kind === 'local' || h.status === 'online'))
}

/** 進行中，或 24 小時內做完的搬家（看得到舊資料保留到什麼時候）。 */
export function isRecent(j: MoveJob, now = Date.now()): boolean {
  return isActive(j) || now - (j.finished_at ?? 0) < 24 * 3600_000
}

export function isActive(j: MoveJob): boolean {
  return j.state === 'queued' || j.state === 'running'
}

/** 一個 Bot 進行中或最近一次的搬家。 */
export function jobOf(s: MoveStatus | null, botId: string): MoveJob | undefined {
  return s?.jobs.find((j) => j.bot_id === botId)
}

/** 搬家的 4 步（E10）：停止電腦 → 打包資料 → 傳到新主機 → 還原並開機。回傳目前在第幾步（0 起算）。 */
export function moveStep(j: MoveJob): number {
  if (j.state === 'done') return 4
  switch (j.phase) {
    case 'stop':
      return 0
    case 'image':
    case 'measure':
      return 1
    case 'copy':
      return 2
    case 'verify':
    case 'switch':
    case 'start':
      return 3
  }
  return 0
}

export function moveSteps(j: MoveJob): string[] {
  return ['停止電腦', '打包資料', `傳到 ${j.to_name}`, '還原並開機']
}

/** 「1.3 / 2.8 GB」「120 / 300 MB」 */
export function sizeText(done: number, total: number): string {
  const big = Math.max(done, total)
  const [unit, div] = big >= 2 ** 30 ? ['GB', 2 ** 30] : big >= 2 ** 20 ? ['MB', 2 ** 20] : ['KB', 2 ** 10]
  const f = (n: number) => {
    const v = n / div
    return v >= 100 || Number.isInteger(v) ? String(Math.round(v)) : v.toFixed(1)
  }
  return total > 0 ? `${f(done)} / ${f(total)} ${unit}` : `${f(done)} ${unit}`
}

/** 搬家的一行說明（進度條下面）。 */
export function moveDetail(j: MoveJob): string {
  switch (j.state) {
    case 'queued':
      return '排隊中，前面的搬完就輪到它'
    case 'done':
      return j.note || (j.keep_until && !j.cleaned ? `已搬到 ${j.to_name}；${j.from_name} 上的舊資料保留到 ${clock(j.keep_until)}` : `已搬到 ${j.to_name}`)
    case 'failed':
      return `沒搬成，留在 ${j.from_name}，原本的資料沒動：${j.error ?? ''}`
    case 'canceled':
      return `已取消，留在 ${j.from_name}，原本的資料沒動`
  }
  switch (j.phase) {
    case 'image':
      return `先把電腦映像傳到 ${j.to_name}`
    case 'stop':
      return '停止電腦'
    case 'measure':
      return '打包 home／workspace／hermes'
    case 'copy':
      return `傳 ${j.volume ?? '資料'} · ${sizeText(j.done, j.total)}`
    case 'verify':
      return `逐一核對 ${j.to_name} 上的檔案`
    case 'switch':
    case 'start':
      return `在 ${j.to_name} 還原並開機`
  }
  return '準備中'
}

/** 「明天 22:54」「今天 18:00」 */
export function clock(ms: number, now = Date.now()): string {
  const t = new Date(ms)
  const hm = `${String(t.getHours()).padStart(2, '0')}:${String(t.getMinutes()).padStart(2, '0')}`
  const day = (d: Date) => new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime()
  const diff = Math.round((day(t) - day(new Date(now))) / 86_400_000)
  if (diff === 0) return `今天 ${hm}`
  if (diff === 1) return `明天 ${hm}`
  return `${t.getMonth() + 1}/${t.getDate()} ${hm}`
}
