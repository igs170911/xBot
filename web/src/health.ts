import { systemPing } from './api/generated'

export type Health = { ok: boolean; version: string }

// 用操作 xbot.system.ping 確認服務有在運作；連不到或被拒絕時回 null。
export async function fetchHealth(fetcher: typeof fetch = fetch): Promise<Health | null> {
  try {
    const out = await systemPing({}, { fetch: fetcher })
    return { ok: out.pong, version: out.build }
  } catch {
    return null
  }
}
