import { expect, test } from '@playwright/test'
import { createServer, type Server } from 'node:http'
import type { AddressInfo } from 'node:net'
import { execFileSync } from 'node:child_process'
import { pastOnboarding } from '../e2e/real-helpers'

// 真的端對端（make e2e-docker，需要 Docker 與 make box 建好的映像）：
// 網頁建 Bot → 設定模型（指向這裡的假 OpenAI 相容服務）→ 送訊息 → Bot 的電腦開機、Hermes 呼叫模型 → 網頁看到串流回覆。
// 假模型跑在主機上；Bot 電腦經 product.sock → Model Proxy（在 XBot 服務裡）連過來，真金鑰不進容器。

const REPLY = ['你好，', '我是測試模型，', '收到你的訊息了。']

let server: Server
let base = ''
const chatCalls: string[] = []
let botId = ''

test.beforeAll(async () => {
  test.skip(process.env.XBOT_E2E_DOCKER !== '1', '要真的開 Bot 電腦：XBOT_E2E_DOCKER=1（make e2e-docker）')
  server = createServer((req, res) => {
    let body = ''
    req.on('data', (c) => (body += c))
    req.on('end', () => {
      const url = req.url ?? ''
      if (url.endsWith('/models')) {
        res.setHeader('Content-Type', 'application/json')
        res.end(JSON.stringify({ object: 'list', data: [{ id: 'fake-model', object: 'model', owned_by: 'xbot' }] }))
        return
      }
      if (!url.endsWith('/chat/completions')) {
        res.statusCode = 404
        res.end()
        return
      }
      chatCalls.push(req.headers.authorization ?? '')
      const stream = (JSON.parse(body || '{}') as { stream?: boolean }).stream
      const usage = { prompt_tokens: 20, completion_tokens: 8, total_tokens: 28 }
      if (!stream) {
        res.setHeader('Content-Type', 'application/json')
        res.end(JSON.stringify({ id: 'c1', object: 'chat.completion', created: 1, model: 'fake-model', choices: [{ index: 0, message: { role: 'assistant', content: REPLY.join('') }, finish_reason: 'stop' }], usage }))
        return
      }
      res.setHeader('Content-Type', 'text/event-stream')
      const chunk = (o: unknown) => res.write(`data: ${typeof o === 'string' ? o : JSON.stringify(o)}\n\n`)
      const delta = (d: object, finish: string | null = null) => ({ id: 'c1', object: 'chat.completion.chunk', created: 1, model: 'fake-model', choices: [{ index: 0, delta: d, finish_reason: finish }] })
      chunk(delta({ role: 'assistant', content: '' }))
      let i = 0
      const next = () => {
        if (i < REPLY.length) {
          chunk(delta({ content: REPLY[i++] }))
          setTimeout(next, 300) // 慢慢吐，網頁才看得到一段一段出現
          return
        }
        chunk(delta({}, 'stop'))
        chunk({ id: 'c1', object: 'chat.completion.chunk', created: 1, model: 'fake-model', choices: [], usage })
        chunk('[DONE]')
        res.end()
      }
      next()
    })
  })
  await new Promise<void>((r) => server.listen(0, '127.0.0.1', r))
  base = `http://127.0.0.1:${(server.address() as AddressInfo).port}/v1`
})

test.afterAll(async () => {
  await new Promise((r) => (server ? server.close(r) : r(null)))
  // 收掉這次開的 Bot 電腦（只動這個 Bot 的容器）
  if (botId) {
    const ids = execFileSync('docker', ['ps', '-aq', '--filter', `label=xbot.bot=${botId}`]).toString().split('\n').filter(Boolean)
    if (ids.length) execFileSync('docker', ['rm', '-f', ...ids])
  }
})

test('建 Bot → 送訊息 → 真的 Bot 電腦＋Hermes 回覆，網頁即時看到', async ({ page }) => {
  // 跳過首次設定（W32）：這個測試從側欄的「新增 Bot」開始。
  await pastOnboarding(page.request)
  await page.goto('/')
  const nav = page.getByRole('navigation', { name: '側欄' })
  await nav.getByRole('button', { name: '新增 Bot' }).first().click()
  const dlg = page.getByRole('dialog', { name: '新增 Bot' })
  await dlg.getByLabel('名字').fill('測試員')
  await dlg.getByRole('button', { name: '建立「測試員」' }).click()
  const main = page.getByRole('main', { name: '和測試員的對話' })
  // 第一個 Bot 的首頁是 W32 的「第一次」畫面（招呼＋之後再做），不是一般的空對話。
  await expect(main.getByText('嗨，我是測試員')).toBeVisible()
  botId = new URL(page.url()).pathname.split('/')[2]

  // 新的資料目錄沒有模型：從提示卡設定
  await main.getByRole('region', { name: '還沒設定模型' }).getByRole('button', { name: '設定模型' }).click()
  const form = page.getByRole('dialog', { name: '設定模型' })
  await form.getByLabel('服務網址（OpenAI 相容）').fill(base)
  await form.getByLabel('金鑰').fill('sk-e2e-real-key-stays-in-proxy')
  await form.getByLabel('預設模型').fill('fake-model')
  await form.getByRole('button', { name: '儲存' }).click()
  await expect(form).toBeHidden()

  const box = main.getByRole('textbox')
  await box.fill('嗨，請自我介紹')
  await box.press('Enter')
  await expect(main.getByTestId('msg-owner')).toHaveText('嗨，請自我介紹')
  // 第一次要開機（桌面就緒上限 75 秒）
  await expect(main.getByTestId('msg-bot')).toContainText(REPLY.join(''), { timeout: 240_000 })
  await expect(main.getByRole('button', { name: '停止測試員' })).toBeHidden({ timeout: 30_000 })
  await expect(main.getByTestId('bot-status')).toHaveText('待命')
  await page.screenshot({ path: 'e2e-results/screens/docker-chat-reply.png' })
  expect(chatCalls.length).toBeGreaterThan(0)
  // 模型收到的是真金鑰（Model Proxy 換上的），容器裡只有 worker 金鑰
  expect(chatCalls.every((a) => a === 'Bearer sk-e2e-real-key-stays-in-proxy')).toBe(true)

  // 重新整理後歷史還在（chat.read）
  await page.reload()
  await expect(page.getByRole('main', { name: '和測試員的對話' }).getByTestId('msg-bot')).toContainText(REPLY.join(''))
})
