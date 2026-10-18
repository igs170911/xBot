import { useEffect } from 'react'
import { networkStatus, type NetworkStatusOutput } from '../api/generated'
import { createStore, useStore } from './store'

// 網路狀態（C35）：本機、tailnet、外部入口的網址。網址觸發（C32）、MCP 入口（J12）、手機連線（J05）都用它。

export type NetworkStatus = NetworkStatusOutput

export const networkStore = createStore<{ status: NetworkStatus | null; error: string | null }>({ status: null, error: null })

let inflight: Promise<NetworkStatus | null> | null = null

/** 重新讀一次網路狀態。 */
export function loadNetwork(): Promise<NetworkStatus | null> {
  if (inflight) return inflight
  inflight = networkStatus({})
    .then((status) => {
      networkStore.set({ status, error: null })
      return status
    })
    .catch((e: unknown) => {
      networkStore.set((s) => ({ ...s, error: e instanceof Error ? e.message : String(e) }))
      return null
    })
    .finally(() => {
      inflight = null
    })
  return inflight
}

export function setNetwork(status: NetworkStatus) {
  networkStore.set({ status, error: null })
}

/** 讀網路狀態；poll 有值時每隔這麼多毫秒重讀（設定頁等登入、等 Funnel 用）。 */
export function useNetwork(poll?: number) {
  const s = useStore(networkStore)
  useEffect(() => {
    void loadNetwork()
    if (!poll) return
    const t = window.setInterval(() => void loadNetwork(), poll)
    return () => window.clearInterval(t)
  }, [poll])
  return s
}

/** 外部（tailnet 外）連得到的網址；沒有時用目前這個網站。網址觸發要給別的服務用。 */
export function publicBase(): string {
  return networkStore.get().status?.public_url || window.location.origin
}

/** 給其他裝置（代理、手機）用的網址：外部網址 → tailnet 網址 → 目前這個網站。 */
export function deviceBase(): string {
  return networkStore.get().status?.base_url || window.location.origin
}
