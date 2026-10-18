import { expect, test, type Page } from '@playwright/test'
import { MockServer, settle } from './mock'

// W32：首次設定（A03～A06、A09、A10）與 Bot 設定（G02～G14）用假服務跑，截圖和 A、G 畫板並排比對。
const shot = (name: string) => `e2e-results/screens/${name}.png`

test.beforeEach(async ({ page }, info) => {
  if (info.project.name === 'desktop') await page.setViewportSize({ width: 1440, height: 900 })
})

const STATE = { completed: false, tour_done: false, dismissed: [] as string[] }

function onboarding(api: MockServer, over: Record<string, unknown> = {}) {
  const st = { ...STATE }
  const status = () => ({
    first_run: api.bots.length === 0 && !st.completed,
    bots: api.bots.length,
    state: st,
    engines: [
      { engine: 'claude-code', label: 'Claude Code', plan: 'Claude', logged_in: true, login: 'claude login' },
      { engine: 'codex', label: 'Codex', plan: 'ChatGPT', logged_in: true, login: 'codex login' },
    ],
    local: [{ id: 'lmstudio', label: 'LM Studio', base_url: 'http://127.0.0.1:1234/v1', preset: 'lmstudio', models: ['qwen3-8b', 'gemma-3-12b'] }],
    computer: { docker: true, image: 'xbot-box:dev', ready: true },
    ...over,
  })
  Object.assign(api.override, {
    'onboard.status': status,
    'onboard.update': (i: Record<string, unknown>) => {
      if (i.completed !== undefined) st.completed = !!i.completed
      if (i.tour_done !== undefined) st.tour_done = !!i.tour_done
      if (i.dismiss) st.dismissed = [...st.dismissed, i.dismiss as string]
      return st
    },
    'engines.link_credentials': (i: Record<string, unknown>) => ({ bot_id: i.bot_id, linked: true }),
    'models.presets': () => ({ presets: [{ id: 'openrouter', label: 'OpenRouter', base_url: 'https://openrouter.ai/api/v1', transport: 'chat_completions', local: false, models: ['anthropic/claude-sonnet-5.5'], configured: false, signup_url: '' }] }),
    'models.add_provider': () => ({ provider: { id: 'openrouter' } }),
    'bots.suggest': () => ({ name: '阿哲', title: '選品研究員 · 熱賣居家小物', description: '每天早上看蝦皮熱賣榜，挑出居家小物，比價後整理成表格，附上來源網址。', soul: '你是阿哲', by: 'rules' }),
  })
  return st
}

