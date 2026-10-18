import { expect, test, type Page } from '@playwright/test'
import { MockServer, settle } from './mock'

// B12 電腦與裝置：主機清單（J06）、新增主機 3 步（J11）、主機詳情與移除。用假服務，截圖和畫板並排比對。
const shot = (name: string) => `e2e-results/screens/${name}.png`
const GB = 2 ** 30

test.beforeEach(async ({ page }, info) => {
  if (info.project.name === 'desktop') await page.setViewportSize({ width: 1440, height: 960 })
})

type Host = Record<string, unknown> & { id: string; name: string; status: string; bots: unknown[] }

function res(cpus: number, mem: number, ts = true) {
  return { cpus, mem_bytes: mem * GB, disk_free_bytes: 61 * GB, disk_total_bytes: 120 * GB, docker_version: '27.3.1', os: 'Debian GNU/Linux 13', arch: 'amd64', hostname: 'x', rtt_ms: 18, tailscale: ts }
}

function seed(api: MockServer) {
  const bot = (name: string, shape: string, color: string, running: boolean) => {
    const b = api.addBot({ name, shape, color })
    return { id: b.id, name, shape, color, state: running ? 'running' : 'sleeping', running }
  }
  const hosts: Host[] = [
    {
      id: 'local', name: '這台電腦', kind: 'local', address: 'unix:///var/run/docker.sock', status: 'online', arch: 'amd64', max_running: 4, running: 3, resources: res(8, 16, false),
      last_seen_at: Date.now(), control_center: true, default: true, queued: 0, created_at: 1,
      bots: [bot('小秘', 'cursor', 'blue', true), bot('阿哲', 'blob', 'orange', true), bot('抓蟲', 'hexagon', 'green', true)],
    },
    {
      id: 'c1', name: 'cloud-01', kind: 'ssh', address: 'cloud-01', port: 22, user: 'xbot', status: 'online', arch: 'amd64', max_running: 3, running: 2, resources: res(4, 8),
      health: { state: 'online', since: Date.now(), connects: 1, rtt_ms: 18, reverse_forwards: 2, local_forwards: 4 },
      last_seen_at: Date.now(), control_center: false, default: false, queued: 0, created_at: 2,
      bots: [bot('小編', 'circle', 'pink', true), bot('數據喵', 'star', 'purple', true)],
    },
    {
      id: 'o1', name: 'office-pc', kind: 'ssh', address: '100.101.7.40', port: 22, user: 'xbot', status: 'offline', status_error: 'dial tcp: i/o timeout', arch: 'amd64', max_running: 2, running: 0,
      resources: res(6, 32), last_seen_at: Date.now() - 20 * 3600_000, control_center: false, default: false, queued: 1, created_at: 3, bots: [],
    },
  ]
  const items = (image: string) => [
    { id: 'ssh', label: 'SSH 連線', status: 'ok', detail: 'ssh://xbot@cloud-02:22 · 往返 18 ms' },
    { id: 'docker', label: 'Docker', status: 'ok', detail: '27.3.1 · 可以使用' },
    { id: 'resources', label: 'CPU／記憶體／磁碟', status: 'ok', detail: '4 核 · 8 GB · 可用 61 GB' },
    { id: 'arch', label: '架構', status: 'ok', detail: 'amd64（arm64 也支援）' },
    { id: 'forwarding', label: 'SSH 轉發', status: 'ok', detail: '埠轉發、socket 轉發、重連換 socket 都可以' },
    { id: 'image', label: '電腦映像', status: image, detail: 'xbot-box:dev · 要從 XBot 傳過去（約 2.8 GB）' },
    { id: 'network', label: '連線方式', status: 'ok', detail: '經 Tailscale；Bot 的畫面埠只綁 127.0.0.1' },
  ]
  Object.assign(api.override, {
    'hosts.list': () => ({ hosts }),
    'hosts.get': (i: Record<string, unknown>) => hosts.find((h) => h.id === i.id),
    'hosts.install_script': (i: Record<string, unknown>) => ({
      command: `echo 'H4sIAAAAAAAC/8Q571cTSbaf6b/i2mYMYbbzA8V9ixvPIuDKjqs+Yd7ZER1Om66QXpLuTHcHxcFzAgokEMQZgScBBpXByTqKjDOrUYIf3p8i6U7yKe9PeKeqOp1uiCO+L56jh3TVvbdu3d/31uFDvqui5FMjzGH4xylZqxayxvR0Jbmkz23BWVFKXIdiftv415rxZlEfXzJSd+GUrEFlebs88ZjuVAupU4HWaiFz4WIX/M9PgePQeZZrrRbSu8nx0nwOFFnWQF97VX6YqRYy7Q2PQ' | base64 -d | gunzip | sudo sh -s --${i.no_tailscale ? ' --no-tailscale' : ''}`,
      script: '#!/bin/sh',
      public_key: 'ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI xbot@home',
      fingerprint: 'SHA256:IkHG+H+OqxAErgJ+5IxcojDtXDguWU2RQh1CIlpwbAo',
      user: 'xbot',
    }),
    'hosts.add': (i: Record<string, unknown>) => {
      const h: Host = {
        id: 'n1', name: 'cloud-02', kind: 'ssh', address: i.address, port: 22, user: 'xbot', status: 'updating', arch: 'amd64', max_running: 3, running: 0, resources: res(4, 8),
        last_seen_at: Date.now(), control_center: false, default: false, queued: 0, created_at: 4, bots: [], image: { host_id: 'n1', done: 1.2 * GB, total: 1.9 * GB, percent: 63 },
      }
      hosts.push(h)
      return { host: h, report: { items: items('running'), resources: res(4, 8), ok: true, need_image: true }, added: true, loading: true }
    },
    'hosts.set_limits': (i: Record<string, unknown>) => {
      const h = hosts.find((x) => x.id === i.id)!
      Object.assign(h, i.name ? { name: i.name } : {}, i.max_running !== undefined ? { max_running: i.max_running } : {})
      return h
    },
    'hosts.check': (i: Record<string, unknown>) => {
      const h = hosts.find((x) => x.id === i.id)!
      return { host: h, report: { items: items('ok'), resources: res(4, 8), ok: true, need_image: false }, added: true, loading: false }
    },
    'hosts.remove': (i: Record<string, unknown>) => {
      const h = hosts.find((x) => x.id === i.id)!
      hosts.splice(hosts.indexOf(h), 1)
      return { id: h.id, name: h.name, removed: true }
    },
    'hosts.set_default': (i: Record<string, unknown>) => {
      for (const h of hosts) h.default = h.id === (i.id || 'local')
      return { id: i.id }
    },
  })
  return { hosts }
}

