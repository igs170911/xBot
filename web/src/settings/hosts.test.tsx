import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { fakeApi, type FakeApi } from '../test-api'
import { ToastProvider } from '../ui/Toast'
import { dispatchEvent } from '../data/events'
import { waitText } from '../chat/assemble'
import { ComputersSection } from './ComputersSection'
import { AddHostPage, HostDetailPage } from './HostPages'
import { activeNav } from './nav'
import { ago, hostsStore } from './hostsData'

const res = { cpus: 4, mem_bytes: 8 * 2 ** 30, disk_free_bytes: 61 * 2 ** 30, disk_total_bytes: 100 * 2 ** 30, docker_version: '27.3.1', os: 'Debian', arch: 'amd64', hostname: 'cloud-01', rtt_ms: 18, tailscale: true }

function host(over: Record<string, unknown>) {
  return { id: 'h', name: 'x', kind: 'ssh', address: 'x', status: 'online', arch: 'amd64', max_running: 3, running: 0, resources: res, last_seen_at: 0, control_center: false, default: false, bots: [], queued: 0, created_at: 1, ...over }
}

let api: FakeApi
let hosts: ReturnType<typeof host>[]
beforeEach(() => {
  hostsStore.set({ hosts: null, progress: {} })
  hosts = [
    host({ id: 'local', name: '這台電腦', kind: 'local', control_center: true, default: true, max_running: 4, running: 3, bots: [{ id: 'b1', name: '小秘', color: 'blue', shape: 'cursor', state: 'running', running: true }] }),
    host({ id: 'c1', name: 'cloud-01', address: 'cloud-01', running: 2 }),
    host({ id: 'o1', name: 'office-pc', address: 'office-pc', status: 'offline', last_seen_at: Date.now() - 3 * 60_000 }),
  ]
  api = fakeApi({
    'hosts.list': () => ({ hosts }),
    'hosts.check': (i) => ({ host: hosts.find((h) => h.id === i.id), report: { items: [], resources: res, ok: true, need_image: false }, added: true, loading: false }),
    'hosts.remove': (i) => {
      hosts = hosts.filter((h) => h.id !== i.id)
      return { id: i.id, name: 'cloud-01', removed: true }
    },
    'hosts.set_default': (i) => ({ id: i.id }),
    'hosts.install_script': (i) => ({ command: `echo 'H4sIAAAAAAAC/8Q571cTSbaf6b' | base64 -d | gunzip | sudo sh -s --${i.no_tailscale ? ' --no-tailscale' : ''}`, script: '#!/bin/sh', public_key: 'ssh-ed25519 AAAA xbot@home', fingerprint: 'SHA256:abcdefghijklmnopqrstuvwxyz', user: 'xbot' }),
  })
})
afterEach(() => vi.unstubAllGlobals())

const wrap = (el: React.ReactNode) => render(<ToastProvider>{el}</ToastProvider>)

it('主機清單（J06）：狀態、開機台數、控制中心不能移除、離線可以重新連線', async () => {
  wrap(<ComputersSection />)
  const rows = await screen.findAllByTestId('host-row')
  expect(rows).toHaveLength(3)
  expect(within(rows[0]).getByText('控制中心')).toBeTruthy()
  expect(within(rows[0]).getByText('開機 3/4')).toBeTruthy()
  expect((within(rows[0]).getByRole('button', { name: '移除' }) as HTMLButtonElement).disabled).toBe(true)
  expect(within(rows[1]).getByText('在線')).toBeTruthy()
  expect(within(rows[2]).getByText('離線')).toBeTruthy()
  expect(within(rows[2]).getByText(/最後在線 3 分鐘前/)).toBeTruthy()
  fireEvent.click(within(rows[2]).getByRole('button', { name: '重新連線' }))
  await waitFor(() => expect(api.calls.some((c) => c.path === 'hosts.check' && c.input.id === 'o1')).toBe(true))
  expect(screen.getByText('控制中心在：這台電腦')).toBeTruthy()

  // 移除要先確認（confirm 級）
  fireEvent.click(within(rows[1]).getByRole('button', { name: '移除' }))
  const dlg = await screen.findByRole('alertdialog')
  expect(within(dlg).getByText('移除 cloud-01？')).toBeTruthy()
  fireEvent.click(within(dlg).getByRole('button', { name: '移除' }))
  await waitFor(() => expect(api.calls.some((c) => c.path === 'hosts.remove' && c.input.id === 'c1')).toBe(true))
  await waitFor(() => expect(screen.getAllByTestId('host-row')).toHaveLength(2))

  // 主機狀態事件會重讀清單
  hosts = hosts.map((h) => (h.id === 'o1' ? { ...h, status: 'online' } : h))
  dispatchEvent({ id: 9, kind: 'host.status', at: 1, payload: { host_id: 'o1', status: 'online' } })
  await waitFor(() => expect(within(screen.getAllByTestId('host-row')[1]).getByText('在線')).toBeTruthy())

  // 新 Bot 放在
  fireEvent.click(screen.getByRole('radio', { name: 'office-pc' }))
  await waitFor(() => expect(api.calls.some((c) => c.path === 'hosts.set_default' && c.input.id === 'o1')).toBe(true))
})

