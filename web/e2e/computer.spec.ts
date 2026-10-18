import { expect, test, type Page } from '@playwright/test'
import { MockServer, settle, type Bot } from './mock'
import { fakeVnc, loadFrame, type Frame } from './rfb'

// B14 看畫面與接手：電腦面板（E01）、全視窗接手（E02、手機 L04）、辦公室（E03、手機 L15）、電腦狀態（E10）。
// 假服務＋假的 RFB 伺服器（畫面是從真的 Bot 電腦擷取的圖），截圖和畫板並排比對。
const shot = (name: string) => `e2e-results/screens/${name}.png`

test.beforeEach(async ({ page }, info) => {
  if (info.project.name === 'desktop') await page.setViewportSize({ width: 1440, height: 960 })
})

type ScreenState = Record<string, unknown> & { bot_id: string; name: string }

function screenOf(bot: Bot, over: Record<string, unknown> = {}): ScreenState {
  return {
    bot_id: bot.id, name: bot.name, host_id: 'local', host_name: '這台主機', box: 'running', host_status: 'online', working: true, paused: false,
    last_active_at: Date.now() - 42 * 60_000, viewers: 1, you: false, vnc: `/api/v1/computers/vnc?bot=${bot.id}`, ...over,
  }
}

/** 一個 Bot 的畫面狀態機：takeover／handback 改狀態，computers.screen 回目前的。 */
function wireScreens(api: MockServer, states: Record<string, ScreenState>) {
  Object.assign(api.override, {
    'computers.screen': (i: Record<string, unknown>) => states[i.bot_id as string],
    'computers.screens': () => ({ screens: Object.values(states) }),
    'computers.takeover': (i: Record<string, unknown>) => {
      const s = states[i.bot_id as string]
      Object.assign(s, { working: false, paused: true, you: true, takeover: { by: 'owner', by_name: '你', since: Date.now(), task_id: 'task-x' } })
      return s
    },
    'computers.handback': (i: Record<string, unknown>) => {
      const s = states[i.bot_id as string]
      delete s.takeover
      delete s.request
      Object.assign(s, { working: true, paused: false, you: false })
      return { note: (i.note as string) || '我剛剛在電腦上操作過，請從目前畫面繼續。', resumed_task_id: 'task-y', thread_id: 'th', screen: s }
    },
  })
}

/** 點畫面（noVNC 的 canvas 會跟著框框縮放，不等它「穩定」）。 */
async function clickScreen(page: Page, x: number, y: number) {
  const box = (await page.getByTestId('vnc-screen').locator('canvas').boundingBox())!
  await page.mouse.click(box.x + x, box.y + y)
}

async function connected(page: Page) {
  await expect(page.getByTestId('watching')).toBeVisible({ timeout: 15000 })
  await page.waitForTimeout(300)
}

test('電腦面板（E01）：對話旁的即時畫面，只能看；接手後可以操作，交還帶說明', async ({ page, context }, info) => {
  test.skip(info.project.name !== 'desktop', '面板只在寬螢幕')
  const api = new MockServer(page)
  const bot = api.addBot({ name: '小秘', shape: 'cursor', color: 'blue', title: '幕僚長' })
  const th = api.addThread(bot.id, '三家競品定價')
  api.addMessage(th.id, { sender: 'owner', content: '幫我比較蝦皮上三家競品的定價，週一前給我調價建議。' }, Date.now() - 5 * 60_000)
  api.addMessage(th.id, { sender: 'bot:' + bot.id, content: '收到。我請阿哲去抓三家賣場的價格，我先在電腦上整理自家 12 項主力商品的現價，好了放進同一張比價表。' }, Date.now() - 4 * 60_000)
  const states = { [bot.id]: screenOf(bot) }
  wireScreens(api, states)
  await api.install()
  const frame = await loadFrame(context, 'desktop.jpg')
  const vnc = await fakeVnc(page, () => frame)

  await page.goto(`/bots/${bot.id}/threads/${th.id}`)
  await page.getByRole('button', { name: '小秘的電腦' }).click()
  const panel = page.getByTestId('computer-panel')
  await expect(panel.getByRole('heading', { name: '小秘的電腦' })).toBeVisible()
  await connected(page)
  expect(vnc.urls[0]).toBe(`/api/v1/computers/vnc?bot=${bot.id}`)
  await expect(panel.getByTestId('capsule')).toContainText('小秘 正在使用電腦')
  // 只能看：點畫面不會送出滑鼠（noVNC viewOnly；服務那端也會丟掉）。
  await clickScreen(page, 100, 100)
  expect(vnc.input).toEqual([])
  await settle(page)
  await page.screenshot({ path: shot('desktop-e01-computer') })

  await panel.getByRole('button', { name: '接手操作' }).first().click()
  await expect(panel.getByRole('button', { name: '完成，交還給小秘' })).toBeVisible()
  await expect(panel.getByTestId('capsule')).toContainText('你正在操作 · 小秘已暫停')
  await expect.poll(() => vnc.urls.at(-1)).toBe(`/api/v1/computers/vnc?bot=${bot.id}&mode=control`)
  await connected(page)
  await clickScreen(page, 200, 120)
  await expect.poll(() => vnc.input.includes(5)).toBe(true)
  await settle(page)
  await page.screenshot({ path: shot('desktop-e01-takeover') })

  await panel.getByRole('textbox', { name: '交還時要說什麼（選填）' }).fill('E7 我改成 435 了')
  await panel.getByRole('button', { name: '完成，交還給小秘' }).click()
  await expect(page.getByText('已交還給小秘')).toBeVisible()
  expect(api.calls.find((c) => c.op === 'computers.handback')?.input).toEqual({ bot_id: bot.id, note: 'E7 我改成 435 了' })
  await expect.poll(() => vnc.urls.at(-1)).toBe(`/api/v1/computers/vnc?bot=${bot.id}`)
})