async function open(page: Page, path: string) {
  const api = new MockServer(page)
  const s = seed(api)
  await api.install()
  await page.goto(path)
  return { api, ...s }
}

test('主機清單（J06）：狀態、資源、開機台數、離線重新連線、新 Bot 放在', async ({ page }, info) => {
  const { api } = await open(page, '/settings/computers')
  const rows = page.getByTestId('host-row')
  await expect(rows).toHaveCount(3)
  await expect(rows.nth(0)).toContainText('控制中心')
  await expect(rows.nth(0)).toContainText('開機 3/4')
  await expect(rows.nth(1)).toContainText('經 Tailscale 用 SSH 控制')
  await expect(rows.nth(2)).toContainText('離線')
  if (info.project.name === 'desktop') await expect(rows.nth(0).getByRole('button', { name: '移除' })).toBeDisabled()
  else await expect(rows.nth(0).getByRole('button', { name: '移除' })).toBeHidden() // 手機版不顯示停用的按鈕
  await settle(page)
  await page.screenshot({ path: shot(`${info.project.name}-b12-computers`), fullPage: true })

  await page.getByRole('button', { name: /各 Bot 放在哪台主機/ }).click()
  await expect(page.getByRole('main').getByText('數據喵')).toBeVisible()
  await rows.nth(2).getByRole('button', { name: '重新連線' }).click()
  await expect.poll(() => api.calls.find((c) => c.op === 'hosts.check')?.input).toEqual({ id: 'o1' })
  await page.getByRole('radio', { name: 'cloud-01' }).click()
  await expect.poll(() => api.calls.find((c) => c.op === 'hosts.set_default')?.input).toEqual({ id: 'c1' })
})

