import { expect, test } from '@playwright/test'
import { MockServer, settle } from './mock'
import { fakeVnc, loadFrame } from './rfb'

// B31 搬家與「從家裡出去」：電腦面板的「在哪裡跑」（E01）、搬家中卡片（E10）、主機清單的「搬家」（J06）。
// 假服務，截圖和畫板並排比對。
const shot = (name: string) => `e2e-results/screens/${name}.png`
const GB = 2 ** 30

test.beforeEach(async ({ page }, info) => {
  if (info.project.name === 'desktop') await page.setViewportSize({ width: 1440, height: 960 })
})

function res(cpus: number, mem: number) {
  return { cpus, mem_bytes: mem * GB, disk_free_bytes: 61 * GB, disk_total_bytes: 120 * GB, docker_version: '27.3.1', os: 'Debian GNU/Linux 13', arch: 'amd64', hostname: 'x', rtt_ms: 18, tailscale: true }
}

function seed(api: MockServer) {
  const kai = api.addBot({ name: '阿凱', shape: 'square', color: 'green' })
  const mi = api.addBot({ name: '小秘', shape: 'cursor', color: 'blue', title: '幕僚長' })
  const ref = (b: { id: string; name: string; shape: string; color: string }, running: boolean) => ({ id: b.id, name: b.name, shape: b.shape, color: b.color, state: running ? 'running' : 'sleeping', running })
  const hosts = [
    {
      id: 'local', name: '這台主機', kind: 'local', address: 'unix:///var/run/docker.sock', status: 'online', arch: 'amd64', max_running: 4, running: 2, resources: res(8, 16),
      last_seen_at: Date.now(), control_center: true, default: true, queued: 0, created_at: 1, bots: [ref(mi, true), ref(kai, true)],
    },
    {
      id: 'c1', name: 'cloud-01', kind: 'ssh', address: 'cloud-01', port: 22, user: 'xbot', status: 'online', arch: 'amd64', max_running: 3, running: 0, resources: res(4, 8),
      health: { state: 'online', since: Date.now(), connects: 1, rtt_ms: 18, reverse_forwards: 0, local_forwards: 0 },
      last_seen_at: Date.now(), control_center: false, default: false, queued: 0, created_at: 2, bots: [],
    },
  ]
  const moving = {
    id: 'm1', bot_id: kai.id, bot_name: '阿凱', from: 'local', from_name: '這台主機', to: 'c1', to_name: 'cloud-01', state: 'running', phase: 'copy', volume: 'workspace',
    percent: 47, done: 1.3 * GB, total: 2.8 * GB, files: 0, created_at: Date.now() - 60_000,
  }
  const moves = { running: true, jobs: [moving] as Record<string, unknown>[] }
  let home = false
  Object.assign(api.override, {
    'hosts.list': () => ({ hosts }),
    'computers.screen': (i: Record<string, unknown>) => ({
      bot_id: i.bot_id, name: i.bot_id === mi.id ? '小秘' : '阿凱', host_id: 'local', host_name: '這台主機', box: i.bot_id === mi.id ? 'running' : 'sleeping', host_status: 'online',
      working: i.bot_id === mi.id, paused: false, last_active_at: Date.now() - 42 * 60_000, viewers: 1, you: false, vnc: `/api/v1/computers/vnc?bot=${i.bot_id}`,
    }),
    'computers.move_status': (i: Record<string, unknown>) => (i.bot_id ? { ...moves, jobs: moves.jobs.filter((j) => j.bot_id === i.bot_id) } : moves),
    'computers.move': (i: Record<string, unknown>) => {
      moves.jobs.unshift({ ...moving, id: 'm2', bot_id: i.bot_id, bot_name: '小秘', state: 'queued', phase: undefined, percent: 0, done: 0, total: 0 })
      return moves
    },
    'computers.move_all': () => moves,
    'computers.status': () => ({ home }),
    'computers.egress_status': () => ({
      exit_node: 'home-pc', has_auth_key: true, shared_key: false, bots: home ? [mi.id] : [],
      hosts: [{ host_id: 'c1', present: true, running: true, backend: 'Running', exit_node: 'home-pc', exit_node_online: true }],
    }),
    'computers.set_egress': (i: Record<string, unknown>) => {
      home = Boolean(i.home)
      return { exit_node: 'home-pc', has_auth_key: true, shared_key: false, bots: [], hosts: [], rebuilt: true }
    },
  })
  return { kai, mi }
}

