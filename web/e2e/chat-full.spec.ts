import { expect, test } from './fixtures'
import { MockServer, settle } from './mock'

// W11 對話完整版：回覆排版（C07）、訊息動作與重試（C02、C11）、模型選擇器（C06）、
// 對話串選單與空狀態（C03）、目前進度（C12）。截圖存在 e2e-results/screens，和畫板並排比對。
const shot = (name: string) => `e2e-results/screens/${name}.png`

test.beforeEach(async ({ page }, info) => {
  if (info.project.name === 'desktop') await page.setViewportSize({ width: 1440, height: 960 })
})

const REPORT = `# 三家競品定價比較 · 10 月

這次比了 **12 項主力商品**，資料來自三家蝦皮賣場的公開頁面，抓取時間是 10 月 17 日下午 2 點。完整表格在 \`/workspace/reports/pricing-1017.xlsx\`，也可以在[檔案](https://example.com/files)裡打開。

> [!NOTE]
> 價差在 5% 以內的 9 項先不動。下週一的週報會再比一次，有變化再告訴你。

> [!WARNING]
> 3 項調價已送進待你核定。你允許之前，我不會動賣場上的價格。

## 價差最大的 7 項

| 商品 | 我們 | 最低競品 | 價差 | 建議價 | 狀態 |
|---|--:|--:|--:|--:|---|
| 真空保溫瓶 500ml | 590 | 499 好物 | +18.2% | 529 | 待你核定 |
| 竹纖維餐具組 | 450 | 399 好物 | +12.8% | 419 | 待你核定 |
| 經典帆布托特包 | 1,280 | 1,180 日日 | +8.5% | 1,199 | 待你核定 |
| 陶瓷內膽保溫杯 | 629 | 610 簡單 | +3.1% | — | 不動 |
| 隨行咖啡杯 360ml | 450 | 439 日日 | +2.5% | — | 不動 |
| 玻璃保鮮盒三件組 | 380 | 375 好物 | +1.3% | — | 不動 |
| 不鏽鋼吸管組 | 120 | 119 簡單 | +0.8% | — | 不動 |

## 怎麼算的

$$
建議價 = min(p₁, p₂, p₃) × 1.02
$$

\`\`\`python
def suggest(ours, rivals, margin=1.02):
    low = min(rivals)
    if ours <= low * 1.05:
        return None  # 差 5% 以內不動
    return round(low * margin)
\`\`\`

待辦：

- [x] 抓三家價格
- [x] 算價差
- [ ] 等你核定調價

> 好物生活館的帆布包已經連續 3 週比我們便宜 7% 左右，可能是長期定價，不是短期促銷。

---

不安全的連結不會變成連結：[點我](javascript:alert(1))，HTML 也只會顯示成文字：<img src=x onerror=alert(1)>`