test('新增主機（J11）：貼一行指令 → 自動檢查（映像進度）→ 取名 → 完成', async ({ page }, info) => {
  const { api } = await open(page, '/settings/computers')
  await page.getByRole('button', { name: '新增主機' }).click()
  await expect(page).toHaveURL(/\/settings\/computers\/add$/)
  await expect(page.getByTestId('install-command')).toContainText('base64 -d | gunzip | sudo sh -s --')
  await settle(page)
  await page.screenshot({ path: shot(`${info.project.name}-b12-addhost-1`), fullPage: true })
  await page.context().grantPermissions(['clipboard-read', 'clipboard-write']).catch(() => {})
  await page.getByRole('button', { name: '複製' }).click()
  await expect(page.getByRole('button', { name: '已複製' })).toBeVisible()

  await page.getByLabel('這台主機的位址').fill('cloud-02')
  await page.getByRole('button', { name: '我貼好了，開始檢查' }).click()
  await expect(page.locator('[data-check="ssh"]')).toContainText('往返 18 ms')
  await expect(page.locator('[data-check="image"]')).toContainText('63%')
  await expect.poll(() => api.calls.find((c) => c.op === 'hosts.add')?.input).toEqual({ address: 'cloud-02' })
  await settle(page)
  await page.screenshot({ path: shot(`${info.project.name}-b12-addhost-2`), fullPage: true })

  await page.getByRole('button', { name: '下一步：取名' }).click()
  await expect(page.getByRole('textbox', { name: '主機名稱' })).toHaveValue('cloud-02')
  await page.getByRole('button', { name: '多一台' }).click()
  await settle(page)
  await page.screenshot({ path: shot(`${info.project.name}-b12-addhost-3`), fullPage: true })
  await page.getByRole('button', { name: '完成', exact: true }).click()
  await expect(page.getByRole('heading', { name: 'cloud-02 已加入' })).toBeVisible()
  await expect.poll(() => api.calls.find((c) => c.op === 'hosts.set_limits')?.input).toEqual({ id: 'n1', name: 'cloud-02', max_running: 4 })
  await settle(page)
  await page.screenshot({ path: shot(`${info.project.name}-b12-addhost-done`), fullPage: true })
})

test('主機詳情：狀態、改上限、移除要確認', async ({ page }, info) => {
  const { api } = await open(page, '/settings/computers/hosts/c1')
  await expect(page.getByRole('heading', { name: 'cloud-01', level: 2 })).toBeAttached()
  await expect(page.getByText('ssh://xbot@cloud-01')).toBeVisible()
  await expect(page.getByRole('main').getByText('小編')).toBeVisible()
  await page.getByRole('button', { name: '重新檢查' }).click()
  await expect(page.locator('[data-check="forwarding"]')).toBeVisible()
  await settle(page)
  await page.screenshot({ path: shot(`${info.project.name}-b12-host-detail`), fullPage: true })
  await page.getByRole('button', { name: '移除這台主機' }).click()
  const dlg = page.getByRole('alertdialog', { name: '移除 cloud-01？' })
  await expect(dlg).toContainText('小編')
  await settle(page)
  await page.screenshot({ path: shot(`${info.project.name}-b12-remove-confirm`) })
  await dlg.getByRole('button', { name: '移除' }).click()
  await expect(page).toHaveURL(/\/settings\/computers$/)
  expect(api.calls.filter((c) => c.op === 'hosts.remove')).toHaveLength(1)
})
