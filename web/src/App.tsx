import { useEffect, useState } from 'react'
import { fetchHealth, type Health } from './health'

// 空白的前端殼（C00）。正式畫面從 W01 開始照 design/ 的畫板做。
export default function App() {
  const [health, setHealth] = useState<Health | null | undefined>(undefined)

  useEffect(() => {
    fetchHealth().then(setHealth)
  }, [])

  return (
    <main className="flex min-h-dvh flex-col items-center justify-center gap-3 p-4">
      <h1 className="text-3xl font-semibold">XBot</h1>
      <p className="text-sm text-white/60" data-testid="health">
        {health === undefined ? '連線中…' : health ? `服務正常 · ${health.version}` : '連不到服務'}
      </p>
    </main>
  )
}