test('首次設定 A03 → A04 → 首頁之後再做（A05）→ 導覽三站（A10）', async ({ page }, info) => {
  const api = new MockServer(page)
  api.modelsReady = false
  onboarding(api)
  await api.install()
  await page.goto('/')

  const connect = page.getByRole('region', { name: '接上 AI' })
  await expect(connect.getByRole('list', { name: '找到的 AI' }).getByRole('listitem')).toHaveCount(3)
  await expect(page.getByText('簡化：6 步 → 2 步')).toBeVisible()
  await settle(page)
  await page.screenshot({ path: shot(`${info.project.name}-w32-a03`), fullPage: true })
  await connect.getByRole('button', { name: '改用 API 金鑰' }).click()
  await expect(connect.getByLabel('API 金鑰')).toBeVisible()
  await page.screenshot({ path: shot(`${info.project.name}-w32-a03-key`), fullPage: true })
  await connect.getByRole('button', { name: '用這個：Claude Code' }).click()

  const meet = page.getByRole('region', { name: '認識小秘' })
  await expect(meet.getByTestId('computer-line')).toContainText('已備好')
  await settle(page)
  await page.screenshot({ path: shot(`${info.project.name}-w32-a04`), fullPage: true })
  await meet.getByRole('button', { name: '換個樣子' }).click()
  await meet.getByRole('radio', { name: '睜大' }).click()
  await page.screenshot({ path: shot(`${info.project.name}-w32-a04-looks`), fullPage: true })
  await meet.getByRole('button', { name: '開始' }).click()

  await expect(page).toHaveURL(/\/bots\/bot\d+$/)
  expect(api.calls.find((c) => c.op === 'bots.create')?.input).toMatchObject({ name: '小秘', engine: 'claude-code', eyes: 'wide', shape: 'cursor' })
  expect(api.calls.find((c) => c.op === 'engines.link_credentials')).toBeTruthy()
  const home = page.getByTestId('first-home')
  await expect(home).toContainText('嗨，我是小秘')
  await expect(page.getByRole('group', { name: '可以這樣開始' })).toBeVisible()
  await settle(page)
  await page.screenshot({ path: shot(`${info.project.name}-w32-a05`), fullPage: true })

  await home.getByRole('button', { name: /開始導覽/ }).click()
  const tip = page.getByRole('dialog', { name: /導覽 1 \/ 3/ })
  await expect(tip).toBeVisible()
  await expect(page.getByTestId('tour-ring')).toBeVisible()
  // 提示框貼在目標旁邊：輸入框在下面，提示框在它上面
  const ring = await page.getByTestId('tour-ring').boundingBox()
  const box = await tip.boundingBox()
  if (info.project.name === 'desktop') expect(box!.y + box!.height).toBeLessThanOrEqual(ring!.y)
  else expect(box!.y + box!.height).toBeGreaterThan(page.viewportSize()!.height - 4) // 手機是底部 sheet
  await page.screenshot({ path: shot(`${info.project.name}-w32-a10-1`) })
  await tip.getByRole('button', { name: '下一步' }).click()
  await expect(page.getByRole('dialog', { name: /導覽 2 \/ 3/ })).toContainText('接手')
  await page.screenshot({ path: shot(`${info.project.name}-w32-a10-2`) })
  await page.getByRole('button', { name: '下一步' }).click()
  const third = page.getByRole('dialog', { name: /導覽 3 \/ 3/ })
  await expect(third).toContainText('核准卡')
  await page.screenshot({ path: shot(`${info.project.name}-w32-a10-3`) })
  await third.getByRole('button', { name: '完成' }).click()
  await expect(page.getByTestId('tour')).toBeHidden()
  await expect(page.getByText('導覽看完了')).toBeVisible()
  expect(api.calls.some((c) => c.op === 'onboard.update' && c.input.tour_done === true)).toBe(true)
})

test('還沒有 Bot（A09）：一句話建議、範本', async ({ page }, info) => {
  const api = new MockServer(page)
  onboarding(api, { first_run: false })
  await api.install()
  await page.goto('/')
  await expect(page.getByRole('heading', { level: 1, name: '建立第一個 Bot' })).toBeVisible()
  await expect(page.getByRole('navigation', { name: '側欄' }).or(page.getByRole('button', { name: '打開側欄' })).first()).toBeVisible()
  await page.getByLabel('一句話說你要它做什麼').fill('每天幫我找蝦皮上熱賣的居家小物，整理成表格')
  await page.getByRole('button', { name: '產生建議' }).click()
  const sug = page.getByRole('region', { name: '建議的 Bot' })
  await expect(sug).toContainText('系統建議 · 都可以改')
  await settle(page)
  await page.screenshot({ path: shot(`${info.project.name}-w32-a09`), fullPage: true })
  await sug.getByRole('button', { name: '建立阿哲' }).click()
  await expect(page).toHaveURL(/\/bots\/bot\d+$/)
  expect(api.calls.find((c) => c.op === 'bots.create')?.input).toMatchObject({ name: '阿哲', soul: '你是阿哲' })
})

// ── Bot 設定 ────────────────────────────────────────────

