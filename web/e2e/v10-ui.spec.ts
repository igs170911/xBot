import { expect, test, type Page } from './fixtures'
import { MockServer, settle, type Bot } from './mock'
import { fakeVnc, loadFrame } from './rfb'

// v1.0 補齊的畫面：電腦的動作與私有網路例外（E01、E10）、「更新電腦」（J06）、備份與更新（J07）、
// 成員與紀錄（J08）、對話附檔（C05「8 · 附件」）、Bot 設定的「在哪裡跑」（G08）。
// 假服務，截圖存在 e2e-results/screens，和畫板並排比對。
const shot = (name: string) => `e2e-results/screens/${name}.png`
const GB = 2 ** 30

test.beforeEach(async ({ page }, info) => {
  if (info.project.name === 'desktop') await page.setViewportSize({ width: 1440, height: 960 })
})

function res(cpus: number, mem: number) {
  return { cpus, mem_bytes: mem * GB, disk_free_bytes: 61 * GB, disk_total_bytes: 120 * GB, docker_version: '27.3.1', os: 'Debian GNU/Linux 13', arch: 'amd64', hostname: 'x', rtt_ms: 18, tailscale: true }
}

function hostsOf(bots: Bot[], outdated = false) {
  const ref = (b: Bot, running: boolean) => ({ id: b.id, name: b.name, shape: b.shape, color: b.color, state: running ? 'running' : 'sleeping', running })
  return [
    {
      id: 'local', name: '這台主機', kind: 'local', address: 'unix:///var/run/docker.sock', status: 'online', arch: 'amd64', max_running: 4, running: 1, resources: res(8, 16),
      last_seen_at: Date.now(), control_center: true, default: true, queued: 0, created_at: 1, bots: bots.slice(0, 1).map((b) => ref(b, true)), image_outdated: false,
    },
    {
      id: 'c1', name: 'cloud-01', kind: 'ssh', address: 'cloud-01', port: 22, user: 'xbot', status: 'online', arch: 'amd64', max_running: 3, running: 0, resources: res(4, 8),
      health: { state: 'online', since: Date.now(), connects: 1, rtt_ms: 18, reverse_forwards: 0, local_forwards: 0 },
      last_seen_at: Date.now(), control_center: false, default: false, queued: 0, created_at: 2, bots: bots.slice(1).map((b) => ref(b, false)), image_outdated: outdated,
    },
  ]
}

function viewOf(bot: Bot, over: Record<string, unknown> = {}) {
  return {
    computer: { bot_id: bot.id, host_id: 'local', container_id: 'c-' + bot.id, state: 'running', running: true, novnc_port: 0, hermes_port: 0 },
    bot_name: bot.name, host_name: '這台主機', idle_minutes: 30, last_active_at: Date.now() - 60_000, sleeps_at: 0, busy: false, outdated: false,
    allow_private: [] as string[], home: false, moving: false, ...over,
  }
}

function screenOf(bot: Bot) {
  return {
    bot_id: bot.id, name: bot.name, host_id: 'local', host_name: '這台主機', box: 'running', host_status: 'online', working: false, paused: false,
    last_active_at: Date.now() - 4 * 60_000, viewers: 1, you: false, vnc: `/api/v1/computers/vnc?bot=${bot.id}`,
  }
}

