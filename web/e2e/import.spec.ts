import { expect, test } from '@playwright/test'
import { MockServer, settle } from './mock'

// V10：從 LazyBoy、OpenMausBot 帶入舊設定。首頁卡片（A05）→ 預演（F10 版型）→ 匯入進度與結果，用假服務跑並截圖。
const shot = (name: string) => `e2e-results/screens/${name}.png`

test.beforeEach(async ({ page }, info) => {
  if (info.project.name === 'desktop') await page.setViewportSize({ width: 1440, height: 900 })
})

const SOURCE = {
  id: 'lazyboy-1', kind: 'lazyboy', label: 'LazyBoy', path: '/home/me/LazyBoyHermes/.data', bots: 3, skills: 4, memory: 2, routines: 2,
  teams: 1, rooms: 1, keys: 2, logins: 1, imported: 0, summary: '在這台電腦找到 LazyBoy 的 3 個 Bot、2 把金鑰和 2 個定時任務。',
}

const PLAN = {
  source: SOURCE,
  items: [
    { key: 'bot:a', kind: 'bot', title: '分潤研究主管', original: '分潤研究主管', name: '分潤研究主管', status: 'ready', checked: true, detail: '研究團隊主管', color: 'purple', shape: 'star' },
    { key: 'skills:a', kind: 'skills', bot: 'bot:a', title: '技能 3 個', detail: 'taiwan-opendata、open-source-map-stack、workspace-cleanup', status: 'ready', checked: true, count: 3, attention: '先關閉；看過內容再打開' },
    { key: 'memory:a', kind: 'memory', bot: 'bot:a', title: '記憶', detail: '6 條', status: 'ready', checked: true, count: 6 },
    { key: 'data:a', kind: 'data', bot: 'bot:a', title: '電腦裡的檔案', detail: '工作資料夾、家目錄的檔案：12 個檔案、11 MB', status: 'ready', checked: true, count: 12, bytes: 11 << 20 },
    { key: 'login:a', kind: 'login', bot: 'bot:a', title: '瀏覽器登入', detail: '已登入的網站（96 MB）；要你勾選才帶', status: 'ready', checked: false, explicit: true, bytes: 96 << 20 },
    { key: 'routine:r1', kind: 'routine', bot: 'bot:a', title: '每日整理', detail: '平日 09:00', status: 'ready', checked: true, attention: '先暫停；你確認後再啟用' },
    { key: 'bot:b', kind: 'bot', title: '小秘', original: '小秘', name: '小秘 2', renamed: true, status: 'ready', checked: true, detail: '助理・你已經有「小秘」，先叫「小秘 2」', color: 'green', shape: 'blob' },
    { key: 'routine:r2', kind: 'routine', bot: 'bot:b', title: '雙週報', detail: '排程帶不過來：無效的排程頻率', status: 'blocked', checked: false },
    { key: 'bot:c', kind: 'bot', title: '分潤平台比較員', original: '分潤平台比較員', name: '分潤平台比較員', status: 'done', checked: false, target: 'old1', color: 'cyan', shape: 'drop' },
    { key: 'skills:c', kind: 'skills', bot: 'bot:c', title: '技能 1 個', status: 'done', checked: false },
    { key: 'team:t', kind: 'team', title: '蝦皮分潤研究', detail: '2 位：分潤研究主管、分潤平台比較員・主管 分潤研究主管', status: 'ready', checked: true, count: 2 },
    { key: 'room:g', kind: 'room', title: '研究群組', detail: '分潤研究主管、分潤平台比較員', status: 'ready', checked: true, count: 2 },
    { key: 'approvals:org', kind: 'approvals', title: '只有你能決定的事', detail: '新商品定價、新增支出、簽約', status: 'ready', checked: true },
    { key: 'key:opencode-go', kind: 'key', title: 'opencode-go', detail: 'opencode-go（https://opencode.ai/zen/go/v1）；金鑰加密後只放在 XBot 服務，不進 Bot 的電腦', status: 'ready', checked: false, explicit: true },
    { key: 'key:openai-api', kind: 'key', title: 'OpenAI', detail: '你已經設定了 openai-api，不覆寫', status: 'blocked', checked: false, explicit: true },
  ],
  new_bots: 2,
  remaining: 48,
  can_create: true,
  not_carried: ['對話紀錄（舊的對話留在原本的系統）', '舊系統的權限設定：一律從「先問我」開始', '團隊管理權限：要你重新打開', '連接的應用程式與 MCP 的登入：之後在「連接應用程式」重新登入'],
  notes: [],
}