test('在哪裡跑（E01）：切換主機＝搬家，確認後開始；搬家中卡片（E10）；從家裡出去', async ({ page, context }, info) => {
  test.skip(info.project.name !== 'desktop', '面板只在寬螢幕')
  const api = new MockServer(page)
  const { kai, mi } = seed(api)
  const th = api.addThread(mi.id, '三家競品定價')
  api.addMessage(th.id, { sender: 'owner', content: '幫我比較蝦皮上三家競品的定價，週一前給我調價建議。' }, Date.now() - 5 * 60_000)
  await api.install()
  const frame = await loadFrame(context, 'desktop.jpg')
  await fakeVnc(page, () => frame)

  await page.goto(`/bots/${mi.id}/threads/${th.id}`)
  await page.getByRole('button', { name: '小秘的電腦' }).click()
  const panel = page.getByTestId('computer-panel')
  const where = panel.getByRole('radiogroup', { name: '在哪裡跑' })
  await expect(where.getByRole('radio', { name: '這台主機' })).toHaveAttribute('aria-checked', 'true')
  await where.getByRole('radio', { name: 'cloud-01（雲端）' }).click()
  const dlg = page.getByRole('dialog')
  await expect(dlg).toContainText('把小秘的電腦搬到 cloud-01？')
  await settle(page)
  await page.screenshot({ path: shot('desktop-b31-move-confirm') })
  await dlg.getByRole('button', { name: '開始搬家' }).click()
  await expect.poll(() => api.calls.find((c) => c.op === 'computers.move')?.input).toEqual({ bot_id: mi.id, host_id: 'c1' })
  await expect(panel.getByTestId('move-card')).toContainText('小秘：這台主機 → cloud-01')
  await panel.getByTestId('egress-row').scrollIntoViewIfNeeded()
  await settle(page)
  await page.screenshot({ path: shot('desktop-b31-where-queued') })

  // 另一個 Bot（阿凱）正在搬：4 步＋進度條。
  await page.goto(`/bots/${kai.id}`)
  await page.getByRole('button', { name: '阿凱的電腦' }).click()
  const card = page.getByTestId('computer-panel').getByTestId('move-card')
  await expect(card).toContainText('阿凱：這台主機 → cloud-01')
  await expect(card).toContainText('傳 workspace · 1.3 / 2.8 GB')
  await card.scrollIntoViewIfNeeded()
  await settle(page)
  await page.screenshot({ path: shot('desktop-b31-moving') })

  // 從家裡出去。
  await page.getByTestId('egress-row').getByRole('switch', { name: '從家裡出去' }).click()
  const eg = page.getByRole('dialog')
  await expect(eg).toContainText('autogroup:internet')
  await settle(page)
  await page.screenshot({ path: shot('desktop-b31-egress') })
  await eg.getByRole('button', { name: '開啟' }).click()
  await expect(page.getByTestId('egress-row').getByRole('switch')).toHaveAttribute('aria-checked', 'true')
})

test('主機清單「搬家」（J06、16.1a）：整台搬到另一台，搬家進度', async ({ page }, info) => {
  const api = new MockServer(page)
  seed(api)
  await api.install()
  await page.goto('/settings/computers')
  const rows = page.getByTestId('host-row')
  await expect(rows).toHaveCount(2)
  await expect(page.getByTestId('moves')).toContainText('阿凱：這台主機 → cloud-01')
  await settle(page)
  await page.screenshot({ path: shot(`${info.project.name}-b31-computers`), fullPage: true })
  await rows.nth(0).getByRole('button', { name: '搬家' }).click()
  const dlg = page.getByRole('dialog')
  await expect(dlg).toContainText('把 這台主機 上的 Bot 全部搬走')
  await settle(page)
  await page.screenshot({ path: shot(`${info.project.name}-b31-move-all`) })
  await dlg.getByRole('button', { name: '全部搬到 cloud-01' }).click()
  await expect.poll(() => api.calls.find((c) => c.op === 'computers.move_all')?.input).toEqual({ from_host_id: 'local', to_host_id: 'c1' })
})
