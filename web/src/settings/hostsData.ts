import { useEffect } from 'react'
import { hostsList, type HostsListOutput } from '../api/generated'
import { onEvent, onReconnect } from '../data/events'
import { createStore, useStore } from '../data/store'

// 主機清單（B12）：xbot.hosts.list ＋ host.status（重讀）／host.image（映像傳輸進度）事件。

export type HostView = HostsListOutput['hosts'][number]
export type ImageProgress = NonNullable<HostView['image']>

export type HostsState = {
  /** null 表示還在讀。 */
  hosts: HostView[] | null
  error?: string
  /** host.image 事件的最新進度（依主機）。 */
  progress: Record<string, ImageProgress>
}

export const hostsStore = createStore<HostsState>({ hosts: null, progress: {} })

let inflight: Promise<void> | null = null
let again = false

/** 重讀主機清單；讀的途中又被叫，就在讀完後再讀一次。 */
export function loadHosts(): Promise<void> {
  if (inflight) {
    again = true
    return inflight
  }
  inflight = hostsList({})
    .then((out) => hostsStore.set((s) => ({ ...s, hosts: out.hosts, error: undefined })))
    .catch((err: unknown) => hostsStore.set((s) => ({ ...s, error: err instanceof Error ? err.message : String(err) })))
    .finally(() => {
      inflight = null
      if (again) {
        again = false
        void loadHosts()
      }
    })
  return inflight
}

let users = 0
let unsub: (() => void) | null = null

function subscribe() {
  const off = onEvent((e) => {
    if (e.kind === 'host.status') void loadHosts()
    if (e.kind === 'host.image') {
      const p = e.payload as ImageProgress | undefined
      if (!p?.host_id) return
      hostsStore.set((s) => ({ ...s, progress: { ...s.progress, [p.host_id]: p } }))
      if (p.error) void loadHosts()
    }
  })
  const offRe = onReconnect(() => void loadHosts())
  return () => {
    off()
    offRe()
  }
}

/** 讀主機清單並跟著事件更新（元件掛著時）。 */
export function useHosts(): HostsState {
  useEffect(() => {
    users++
    if (users === 1) unsub = subscribe()
    void loadHosts()
    return () => {
      users--
      if (users === 0 && unsub) {
        unsub()
        unsub = null
      }
    }
  }, [])
  return useStore(hostsStore)
}

/** 這台主機的映像傳輸進度（事件比清單新就用事件的）。 */
export function imageProgress(h: HostView, s: HostsState): ImageProgress | undefined {
  const ev = s.progress[h.id]
  if (h.image) return ev && ev.done >= h.image.done ? ev : h.image
  if (h.status === 'updating' && ev && !ev.error) return ev
  return undefined
}

export function gb(bytes: number): string {
  const g = bytes / 2 ** 30
  if (g >= 10 || Number.isInteger(g)) return `${Math.round(g)} GB`
  return `${g.toFixed(1)} GB`
}

/** 「8 核 16 GB」 */
export function resourceText(h: HostView): string {
  const r = h.resources
  if (!r || !r.cpus) return ''
  return `${r.cpus} 核 ${gb(r.mem_bytes)}`
}

export const STATUS_LABEL: Record<string, string> = {
  online: '在線',
  offline: '離線',
  updating: '更新中',
  failed: '要處理',
  unknown: '連線中',
}

/** 「3 分鐘前」「昨天 18:42」 */
export function ago(ms: number, now = Date.now()): string {
  if (!ms) return '從來沒有'
  const d = Math.max(0, now - ms)
  if (d < 60_000) return '剛剛'
  if (d < 3_600_000) return `${Math.floor(d / 60_000)} 分鐘前`
  const t = new Date(ms)
  const hm = `${String(t.getHours()).padStart(2, '0')}:${String(t.getMinutes()).padStart(2, '0')}`
  const today = new Date(now)
  if (t.toDateString() === today.toDateString()) return `今天 ${hm}`
  const y = new Date(now - 86_400_000)
  if (t.toDateString() === y.toDateString()) return `昨天 ${hm}`
  return `${t.getMonth() + 1}/${t.getDate()} ${hm}`
}

/** 「家裡 · 8 核 16 GB · 本機 Docker」這一行。 */
export function hostSub(h: HostView, s: HostsState): string {
  const parts: string[] = []
  const res = resourceText(h)
  if (h.kind === 'local') {
    if (res) parts.push(res)
    parts.push('本機 Docker')
    return parts.join(' · ')
  }
  const p = imageProgress(h, s)
  if (h.status === 'offline' || h.status === 'failed') {
    if (res) parts.push(res)
    parts.push(h.status === 'failed' ? h.status_error || '要處理' : `最後在線 ${ago(h.last_seen_at)}`)
    return parts.join(' · ')
  }
  if (res) parts.push(res)
  if (p) parts.push(`傳電腦映像 ${p.percent}%`)
  else parts.push(h.resources?.tailscale ? '經 Tailscale 用 SSH 控制' : '用 SSH 控制')
  return parts.join(' · ')
}