test('回覆排版（C07）：標題、提示框、表格工具列、程式碼高亮與複製、待辦、安全', async ({ page, context }, info) => {
  await context.grantPermissions(['clipboard-read', 'clipboard-write']).catch(() => {})
  const api = new MockServer(page)
  await api.install()
  const bot = api.addBot({ name: '小秘', title: '幕僚長', shape: 'cursor', color: 'blue' })
  const t = api.addThread(bot.id, '比價報告')
  api.addMessage(t.id, { sender: 'owner', content: '把這次比價整理成一份報告給我', task_id: 'r1' })
  api.addMessage(t.id, { sender: 'bot:' + bot.id, content: REPORT, task_id: 'r1', status: 'complete' })
  let alerted = false
  page.on('dialog', (d) => {
    alerted = true
    void d.dismiss()
  })
  await page.goto(`/bots/${bot.id}/threads/${t.id}`)
  const main = page.getByRole('main', { name: '和小秘的對話' })
  const msg = main.getByTestId('msg-bot')
  await expect(msg.getByRole('heading', { name: '三家競品定價比較 · 10 月' })).toBeVisible()
  await expect(msg.getByRole('note').first()).toContainText('提示')
  await expect(msg.getByRole('note').nth(1)).toContainText('需要你決定')
  const table = msg.getByTestId('md-table')
  await expect(table).toContainText('7 列 · 6 欄')
  await expect(table.getByRole('row')).toHaveCount(6)
  // 程式碼高亮是延遲載入的：等關鍵字上色
  const code = msg.getByTestId('md-code')
  await expect(code.locator('.hljs-keyword').first()).toHaveText('def')
  await expect(msg.getByRole('math')).toContainText('建議價')
  await expect(msg.getByRole('checkbox')).toHaveCount(3)
  await expect(msg.getByRole('link', { name: '點我' })).toHaveCount(0)
  await expect(msg.locator('img')).toHaveCount(0)
  await expect(msg.getByRole('link', { name: '檔案' })).toHaveAttribute('rel', /noopener/)
  await settle(page)
  // 整頁不會被寬表格撐開（只在框內橫向捲動）
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
  await page.screenshot({ path: shot(`${info.project.name}-markdown-page`) })
  await msg.screenshot({ path: shot(`${info.project.name}-markdown`) })

  await table.getByRole('button', { name: '顯示全部' }).click()
  await expect(table.getByRole('row')).toHaveCount(8)
  await table.getByRole('searchbox', { name: '搜尋表格' }).fill('吸管')
  await expect(table.getByRole('row')).toHaveCount(2)
  const download = page.waitForEvent('download')
  await table.getByRole('button', { name: '下載 CSV' }).click()
  expect((await download).suggestedFilename()).toBe('表格.csv')

  if (info.project.name === 'desktop') {
    await code.getByRole('button', { name: '複製' }).click()
    await expect(code.getByRole('button', { name: '已複製' })).toBeVisible()
    expect(await page.evaluate(() => navigator.clipboard.readText())).toContain('def suggest')
  }
  // 原始內容
  await main.getByRole('button', { name: '原始內容' }).click()
  await expect(msg.locator('pre').first()).toContainText('# 三家競品定價比較')
  expect(alerted).toBe(false)
})

test('流程圖（C07）：mermaid 畫成方塊和箭頭、可切換原始碼；一行 A → B → C 畫成步驟', async ({ page }, info) => {
  const api = new MockServer(page)
  await api.install()
  const bot = api.addBot({ name: '小秘', title: '幕僚長', shape: 'cursor', color: 'blue' })
  const t = api.addThread(bot.id, '流程')
  api.addMessage(t.id, { sender: 'owner', content: '發文流程是什麼', task_id: 'f1' })
  api.addMessage(t.id, {
    sender: 'bot:' + bot.id,
    content: '流程整理好了。\n\n選題 → 寫稿 → 核准 → 發文\n\n```mermaid\nflowchart LR\n  A[選題] --> B{要核准嗎}\n  B -- 要 --> C[送核准卡]\n  B -- 不用 --> D[直接發文]\n```',
    task_id: 'f1',
    status: 'complete',
  })
  await page.goto(`/bots/${bot.id}/threads/${t.id}`)
  const msg = page.getByRole('main', { name: '和小秘的對話' }).getByTestId('msg-bot')
  await expect(msg.getByTestId('md-flow').getByRole('listitem')).toHaveText(['選題', '寫稿', '核准', '發文'])
  const diagram = msg.getByTestId('md-diagram')
  const drawing = diagram.getByRole('img', { name: '流程圖' })
  await expect(drawing.locator('svg').first()).toBeVisible({ timeout: 15_000 })
  await expect(drawing).toContainText('送核准卡')
  await diagram.getByRole('button', { name: '原始碼' }).click()
  await expect(diagram.locator('pre')).toContainText('flowchart LR')
  await diagram.getByRole('button', { name: '看圖' }).click()
  await expect(drawing).toBeVisible()
  await settle(page)
  await msg.screenshot({ path: shot(`${info.project.name}-diagram`) })
})