function botSettings(api: MockServer) {
  const xm = api.addBot({ name: '小秘', title: '幕僚長 · 每日重點與協調', description: '每天整理重點，把工作交給對的隊友', shape: 'cursor', color: 'blue', soul: '你是小秘，我的幕僚長。說話簡短、先講結論，用台灣用語。\n負責：每天早上 8:30 整理昨天到今天的變化、哪些事要我決定；把工作交給對的隊友。\n交付：先講結論，再列三個以內的重點。' })
  api.addBot({ name: '阿哲', shape: 'blob', color: 'orange' })
  const now = Date.now()
  Object.assign(api.override, {
    'engines.status': (i: Record<string, unknown>) => ({ bots: api.bots.filter((b) => b.id === i.bot_id).map((b) => ({ bot_id: b.id, name: b.name, engine: b.engine, engine_label: b.engine, needs_link: false, linked: false })), host: [] }),
    'models.presets': () => ({ presets: [] }),
    'models.get': () => ({ providers: [{ id: 'openai-api', label: 'OpenAI API', base_url: 'https://api.openai.com/v1', api_key_set: true, models: ['gpt-5.5'] }], default: { provider: 'openai-api', model: 'gpt-5.5' }, ready: true }),
    'skills.list': () => ({ skills: [], counts: { list: 0, library: 0, off: 0 }, computer: false, note: '這個 Bot 還沒有電腦；第一次開機會帶入內建技能。' }),
    'apps.list': () => ({
      connections: [
        { id: 'c1', app_id: 'gmail', name: 'gmail', title: 'Gmail', description: '', icon: 'mail', custom: false, transport: 'http', url: '', command: '', args: [], auth: 'oauth2', account: 'me@example.com', status: 'ok', error: '', secret_hint: '', header_names: [], env_names: [], missing_env: [], tools: [], disabled_tools: [], all_bots: false, bot_ids: [xm.id], enabled: true, checked_at: 0, last_used_at: 0, created_at: 0 },
        { id: 'c2', app_id: 'notion', name: 'notion', title: 'Notion', description: '', icon: 'notebook', custom: false, transport: 'http', url: '', command: '', args: [], auth: 'oauth2', account: '', status: 'needs_signin', error: '', secret_hint: '', header_names: [], env_names: [], missing_env: [], tools: [], disabled_tools: [], all_bots: false, bot_ids: [], enabled: true, checked_at: 0, last_used_at: 0, created_at: 0 },
      ],
    }),
    'apps.grant_to_bot': () => ({}),
    'hosts.list': () => ({ hosts: [{ id: 'local', name: '這台主機', kind: 'local', address: '', status: 'online', arch: 'amd64', max_running: 0, running: 1, resources: {}, bots: [], queued: 0, image_outdated: false, control_center: true, default: true, last_seen_at: now, created_at: 0 }] }),
    'usage.caps': () => ({ day: '', month: '', bots: [{ key: xm.id, label: '小秘', daily_usd: 10, monthly_usd: 200, spent_today_usd: 6.2, spent_month_usd: 168, exceeded: false, daily_micros: 0, monthly_micros: 0, spent_day_micros: 0, spent_month_micros: 0 }], providers: [] }),
    'audit.list': () => ({
      entries: [
        { id: 4, actor: 'owner', via: 'ui', op: 'xbot.bots.update', request_id: '', target: xm.id, params: { id: xm.id, soul: '…' }, status: 'ok', error_code: '', created_at: now - 3_600_000 },
        { id: 3, actor: 'agent:a1', via: 'mcp', op: 'xbot.bots.update', request_id: '', target: xm.id, params: { id: xm.id, approval_mode: 'auto' }, status: 'pending_approval', error_code: '', created_at: now - 7_200_000 },
        { id: 2, actor: 'bot:' + xm.id, via: 'mcp', op: 'xbot.skills.create', request_id: '', target: xm.id, params: {}, status: 'ok', error_code: '', created_at: now - 86_400_000 },
        { id: 1, actor: 'owner', via: 'ui', op: 'xbot.bots.create', request_id: '', target: xm.id, params: {}, status: 'ok', error_code: '', created_at: now - 3 * 86_400_000 },
      ],
    }),
    'bots.export': () => ({ filename: '小秘.xbot', content: JSON.stringify({ format: 'xbot.bot', version: 1, bot: { name: '小秘' } }), bytes: 120, skills: 3, apps: 1 }),
    'bots.import': (i: Record<string, unknown>) => {
      const out = {
        preview: !!i.preview,
        bot: { ...xm, id: '', name: (i.name as string) || '小秘 2', approval_mode: 'ask' },
        renamed: true,
        original: '小秘',
        items: [
          { kind: 'persona', status: 'ok', title: '人設與頭像', detail: '你已經有「小秘」，先叫「小秘 2」' },
          { kind: 'skills', status: 'ok', title: '技能 3 個', detail: '放進技能庫，先停用；你看過內容再裝上' },
          { kind: 'apps', status: 'action', title: '應用程式 1 個', detail: 'Gmail 要你登入；可以之後再登入，Bot 先建立' },
          { kind: 'permissions', status: 'info', title: '權限', detail: '一律從「先問我」開始；引擎與模型用你這裡的預設' },
        ],
        skills_added: [] as string[],
        skills_merged: [],
        apps_missing: ['Gmail'],
        remaining: 48,
        can_create: true,
        exported_at: now - 86_400_000,
        bytes: 3200,
      }
      if (i.preview) return out
      const b = api.addBot({ name: out.bot.name, title: xm.title, shape: 'cursor', color: 'blue' })
      return { ...out, bot: b, skills_added: ['s1', 's2', 's3'] }
    },
  })
  return xm
}