test('帶入舊設定：首頁卡片（A05）→ 預演（F10）→ 匯入完成', async ({ page }, info) => {
  const api = new MockServer(page)
  const bot = api.addBot({ name: '小秘', title: '幕僚長' })
  api.addThread(bot.id, '新對話')
  Object.assign(api.override, {
    'onboard.status': () => ({ first_run: false, bots: 1, state: { completed: true, tour_done: true, dismissed: [] }, engines: [], local: [],
      computer: { docker: true, image: 'xbot-box:dev', ready: true }, legacy: { found: true, summary: SOURCE.summary, sources: 1 } }),
    'import.detect': () => ({ sources: [SOURCE] }),
    'import.plan': () => PLAN,
    'import.run': (i: Record<string, unknown>) => ({
      id: 'run1', source_id: SOURCE.id, label: 'LazyBoy', state: 'done', done: (i.items as string[]).length, total: (i.items as string[]).length, percent: 100,
      bytes: 11 << 20, bots: ['bot91', 'bot92'], started_at: 1, finished_at: 2,
      results: [
        { key: 'key:opencode-go', kind: 'key', title: 'opencode-go', status: 'ok', detail: '加密後存在 XBot 服務；到「模型」設成預設或給 Bot 用' },
        { key: 'bot:a', kind: 'bot', title: '分潤研究主管', status: 'ok', detail: '權限從「先問我」開始；模型用你這裡的預設', target: 'bot91' },
        { key: 'bot:b', kind: 'bot', title: '小秘', status: 'ok', detail: '權限從「先問我」開始', target: 'bot92' },
        { key: 'team:t', kind: 'team', title: '蝦皮分潤研究', status: 'ok', detail: '2 位成員' },
        { key: 'routine:r1', kind: 'routine', title: '每日整理', status: 'ok', detail: '平日 09:00；先暫停，你確認後再啟用' },
        { key: 'skills:a', kind: 'skills', title: '技能 3 個', status: 'ok', detail: '放進 3 個，先關閉；到 Bot 的「能力」看過內容再打開' },
        { key: 'data:a', kind: 'data', title: '電腦裡的檔案', status: 'failed', detail: '要先建立 Bot 的電腦才能放進去：Docker 沒有開' },
      ],
    }),
  })
  await api.install()
  await page.goto(`/bots/${bot.id}`)

  const card = page.getByTestId('later-import')
  await expect(card).toContainText(SOURCE.summary)
  await settle(page)
  await page.screenshot({ path: shot(`${info.project.name}-v10-a05`), fullPage: true })
  await card.getByRole('button', { name: /看看要帶哪些/ }).click()

  await expect(page).toHaveURL(/\/import$/)
  await expect(page.getByTestId('import-source')).toContainText(SOURCE.summary)
  await expect(page.getByTestId('import-summary')).toContainText('會建立 2 個 Bot')
  const notes = page.getByRole('complementary', { name: '加入前請看一下' })
  await expect(notes.getByText('3 個技能先關閉')).toBeVisible()
  await notes.getByRole('checkbox', { name: /opencode-go/ }).check({ force: true })
  await page.getByLabel('小秘 帶進來後的名字').fill('舊小秘')
  await settle(page)
  await page.screenshot({ path: shot(`${info.project.name}-v10-f10`), fullPage: true })

  await page.getByRole('button', { name: '開始匯入' }).click()
  const progress = page.getByTestId('import-progress')
  await expect(progress.getByRole('heading', { level: 1 })).toContainText('帶完了，有幾項沒成功')
  const sent = api.calls.find((c) => c.op === 'import.run')!.input
  expect((sent.items as string[]).sort()).toEqual(['approvals:org', 'bot:a', 'bot:b', 'data:a', 'key:opencode-go', 'memory:a', 'room:g', 'routine:r1', 'skills:a', 'team:t'])
  expect(sent.names).toEqual([{ key: 'bot:b', name: '舊小秘' }])
  await settle(page)
  await page.screenshot({ path: shot(`${info.project.name}-v10-done`), fullPage: true })
})