test('訊息動作與重試（C02、C11）：工作未完成 → 再試一次會再送一次同樣的話；長訊息收起', async ({ page }, info) => {
  const api = new MockServer(page)
  await api.install()
  const bot = api.addBot({ name: '小秘', shape: 'cursor', color: 'blue' })
  const t = api.addThread(bot.id, '匯出訂單')
  const long = Array.from({ length: 30 }, (_, i) => `第 ${i + 1} 行：這是一段很長的需求說明，要它照著做。`).join('\n')
  api.addMessage(t.id, { sender: 'owner', content: long, task_id: 'k0' }, Date.now() - 600_000)
  api.addMessage(t.id, { sender: 'bot:' + bot.id, content: '收到，我照這 30 行做完了。', task_id: 'k0', status: 'complete' }, Date.now() - 590_000)
  api.addMessage(t.id, { sender: 'owner', content: '把昨天的訂單從蝦皮賣家中心匯出來。', task_id: 'k1' })
  api.addMessage(t.id, { sender: 'bot:' + bot.id, content: '蝦皮賣家中心的登入過期了，我進不去後台，昨天的訂單沒辦法匯出。', task_id: 'k1', status: 'error' })
  await page.goto(`/bots/${bot.id}/threads/${t.id}`)
  const main = page.getByRole('main', { name: '和小秘的對話' })
  const outcome = main.getByTestId('outcome')
  await expect(outcome).toContainText('工作未完成')
  const ownerLong = main.getByTestId('msg-owner').first()
  await expect(ownerLong.getByRole('button', { name: '顯示全部' })).toBeVisible()
  const box = await ownerLong.boundingBox()
  expect(box!.height).toBeLessThan(400)
  await settle(page)
  await page.screenshot({ path: shot(`${info.project.name}-actions`) })
  await ownerLong.getByRole('button', { name: '顯示全部' }).click()
  await expect(ownerLong.getByRole('button', { name: '收起' })).toBeVisible()

  await outcome.getByRole('button', { name: '再試一次' }).click()
  await expect.poll(() => api.calls.filter((c) => c.op === 'chat.send').map((c) => c.input)).toEqual([{ bot_id: bot.id, text: '把昨天的訂單從蝦皮賣家中心匯出來。', thread_id: t.id }])
})

test('模型選擇器（C06）：換這個 Bot 的模型與推理強度', async ({ page }, info) => {
  const api = new MockServer(page)
  await api.install()
  const bot = api.addBot({ name: '小秘', shape: 'cursor', color: 'blue' })
  const t = api.addThread(bot.id, '比價')
  api.addMessage(t.id, { sender: 'bot:' + bot.id, content: '收到。我請阿哲去抓三家的價格。', status: 'complete' })
  await page.goto(`/bots/${bot.id}/threads/${t.id}`)
  const main = page.getByRole('main', { name: '和小秘的對話' })
  test.skip(info.project.name === 'mobile', '手機頁首照 L03（沒有模型）；手機在 Bot 資料 › 進階 › 引擎與模型改')
  // 頁首只留常用的（2026-10-20）：模型在「⋯」裡，那一項寫出目前的模型
  await main.getByRole('button', { name: '更多', exact: true }).click()
  await page.getByRole('menuitem', { name: /^模型：gpt-4.1-mini/ }).click()
  const picker = page.getByRole('dialog', { name: '選擇模型' })
  await expect(picker.getByRole('menuitemradio', { name: /gpt-4.1-mini/ })).toHaveAttribute('aria-checked', 'true')
  await expect(picker.getByRole('radio', { name: '中' })).toHaveAttribute('aria-checked', 'true')
  await settle(page)
  await page.screenshot({ path: shot(`${info.project.name}-model-picker`) })
  await picker.getByRole('radio', { name: '高' }).click()
  await expect.poll(() => api.calls.find((c) => c.op === 'bots.update')?.input).toEqual({ id: bot.id, effort: 'high' })
  await expect(picker.getByRole('radio', { name: '高' })).toHaveAttribute('aria-checked', 'true')
  await picker.getByLabel('其他模型名稱').fill('gpt-4.1')
  await picker.getByRole('button', { name: '使用' }).click()
  await expect(picker).toBeHidden()
  expect(api.calls.filter((c) => c.op === 'bots.update').at(-1)?.input).toEqual({ id: bot.id, model: 'openai/gpt-4.1' })
  await main.getByRole('button', { name: '更多', exact: true }).click()
  await expect(page.getByRole('menuitem', { name: '模型：gpt-4.1', exact: true })).toBeVisible()
  await page.keyboard.press('Escape')
})