it('新增主機（J11）：複製指令 → 檢查（含映像進度）→ 取名 → 完成', async () => {
  let added = false
  api.ops['hosts.add'] = (i) => {
    added = true
    hosts = [...hosts, host({ id: 'n1', name: 'cloud-02', address: i.address as string, status: 'updating' })]
    return {
      host: hosts[hosts.length - 1],
      report: {
        ok: true,
        need_image: true,
        resources: res,
        items: [
          { id: 'ssh', label: 'SSH 連線', status: 'ok', detail: 'ssh://xbot@cloud-02 · 往返 18 ms' },
          { id: 'docker', label: 'Docker', status: 'ok', detail: '27.3.1 · 可以使用' },
          { id: 'image', label: '電腦映像', status: 'running', detail: 'xbot-box:dev' },
          { id: 'network', label: '連線方式', status: 'ok', detail: '經 Tailscale' },
        ],
      },
      added: true,
      loading: true,
    }
  }
  api.ops['hosts.set_limits'] = (i) => ({ ...hosts[hosts.length - 1], name: i.name, max_running: i.max_running })
  wrap(<AddHostPage />)
  expect(await screen.findByTestId('install-command')).toBeTruthy()
  await waitFor(() => expect(screen.getByTestId('install-command').textContent).toContain('sudo sh -s --'))
  fireEvent.click(screen.getByRole('switch', { name: '也安裝 Tailscale' }))
  await waitFor(() => expect(screen.getByTestId('install-command').textContent).toContain('--no-tailscale'))

  fireEvent.click(screen.getByRole('button', { name: '我貼好了，開始檢查' }))
  expect(await screen.findByText('先填主機的位址')).toBeTruthy()
  fireEvent.change(screen.getByLabelText('這台主機的位址'), { target: { value: 'cloud-02' } })
  fireEvent.click(screen.getByRole('button', { name: '我貼好了，開始檢查' }))
  await waitFor(() => expect(added).toBe(true))
  expect(await screen.findByText('ssh://xbot@cloud-02 · 往返 18 ms')).toBeTruthy()
  dispatchEvent({ kind: 'host.image', at: 2, payload: { host_id: 'n1', done: 2 ** 30, total: 2 * 2 ** 30, percent: 50 } })
  expect(await screen.findByText('50%')).toBeTruthy()

  fireEvent.click(screen.getByRole('button', { name: '下一步：取名' }))
  fireEvent.change(screen.getByLabelText('主機名稱'), { target: { value: '辦公室' } })
  fireEvent.click(screen.getByRole('button', { name: '多一台' }))
  fireEvent.click(screen.getByRole('button', { name: '完成' }))
  expect(await screen.findByText('辦公室 已加入')).toBeTruthy()
  const call = api.calls.find((c) => c.path === 'hosts.set_limits')
  expect(call?.input).toEqual({ id: 'n1', name: '辦公室', max_running: 4 })
})

it('主機詳情：改上限、移除要確認', async () => {
  api.ops['hosts.set_limits'] = (i) => ({ ...hosts[1], max_running: i.max_running })
  wrap(<HostDetailPage hostId="c1" />)
  expect(await screen.findByRole('heading', { name: 'cloud-01' })).toBeTruthy()
  fireEvent.click(screen.getByRole('button', { name: '少一台' }))
  await waitFor(() => expect(api.calls.find((c) => c.path === 'hosts.set_limits')?.input).toEqual({ id: 'c1', max_running: 2 }))
  fireEvent.click(screen.getByRole('button', { name: '移除這台主機' }))
  expect(await screen.findByRole('alertdialog')).toBeTruthy()
})

it('排隊原因與時間的寫法', () => {
  expect(waitText({ reason: 'host_offline', host_name: 'cloud-01' })).toBe('主機「cloud-01」離線，工作先排隊')
  expect(waitText({ reason: 'host_full', position: 2 })).toBe('等電腦空出來（排第 2 位）')
  expect(waitText(undefined)).toBeUndefined()
  expect(ago(0)).toBe('從來沒有')
  expect(ago(Date.now() - 5 * 60_000)).toBe('5 分鐘前')
  expect(activeNav('/settings/computers/add')).toBe('/settings/computers')
  expect(activeNav('/settings/computers/hosts/x')).toBe('/settings/computers')
})
