import type { APIRequestContext } from '@playwright/test'

// 對著真的服務跑的測試共用：先把首次設定記成做完（W32），需要時確保至少有一個 Bot（沒有 Bot 時首頁是「建立第一個 Bot」）。

export async function pastOnboarding(request: APIRequestContext) {
  await request.post('/api/v1/onboard.update', { data: { completed: true } })
}

export async function ensureBot(request: APIRequestContext, name = '小秘') {
  await pastOnboarding(request)
  const list = await (await request.post('/api/v1/bots.list', { data: {} })).json()
  if (list.output.bots.length > 0) return list.output.bots[0]
  const res = await (await request.post('/api/v1/bots.create', { data: { name, color: 'blue', shape: 'cursor' } })).json()
  return res.output
}