test('對話串選單與空狀態（C03）：切換、開新的、建議放進輸入框', async ({ page }, info) => {
  const api = new MockServer(page)
  await api.install()
  const bot = api.addBot({ name: '抓蟲', title: 'Bug 重現', description: '照你的描述重做一次，錄下步驟和截圖', shape: 'shield', color: 'red' })
  const a = api.addThread(bot.id, '結帳頁沒反應')
  api.addMessage(a.id, { sender: 'owner', content: '結帳頁按了沒反應' }, Date.now() - 3 * 3600_000)
  const b = api.addThread(bot.id, '主要對話')
  api.addMessage(b.id, { sender: 'owner', content: '登入頁跑版' })
  await page.goto(`/bots/${bot.id}/threads/${b.id}`)
  const main = page.getByRole('main', { name: '和抓蟲的對話' })
  await main.getByRole('button', { name: '對話串：主要對話' }).click()
  if (info.project.name === 'mobile') {
    // 手機：對話串是從右邊滑出來的抽屜
    const drawer = page.getByRole('dialog', { name: '抓蟲的對話串' })
    await expect(drawer.getByRole('button', { name: /^主要對話/ })).toHaveAttribute('aria-current', 'true')
    await settle(page)
    await page.screenshot({ path: shot(`${info.project.name}-thread-menu`) })
    await drawer.getByRole('button', { name: /^結帳頁沒反應/ }).click()
    await expect(page).toHaveURL(new RegExp(`/threads/${a.id}$`))
    await expect(main.getByTestId('msg-owner')).toHaveText('結帳頁按了沒反應')
    await main.getByRole('button', { name: /^對話串：/ }).click()
    await page.getByRole('dialog', { name: '抓蟲的對話串' }).getByRole('button', { name: '新對話串' }).click()
  } else {
    const menu = page.getByRole('menu', { name: '抓蟲的對話串' })
    await expect(menu.getByRole('menuitemradio', { name: /主要對話/ })).toHaveAttribute('aria-checked', 'true')
    await settle(page)
    await page.screenshot({ path: shot(`${info.project.name}-thread-menu`) })
    await menu.getByRole('menuitemradio', { name: /結帳頁沒反應/ }).click()
    await expect(page).toHaveURL(new RegExp(`/threads/${a.id}$`))
    await expect(main.getByTestId('msg-owner')).toHaveText('結帳頁按了沒反應')
    await main.getByRole('button', { name: /^對話串：/ }).click()
    await page.getByRole('menuitem', { name: '新對話串' }).click()
  }
  await expect(page).toHaveURL(/\/threads\/new$/)
  const empty = main.getByTestId('chat-empty')
  await expect(empty).toContainText('嗨，我是抓蟲。')
  // 已經聊過的 Bot：開新對話只打招呼，不再出現「可以這樣開始」（2026-10-20）
  await expect(empty.getByRole('region', { name: '可以這樣開始' })).toHaveCount(0)
  await settle(page)
  await page.screenshot({ path: shot(`${info.project.name}-chat-empty-full`) })

  // 第一次和這個 Bot 聊：才有建議，點了放進輸入框
  const fresh = api.addBot({ name: '小秘', shape: 'cursor', color: 'blue' })
  await page.goto(`/bots/${fresh.id}/threads/new`)
  const main2 = page.getByRole('main', { name: '和小秘的對話' })
  const first = main2.getByTestId('chat-empty')
  await first.getByRole('button', { name: /看看你的電腦/ }).click()
  await expect(main2.getByRole('textbox')).toHaveValue('看看你的電腦，告訴我裡面有什麼')
  await expect(main2.getByRole('textbox')).toBeFocused()
})