async function openSettings(page: Page, path: string) {
  const api = new MockServer(page)
  onboarding(api, { first_run: false })
  const xm = botSettings(api)
  await api.install()
  await page.goto(path.replace(':id', xm.id))
  return { api, xm }
}

const main = (page: Page) => page.getByRole('main')

test('Bot 設定 · 基本（G02）＋外觀（G03）＋換成圖片（G04）', async ({ page }, info) => {
  const { api, xm } = await openSettings(page, '/settings/bots/:id')
  const m = main(page)
  await expect(m.getByRole('navigation', { name: 'Bot 設定分頁' }).getByRole('link')).toHaveText(['基本', '能力', '權限與花費', '進階'])
  if (info.project.name === 'desktop') await expect(page.getByText('簡化：12 個分頁 → 4 個')).toBeVisible()
  await settle(page)
  await page.screenshot({ path: shot(`${info.project.name}-w32-g02`), fullPage: true })

  await m.getByRole('button', { name: '換外觀' }).click()
  const sheet = page.getByRole('dialog', { name: '外觀' })
  await sheet.getByRole('radiogroup', { name: '形狀' }).getByRole('radio', { name: '星星' }).click()
  await expect.poll(() => api.calls.filter((c) => c.op === 'bots.update').at(-1)?.input).toEqual({ id: xm.id, shape: 'star' })
  await sheet.getByRole('radiogroup', { name: '眼睛' }).getByRole('radio', { name: '開心' }).click()
  await sheet.getByRole('radio', { name: '粉紅' }).click()
  await expect.poll(() => api.calls.filter((c) => c.op === 'bots.update').at(-1)?.input).toEqual({ id: xm.id, color: 'pink' })
  await settle(page)
  await page.screenshot({ path: shot(`${info.project.name}-w32-g03`) })
  await sheet.getByRole('button', { name: '上傳圖片' }).click()

  // 上傳一張照片 → 選圓形裁切 → 用這張：存成 256×256 的圖片
  const dlg = page.getByRole('dialog', { name: '換成圖片' })
  const png = await page.evaluate(() => {
    const c = document.createElement('canvas')
    c.width = 600
    c.height = 400
    const ctx = c.getContext('2d')!
    const g = ctx.createLinearGradient(0, 0, 600, 400)
    g.addColorStop(0, '#d9746b')
    g.addColorStop(1, '#3b3a6b')
    ctx.fillStyle = g
    ctx.fillRect(0, 0, 600, 400)
    ctx.fillStyle = '#1b1b2b'
    ctx.beginPath()
    ctx.arc(300, 260, 120, 0, Math.PI * 2)
    ctx.fill()
    return c.toDataURL('image/png').split(',')[1]
  })
  await dlg.getByLabel('選擇頭像圖片').setInputFiles({ name: 'portrait.png', mimeType: 'image/png', buffer: Buffer.from(png, 'base64') })
  await expect(dlg.getByTestId('crop')).toBeVisible()
  await dlg.getByRole('radio', { name: '圓', exact: true }).click()
  await dlg.getByRole('button', { name: '放大' }).click()
  await settle(page)
  await page.screenshot({ path: shot(`${info.project.name}-w32-g04`) })
  await dlg.getByRole('button', { name: '用這張' }).click()
  await expect.poll(() => api.calls.filter((c) => c.op === 'bots.update').at(-1)?.input.shape).toBe('circle')
  const image = api.calls.filter((c) => c.op === 'bots.update').at(-1)?.input.image as string
  expect(image).toMatch(/^data:image\/(webp|png);base64,/)
  const size = await page.evaluate(
    (src) => new Promise<number[]>((ok) => {
      const i = new Image()
      i.onload = () => ok([i.naturalWidth, i.naturalHeight])
      i.src = src
    }),
    image,
  )
  expect(size).toEqual([256, 256])
})