test('電腦的動作（E01、E10）：重新開機要確認、刪除電腦資料是危險確認、私有網路例外', async ({ page, context }, info) => {
  test.skip(info.project.name !== 'desktop', '面板只在寬螢幕')
  const api = new MockServer(page)
  const mi = api.addBot({ name: '小秘', shape: 'cursor', color: 'blue', title: '幕僚長' })
  const th = api.addThread(mi.id, '三家競品定價')
  api.addMessage(th.id, { sender: 'owner', content: '幫我比較蝦皮上三家競品的定價，週一前給我調價建議。' }, Date.now() - 5 * 60_000)
  let view = viewOf(mi, { allow_private: ['192.168.1.30:1234'] })
  Object.assign(api.override, {
    'hosts.list': () => ({ hosts: hostsOf([mi]) }),
    'computers.screen': () => screenOf(mi),
    'computers.status': () => view,
    'computers.move_status': () => ({ running: false, jobs: [] }),
    'computers.egress_status': () => ({ exit_node: '', has_auth_key: false, shared_key: false, bots: [], hosts: [] }),
    'computers.restart': () => view,
    'computers.purge': () => (view = viewOf(mi, { computer: { ...view.computer, state: 'none', running: false, container_id: '' } })),
    'computers.set_network': (i: Record<string, unknown>) => (view = { ...view, allow_private: i.allow_private as string[] }),
  })
  await api.install()
  const frame = await loadFrame(context, 'desktop.jpg')
  await fakeVnc(page, () => frame)

  await page.goto(`/bots/${mi.id}/threads/${th.id}`)
  await page.getByRole('button', { name: '小秘的電腦' }).click()
  const panel = page.getByTestId('computer-panel')
  await panel.getByRole('button', { name: '電腦設定' }).click()
  const actions = panel.getByTestId('computer-actions')
  await expect(actions.getByRole('button', { name: '休眠' })).toBeEnabled()
  await expect(panel.getByTestId('network-allow')).toContainText('192.168.1.30:1234')
  await actions.scrollIntoViewIfNeeded()
  await settle(page)
  await page.screenshot({ path: shot('desktop-v10-computer-actions') })

  await actions.getByRole('button', { name: '重新開機' }).click()
  const restart = page.getByRole('alertdialog', { name: '重新開機小秘的電腦？' })
  await expect(restart).toContainText('正在做的工作會中斷，檔案和瀏覽器登入都會保留。')
  await settle(page)
  await page.screenshot({ path: shot('desktop-v10-restart-confirm') })
  await restart.getByRole('button', { name: '重新開機' }).click()
  await expect.poll(() => api.calls.filter((c) => c.op === 'computers.restart').length).toBe(1)

  // 開一個例外要再確認；確認前不會送出。
  const net = panel.getByTestId('network-allow')
  await net.getByRole('textbox').fill('192.168.1.10')
  await net.getByRole('button', { name: '加入例外' }).click()
  const open = page.getByRole('alertdialog', { name: '讓小秘的電腦連到 192.168.1.10？' })
  await expect(open).toBeVisible()
  expect(api.calls.filter((c) => c.op === 'computers.set_network')).toHaveLength(0)
  await open.getByRole('button', { name: '打開' }).click()
  await expect.poll(() => api.calls.find((c) => c.op === 'computers.set_network')?.input).toEqual({ bot_id: mi.id, allow_private: ['192.168.1.30:1234', '192.168.1.10'] })
  await expect(net.getByRole('button', { name: '移除 192.168.1.10' })).toBeVisible()

  await actions.getByRole('button', { name: '刪除電腦資料' }).click()
  const purge = page.getByRole('alertdialog', { name: '刪除小秘的電腦和裡面所有資料？' })
  await expect(purge).toContainText('無法復原')
  await settle(page)
  await page.screenshot({ path: shot('desktop-v10-purge-confirm') })
  await purge.getByRole('button', { name: '刪除電腦資料' }).click()
  await expect.poll(() => api.calls.filter((c) => c.op === 'computers.purge').length).toBe(1)
  await expect(actions.getByRole('button', { name: '刪除電腦資料' })).toBeDisabled()
})

test('更新電腦（J06、B13）：映像過期時出現按鈕，確認後在背景更新', async ({ page }, info) => {
  const api = new MockServer(page)
  const mi = api.addBot({ name: '小秘', shape: 'cursor', color: 'blue' })
  const zhe = api.addBot({ name: '阿哲', shape: 'blob', color: 'orange' })
  const plan = {
    running: false, total: 2, done: 0,
    hosts: [
      { host_id: 'local', name: '這台主機', image_outdated: false, state: 'pending', bots: [{ bot_id: mi.id, name: '小秘', state: 'pending' }] },
      { host_id: 'c1', name: 'cloud-01', image_outdated: true, state: 'pending', bots: [{ bot_id: zhe.id, name: '阿哲', state: 'pending' }] },
    ],
  }
  let started = false
  Object.assign(api.override, {
    'hosts.list': () => ({ hosts: hostsOf([mi, zhe], true) }),
    'computers.move_status': () => ({ running: false, jobs: [] }),
    'computers.egress_status': () => ({ exit_node: '', has_auth_key: false, shared_key: false, bots: [], hosts: [] }),
    'computers.update': () => (started ? { ...plan, running: true, started_at: Date.now() } : plan),
  })
  await api.install()
  await page.goto('/settings/computers')
  await expect(page.getByTestId('update-needed')).toContainText('2 台電腦要更新，1 台主機的電腦映像過期')
  await expect(page.getByTestId('host-row').nth(1)).toContainText('映像過期')
  await settle(page)
  await page.screenshot({ path: shot(`${info.project.name}-v10-update-computers`) })
  started = true
  await page.getByRole('button', { name: '更新電腦' }).click()
  const dlg = page.getByRole('alertdialog', { name: '更新電腦？' })
  await expect(dlg).toContainText('會重建：小秘、阿哲')
  await dlg.getByRole('button', { name: '開始更新' }).click()
  await expect(page.getByTestId('update-progress')).toContainText('正在更新電腦 0/2')
  await api.emit({ kind: 'computers.update', payload: { ...plan, running: true, done: 1, current: '阿哲' }, persist: false })
  await expect(page.getByTestId('update-progress')).toContainText('正在處理阿哲')
})

