import { expect, test, type Page } from './fixtures'
import { MockServer, settle } from './mock'
import { overflow } from './overflow'

// W31 手機版：L01～L21 每個畫面在 Pixel 7（mobile）與 iPhone（iphone）各截一張，和 design/L*.dc.html 畫板並排比對。
// 用瀏覽器層的假服務（資料照畫板）；L16／L17 用真的服務（定時任務）。桌面跳過。
const shot = (info: { project: { name: string } }, name: string) => `e2e-results/screens/phone-${info.project.name}-${name}.png`

test.skip(({ isMobile }) => !isMobile, '只在手機尺寸跑')
test.use({ serviceWorkers: 'block' })

const min = 60_000

function seed(api: MockServer) {
  const now = Date.now()
  const at = (h: number, m: number, daysAgo = 0) => {
    const d = new Date(now - daysAgo * 86_400_000)
    d.setHours(h, m, 0, 0)
    return Math.min(d.getTime(), now - min)
  }
  const mi = api.addBot({ name: '小秘', title: '幕僚長', description: '每日重點與協調', shape: 'cursor', color: 'blue' })
  Object.assign(mi, { primary: true, team_id: '', host_id: 'local', fallback: '', reports_to: '', can_manage: true })
  const zhe = api.addBot({ name: '阿哲', title: '選品研究員', shape: 'blob', color: 'orange' })
  const bian = api.addBot({ name: '小編', title: '社群小編', shape: 'circle', color: 'pink' })
  const kai = api.addBot({ name: '阿凱', title: '業務開發', shape: 'squircle', color: 'green' })
  const zf = api.addBot({ name: '帳房', title: '費用管理', shape: 'circle', color: 'yellow' })
  const miao = api.addBot({ name: '數據喵', title: '數據分析師', shape: 'diamond', color: 'purple' })
  const xin = api.addBot({ name: '信差', title: '收件匣管家', shape: 'star', color: 'cyan' })
  const bug = api.addBot({ name: '抓蟲', title: 'Bug 重現', shape: 'shield', color: 'red' })
  for (const b of [zhe, bian, kai]) Object.assign(b, { team_id: 'ops' })
  for (const b of [zf, miao, xin, bug]) Object.assign(b, { team_id: 'back' })
  for (const b of api.bots) Object.assign(b, { host_id: b === bian || b === miao || b === bug ? 'cloud' : 'local', fallback: '', reports_to: '', can_manage: false, primary: b === mi })
  api.override['teams.list'] = () => ({
    teams: [
      { id: 'ops', name: '營運部', chief_id: '', description: '', members: [] },
      { id: 'back', name: '後勤', chief_id: '', description: '', members: [] },
    ],
  })
  // 對話串（L01 的時間與未讀）
  const t1 = api.addThread(mi.id, '比價三家競品')
  t1.last_message_at = at(15, 2)
  const t2 = api.addThread(zhe.id, '在蝦皮比價第 3 家')
  t2.last_message_at = at(15, 1)
  const t3 = api.addThread(bian.id, '需要你輸入驗證碼')
  t3.last_message_at = at(14, 47)
  const t4 = api.addThread(kai.id, '3 封開發信草稿已完成')
  t4.last_message_at = at(10, 42)
  t4.unread = 2
  const t5 = api.addThread(zf.id, '付款 NT$1,280')
  t5.last_message_at = at(14, 55)
  const t6 = api.addThread(miao.id, '週報資料處理')
  t6.last_message_at = at(14, 50)
  const t7 = api.addThread(xin.id, '整理了 24 封信')
  t7.last_message_at = at(9, 0, 1)
  api.addMessage(t1.id, { sender: 'owner', content: '幫我比較蝦皮上三家競品的定價，週一早會前給我建議。' }, now - 12 * min)
  api.addMessage(t1.id, { sender: 'bot', content: '收到。我先請阿哲抓價格、數據喵算差距，好了整理成表格給你。' }, now - 11 * min)
  // 群組
  const room = api.addRoom([mi.id, zhe.id, bian.id], '新品上架專案', 'auto', { [bian.id]: 'working' })
  room.last_message_at = at(14, 58)
  const room2 = api.addRoom([mi.id, miao.id, zhe.id, bian.id, kai.id, zf.id], '每週一早會')
  room2.last_message_at = at(9, 0, 3)
  api.addRoomMessage(room.id, { sender: 'owner', content: '@阿哲 先抓三家帆布包的價格，@小編 等價格出來再寫上架文案。' })
  api.addRoomMessage(room.id, { sender: 'bot:' + mi.id, sender_name: '小秘', content: '好，我來盯進度。價格出來我會整理成提案給你。' })
  api.addRoomMessage(room.id, { sender: 'bot:' + zhe.id, sender_name: '阿哲', content: '三家分別是 NT$990、NT$1,080、NT$1,150。建議定 NT$1,020，比價表在這裡。' })
  // 待我處理
  api.inbox = [
    {
      id: 'pay', kind: 'approval', group: 'decide', bot_id: zf.id, task_id: 'k-pay', thread_id: t5.id, title: '帳房想付款', body: '付款 NT$1,280 給晨光廣告（10 月廣告費）',
      payload: { prompt: '付款', command: 'python3 /workspace/pay.py --vendor 晨光廣告 --amount 1280', description: '付款 NT$1,280 給晨光廣告（10 月廣告費）', choices: ['once', 'deny'], thread_title: '小秘交辦的工作 · 第 2/6 棒', bot_name: '帳房' },
      status: 'open', read: false, created_at: now - 3 * min,
    },
    { id: 'take', kind: 'takeover', group: 'decide', bot_id: bian.id, thread_id: t3.id, title: '小編需要你的手', body: '發新品貼文 · 社群後台要簡訊驗證碼', payload: {}, status: 'open', read: false, created_at: now - 8 * min },
    {
      id: 'q', kind: 'question', group: 'decide', bot_id: mi.id, task_id: 'k-q', thread_id: t1.id, title: '要不要也比較日本賣場？', body: '比價三家競品 · 選擇近的，之後都能改',
      payload: { prompt: '要不要也比較日本賣場？', choices: ['要，一起比較', '不用，台灣就好'], thread_title: '比價三家競品' },
      status: 'open', read: false, created_at: now - 9 * min,
    },
    { id: 'done1', kind: 'mention', group: 'mention', bot_id: zhe.id, title: '@你 價格抓好了，要看嗎？', body: '', payload: { room_id: room.id, room_name: '新品上架專案' }, status: 'resolved', read: false, created_at: now - 30 * min },
    { id: 'err1', kind: 'error', group: 'error', bot_id: bug.id, title: '測試網站連不上，先停下來了。', body: '已自動重試 3 次', payload: {}, status: 'resolved', read: false, created_at: now - 120 * min },
    { id: 'exp', kind: 'question', group: 'decide', bot_id: xin.id, title: '整理收件匣時問了你', body: '要不要退訂這 12 份電子報？', payload: {}, status: 'expired', read: true, created_at: now - 160 * min },
  ]
  api.override['inbox.list'] = (i) => ({
    items: api.inbox.filter((x) => (((i.status as string) ?? 'open') === 'all' || x.status === ((i.status as string) ?? 'open')) && (!i.kind || x.kind === i.kind) && (!i.group || x.group === i.group)),
  })
  api.override['inbox.counts'] = () => ({ open: 3, unread: 5, groups: { decide: { open: 3, unread: 3 }, done: { open: 0, unread: 1 }, mention: { open: 0, unread: 1 }, error: { open: 0, unread: 1 } } })
  // 主機與電腦（L15）
  const host = (id: string, name: string, kind: string, status: string, running: number, max: number) => ({
    id, name, kind, address: '', status, arch: 'amd64', max_running: max, running,
    resources: { cpus: 8, mem_bytes: 0, disk_free_bytes: 0, disk_total_bytes: 0, docker_version: '', os: '', arch: 'amd64', hostname: kind === 'local' ? 'office-mac-mini' : name, rtt_ms: 0, tailscale: true },
    health: { state: status === 'online' ? 'online' : 'failed', since: now - 4 * 3600_000, connects: 1, rtt_ms: 0, reverse_forwards: 0 },
  })
  api.override['hosts.list'] = () => ({ hosts: [host('local', '這台主機', 'local', 'online', 3, 4), host('cloud', 'cloud-01（雲端）', 'ssh', 'online', 2, 3), host('pc', 'office-pc', 'ssh', 'offline', 0, 2)] })
  const off = new Set([kai.id, bug.id])
  api.override['computers.status'] = (i) => {
    const b = api.bots.find((x) => x.id === i.bot_id)!
    const h = b.id === xin.id ? 'pc' : (b as unknown as { host_id: string }).host_id
    return { computer: { bot_id: b.id, host_id: h, container_id: 'c', state: off.has(b.id) ? 'stopped' : 'running', running: !off.has(b.id), novnc_port: 0, hermes_port: 0 }, bot_name: b.name, host_name: h === 'local' ? '這台主機' : h, idle_minutes: 30, last_active_at: now, sleeps_at: 0, busy: false, outdated: false, allow_private: [] }
  }
  const screen = (b: { id: string; name: string }) => {
    const h = b.id === xin.id ? 'pc' : (b as unknown as { host_id: string }).host_id
    const waiting = b.id === bian.id || b.id === zf.id
    return {
      bot_id: b.id, name: b.name, host_id: h, host_name: h === 'local' ? '這台主機' : h === 'cloud' ? 'cloud-01（雲端）' : 'office-pc', box: off.has(b.id) ? 'stopped' : 'running',
      host_status: h === 'pc' ? 'offline' : 'online', working: !off.has(b.id) && !waiting, paused: false, last_active_at: now - 20 * min, viewers: 0, you: false,
      vnc: `/api/v1/computers/vnc?bot=${b.id}`, ...(b.id === bian.id ? { request: { reason: '要輸入簡訊驗證碼', since: now - 8 * min } } : {}),
    }
  }
  api.override['computers.screen'] = (i) => screen(api.bots.find((x) => x.id === i.bot_id)!)
  api.override['computers.screens'] = () => ({ screens: api.bots.map(screen) })
  api.override['apps.list'] = () => ({
    connections: [
      { id: 'a1', app_id: 'shopee', name: 'shopee', title: '蝦皮賣家中心', description: '', icon: '', custom: false, transport: 'http', url: '', command: '', args: [], auth: 'oauth2', account: '', status: 'ok', error: '', secret_hint: '', header_names: [], env_names: [], missing_env: [], tools: [] },
      { id: 'a2', app_id: 'gsheets', name: 'gsheets', title: 'Google 試算表', description: '', icon: '', custom: false, transport: 'http', url: '', command: '', args: [], auth: 'oauth2', account: '', status: 'ok', error: '', secret_hint: '', header_names: [], env_names: [], missing_env: [], tools: [] },
    ],
  })
  api.override['computers.wake'] = (i) => {
    off.delete(i.bot_id as string)
    return api.override['computers.status'](i)
  }
  // 設定（L06）、入門（L08）、新增 Bot（L12）
  api.override['usage.caps'] = () => ({ day: '', month: '', global: { key: 'all', daily_usd: 0, monthly_usd: 250, spent_today_usd: 4, spent_month_usd: 143, exceeded: false, daily_micros: 0, monthly_micros: 0, spent_day_micros: 0, spent_month_micros: 0 }, bots: [], providers: [] })
  api.override['push.config'] = () => ({ public_key: 'BEl62iUYgUivxIkv69yViEuiBIa-Ib9-SkvMeAtA3LFgDzkrxZJjSgSnfckjBJuBkr3qBUYIHBQFLXYp5Nksh8U', subscriptions: [] })
  api.override['engines.status'] = () => ({
    bots: [],
    host: [
      { engine: 'claude-code', label: 'Claude Code', plan: '訂閱方案', logged_in: true, login: '' },
      { engine: 'codex', label: 'Codex', plan: 'ChatGPT 帳號', logged_in: true, login: '' },
    ],
  })
  api.override['bots.suggest'] = () => ({ name: '客服小蝦', title: '蝦皮客服', description: '回覆買家問題', soul: '你是蝦皮客服…', by: 'model', p: 0.91, why: '回覆買家、查訂單狀態、整理常見問題；退款與對客承諾前先問你。' })
  api.override['judge.precheck'] = () => ({
    chips: [
      { use: 'precheck_clarity', n: 13, label: 'clear', shown: '夠清楚', p: 0.93, by: 'model', options: [{ key: 'missing', shown: '缺資訊 · 先問清楚' }] },
      { use: 'precheck_computer', n: 12, label: 'none', shown: '不用開電腦', p: 0.88, by: 'model', options: [{ key: 'browser', shown: '要開電腦 · 用瀏覽器' }] },
      { use: 'precheck_engine', n: 11, label: 'hermes_medium', shown: '交給 Hermes', p: 0.81, by: 'model', options: [{ key: 'codex_high', shown: '交給 Codex · 高' }] },
      { use: 'precheck_cost', n: 14, label: 'small', shown: '預估約 8k token', p: 0.9, by: 'model', options: [{ key: 'large', shown: '預估 大' }] },
    ],
  })
  api.override['skills.list'] = () => ({ skills: [] })
  api.override['files.list'] = (i) => ({
    bot_id: i.bot_id, path: '/workspace', state: 'ready', total: 4, offset: 0, truncated: false,
    entries: [
      { name: '三家競品定價.csv', path: '/workspace/三家競品定價.csv', dir: false, size: 12_000, modified: now - 5 * min, kind: 'csv', group: 'result', important: true },
      { name: '週一早會簡報.pdf', path: '/workspace/週一早會簡報.pdf', dir: false, size: 2_100_000, modified: now - 30 * min, kind: 'pdf', group: 'result', important: true },
      { name: '帆布包商品照.jpg', path: '/workspace/uploads/帆布包商品照.jpg', dir: false, size: 1_600_000, modified: now - 26 * 3600_000, kind: 'image', group: 'upload', important: true },
      { name: '比價表.csv', path: '/workspace/from/阿哲/比價表.csv', dir: false, size: 8_000, modified: now - 40 * min, kind: 'csv', group: 'peer', from: '阿哲', important: true },
    ],
  })
  api.override['files.read'] = (i) => {
    const text = '商品,我們,帆布家,包小舖,選物町\n經典帆布包,1080,990,1020,1150\n托特包 L,890,850,920,880\n束口後背包,1280,1190,1250,1320\n零錢包,320,290,310,300\n筆袋,260,240,250,270\n側背包,990,950,980,1010'
    const lines = text.split('\n').map((l) => l.split(','))
    return { name: '三家競品定價.csv', path: i.path, dir: false, size: 12_000, modified: now, kind: 'csv', group: 'result', important: true, download_url: '/x', truncated: false, binary: false, text, encoding: 'utf-8', table: { columns: lines[0], rows: lines.slice(1), total_rows: lines.length - 1, truncated: false } }
  }
  return { mi, zhe, bian, kai, zf, miao, xin, bug, room, t1 }
}