test('全視窗接手（E02／手機 L04）：Bot 請你接手 → 接手 → 完成交還', async ({ page, context }, info) => {
  const api = new MockServer(page)
  const bot = api.addBot({ name: '小編', shape: 'circle', color: 'pink' })
  const reason = '發佈新品貼文時，Threads 要求輸入簡訊安全碼。我收不到你的簡訊，請在左邊畫面輸入，好了交還給我。'
  api.inbox.push({ id: 'i1', kind: 'takeover', group: 'decide', bot_id: bot.id, thread_id: 'th', title: '小編需要你接手電腦', body: reason, status: 'open', created_at: Date.now() - 2 * 60_000, payload: { reason } })
  const states = { [bot.id]: screenOf(bot, { host_id: 'c1', host_name: 'cloud-01', working: false, request: { inbox_id: 'i1', reason, thread_id: 'th', at: Date.now() } }) }
  wireScreens(api, states)
  await api.install()
  const frame = await loadFrame(context, 'desktop-code.jpg')
  const vnc = await fakeVnc(page, () => frame)

  // 待我處理的卡：「接手電腦」到全視窗。
  await page.goto('/')
  const card = page.locator('[data-kind="takeover"]')
  await expect(card).toContainText('等你接手')
  await card.getByRole('link', { name: '接手電腦' }).click()
  await expect(page).toHaveURL(new RegExp(`/bots/${bot.id}/computer$`))
  await expect(page.getByTestId('takeover-request')).toContainText('Threads 要求輸入簡訊安全碼')
  await connected(page)

  await page.getByRole('button', { name: '接手操作' }).first().click()
  await expect(page.getByRole('heading', { name: '你正在操作小編的電腦' })).toBeVisible()
  await expect.poll(() => vnc.urls.at(-1)).toContain('&mode=control')
  await connected(page)
  if (info.project.name === 'mobile') {
    await page.getByRole('button', { name: '更多' }).click()
    await expect(page.getByRole('button', { name: 'Ctrl' })).toBeVisible()
    await page.getByRole('button', { name: 'Tab' }).click()
    await expect.poll(() => vnc.input.includes(4)).toBe(true)
  }
  await settle(page)
  await page.screenshot({ path: shot(`${info.project.name}-${info.project.name === 'mobile' ? 'l04-phone-computer' : 'e02-takeover'}`) })

  await page.getByRole('button', { name: '完成，交還給小編' }).click()
  await expect(page.getByTestId('handed-back')).toContainText('我剛剛在電腦上操作過，請從目前畫面繼續。')
  expect(api.calls.find((c) => c.op === 'computers.handback')?.input).toEqual({ bot_id: bot.id })
})

test('電腦狀態（E10）：休眠、開機中、主機離線、畫面連不上', async ({ page }, info) => {
  test.skip(info.project.name !== 'desktop', '狀態卡截一次就好')
  const api = new MockServer(page)
  const bots = ['阿凱', '數據喵', '信差', '小秘'].map((name, i) => api.addBot({ name, shape: ['squircle', 'blob', 'star', 'cursor'][i], color: ['green', 'purple', 'teal', 'blue'][i] }))
  const states = {
    [bots[0].id]: screenOf(bots[0], { box: 'sleeping', working: false }),
    [bots[1].id]: screenOf(bots[1], { box: 'creating', working: false }),
    [bots[2].id]: screenOf(bots[2], { host_status: 'offline', host_name: 'cloud-01' }),
    [bots[3].id]: screenOf(bots[3]),
  }
  wireScreens(api, states)
  await api.install()
  // 畫面連線一直失敗：8 秒後重試、冷卻 15 秒再試，之後請你按重新連線。
  await page.routeWebSocket(/\/api\/v1\/computers\/vnc/, (ws) => ws.close())
  await page.clock.install()
  for (const [i, b] of bots.entries()) {
    await page.goto(`/bots/${b.id}/computer`)
    const stage = page.getByTestId('computer-stage')
    if (i === 3) {
      await expect(stage).toContainText('8 秒後重新連線')
      await page.clock.fastForward(8000)
      await expect(stage).toContainText('先等 15 秒再試')
      await page.clock.fastForward(15000)
      await expect(stage).toContainText('畫面連不上，請按重新連線')
      await expect(stage.getByRole('button', { name: '重新連線' })).toBeVisible()
    } else {
      await expect(stage).toContainText(['休眠中', '正在開機…', '主機離線'][i])
    }
    await settle(page)
    await stage.screenshot({ path: shot(`desktop-e10-${['sleeping', 'booting', 'host-offline', 'retry-failed'][i]}`) })
  }
})

