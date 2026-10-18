export type Health = { ok: boolean; version: string }

// 讀取服務的健康狀態；服務沒開時回 null。
export async function fetchHealth(fetcher: typeof fetch = fetch): Promise<Health | null> {
  try {
    const res = await fetcher('/healthz')
    if (!res.ok) return null
    return (await res.json()) as Health
  } catch {
    return null
  }
}