async function working(api: MockServer, states: Record<string, string>) {
  await api.emit(...Object.entries(states).map(([bot_id, state]) => ({ kind: 'task.state', bot_id, task_id: 'k-' + bot_id, payload: { state }, persist: false })))
}

async function open(page: Page, path: string) {
  const api = new MockServer(page)
  const s = seed(api)
  await api.install()
  await page.goto(path)
  return { api, ...s }
}

async function snap(page: Page, info: Parameters<typeof shot>[0], name: string) {
  await settle(page)
  await page.screenshot({ path: shot(info, name) })
}

test('L01 對話分頁：清單、狀態、分頁列、抽屜', async ({ page }, info) => {
  const { api, mi, zhe, bian, zf, miao } = await open(page, '/chats')
  await expect(page.getByRole('heading', { level: 1, name: '對話' })).toBeVisible()
  await working(api, { [mi.id]: 'running', [zhe.id]: 'running', [bian.id]: 'waiting_input', [zf.id]: 'waiting_approval', [miao.id]: 'queued' })
  const tabs = page.getByRole('navigation', { name: '分頁', exact: true })
  await expect(tabs.getByRole('link', { name: /對話/ })).toHaveAttribute('aria-current', 'page')
  await expect(tabs).toContainText('3') // 待我處理的數字
  await expect(page.getByRole('main')).toContainText('等你回覆')
  await expect(page.getByRole('main')).toContainText('新品上架專案')
  // 每個可點的列 ≥ 44 高
  for (const link of await page.getByRole('main').getByRole('link').all()) expect((await link.boundingBox())!.height).toBeGreaterThanOrEqual(44)
  await snap(page, info, 'L01')
  // 從左邊緣滑：分頁首頁打開側欄
  await page.getByRole('button', { name: '打開側欄' }).click()
  await expect(page.getByRole('dialog', { name: '側欄' })).toBeVisible()
  expect(await page.evaluate(() => document.documentElement.style.overflow)).toBe('hidden') // 側欄開著：後面不跟著捲
  await page.getByRole('button', { name: '關閉側欄' }).click()
  await expect.poll(() => page.evaluate(() => document.documentElement.style.overflow)).toBe('')
  // 點一列進對話；返回鈕回到清單
  await page.getByRole('link', { name: /阿哲/ }).click()
  await expect(page).toHaveURL(/\/bots\//)
  await page.getByRole('button', { name: /返回/ }).first().click()
  await expect(page).toHaveURL(/\/chats$/)
})

test('L02 待我處理：就地允許、接手、回答', async ({ page }, info) => {
  await open(page, '/')
  await expect(page.getByRole('heading', { level: 1, name: '待我處理' })).toBeVisible()
  await expect(page.getByText('3 件需要你，其他 Bot 照常工作')).toBeVisible()
  await expect(page.getByRole('button', { name: '允許這次' }).first()).toBeVisible()
  await snap(page, info, 'L02')
})

test('L03 對話：手機頁首（不重複上方列）、工作中、輸入框在安全區上方', async ({ page }, info) => {
  const { api, mi } = await open(page, '/chats')
  await page.getByRole('link', { name: /小秘/ }).click()
  await expect(page.getByRole('button', { name: '小秘的資料' })).toBeVisible()
  await expect(page.getByRole('button', { name: /對話串/ })).toBeVisible()
  await expect(page.getByRole('button', { name: '打開側欄' })).toHaveCount(0) // 沒有第二條上方列
  await working(api, { [mi.id]: 'running' })
  await expect(page.getByTestId('composer-input')).toBeVisible()
  const box = (await page.getByTestId('composer-input').boundingBox())!
  expect(box.y + box.height).toBeLessThan(page.viewportSize()!.height)
  await snap(page, info, 'L03')
})

test('L04 電腦（B14 的接手頁）：手機全螢幕、交還鈕', async ({ page }, info) => {
  const { bian } = await open(page, '/chats')
  await page.goto(`/bots/${bian.id}/computer`)
  await expect(page.getByRole('heading', { name: '小編的電腦' })).toBeVisible()
  await expect(page.getByRole('heading', { name: '小編的電腦' })).toHaveCount(1) // 只有一條頁首
  await page.waitForTimeout(500)
  await snap(page, info, 'L04')
})

test('L05 群組：手機頁首、@ 晶片 ≥ 44、成員 sheet', async ({ page }, info) => {
  const { room } = await open(page, '/chats')
  await page.goto(`/rooms/${room.id}`)
  await expect(page.getByRole('button', { name: '@阿哲' })).toBeVisible()
  expect((await page.getByRole('button', { name: '@阿哲' }).boundingBox())!.height).toBeGreaterThanOrEqual(44)
  await page.getByRole('button', { name: '@所有人' }).click()
  await expect(page.getByRole('textbox')).toHaveValue('@所有人 ')
  await page.getByRole('textbox').fill('')
  await snap(page, info, 'L05')
  await page.getByRole('button', { name: '成員', exact: true }).click()
  await expect(page.getByRole('dialog', { name: /成員/ })).toBeVisible()
})

test('L06 設定：5 區、推播開關、這支手機', async ({ page }, info) => {
  await open(page, '/settings')
  await expect(page.getByRole('heading', { level: 1, name: '設定' })).toBeVisible()
  await expect(page.getByRole('switch', { name: '等你的事推播到這支手機' })).toBeVisible()
  await expect(page.getByText('這支手機', { exact: true })).toBeVisible()
  await expect(page.getByRole('heading', { level: 1 })).toHaveCount(1) // 只有一條頁首
  await snap(page, info, 'L06')
  await page.getByRole('link', { name: /本月花費/ }).click()
  await expect(page).toHaveURL(/\/settings\/usage/)
})

test('L07 從通知打開的核定 sheet', async ({ page }, info) => {
  const { api } = await open(page, '/notifications')
  await page.getByRole('button', { name: '帳房想付款' }).click()
  const sheet = page.getByRole('dialog', { name: '帳房想付款' })
  await expect(sheet).toContainText('只有你能決定')
  await snap(page, info, 'L07')
  // sheet 開著時後面的頁面鎖住（iPhone 在 sheet 不能捲的地方滑，會改捲後面）；關掉就放開
  expect(await page.evaluate(() => document.documentElement.style.overflow)).toBe('hidden')
  api.override['chat.respond'] = () => ({ ok: true })
  await sheet.getByRole('button', { name: '允許這次' }).click()
  await expect(page.getByText(/已允許這次/)).toBeVisible()
  await expect(sheet).toBeHidden()
  expect(await page.evaluate(() => document.documentElement.style.overflow)).toBe('')
  expect(api.calls.find((c) => c.op === 'chat.respond')?.input).toEqual({ inbox_id: 'pay', choice: 'once' })
})

test('L08 第一次設定：接上 AI → 認識小秘', async ({ page }, info) => {
  const { api } = await open(page, '/welcome')
  await expect(page.getByRole('heading', { name: '先接上 AI' })).toBeVisible()
  await expect(page.getByText('找到 3 個可以用的')).toBeVisible()
  await snap(page, info, 'L08')
  await page.getByRole('button', { name: '用這個' }).first().click()
  await expect(page.getByRole('heading', { name: '嗨，我是小秘' })).toBeVisible()
  api.override['engines.link_credentials'] = () => ({ bot_id: 'x', name: '小秘', engine: 'claude-code', linked: true })
  await snap(page, info, 'L08-step2')
})

test('L09 連接這支手機：掃描畫面；QR 帶配對碼就直接登入', async ({ page }, info) => {
  const api = new MockServer(page)
  seed(api)
  await api.install()
  await page.goto('/pair')
  await expect(page.getByRole('heading', { name: '連接這支手機' })).toBeVisible()
  await expect(page.getByText(/Tailscale/).first()).toBeVisible()
  await snap(page, info, 'L09')
  await page.getByRole('button', { name: '沒有 Tailscale？改用同一個 Wi-Fi' }).click()
  await expect(page.getByText('只在同一個 Wi-Fi 有效；離開家裡或辦公室就連不到。')).toBeVisible()
  // 掃到 QR：/pair#配對碼 → POST /api/v1/auth/pair
  let paired = ''
  await page.route('**/api/v1/auth/pair', (r) => {
    paired = (r.request().postDataJSON() as { code: string }).code
    return r.fulfill({ json: { ok: true } })
  })
  await page.goto('about:blank')
  await page.goto('/pair#ABCD-EFGH')
  await expect(page.getByRole('heading', { name: /已連上/ })).toBeVisible()
  expect(paired).toBe('ABCD-EFGH')
  await expect(page).toHaveURL(/\/pair$/) // 用過的配對碼從網址拿掉
  await snap(page, info, 'L09-paired')
})

test('L10 Bot 資料與 L11 換頭像 sheet', async ({ page }, info) => {
  const { api, mi } = await open(page, '/chats')
  await page.goto(`/bots/${mi.id}/profile`)
  await expect(page.getByRole('heading', { level: 1, name: /小秘/ })).toBeVisible()
  await expect(page.getByRole('radio', { name: '基本' })).toBeVisible()
  await snap(page, info, 'L10')
  await page.getByRole('button', { name: '換頭像' }).click()
  const sheet = page.getByRole('dialog', { name: '頭像' })
  await sheet.getByRole('radio', { name: /盾牌/ }).click()
  await sheet.getByRole('radio', { name: '青' }).click()
  await snap(page, info, 'L11')
  await sheet.getByRole('button', { name: '完成' }).click()
  await expect.poll(() => api.calls.find((c) => c.op === 'bots.update')?.input).toMatchObject({ id: mi.id, shape: 'shield', color: 'cyan' })
})

test('L12 新增 Bot：一句話 → 系統建議 → 建立', async ({ page }, info) => {
  const { api } = await open(page, '/new/bot')
  await page.getByRole('textbox').fill('每天幫我回覆蝦皮買家的問題，不確定的先問我')
  await page.getByRole('button', { name: '幫我想好其他的' }).click()
  await expect(page.getByRole('region', { name: '系統建議' })).toContainText('客服小蝦')
  await snap(page, info, 'L12')
  await page.getByRole('button', { name: '建立' }).click()
  await expect(page).toHaveURL(/\/bots\//)
  expect(api.calls.find((c) => c.op === 'bots.create')?.input).toMatchObject({ name: '客服小蝦', title: '蝦皮客服' })
})

test('L13 新增群組：勾成員（少於 2 位停用）→ 確認 → 建立', async ({ page }, info) => {
  const { api } = await open(page, '/new/room')
  const next = page.getByRole('button', { name: '下一步' })
  await page.getByRole('checkbox', { name: /小秘/ }).click()
  await expect(next).toBeDisabled()
  await page.getByRole('checkbox', { name: /阿哲/ }).click()
  await page.getByRole('checkbox', { name: /小編/ }).click()
  await expect(page.getByText('已選 3 位')).toBeVisible()
  await snap(page, info, 'L13')
  await next.click()
  await expect(page.getByText('自動', { exact: true })).toBeVisible()
  await page.getByRole('button', { name: '建立' }).click()
  await expect(page).toHaveURL(/\/rooms\//)
  expect((api.calls.find((c) => c.op === 'rooms.create')?.input as { member_ids: string[] }).member_ids).toHaveLength(3)
})

test('L14 通知中心：篩選、就地允許、過期提問、全部標為已讀', async ({ page }, info) => {
  const { api } = await open(page, '/')
  await page.getByRole('button', { name: '通知' }).click()
  await expect(page).toHaveURL(/\/notifications$/)
  await expect(page.getByRole('tab', { name: /要你決定/ })).toContainText('3')
  await expect(page.getByText('請它再問一次')).toBeVisible()
  await snap(page, info, 'L14')
  await page.getByRole('tab', { name: /出錯/ }).click()
  await expect(page.getByTestId('notification')).toHaveCount(1)
  await page.getByRole('button', { name: '全部標為已讀' }).click()
  expect(api.calls.find((c) => c.op === 'inbox.mark_read')?.input).toEqual({ all: true })
})

test('L15 辦公室（B14）：依主機分組、等你的用主要按鈕、分頁列', async ({ page }, info) => {
  const { api, bian, zf } = await open(page, '/office')
  await working(api, { [bian.id]: 'waiting_input', [zf.id]: 'waiting_approval' })
  await expect(page.getByRole('navigation', { name: '分頁', exact: true }).getByRole('link', { name: /辦公室/ })).toHaveAttribute('aria-current', 'page')
  await expect(page.getByTestId('office')).toContainText('小編')
  await page.waitForTimeout(500)
  await snap(page, info, 'L15')
})

test('L18 通話：全螢幕畫面、掛斷回到對話（語音流程與截圖在 voice.spec）', async ({ page }) => {
  const { mi } = await open(page, '/chats')
  await page.goto(`/bots/${mi.id}/call`)
  await expect(page.getByRole('main', { name: '通話' })).toBeVisible()
  await expect(page.getByRole('button', { name: '靜音' })).toBeVisible()
  await page.getByRole('button', { name: '掛斷' }).click()
  await expect(page).toHaveURL(new RegExp(`/bots/${mi.id}`))
})

test('L19 Bot 的檔案：分組與 CSV 預覽 sheet', async ({ page }, info) => {
  const { mi } = await open(page, '/chats')
  await page.goto(`/bots/${mi.id}/files`)
  await page.getByRole('button', { name: /三家競品定價\.csv/ }).first().click()
  await expect(page.getByRole('dialog', { name: /預覽/ })).toBeVisible()
  await snap(page, info, 'L19')
})

test('L19 檔案很多時只有清單在捲，整頁不跟著動（上傳鈕一直在底部）', async ({ page }) => {
  const { api, mi } = await open(page, '/chats')
  api.override['files.list'] = (i) => ({
    bot_id: i.bot_id, path: '/workspace', state: 'ready', total: 40, offset: 0, truncated: false,
    entries: Array.from({ length: 40 }, (_, n) => ({ name: `報告-${n}.md`, path: `/workspace/報告-${n}.md`, dir: false, size: 1000, modified: Date.now() - n * 60_000, kind: 'markdown', group: 'result', important: true })),
  })
  await page.goto(`/bots/${mi.id}/files`)
  await expect(page.getByRole('button', { name: /報告-39\.md/ })).toBeAttached()
  const sizes = await page.evaluate(() => ({ doc: document.scrollingElement!.scrollHeight, vh: innerHeight, list: document.querySelector('[data-testid="file-list"]')!.scrollHeight - document.querySelector('[data-testid="file-list"]')!.clientHeight }))
  expect(sizes.doc).toBeLessThanOrEqual(sizes.vh + 1)
  expect(sizes.list).toBeGreaterThan(0)
  await expect(page.getByRole('button', { name: /上傳檔案給/ })).toBeInViewport()
})

test('L19 預覽 sheet：手指在裡面滑是捲內容，後面不動；捲到最上面再往下拉就關掉', async ({ page, browserName }, info) => {
  test.skip(browserName !== 'chromium' || info.project.name === 'desktop', '要用 Chromium 模擬觸控')
  const { api, mi } = await open(page, '/chats')
  const text = Array.from({ length: 60 }, (_, i) => `## 第 ${i + 1} 段\n\n這是一段很長的報告內容，用來測試手機上能不能順順地往下捲。`).join('\n\n')
  api.override['files.read'] = (i) => ({ name: '報告.md', path: i.path, dir: false, size: text.length, modified: Date.now(), kind: 'markdown', group: 'result', important: true, download_url: '/x', truncated: false, binary: false, text, encoding: 'utf-8' })
  await page.goto(`/bots/${mi.id}/files?path=${encodeURIComponent('/workspace/報告.md')}`)
  const sheet = page.getByRole('dialog', { name: /預覽/ })
  await expect(sheet.getByText('第 60 段')).toBeAttached()
  await page.waitForTimeout(400) // 滑上來的動畫
  const cdp = await page.context().newCDPSession(page)
  const drag = async (y0: number, y1: number) => {
    await cdp.send('Input.dispatchTouchEvent', { type: 'touchStart', touchPoints: [{ x: 195, y: y0 }] })
    for (let i = 1; i <= 12; i++) {
      await cdp.send('Input.dispatchTouchEvent', { type: 'touchMove', touchPoints: [{ x: 195, y: y0 + ((y1 - y0) * i) / 12 }] })
      await page.waitForTimeout(16)
    }
    await cdp.send('Input.dispatchTouchEvent', { type: 'touchEnd', touchPoints: [] })
    await page.waitForTimeout(400)
  }
  const inner = () => sheet.evaluate((d) => Math.max(0, ...[...d.querySelectorAll<HTMLElement>('*')].filter((e) => /(auto|scroll)/.test(getComputedStyle(e).overflowY)).map((e) => e.scrollTop)))
  const behind = () => page.evaluate(() => [document.scrollingElement!.scrollTop, document.querySelector('[data-testid="file-list"]')!.scrollTop])
  const before = await behind()
  await drag(700, 350)
  expect(await inner(), '預覽內容沒有捲動').toBeGreaterThan(100)
  expect(await behind()).toEqual(before)
  await drag(350, 700) // 捲回最上面：還是捲內容，不是拉 sheet（慣性長短不一，還沒到頂就像人一樣再滑一次）
  if ((await inner()) > 0) await drag(350, 700)
  await expect(sheet).toBeVisible()
  expect(await inner()).toBe(0)
  await drag(300, 780) // 在最上面往下拉：關掉
  await expect(sheet).toBeHidden()
  expect(page.url()).not.toContain('path=')
})

test('L20 @ 提到選單與停止鈕', async ({ page }, info) => {
  const { api, mi, t1 } = await open(page, '/chats')
  await page.goto(`/bots/${mi.id}/threads/${t1.id}`)
  await working(api, { [mi.id]: 'running' })
  await api.emit({ kind: 'task.state', bot_id: mi.id, thread_id: t1.id, task_id: 'k-run', payload: { state: 'running' }, persist: false })
  await page.getByTestId('composer-input').fill('也請 @')
  const menu = page.getByRole('listbox', { name: '提到誰' })
  await expect(menu).toContainText('@阿哲')
  await expect(menu).toContainText('@新品上架專案')
  await expect(menu).not.toContainText('@小秘')
  await expect(menu).toContainText('@蝦皮賣家中心')
  await snap(page, info, 'L20')
  await menu.getByRole('option', { name: /@阿哲/ }).click()
  await expect(page.getByTestId('composer-input')).toHaveValue('也請 @阿哲 ')
})

test('L21 先判斷列', async ({ page }, info) => {
  const { api, mi, t1 } = await open(page, '/chats')
  await page.goto(`/bots/${mi.id}/threads/${t1.id}`)
  await page.getByTestId('composer-input').fill('幫新品帆布包寫 150 字介紹。')
  await page.getByTestId('composer-input').press('Enter')
  await expect.poll(() => api.lastSend).not.toBeNull()
  const s = api.lastSend!
  const m = api.messages[s.threadId].at(-1)!
  await api.emit({ bot_id: s.botId, thread_id: s.threadId, task_id: s.taskId, kind: 'message.created', payload: api.storeMessage(s.threadId, m) })
  await expect(page.getByText('預估約 8k token')).toBeVisible()
  await snap(page, info, 'L21')
})

// ── L16／L17：真的服務（定時任務的解析與行事曆）──

test('L16 行事曆與 L17 一句話建立定時任務', async ({ page, request }, info) => {
  const bot = (await (await request.post('/api/v1/bots.create', { data: { name: '小秘', title: '幕僚長', shape: 'cursor', color: 'blue' } })).json()).output
  await request.post('/api/v1/routines.create', { data: { bot_id: bot.id, title: '每日重點', prompt: '整理今天的重點', schedule: { kind: 'daily', time: '09:00' } } })
  await page.goto('/calendar')
  await expect(page.getByRole('navigation', { name: '分頁', exact: true }).getByRole('link', { name: /行事曆/ })).toHaveAttribute('aria-current', 'page')
  await snap(page, info, 'L16')
  await page.getByRole('button', { name: /新增|一句話/ }).first().click()
  const box = page.getByRole('textbox').first()
  await box.fill('每天早上 9 點整理新聞給我')
  await expect(page.getByText(/每天/).first()).toBeVisible()
  await page.waitForTimeout(800)
  await snap(page, info, 'L17')
})

// 破版檢查：手機寬度打開每一頁，不能有橫向捲動，也不能有元素超出畫面（可以橫向捲動的容器裡面的除外）。
const AUDIT = [
  '/', '/chats', '/office', '/notifications', '/agenda', '/files', '/apps', '/apps/mcp', '/apps/logins', '/teams', '/org', '/calendar',
  '/settings', '/settings/general', '/settings/models', '/settings/models/judge', '/settings/models/routing', '/settings/computers', '/settings/usage',
  '/settings/advanced', '/settings/advanced/backup', '/settings/advanced/people', '/settings/advanced/lab', '/settings/advanced/network', '/settings/advanced/agents',
  '/new/bot', '/new/room', '/share', '/calendar/routines', 'LONG',
  'BOT', 'BOT/computer', 'BOT/files', 'BOT/voice', 'BOT/profile', 'BOTSET', 'BOTSET/abilities', 'BOTSET/permissions', 'BOTSET/advanced', 'ROOM',
]

// 2026-10-20 實機：主畫面最下面的選單先出現在比較上面、才掉下去；對話滑動卡住、檔案預覽滑不動；狀態列改成 black-translucent
// （App 蓋滿整個螢幕，各頁自己留瀏海與底部橫條的空間）。模擬 iPhone 的瀏海（59）與底部橫條（34），手指真的滑一次。
test('iPhone 原生感：瀏海和底部橫條不蓋到能點的東西、分頁列不跳、手指滑得動', async ({ page, browserName }, info) => {
  test.skip(browserName !== 'chromium' || info.project.name === 'desktop', '要用 Chromium 模擬瀏海與觸控')
  test.setTimeout(300_000)
  const api = new MockServer(page)
  const { mi, room } = seed(api)
  const long = api.addThread(mi.id, '長內容')
  for (let i = 0; i < 30; i++) api.addMessage(long.id, { sender: i % 2 ? 'bot:' + mi.id : 'owner', status: 'complete', content: `第 ${i + 1} 則：` + '內容很長的一段話。'.repeat(6) })
  await api.install()
  await page.goto('/')
  const cdp = await page.context().newCDPSession(page)
  const TOP = 59
  const BOTTOM = 34
  await cdp.send('Emulation.setSafeAreaInsetsOverride', { insets: { top: TOP, bottom: BOTTOM, left: 0, right: 0 } })
  const drag = async (y0: number, y1: number) => {
    await cdp.send('Input.dispatchTouchEvent', { type: 'touchStart', touchPoints: [{ x: 195, y: y0 }] })
    for (let i = 1; i <= 10; i++) await cdp.send('Input.dispatchTouchEvent', { type: 'touchMove', touchPoints: [{ x: 195, y: y0 + ((y1 - y0) * i) / 10 }] })
    await cdp.send('Input.dispatchTouchEvent', { type: 'touchEnd', touchPoints: [] })
    await page.waitForTimeout(400)
  }
  const positions = () =>
    page.evaluate(() => [document.scrollingElement!.scrollTop, ...[...document.querySelectorAll<HTMLElement>('*')].filter((e) => /(auto|scroll)/.test(getComputedStyle(e).overflowY) && e.scrollHeight > e.clientHeight + 40 && e.clientHeight > 200).map((e) => e.scrollTop)])
  const problems: Record<string, string[]> = {}
  for (const raw of AUDIT) {
    const path = raw.replace('LONG', `/bots/${mi.id}/threads/${long.id}`).replace('BOTSET', `/settings/bots/${mi.id}`).replace('BOT', `/bots/${mi.id}`).replace('ROOM', `/rooms/${room.id}`)
    const bad: string[] = []
    await page.goto(path)
    const navAt = () => page.evaluate(() => document.querySelector('nav[aria-label="分頁"]')?.getBoundingClientRect().top ?? null)
    await page.waitForTimeout(150)
    const early = await navAt()
    await page.waitForLoadState('networkidle')
    await page.waitForTimeout(500)
    const late = await navAt()
    if (early !== null && late !== null && Math.abs(early - late) > 1) bad.push(`分頁列跳動 ${early}→${late}`)
    // 能點的東西：真的在最上層（沒被頁首、分頁列蓋住）才算；固定的列（fixed／sticky）不能在瀏海或底部橫條下面
    const zones = (where: string) =>
      page.evaluate(
        ([top, bottom, where]) => {
          const H = window.innerHeight
          const out: string[] = []
          const pinned = (el: Element) => {
            for (let e: Element | null = el; e; e = e.parentElement) if (/(fixed|sticky)/.test(getComputedStyle(e).position)) return true
            return false
          }
          for (const el of document.querySelectorAll<HTMLElement>('button, a[href], input, textarea, select, [role="button"], [role="tab"]')) {
            const r = el.getBoundingClientRect()
            const s = getComputedStyle(el)
            if (r.width < 2 || r.height < 2 || s.visibility === 'hidden' || el.closest('.sr-only-text,[aria-hidden="true"],[inert]') || r.bottom <= 0 || r.top >= H) continue
            const cx = Math.min(Math.max(r.left + r.width / 2, 1), innerWidth - 1)
            const probe = (y: number) => {
              const hit = document.elementFromPoint(cx, Math.min(Math.max(y, 0), H - 1))
              return !!hit && (el === hit || el.contains(hit) || hit.contains(el))
            }
            const label = (el.getAttribute('aria-label') || el.textContent || el.tagName).trim().slice(0, 16)
            if (r.top < top - 1 && probe(r.top + 2)) out.push(`瀏海蓋到「${label}」(top ${Math.round(r.top)})`)
            const lowEdge = r.bottom > H - bottom + 1 && probe(r.bottom - 2)
            if (lowEdge && (where === 'end' || pinned(el))) out.push(`底部橫條蓋到「${label}」(bottom ${Math.round(r.bottom)}${where === 'end' ? '，捲到底' : ''})`)
          }
          return [...new Set(out)].slice(0, 5)
        },
        [TOP, BOTTOM, where] as const,
      )
    const covered = await zones('start')
    await page.evaluate(() => {
      document.scrollingElement!.scrollTop = 1e7
      for (const e of document.querySelectorAll<HTMLElement>('*')) if (/(auto|scroll)/.test(getComputedStyle(e).overflowY) && e.scrollHeight > e.clientHeight) e.scrollTop = 1e7
    })
    await page.waitForTimeout(300)
    covered.push(...(await zones('end')))
    await page.evaluate(() => {
      document.scrollingElement!.scrollTop = 0
      for (const e of document.querySelectorAll<HTMLElement>('*')) if (/(auto|scroll)/.test(getComputedStyle(e).overflowY) && e.scrollHeight > e.clientHeight) e.scrollTop = 0
    })
    await page.waitForTimeout(200)
    bad.push(...covered)
    // 手指往上滑（內容往下）、再往下滑：有能捲的就要動
    const before = await positions()
    const H = page.viewportSize()!.height
    await drag(H * 0.7, H * 0.35)
    const mid = await positions()
    await drag(H * 0.35, H * 0.7)
    const after = await positions()
    const canScroll = await page.evaluate(() => document.scrollingElement!.scrollHeight > innerHeight + 40 || [...document.querySelectorAll<HTMLElement>('*')].some((e) => /(auto|scroll)/.test(getComputedStyle(e).overflowY) && e.scrollHeight > e.clientHeight + 40 && e.clientHeight > 200))
    if (canScroll && JSON.stringify(before) === JSON.stringify(mid) && JSON.stringify(mid) === JSON.stringify(after)) bad.push('手指滑不動 ' + JSON.stringify(before))
    if (bad.length) {
      problems[raw] = bad
      await page.screenshot({ path: shot(info, 'native' + raw.replace(/\//g, '_')) })
    }
  }
  expect(problems).toEqual({})
})

test('打開對話停在最新一則：訊息晚一點才排好（變高）也跟著到底；往上看時不跟', async ({ page }) => {
  const api = new MockServer(page)
  const { mi } = seed(api)
  const long = api.addThread(mi.id, '長內容')
  for (let i = 0; i < 30; i++) api.addMessage(long.id, { sender: i % 2 ? 'bot:' + mi.id : 'owner', status: 'complete', content: `第 ${i + 1} 則：` + '內容很長的一段話。'.repeat(6) })
  await api.install()
  await page.goto(`/bots/${mi.id}/threads/${long.id}`)
  await expect(page.getByText('第 30 則', { exact: false })).toBeVisible()
  const scroller = page.getByTestId('chat-scroller')
  const gap = () => scroller.evaluate((el) => el.scrollHeight - el.scrollTop - el.clientHeight)
  await expect.poll(gap).toBeLessThan(5)
  // 最後一則晚一點才變高（例：程式碼上色、圖片載入）
  const grow = () => scroller.evaluate((el) => {
    const last = [...el.querySelectorAll('[data-testid="message"], .group')].at(-1) ?? el.firstElementChild!.lastElementChild!
    const pad = document.createElement('div')
    pad.style.height = '240px'
    last.appendChild(pad)
  })
  await grow()
  await expect.poll(gap, { message: '內容變高後沒有跟著捲到底' }).toBeLessThan(5)
  // 往上看舊訊息時，內容變高不能把人拉回去
  await scroller.evaluate((el) => {
    el.scrollTop = 0
    el.dispatchEvent(new Event('scroll'))
  })
  await page.waitForTimeout(200)
  await grow()
  await page.waitForTimeout(300)
  expect(await scroller.evaluate((el) => el.scrollTop)).toBe(0)
})

test('真機健康摘要：滑了幾次、幾次有捲動、分頁列有沒有動，切到背景時回報一次（不含對話內容）', async ({ page, browserName }, info) => {
  test.skip(browserName !== 'chromium' || info.project.name === 'desktop', '要用 Chromium 模擬觸控')
  const api = new MockServer(page)
  const { mi } = seed(api)
  const long = api.addThread(mi.id, '長內容')
  for (let i = 0; i < 30; i++) api.addMessage(long.id, { sender: i % 2 ? 'bot:' + mi.id : 'owner', status: 'complete', content: `第 ${i + 1} 則：` + '內容很長的一段話。'.repeat(6) })
  api.override['system.client_report'] = () => ({ logged: true })
  await api.install()
  await page.goto(`/bots/${mi.id}/threads/${long.id}`)
  await expect(page.getByText('第 30 則', { exact: false })).toBeVisible()
  const cdp = await page.context().newCDPSession(page)
  for (let n = 0; n < 6; n++) {
    const [y0, y1] = n % 2 ? [650, 300] : [300, 650]
    await cdp.send('Input.dispatchTouchEvent', { type: 'touchStart', touchPoints: [{ x: 195, y: y0 }] })
    for (let i = 1; i <= 10; i++) await cdp.send('Input.dispatchTouchEvent', { type: 'touchMove', touchPoints: [{ x: 195, y: y0 + ((y1 - y0) * i) / 10 }] })
    await cdp.send('Input.dispatchTouchEvent', { type: 'touchEnd', touchPoints: [] })
    await page.waitForTimeout(600)
  }
  await page.evaluate(() => {
    Object.defineProperty(document, 'visibilityState', { value: 'hidden', configurable: true })
    document.dispatchEvent(new Event('visibilitychange'))
  })
  await expect.poll(() => api.calls.filter((c) => c.op === 'system.client_report' && c.input.kind === 'phone_health').length).toBe(1)
  const data = String(api.calls.find((c) => c.input.kind === 'phone_health')!.input.data)
  expect(data).toMatch(/swipes ok=\d+ stuck=0 edge=\d+/)
  expect(Number(/ok=(\d+)/.exec(data)![1])).toBeGreaterThanOrEqual(4)
  expect(data).not.toContain('內容很長')
  expect(api.calls.filter((c) => c.input.kind === 'scroll_stuck')).toHaveLength(0)
})

test('Bot 一邊回覆，往上看就不再被拉回，滑回最下面才又跟著（各種瀏覽器核心，含 iPhone 的 WebKit）', async ({ page }) => {
  const api = new MockServer(page)
  const { mi } = seed(api)
  const long = api.addThread(mi.id, '長內容')
  for (let i = 0; i < 30; i++) api.addMessage(long.id, { sender: i % 2 ? 'bot:' + mi.id : 'owner', status: 'complete', content: `第 ${i + 1} 則：` + '內容很長的一段話。'.repeat(6) })
  const reply = api.addMessage(long.id, { sender: 'bot:' + mi.id, content: '', streaming: true, task_id: 'k-live' })
  await api.install()
  await page.goto(`/bots/${mi.id}/threads/${long.id}`)
  await expect(page.getByText('第 30 則', { exact: false })).toBeVisible()
  const scroller = page.getByTestId('chat-scroller')
  const gap = () => scroller.evaluate((el) => el.scrollHeight - el.scrollTop - el.clientHeight)
  await expect.poll(gap).toBeLessThan(5)
  let text = ''
  const more = async (n: number) => {
    for (let i = 0; i < n; i++) {
      text += '回覆一段。'.repeat(4)
      await api.emit({ kind: 'message.created', bot_id: mi.id, thread_id: long.id, task_id: 'k-live', persist: false, payload: api.storeMessage(long.id, { ...reply, content: text, streaming: true }) })
      await page.waitForTimeout(50)
    }
  }
  // 手指按下、往上捲 40px（比「在最下面」的 80px 還近），按著不放，Bot 繼續吐字
  await scroller.evaluate((el) => {
    el.dispatchEvent(new Event('touchstart', { bubbles: true }))
    el.scrollTop -= 40
    el.dispatchEvent(new Event('scroll'))
  })
  await more(6)
  expect(await gap(), '手指按著時被拉回最底下').toBeGreaterThan(30)
  // 放開：只往上看了一點點也不再跟，下一段字不會把人拉回去
  await scroller.evaluate((el) => el.dispatchEvent(new Event('touchend', { bubbles: true })))
  await page.waitForTimeout(300)
  const after = await scroller.evaluate((el) => el.scrollTop)
  await more(3)
  expect(await scroller.evaluate((el) => el.scrollTop), '放開後被拉回最底下').toBe(after)
  // 自己滑回最下面：之後的新內容又跟著到底
  await scroller.evaluate((el) => {
    el.dispatchEvent(new Event('touchstart', { bubbles: true }))
    el.scrollTop = el.scrollHeight
    el.dispatchEvent(new Event('scroll'))
    el.dispatchEvent(new Event('touchend', { bubbles: true }))
  })
  await page.waitForTimeout(300)
  await more(3)
  await expect.poll(gap).toBeLessThan(5)
})

test('Bot 一邊回覆，手指往上滑不會被拉回最底下（對話不像被鎖住）', async ({ page, browserName }, info) => {
  test.skip(browserName !== 'chromium' || info.project.name === 'desktop', '要用 Chromium 模擬觸控')
  const api = new MockServer(page)
  const { mi } = seed(api)
  const long = api.addThread(mi.id, '長內容')
  for (let i = 0; i < 30; i++) api.addMessage(long.id, { sender: i % 2 ? 'bot:' + mi.id : 'owner', status: 'complete', content: `第 ${i + 1} 則：` + '內容很長的一段話。'.repeat(6) })
  const reply = api.addMessage(long.id, { sender: 'bot:' + mi.id, content: '', streaming: true, task_id: 'k-live' })
  await api.install()
  await page.goto(`/bots/${mi.id}/threads/${long.id}`)
  await expect(page.getByText('第 30 則', { exact: false })).toBeVisible()
  const gap = () => page.evaluate(() => {
    const el = [...document.querySelectorAll<HTMLElement>('main *')].find((e) => getComputedStyle(e).overflowY === 'auto' && e.scrollHeight > e.clientHeight + 40)!
    return el.scrollHeight - el.scrollTop - el.clientHeight
  })
  expect(await gap()).toBeLessThan(5)
  // Bot 每 60ms 多吐一段字
  let text = ''
  let on = true
  const stream = (async () => {
    while (on) {
      text += '回覆一段。'
      await api.emit({ kind: 'message.created', bot_id: mi.id, thread_id: long.id, task_id: 'k-live', persist: false, payload: api.storeMessage(long.id, { ...reply, content: text, streaming: true }) })
      await page.waitForTimeout(60)
    }
  })()
  // 手指按住往下拉 50px（內容往上捲），停住不放，等 Bot 再更新幾次
  const cdp = await page.context().newCDPSession(page)
  await cdp.send('Input.dispatchTouchEvent', { type: 'touchStart', touchPoints: [{ x: 195, y: 400 }] })
  for (let i = 1; i <= 10; i++) {
    await cdp.send('Input.dispatchTouchEvent', { type: 'touchMove', touchPoints: [{ x: 195, y: 400 + i * 5 }] })
    await page.waitForTimeout(16)
  }
  await page.waitForTimeout(400)
  const held = await gap()
  await cdp.send('Input.dispatchTouchEvent', { type: 'touchEnd', touchPoints: [] })
  on = false
  await stream
  expect(held, '手指按著時被拉回最底下').toBeGreaterThan(20)
})

test('破版檢查：每一頁在手機寬度都不超出畫面', async ({ page }, info) => {
  test.setTimeout(240_000)
  // Pixel 7 換成較窄的 360（常見的小 Android），iPhone 維持 390。
  if (info.project.name === 'mobile') await page.setViewportSize({ width: 360, height: 780 })
  const api = new MockServer(page)
  const { mi, room } = seed(api)
  // 很長的內容：沒有空白的網址、長程式碼、寬表格
  const long = api.addThread(mi.id, '長內容')
  api.addMessage(long.id, { sender: 'owner', content: '看這個 https://example.com/' + 'a'.repeat(120) })
  const wide = '結果：https://example.com/' + 'b'.repeat(120) + '\n\n```\n' + 'const x = ' + '"y"'.repeat(60) + '\n```\n\n| 欄位一 | 欄位二 | 欄位三 | 欄位四 | 欄位五 | 欄位六 |\n|---|---|---|---|---|---|\n| 很長很長的內容 | 很長很長的內容 | 很長很長的內容 | 很長很長的內容 | 很長很長的內容 | 很長很長的內容 |\n\n文件：`/workspace/reports/final-decision-memo-2026-10-19-very-long-name.md`'
  api.addMessage(long.id, { sender: 'bot:' + mi.id, status: 'complete', content: wide })
  // 群組裡同樣的長內容（2026-10-20 手機：群組的 Bot 訊息撐出畫面，整個對話可以左右拖）
  api.addRoomMessage(room.id, { sender: 'bot:' + mi.id, sender_name: '小秘', content: wide })
  await api.install()
  await page.goto('/')
  const problems: Record<string, string[]> = {}
  for (const raw of AUDIT) {
    const path = raw.replace('LONG', `/bots/${mi.id}/threads/${long.id}`).replace('BOTSET', `/settings/bots/${mi.id}`).replace('BOT', `/bots/${mi.id}`).replace('ROOM', `/rooms/${room.id}`)
    await page.goto(path)
    await page.waitForLoadState('networkidle')
    await page.waitForTimeout(400)
    const bad = await overflow(page)
    if (bad.length) problems[path] = bad
    if (bad.length || process.env.XBOT_AUDIT_SHOTS) await page.screenshot({ path: shot(info, 'audit' + path.replace(/\//g, '_')) })
  }
  expect(problems).toEqual({})
})

// iPhone 點到字體小於 16px 的輸入框會把整頁放大（看起來像破版）；手機上所有輸入框都要 ≥ 16px。
test('手機的輸入框字體 ≥ 16px（iPhone 才不會自動放大）', async ({ page }) => {
  const { mi } = await open(page, '/')
  for (const path of [`/bots/${mi.id}`, '/settings/models', '/files']) {
    await page.goto(path)
    await page.waitForTimeout(600)
    const small = await page.evaluate(() =>
      [...document.querySelectorAll('input, textarea, select')]
        .filter((el) => (el as HTMLInputElement).type !== 'checkbox' && (el as HTMLInputElement).type !== 'radio' && (el as HTMLElement).offsetParent)
        .map((el) => parseFloat(getComputedStyle(el).fontSize))
        .filter((px) => px < 16),
    )
    expect(small, path).toEqual([])
  }
  // 對話頁的高度跟著看得到的高度（--app-h）
  await page.goto(`/bots/${mi.id}`)
  await expect(page.getByRole('main', { name: /^和/ })).toBeVisible()
  const h = await page.evaluate(() => [document.querySelector('main[aria-label^="和"]')?.getBoundingClientRect().height, window.visualViewport?.height])
  expect(Math.abs((h[0] ?? 0) - (h[1] ?? 0))).toBeLessThan(2)
})

// 「/」叫出技能：選了帶入輸入框，送出的是「使用技能「…」：…」（任何引擎都看得懂）。手機與桌面都跑。
test('「/」技能選單：列出技能、選了送出變成使用技能', async ({ page }) => {
  const api = new MockServer(page)
  const { mi } = seed(api)
  api.override['skills.list'] = () => ({
    skills: [
      { id: 'price', name: '比價', description: '比三家價格', place: 'list', enabled: true, source: 'custom', created_at: 1, updated_at: 1 },
      { id: 'news', name: '新聞摘要', description: '整理今天的新聞', place: 'library', enabled: true, source: 'custom', created_at: 1, updated_at: 1 },
    ],
    counts: {},
    computer: true,
  })
  await api.install()
  await page.goto(`/bots/${mi.id}`)
  const box = page.getByRole('main', { name: /^和/ }).getByTestId('composer-input')
  await box.fill('/')
  const menu = page.getByRole('listbox', { name: '技能' })
  await expect(menu.getByRole('option')).toHaveCount(3) // 內建的 /plan ＋ 兩個技能
  // 選單整個在畫面裡
  const r = await menu.boundingBox()
  const vw = page.viewportSize()!.width
  expect(r && r.x >= 0 && r.x + r.width <= vw && r.y >= 0).toBe(true)
  await menu.getByRole('option', { name: /新聞摘要/ }).click()
  await expect(box).toHaveValue('/新聞摘要 ')
  await box.pressSequentially('只要科技類')
  await box.press('Enter')
  await expect.poll(() => api.lastSend?.text).toBe('使用技能「新聞摘要」：只要科技類')
})

// iPhone 鍵盤彈出（2026-10-19 實機截圖）：頁面不會變矮，Safari 把「看得到的範圍」往下移、變矮（visualViewport.offsetTop／height）。
// 對話頁要貼著看得到的範圍：頁首在最上面、輸入框貼著鍵盤；不能留在頁面頂端（那時整個畫面會是黑的）。
test('鍵盤彈出時（iPhone）：頁首和輸入框都在看得到的範圍裡', async ({ page }) => {
  await page.addInitScript(() => {
    const t = new EventTarget() as EventTarget & { height: number; width: number; offsetTop: number; offsetLeft: number; scale: number }
    t.height = window.innerHeight
    t.width = window.innerWidth
    t.offsetTop = 0
    t.offsetLeft = 0
    t.scale = 1
    Object.defineProperty(window, 'visualViewport', { value: t, configurable: true })
    ;(window as unknown as { __kb: (h: number) => void }).__kb = (h: number) => {
      t.height = h
      t.offsetTop = window.innerHeight - h // 看得到的範圍移到下半部（鍵盤蓋住的是下面，Safari 把畫面往上推）
      t.dispatchEvent(new Event('resize'))
      t.dispatchEvent(new Event('scroll'))
    }
  })
  const api = new MockServer(page)
  const { mi } = seed(api)
  await api.install()
  await page.goto(`/bots/${mi.id}`)
  const main = page.getByRole('main', { name: /^和/ })
  await expect(main).toBeVisible()
  const box = main.getByTestId('composer-input')
  await box.focus()
  const vh = page.viewportSize()!.height
  const kb = Math.round(vh * 0.4) // 鍵盤加上候選字列約佔 60%
  const top = vh - kb
  await page.evaluate((h) => (window as unknown as { __kb: (h: number) => void }).__kb(h), kb)
  await expect.poll(() => page.evaluate(() => document.documentElement.style.getPropertyValue('--app-top'))).toBe(`${top}px`)
  await expect.poll(async () => Math.round((await main.boundingBox())?.y ?? -1)).toBe(top)
  const m = await main.boundingBox()
  const b = await box.boundingBox()
  expect(Math.abs((m?.height ?? 0) - kb)).toBeLessThan(2)
  expect(b!.y).toBeGreaterThanOrEqual(top) // 輸入框在看得到的範圍裡
  expect(b!.y + b!.height).toBeLessThanOrEqual(top + kb + 1)
  const head = await page.getByRole('button', { name: /對話串/ }).boundingBox()
  expect(head!.y).toBeGreaterThanOrEqual(top - 1) // 頁首在看得到的範圍最上面
  expect(head!.y).toBeLessThan(top + 80)
  // 鍵盤收起來：回到原位
  await page.evaluate(() => (window as unknown as { __kb: (h: number) => void }).__kb(window.innerHeight))
  await expect.poll(async () => Math.round((await main.boundingBox())?.y ?? -1)).toBe(0)
})

// ── 手勢（跟著手指走，像原生 App）────────────────────────────

/** 用觸控事件拖一段（分成幾步，模擬手指移動）。 */
async function drag(page: Page, from: [number, number], to: [number, number], steps = 8) {
  await page.evaluate(
    async ({ from, to, steps }) => {
      const target = document.elementFromPoint(from[0], from[1]) ?? document.body
      // WebKit 不能 new Touch()：改送一般事件、自己帶上 touches（手勢只讀 touches[0] 的座標）
      const fire = (type: string, x: number, y: number) => {
        const t = { identifier: 1, target, clientX: x, clientY: y }
        let ev: Event
        try {
          const touch = new Touch(t)
          ev = new TouchEvent(type, { touches: type === 'touchend' ? [] : [touch], changedTouches: [touch], bubbles: true, cancelable: true })
        } catch {
          ev = new Event(type, { bubbles: true, cancelable: true })
          Object.defineProperty(ev, 'touches', { value: type === 'touchend' ? [] : [t] })
          Object.defineProperty(ev, 'changedTouches', { value: [t] })
        }
        target.dispatchEvent(ev)
      }
      fire('touchstart', from[0], from[1])
      for (let i = 1; i <= steps; i++) {
        fire('touchmove', from[0] + ((to[0] - from[0]) * i) / steps, from[1] + ((to[1] - from[1]) * i) / steps)
        await new Promise((r) => setTimeout(r, 16))
      }
      fire('touchend', to[0], to[1])
    },
    { from, to, steps },
  )
}

test('手勢：從左邊緣拉出側欄、往左拖收回；拉太短會彈回去', async ({ page }) => {
  await open(page, '/chats')
  await expect(page.getByRole('heading', { level: 1, name: '對話' })).toBeVisible()
  await settle(page)
  const vw = page.viewportSize()!.width
  const y = 400
  await drag(page, [4, y], [60, y])
  await expect(page.getByRole('dialog', { name: '側欄' })).toBeHidden()
  await drag(page, [4, y], [vw * 0.75, y])
  const drawer = page.getByRole('dialog', { name: '側欄' })
  await expect(drawer).toBeVisible()
  await expect.poll(async () => Math.round((await drawer.boundingBox())?.x ?? -999)).toBe(0)
  await drag(page, [200, y], [10, y])
  await expect(drawer).toBeHidden()
})

test('手勢：其他頁從左邊緣往右拖＝回上一頁', async ({ page }) => {
  await open(page, '/chats')
  await page.getByRole('button', { name: '設定', exact: true }).click()
  await expect(page).toHaveURL(/\/settings\/?$/)
  const vw = page.viewportSize()!.width
  await drag(page, [4, 400], [vw * 0.7, 400])
  await expect(page).toHaveURL(/\/chats$/)
})

test('手勢：對話頁從右邊緣往左拉出對話串，往右拖收回', async ({ page }) => {
  const { mi } = await open(page, '/')
  await page.goto(`/bots/${mi.id}`)
  await expect(page.getByRole('main', { name: /^和/ })).toBeVisible()
  const vw = page.viewportSize()!.width
  await drag(page, [vw - 4, 400], [vw * 0.25, 400])
  const panel = page.getByRole('dialog', { name: '小秘的對話串' })
  await expect(panel).toBeVisible()
  await expect(panel.getByRole('button', { name: /比價三家競品/ })).toBeVisible()
  await drag(page, [vw - 200, 400], [vw - 10, 400])
  await expect(panel).toBeHidden()
  // 按「對話串」也能打開
  await page.getByRole('button', { name: /^對話串/ }).click()
  await expect(panel).toBeVisible()
})

test('手勢：底部 sheet 往下拖就關', async ({ page }) => {
  const { mi } = await open(page, '/')
  await page.goto(`/bots/${mi.id}`)
  await page.getByRole('button', { name: /^對話串/ }).click()
  await page.getByRole('button', { name: '刪除這個對話串' }).click()
  const sheet = page.getByRole('alertdialog', { name: '刪除這個對話串？' })
  await expect(sheet).toBeVisible()
  const box = (await sheet.boundingBox())!
  await drag(page, [box.x + box.width / 2, box.y + 12], [box.x + box.width / 2, box.y + box.height])
  await expect(sheet).toBeHidden()
})