test('辦公室（E03／手機 L15）：依主機分組、即時縮圖、等你、篩選', async ({ page, context }, info) => {
  const api = new MockServer(page)
  const frame: Frame = await loadFrame(context, 'desktop.jpg')
  const code: Frame = await loadFrame(context, 'desktop-code.jpg')
  const mk = (name: string, shape: string, color: string) => api.addBot({ name, shape, color })
  const [mi, zhe, zhang, kai, bug, bian, cat] = [
    mk('小秘', 'cursor', 'blue'),
    mk('阿哲', 'blob', 'orange'),
    mk('帳房', 'circle', 'yellow'),
    mk('阿凱', 'squircle', 'green'),
    mk('抓蟲', 'shield', 'red'),
    mk('小編', 'circle', 'pink'),
    mk('數據喵', 'blob', 'purple'),
  ]
  const cloud = { host_id: 'c1', host_name: 'cloud-01' }
  const states = {
    [mi.id]: screenOf(mi),
    [zhe.id]: screenOf(zhe, { last_active_at: Date.now() - 60 * 60_000 }),
    [zhang.id]: screenOf(zhang, { working: false, last_active_at: Date.now() - 3 * 60_000 }),
    [kai.id]: screenOf(kai, { box: 'sleeping', working: false }),
    [bug.id]: screenOf(bug, { box: 'sleeping', working: false, last_active_at: Date.now() - 3 * 86400_000 }),
    [bian.id]: screenOf(bian, { ...cloud, working: false, request: { inbox_id: 'i2', reason: '要輸入驗證碼', at: Date.now() } }),
    [cat.id]: screenOf(cat, { ...cloud, box: 'creating', working: false }),
  }
  wireScreens(api, states)
  api.inbox.push({ id: 'i1', kind: 'approval', group: 'decide', bot_id: zhang.id, title: '付款 NT$1,280', body: '', status: 'open', created_at: Date.now() })
  const res = (cpus: number, mem: number) => ({ cpus, mem_bytes: mem * 2 ** 30, disk_free_bytes: 0, disk_total_bytes: 0, docker_version: '', os: '', arch: 'amd64', hostname: '', rtt_ms: 0, tailscale: true })
  api.override['hosts.list'] = () => ({
    hosts: [
      { id: 'local', name: '這台主機', kind: 'local', status: 'online', max_running: 4, running: 3, default: true, control_center: true, resources: res(8, 16), bots: [], queued: 0 },
      { id: 'c1', name: 'cloud-01', kind: 'ssh', status: 'online', max_running: 3, running: 2, default: false, control_center: false, resources: res(4, 8), bots: [], queued: 0 },
    ],
  })
  api.override['computers.snapshot'] = (i: Record<string, unknown>) => ({ bot_id: i.bot_id, available: true, image: i.bot_id === bian.id ? code.jpeg : frame.jpeg, media_type: 'image/jpeg', taken_at: Date.now() })
  await api.install()

  await page.goto('/office')
  await expect(page.getByRole('heading', { level: 1, name: '辦公室' })).toBeVisible()
  const groups = page.getByTestId('host-group')
  await expect(groups).toHaveCount(2)
  const item = info.project.name === 'mobile' ? 'office-row' : 'office-card'
  await expect(groups.nth(0).getByTestId(item)).toHaveCount(5)
  await expect(page.locator(`[data-testid="${item}"][data-status="waiting"]`)).toHaveCount(2)
  await expect(page.getByRole('img', { name: '小編的電腦畫面' })).toBeVisible()
  await settle(page)
  await page.screenshot({ path: shot(`${info.project.name}-${info.project.name === 'mobile' ? 'l15-phone-office' : 'e03-office'}`), fullPage: info.project.name === 'mobile' })

  await page.getByRole('tab', { name: /休眠中/ }).click()
  await expect(page.getByTestId(item)).toHaveCount(2)
  await expect(groups).toHaveCount(1)
  await page.getByRole('tab', { name: /等你/ }).click()
  await page.getByTestId(item).filter({ hasText: '小編' }).getByRole('link', { name: '接手' }).click()
  await expect(page).toHaveURL(new RegExp(`/bots/${bian.id}/computer$`))
})