test('備份與更新（J07）：版本、最近的備份、立即備份、還原與更新的指令', async ({ page }, info) => {
  const api = new MockServer(page)
  const at = (d: number, h: number) => new Date(2026, 9, d, h, 0).getTime()
  const backups = [
    { name: 'xbot-backup-20261018-030000.tar.gz', path: '/home/u/.local/share/xbot-backups/xbot-backup-20261018-030000.tar.gz', size: 1.8 * GB, created_at: at(18, 3) },
    { name: 'xbot-backup-20261017-030000.tar.gz', path: '/home/u/.local/share/xbot-backups/xbot-backup-20261017-030000.tar.gz', size: 1.7 * GB, created_at: at(17, 3) },
  ]
  let job: Record<string, unknown> | undefined
  const f = { name: 'xbot-backup-20261019-090000.tar.gz', path: '/home/u/.local/share/xbot-backups/xbot-backup-20261019-090000.tar.gz', size: 1.9 * GB, created_at: at(19, 9) }
  Object.assign(api.override, {
    'backup.list': () => ({ dir: '/home/u/.local/share/xbot-backups', data_dir: '/home/u/.local/share/xbot', backups, job }),
    'backup.create': () => (job = { running: true, started_at: Date.now(), file: { ...f, size: 0 } }),
  })
  await api.install()
  await page.goto('/settings/advanced/backup')
  await expect(page.getByText('/home/u/.local/share/xbot', { exact: true })).toBeVisible()
  await expect(page.getByTestId('backup-row')).toHaveCount(2)
  await expect(page.getByText('XBot e2e-mock', { exact: true })).toBeVisible()
  await settle(page)
  await page.screenshot({ path: shot(`${info.project.name}-v10-backup`), fullPage: true })
  await page.getByRole('button', { name: '立即備份' }).click()
  await expect(page.getByRole('button', { name: '備份中…' })).toBeVisible()
  backups.unshift(f)
  job = { running: false, started_at: Date.now(), finished_at: Date.now(), file: f, files: 1284, computers: 2 }
  await expect(page.getByText(/已備份 1284 個檔案和 2 台 Bot 電腦的資料/)).toBeVisible({ timeout: 10_000 })
  await expect(page.getByTestId('backup-row')).toHaveCount(3)
  await expect(page.getByText('make restore FILE=/home/u/.local/share/xbot-backups/xbot-backup-20261019-090000.tar.gz')).toBeVisible()
})