test('目前進度（C12）與電腦按鈕；載入更早的訊息', async ({ page }, info) => {
  const api = new MockServer(page)
  await api.install()
  const bot = api.addBot({ name: '小秘', shape: 'cursor', color: 'blue' })
  const t = api.addThread(bot.id, '比價')
  const t0 = Date.now() - 3 * 86_400_000
  for (let i = 0; i < 120; i++) api.addMessage(t.id, { sender: i % 2 ? 'bot:' + bot.id : 'owner', content: `舊訊息 ${i + 1}`, status: i % 2 ? 'complete' : undefined }, t0 + i * 60_000)
  api.addMessage(t.id, { sender: 'owner', content: '早上的日報做完了嗎？', task_id: 'd1' }, Date.now() - 4 * 3600_000)
  api.addMessage(t.id, { sender: 'bot:' + bot.id, content: '做完了，放在 /workspace/daily.md。', task_id: 'd1', status: 'complete' }, Date.now() - 4 * 3600_000 + 60_000)
  api.addMessage(t.id, { sender: 'owner', content: '幫我比較蝦皮上三家競品的定價', task_id: 'k1' }, Date.now() - 72_000)
  await page.goto(`/bots/${bot.id}/threads/${t.id}`)
  const main = page.getByRole('main', { name: '和小秘的對話' })
  await expect(main.getByTestId('msg-owner').last()).toHaveText('幫我比較蝦皮上三家競品的定價')
  // 一頁 100 則：還有更早的
  await expect(main.getByText('舊訊息 23', { exact: true })).toHaveCount(0)
  await main.getByRole('button', { name: '載入更早的訊息' }).click()
  await expect(main.getByText('舊訊息 1', { exact: true })).toBeAttached()
  await expect(main.getByRole('button', { name: '載入更早的訊息' })).toHaveCount(0)

  const base = { bot_id: bot.id, thread_id: t.id, task_id: 'k1' }
  const started = Date.now() - 72_000
  await api.emit(
    { ...base, kind: 'task.state', at: started, payload: { state: 'running' } },
    { ...base, kind: 'chat.tool_call', at: started, payload: { type: 'tool_call', tool: { id: 's1', name: 'read_file', context: '讀取自家 12 項主力商品現價' } } },
    { ...base, kind: 'chat.tool_result', at: started, payload: { type: 'tool_result', tool: { id: 's1', ok: true } } },
    { ...base, kind: 'chat.tool_call', at: Date.now() - 12_000, payload: { type: 'tool_call', tool: { id: 's2', name: 'browser_open', context: '打開蝦皮搜尋「保溫瓶」' } } },
  )
  if (info.project.name === 'mobile') {
    // 手機：頭像打開 Bot 資料，目前進度在對話串清單裡
    await main.getByRole('button', { name: /^對話串：/ }).click()
    await page.getByRole('dialog', { name: '小秘的對話串' }).getByRole('button', { name: '目前進度' }).click()
  }
  else {
    await main.getByRole('button', { name: '更多' }).click()
    await page.getByRole('menuitem', { name: '目前進度' }).click()
  }
  const dlg = page.getByRole('dialog', { name: '小秘的目前進度' })
  await expect(dlg).toContainText('有工作正在進行')
  await expect(dlg).toContainText('幫我比較蝦皮上三家競品的定價')
  await expect(dlg).toContainText('第 2 步')
  await expect(dlg).toContainText('打開蝦皮搜尋「保溫瓶」')
  await expect(dlg).toContainText('早上的日報做完了嗎？')
  await settle(page)
  await page.screenshot({ path: shot(`${info.project.name}-progress`) })
  await page.keyboard.press('Escape')
  await expect(dlg).toBeHidden()

  // 電腦按鈕（B14）：寬螢幕在對話右邊打開電腦面板（E01）；手機到全視窗的電腦頁（L04）。
  await main.getByRole('button', { name: '小秘的電腦' }).click()
  if (info.project.name === 'desktop') await expect(page.getByTestId('computer-panel')).toBeVisible()
  else await expect(page).toHaveURL(new RegExp(`/bots/${bot.id}/computer$`))
  await expect(page.getByRole('heading', { name: '小秘的電腦' })).toBeVisible()
})