test('Bot 設定 · 能力 › 應用程式與電腦、權限與花費、進階（G07～G12）', async ({ page }, info) => {
  const { api, xm } = await openSettings(page, '/settings/bots/:id/abilities/access')
  const m = main(page)
  const apps = m.getByRole('group', { name: '小秘能用的應用程式' })
  await expect(apps.getByRole('switch', { name: '讓小秘用 Gmail' })).toHaveAttribute('aria-checked', 'true')
  await apps.getByRole('switch', { name: '讓小秘用 Notion' }).click()
  await expect.poll(() => api.calls.find((c) => c.op === 'apps.grant_to_bot')?.input).toEqual({ id: 'c2', bot_id: xm.id })
  await settle(page)
  await page.screenshot({ path: shot(`${info.project.name}-w32-g07`), fullPage: true })

  await m.getByRole('navigation', { name: 'Bot 設定分頁' }).getByRole('link', { name: '權限與花費' }).click()
  const modes = m.getByRole('radiogroup', { name: '權限等級' })
  await expect(modes.getByRole('radio', { name: /^先問我/ })).toHaveAttribute('aria-checked', 'true')
  await expect(m.getByText('接近上限')).toBeVisible()
  await settle(page)
  await page.screenshot({ path: shot(`${info.project.name}-w32-g08`), fullPage: true })

  await m.getByRole('navigation', { name: 'Bot 設定分頁' }).getByRole('link', { name: '進階' }).click()
  await expect(m.getByRole('radiogroup', { name: '引擎' })).toBeVisible()
  await settle(page)
  await page.screenshot({ path: shot(`${info.project.name}-w32-g09`), fullPage: true })
  await m.getByRole('navigation', { name: '進階分頁' }).getByRole('link', { name: '聲音、通知、誰看得到' }).click()
  await expect(m.getByText('語音還沒開放')).toBeVisible()
  await page.screenshot({ path: shot(`${info.project.name}-w32-g11`), fullPage: true })
  await m.getByRole('navigation', { name: '進階分頁' }).getByRole('link', { name: '歷史' }).click()
  const hist = m.getByRole('group', { name: '設定的變更' })
  await expect(hist).toContainText('權限改成「放手做」')
  await expect(hist).toContainText('等你核准')
  await settle(page)
  await page.screenshot({ path: shot(`${info.project.name}-w32-g12`), fullPage: true })
})

test('分享 Bot（G13）與匯入 Bot（G14）', async ({ page }, info) => {
  const { api } = await openSettings(page, '/settings/bots/:id')
  await main(page).getByRole('button', { name: '分享' }).click()
  const share = page.getByRole('dialog', { name: '分享小秘' })
  await expect(share.getByText('3 個技能')).toBeVisible()
  await expect(share).toContainText('API 金鑰')
  await settle(page)
  await page.screenshot({ path: shot(`${info.project.name}-w32-g13`) })
  const download = page.waitForEvent('download')
  await share.getByRole('button', { name: '下載 .xbot 檔' }).click()
  expect((await download).suggestedFilename()).toBe('小秘.xbot')

  // 從新增 Bot 對話框的「匯入 Bot」進來（G01 的次要入口）
  if (info.project.name === 'mobile') await page.getByRole('button', { name: '打開側欄' }).click()
  await page.getByRole('button', { name: '新增 Bot' }).first().click()
  await page.getByRole('dialog', { name: '新增 Bot' }).getByRole('button', { name: '匯入 Bot' }).click()
  const imp = page.getByRole('dialog', { name: '匯入 Bot' })
  await expect(imp.getByRole('button', { name: '下一步' })).toBeDisabled()
  await page.screenshot({ path: shot(`${info.project.name}-w32-g14-1`) })
  await imp.getByLabel('選擇 .xbot 檔').setInputFiles({ name: '小秘.xbot', mimeType: 'application/json', buffer: Buffer.from('{"format":"xbot.bot","version":1,"bot":{"name":"小秘"}}') })
  await imp.getByRole('button', { name: '下一步' }).click()
  const items = imp.getByRole('list', { name: '會帶入的內容' })
  await expect(items.locator('[data-status="action"]')).toContainText('Gmail')
  await expect(imp.getByLabel('名字')).toHaveValue('小秘 2')
  await settle(page)
  await page.screenshot({ path: shot(`${info.project.name}-w32-g14-2`) })
  await imp.getByRole('button', { name: '匯入' }).click()
  await expect(page).toHaveURL(/\/bots\/bot\d+$/)
  expect(api.calls.filter((c) => c.op === 'bots.import').at(-1)?.input).toMatchObject({ filename: '小秘.xbot', name: '小秘 2' })
})
