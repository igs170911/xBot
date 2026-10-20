import { expect, test, sidebarAdd } from './fixtures'
import { pastOnboarding } from './real-helpers'

// W32 對著真的服務（不攔截網路）：首次設定的偵測操作回得來；分享出去的 .xbot 檔在網頁匯入，
// 匯入的 Bot 一律「先問我」、引擎用預設、同名自動加編號；稽核紀錄不記整份檔案。

test('真的服務：onboard.status 偵測、.xbot 匯出再從網頁匯入', async ({ page, request }, info) => {
  test.skip(info.project.name !== 'desktop', '同一個服務只跑一次')
  await page.setViewportSize({ width: 1440, height: 960 })

  const st = (await (await request.post('/api/v1/onboard.status', { data: {} })).json()).output
  expect(Array.isArray(st.engines)).toBe(true)
  expect(st.engines.map((e: { engine: string }) => e.engine)).toEqual(['claude-code', 'codex', 'gemini', 'opencode', 'grok'])
  expect(Array.isArray(st.local)).toBe(true)
  expect(typeof st.computer.ready).toBe('boolean')
  await pastOnboarding(request)

  const name = `分享喵${Date.now() % 10000}`
  const src = (await (await request.post('/api/v1/bots.create', { data: { name, title: '測試員', soul: '說話簡短', color: 'pink', shape: 'star', eyes: 'happy' } })).json()).output
  const ex = (await (await request.post('/api/v1/bots.export', { data: { id: src.id } })).json()).output
  expect(ex.filename).toBe(`${name}.xbot`)
  expect(ex.content).not.toContain('api_key')

  await page.goto('/')
  await sidebarAdd(page, '新增 Bot')
  await page.getByRole('dialog', { name: '新增 Bot' }).getByRole('button', { name: '匯入 Bot' }).click()
  const imp = page.getByRole('dialog', { name: '匯入 Bot' })
  await imp.getByLabel('選擇 .xbot 檔').setInputFiles({ name: ex.filename, mimeType: 'application/json', buffer: Buffer.from(ex.content) })
  await imp.getByRole('button', { name: '下一步' }).click()
  await expect(imp.getByLabel('名字')).toHaveValue(`${name} 2`)
  await expect(imp.getByRole('list', { name: '會帶入的內容' })).toContainText('一律從「先問我」開始')
  await imp.getByRole('button', { name: '匯入' }).click()
  await expect(page).toHaveURL(/\/bots\/[^/]+$/)
  const id = new URL(page.url()).pathname.split('/')[2]
  const got = (await (await request.post('/api/v1/bots.get', { data: { id } })).json()).output
  expect(got).toMatchObject({ name: `${name} 2`, approval_mode: 'ask', engine: 'hermes', shape: 'star', eyes: 'happy', color: 'pink', soul: '說話簡短' })

  const audit = (await (await request.post('/api/v1/audit.list', { data: { limit: 20 } })).json()).output
  const row = audit.entries.find((e: { op: string }) => e.op === 'xbot.bots.import')
  expect(JSON.stringify(row.params)).toContain('不記錄內容')
})
