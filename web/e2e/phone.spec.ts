import { expect, test, type Page } from '@playwright/test'
import { MockServer, settle } from './mock'

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
  api.addRoomMessage(room.id, { sender: mi.id, sender_name: '小秘', content: '好，我來盯進度。價格出來我會整理成提案給你。' })
  api.addRoomMessage(room.id, { sender: zhe.id, sender_name: '阿哲', content: '三家分別是 NT$990、NT$1,080、NT$1,150。建議定 NT$1,020，比價表在這裡。' })
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
  await page.getByRole('button', { name: '關閉側欄' }).click()
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
  await expect(page.getByRole('textbox')).toBeVisible()
  const box = (await page.getByRole('textbox').boundingBox())!
  expect(box.y + box.height).toBeLessThan(page.viewportSize()!.height)
  await snap(page, info, 'L03')
})

test('L04 電腦：狀態與叫醒（即時畫面等 B14）', async ({ page }, info) => {
  const { kai } = await open(page, '/chats')
  await page.goto(`/bots/${kai.id}/computer`)
  await expect(page.getByRole('heading', { name: '阿凱的電腦' })).toHaveCount(1) // 只有一條頁首
  await page.getByRole('button', { name: '叫醒' }).click()
  await expect(page.getByText(/電腦開著/)).toBeVisible()
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
  api.override['chat.respond'] = () => ({ ok: true })
  await sheet.getByRole('button', { name: '允許這次' }).click()
  await expect(page.getByText(/已允許這次/)).toBeVisible()
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

test('L15 辦公室：依主機分組、叫醒、接手', async ({ page }, info) => {
  const { api, bian, zf, kai } = await open(page, '/office')
  await working(api, { [bian.id]: 'waiting_input', [zf.id]: 'waiting_approval' })
  await expect(page.getByRole('region', { name: 'office-pc' })).toContainText('離線')
  await expect(page.getByTestId('office-row')).toHaveCount(8)
  await snap(page, info, 'L15')
  await page.getByTestId('office-row').filter({ hasText: '阿凱' }).getByRole('button', { name: '叫醒' }).click()
  await expect.poll(() => api.calls.some((c) => c.op === 'computers.wake' && c.input.bot_id === kai.id)).toBe(true)
})

test('L18 通話（語音在 E32）：全螢幕畫面、掛斷回到對話', async ({ page }, info) => {
  const { mi } = await open(page, '/chats')
  await page.goto(`/bots/${mi.id}/call`)
  await expect(page.getByText('語音通話還在做')).toBeVisible()
  await snap(page, info, 'L18')
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

test('L20 @ 提到選單與停止鈕', async ({ page }, info) => {
  const { api, mi, t1 } = await open(page, '/chats')
  await page.goto(`/bots/${mi.id}/threads/${t1.id}`)
  await working(api, { [mi.id]: 'running' })
  await api.emit({ kind: 'task.state', bot_id: mi.id, thread_id: t1.id, task_id: 'k-run', payload: { state: 'running' }, persist: false })
  await page.getByRole('textbox').fill('也請 @')
  const menu = page.getByRole('listbox', { name: '提到誰' })
  await expect(menu).toContainText('@阿哲')
  await expect(menu).toContainText('@新品上架專案')
  await expect(menu).not.toContainText('@小秘')
  await snap(page, info, 'L20')
  await menu.getByRole('option', { name: /@阿哲/ }).click()
  await expect(page.getByRole('textbox')).toHaveValue('也請 @阿哲 ')
})

test('L21 先判斷列', async ({ page }, info) => {
  const { api, mi, t1 } = await open(page, '/chats')
  await page.goto(`/bots/${mi.id}/threads/${t1.id}`)
  await page.getByRole('textbox').fill('幫新品帆布包寫 150 字介紹。')
  await page.getByRole('textbox').press('Enter')
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