test('成員與紀錄（J08）：你、連線的代理；活動紀錄依誰、種類、結果、日期篩選', async ({ page }, info) => {
  const api = new MockServer(page)
  const mi = api.addBot({ name: '小秘', shape: 'cursor', color: 'blue' })
  const zf = api.addBot({ name: '帳房', shape: 'square', color: 'green' })
  const t = (m: number) => Date.now() - m * 60_000
  const all = [
    { id: 9, actor: 'owner', via: 'http', op: 'xbot.approvals.decide', target: 'ap1', status: 'ok', created_at: t(3) },
    { id: 8, actor: `bot:${mi.id}`, via: 'mcp', op: 'xbot.collab.message_bot', target: zf.id, status: 'ok', created_at: t(30) },
    { id: 7, actor: 'agent:a1', via: 'mcp', op: 'xbot.bots.delete', target: zf.id, status: 'pending_approval', created_at: t(70) },
    { id: 6, actor: 'owner', via: 'http', op: 'xbot.usage.set_caps', target: '', status: 'ok', created_at: t(200) },
    { id: 5, actor: 'agent:a1', via: 'mcp', op: 'xbot.computers.purge', target: mi.id, status: 'error', error_code: 'forbidden', created_at: t(400) },
    { id: 4, actor: `bot:${zf.id}`, via: 'mcp', op: 'xbot.approvals.request', target: '', status: 'ok', created_at: t(900) },
    // 例行操作：預設不列（只看變更）
    { id: 3, actor: 'owner', via: 'http', op: 'xbot.inbox.mark_read', target: '', status: 'ok', created_at: t(901) },
    { id: 2, actor: `bot:${mi.id}`, via: 'mcp', op: 'xbot.computers.snapshot', target: mi.id, status: 'ok', created_at: t(902) },
  ].map((e) => ({ request_id: '', params: {}, error_code: '', ...e }))
  Object.assign(api.override, {
    'agents.list': () => ({ agents: [{ id: 'a1', name: 'Claude Code', status: 'active', scopes: ['read', 'operate'] }] }),
    'audit.list': (i: Record<string, unknown>) => ({
      entries: all.filter((e) => (!i.actor || e.actor === i.actor) && (!i.op || e.op.startsWith(String(i.op) + '.')) && (!i.status || e.status === i.status)),
    }),
  })
  await api.install()
  await page.goto('/settings/advanced/people')
  await expect(page.getByText('你（擁有者）')).toBeVisible()
  await expect(page.getByTestId('member-agents')).toContainText('Claude Code')
  await expect(page.getByTestId('audit-row')).toHaveCount(6)
  await settle(page)
  await page.screenshot({ path: shot(`${info.project.name}-v10-people`), fullPage: true })
  await expect(page.getByText('把通知標為已讀')).toHaveCount(0)
  await page.getByRole('button', { name: /只看變更/ }).click()
  await page.getByRole('option', { name: '全部，含查看' }).click()
  await expect(page.getByTestId('audit-row')).toHaveCount(8)
  await page.getByRole('button', { name: /全部，含查看/ }).click()
  await page.getByRole('option', { name: '電腦' }).click()
  await expect(page.getByTestId('audit-row')).toHaveCount(1)
  await expect(page.getByTestId('audit-row')).toContainText('被拒：權限不足')
  expect(api.calls.filter((c) => c.op === 'audit.list').at(-1)?.input).toMatchObject({ op: 'xbot.computers', limit: 200 })
})

async function dropFiles(page: Page, selector: string, files: Array<{ name: string; type: string; text: string }>) {
  await page.evaluate(
    ({ selector, files }) => {
      const dt = new DataTransfer()
      for (const f of files) dt.items.add(new File([f.text], f.name, { type: f.type }))
      const el = document.querySelector(selector)!
      el.dispatchEvent(new DragEvent('dragenter', { dataTransfer: dt, bubbles: true, cancelable: true }))
      el.dispatchEvent(new DragEvent('dragover', { dataTransfer: dt, bubbles: true, cancelable: true }))
      el.dispatchEvent(new DragEvent('drop', { dataTransfer: dt, bubbles: true, cancelable: true }))
    },
    { selector, files },
  )
}

