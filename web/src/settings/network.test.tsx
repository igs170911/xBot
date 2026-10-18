import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import type { NetworkStatusOutput } from '../api/generated'
import { networkStore } from '../data/network'
import { hookURL } from '../routines/data'
import { fakeApi, type FakeApi } from '../test-api'
import { ToastProvider } from '../ui/Toast'
import { NetworkPage } from './NetworkPage'
import { activeNav } from './nav'

function status(over: Partial<NetworkStatusOutput> = {}): NetworkStatusOutput {
  return {
    local_url: 'http://localhost:8780',
    tailscale: { enabled: false, state: 'off', hostname: 'xbot', ips: [], magic_dns: false, https: false, cert: 'none', devices: 0, online: 0, has_auth_key: false, qr: [] },
    funnel: { enabled: false, state: 'off', allowed: false, allow_ips: [] },
    proxy: { enabled: false, trusted_proxies: ['127.0.0.1/32'] },
    public_url: '',
    base_url: '',
    external_paths: ['/mcp', '/api/v1/agents.pair', '/hooks/*'],
    external_rate_per_min: 20,
    ...over,
  }
}

const running = (): NetworkStatusOutput['tailscale'] => ({
  enabled: true,
  state: 'running',
  hostname: 'xbot',
  url: 'https://xbot.tail3a9e.ts.net',
  user: 'me@example.com',
  ips: ['100.64.0.5'],
  magic_dns: true,
  https: true,
  cert: 'ready',
  devices: 3,
  online: 2,
  has_auth_key: false,
  qr: ['1110111', '1010101', '1110111', '0000000', '1110111', '1010101', '1110111'],
})

let api: FakeApi
let st: NetworkStatusOutput
beforeEach(() => {
  networkStore.set({ status: null, error: null })
  st = status()
  api = fakeApi({
    'network.status': () => st,
    'network.tailscale_set': (i) => {
      st = status({ tailscale: { ...running(), enabled: !!i.enabled, state: i.enabled ? 'needs_login' : 'off', login_url: 'https://login.tailscale.com/a/x', url: undefined, qr: [] } })
      return st
    },
    'network.funnel_set': (i) => {
      st = { ...st, funnel: { enabled: !!i.enabled, state: i.enabled ? 'on' : 'off', allowed: true, allow_ips: (i.allow_ips as string[]) ?? [], url: i.enabled ? st.tailscale.url : undefined } }
      st.public_url = st.funnel.url ?? ''
      return st
    },
  })
})
afterEach(() => vi.unstubAllGlobals())

const wrap = () => render(<ToastProvider><NetworkPage /></ToastProvider>)

it('導覽：進階底下有「網路與 Tailscale」', () => {
  expect(activeNav('/settings/advanced/network')).toBe('/settings/advanced/network')
})

it('關閉時：開啟內建 Tailscale 後顯示登入網址；沒開 Tailscale 不能開外部入口', async () => {
  wrap()
  await screen.findByText('還沒開啟。下面打開「內建 Tailscale」，這台 XBot 就會出現在你的 tailnet 上。')
  expect((screen.getByRole('switch', { name: '開放外部入口' }) as HTMLButtonElement).disabled).toBe(true)
  fireEvent.click(screen.getByRole('switch', { name: '內建 Tailscale' }))
  await waitFor(() => expect(api.calls.some((c) => c.path === 'network.tailscale_set' && c.input.enabled === true)).toBe(true))
  const login = await screen.findByTestId('ts-login')
  expect(login.getAttribute('href')).toBe('https://login.tailscale.com/a/x')
  expect(screen.getByTestId('ts-state').textContent).toBe('等你登入')
})

it('連上時：tailnet 網址、QR；外部入口要確認才開，白名單、開放路徑', async () => {
  st = status({ tailscale: running(), funnel: { enabled: false, state: 'off', allowed: true, allow_ips: [] } })
  wrap()
  expect((await screen.findByTestId('tailnet-url')).textContent).toBe('https://xbot.tail3a9e.ts.net')
  expect(screen.getByRole('img', { name: /tailnet 網址/ })).toBeTruthy()
  expect(screen.getByTestId('external-paths').textContent).toContain('/hooks/*')
  fireEvent.click(screen.getByRole('switch', { name: '開放外部入口' }))
  const dlg = await screen.findByRole('alertdialog')
  expect(dlg.textContent).toContain('網際網路上的任何人')
  expect(api.calls.some((c) => c.path === 'network.funnel_set')).toBe(false)
  fireEvent.click(within(dlg).getByRole('button', { name: '開啟' }))
  await waitFor(() => expect(screen.getByTestId('funnel-state').textContent).toBe('開放中'))
  // 網址觸發用對外網址（C32）。
  expect(hookURL('/hooks/h1')).toBe('https://xbot.tail3a9e.ts.net/hooks/h1')
  // 白名單
  fireEvent.change(screen.getByLabelText('IP 白名單'), { target: { value: '203.0.113.7\n198.51.100.0/24' } })
  fireEvent.click(screen.getByRole('button', { name: '儲存白名單' }))
  await waitFor(() => expect(api.calls.at(-1)?.input).toEqual({ enabled: true, allow_ips: ['203.0.113.7', '198.51.100.0/24'] }))
  // 關閉不用確認。
  fireEvent.click(screen.getByRole('switch', { name: '開放外部入口' }))
  await waitFor(() => expect(screen.getByTestId('funnel-state').textContent).toBe('關閉'))
})

it('沒有對外網址時，網址觸發用目前這個網站', () => {
  networkStore.set({ status: status(), error: null })
  expect(hookURL('/hooks/h1')).toBe(window.location.origin + '/hooks/h1')
})