test('對話附檔（C05「8 · 附件」）：＋選檔、拖進對話、貼上截圖，送出時附上路徑', async ({ page }, info) => {
  const api = new MockServer(page)
  const mi = api.addBot({ name: '小秘', shape: 'cursor', color: 'blue', title: '幕僚長' })
  const th = api.addThread(mi.id, '十月報表')
  api.addMessage(th.id, { sender: 'owner', content: '幫我整理十月的訂單。' }, Date.now() - 10 * 60_000)
  api.addMessage(th.id, { sender: 'bot:' + mi.id, content: '好的，請把訂單檔傳給我。' }, Date.now() - 9 * 60_000)
  await api.install()
  const uploads: string[] = []
  let failNext = false
  await page.route('**/api/v1/files.upload?**', async (route) => {
    const url = new URL(route.request().url())
    const body = route.request().postData() ?? ''
    const name = /filename="([^"]+)"/.exec(body)?.[1] ?? 'file'
    uploads.push(`${url.searchParams.get('bot_id')}:${url.searchParams.get('dir')}:${name}`)
    if (failNext) {
      failNext = false
      return route.fulfill({ status: 400, json: { error: { code: 'invalid_input', message: '檔案太大', retryable: false } } })
    }
    return route.fulfill({ json: { files: [{ name, path: `/workspace/uploads/${name}`, size: body.length, dir: false, modified: Date.now(), kind: 'other', important: true }] } })
  })

  await page.goto(`/bots/${mi.id}/threads/${th.id}`)
  await page.getByTestId('attach-input').setInputFiles([
    { name: '10月訂單.xlsx', mimeType: 'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet', buffer: Buffer.from('xlsx') },
    { name: '報價單.pdf', mimeType: 'application/pdf', buffer: Buffer.from('%PDF-1.4') },
  ])
  const chips = page.getByTestId('attachments')
  await expect(chips).toContainText('10月訂單.xlsx')
  await expect(chips).toContainText('報價單.pdf')

  // 拖進對話：整個對話區都可以放。
  await dropFiles(page, 'main[aria-label="和小秘的對話"]', [{ name: '店面照片.png', type: 'image/png', text: 'png' }])
  await expect(chips).toContainText('店面照片.png')

  // 貼上截圖（剪貼簿裡的圖片沒有檔名，補成「貼上的圖片-日期時間.png」）。
  await page.evaluate(() => {
    const dt = new DataTransfer()
    dt.items.add(new File(['png'], 'image.png', { type: 'image/png' }))
    const ta = document.querySelector('textarea')!
    ta.dispatchEvent(new ClipboardEvent('paste', { clipboardData: dt, bubbles: true, cancelable: true }))
  })
  await expect(chips).toContainText('貼上的圖片-')
  await expect(page.getByRole('button', { name: '送出' })).toBeEnabled()
  expect(uploads.every((u) => u.startsWith(`${mi.id}:uploads:`))).toBe(true)
  expect(uploads).toHaveLength(4)

  await page.getByRole('textbox').fill('幫我整理成一張表')
  await settle(page)
  await page.screenshot({ path: shot(`${info.project.name}-v10-attach`) })

  failNext = true
  await page.getByTestId('attach-input').setInputFiles([{ name: '太大的檔案.zip', mimeType: 'application/zip', buffer: Buffer.from('zip') }])
  await expect(chips).toContainText('沒有傳上去：檔案太大')
  await page.getByRole('button', { name: '移除 太大的檔案.zip' }).click()

  await page.getByRole('button', { name: '送出' }).click()
  await expect.poll(() => api.lastSend?.text).toContain('幫我整理成一張表\n\n附件：/workspace/uploads/10月訂單.xlsx\n附件：/workspace/uploads/報價單.pdf\n附件：/workspace/uploads/店面照片.png\n附件：/workspace/uploads/貼上的圖片-')
  await expect(chips).toBeHidden()
})

test('Bot 設定 › 應用程式與電腦（G08）：在哪裡跑換主機＝搬家；電腦的動作也在這裡', async ({ page }, info) => {
  const api = new MockServer(page)
  const mi = api.addBot({ name: '小秘', shape: 'cursor', color: 'blue', title: '幕僚長' })
  Object.assign(api.override, {
    'hosts.list': () => ({ hosts: hostsOf([mi]) }),
    'apps.list': () => ({ connections: [] }),
    'computers.status': () => viewOf(mi),
    'computers.move_status': () => ({ running: false, jobs: [] }),
    'computers.move': () => ({ running: true, jobs: [] }),
    'skills.list': () => ({ skills: [] }),
    'engines.status': () => ({ bots: [], host: [] }),
  })
  await api.install()
  await page.goto(`/settings/bots/${mi.id}/abilities/access`)
  await expect(page.getByText('換主機就是搬家：家目錄、工作資料夾、瀏覽器登入都跟著走')).toBeVisible()
  await expect(page.getByText(/之後的版本/)).toHaveCount(0)
  await expect(page.getByTestId('computer-actions')).toBeVisible()
  await settle(page)
  await page.screenshot({ path: shot(`${info.project.name}-v10-bot-access`), fullPage: true })
  await page.getByRole('button', { name: /這台主機（這台）/ }).click()
  await page.getByRole('option', { name: 'cloud-01' }).click()
  const dlg = page.getByRole('dialog', { name: '把小秘的電腦搬到 cloud-01？' })
  await expect(dlg).toBeVisible()
  await dlg.getByRole('button', { name: '開始搬家' }).click()
  await expect.poll(() => api.calls.find((c) => c.op === 'computers.move')?.input).toEqual({ bot_id: mi.id, host_id: 'c1' })
  expect(api.calls.filter((c) => c.op === 'bots.update')).toHaveLength(0)
})
