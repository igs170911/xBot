import { expect, test } from '@playwright/test'
import { MockServer, settle } from './mock'

// C35 內建 Tailscale 與外部入口：真的服務（預設關閉）＋假服務（已連上、Funnel 開著），截圖和 J05、P02、J12 畫板比對。
const shot = (name: string) => `e2e-results/screens/${name}.png`

test.beforeEach(async ({ page }, info) => {
  if (info.project.name === 'desktop') await page.setViewportSize({ width: 1440, height: 1000 })
})

test('真的服務：網路頁預設關閉；外部入口不能開；本機網址', async ({ page }, info) => {
  await page.goto('/settings/advanced/network')
  await expect(page.getByTestId('ts-state')).toHaveText('關閉')
  await expect(page.getByTestId('funnel-state')).toHaveText('關閉')
  await expect(page.getByRole('switch', { name: '開放外部入口' })).toBeDisabled()
  await expect(page.getByTestId('external-paths')).toContainText('/api/v1/agents.pair')
  await settle(page)
  await page.screenshot({ path: shot(`${info.project.name}-c35-network-off`), fullPage: true })
  // 外部入口在「外部代理與 MCP」也是關閉。
  await page.goto('/settings/advanced/agents')
  await expect(page.getByTestId('external-entry')).toContainText('關閉')
})

const QR = (() => {
  // 假的 25×25 點陣（截圖用；真的 QR 由服務端產生）
  const rows: string[] = []
  for (let y = 0; y < 25; y++) {
    let r = ''
    for (let x = 0; x < 25; x++) {
      const finder = (a: number, b: number) => x >= a && x < a + 7 && y >= b && y < b + 7 && (x === a || x === a + 6 || y === b || y === b + 6 || (x >= a + 2 && x <= a + 4 && y >= b + 2 && y <= b + 4))
      const inFinder = (x < 8 && y < 8) || (x > 16 && y < 8) || (x < 8 && y > 16)
      r += finder(0, 0) || finder(18, 0) || finder(0, 18) ? '1' : !inFinder && (x * 7 + y * 13 + x * y) % 3 === 0 ? '1' : '0'
    }
    rows.push(r)
  }
  return rows
})()

test('已連上 tailnet、外部入口開著（假服務）：QR、網址、確認開啟、網址觸發用對外網址', async ({ page }, info) => {
  const api = new MockServer(page)
  let st = {
    local_url: 'http://localhost:8780',
    tailscale: {
      enabled: true, state: 'running', hostname: 'xbot', url: 'https://xbot.tail3a9e.ts.net', tailnet: 'me@example.com', user: 'me@example.com',
      ips: ['100.101.102.103'], magic_dns: true, https: true, cert: 'ready', devices: 3, online: 2, has_auth_key: true, qr: QR,
    },
    funnel: { enabled: false, state: 'off', allowed: true, allow_ips: [] as string[], url: undefined as string | undefined },
    proxy: { enabled: false, trusted_proxies: ['127.0.0.1/32', '::1/128'] },
    public_url: '',
    base_url: 'https://xbot.tail3a9e.ts.net',
    external_paths: ['/mcp', '/api/v1/agents.pair', '/hooks/*'],
    external_rate_per_min: 20,
  }
  api.override['network.status'] = () => st
  api.override['network.funnel_set'] = (i) => {
    const on = !!i.enabled
    st = { ...st, funnel: { enabled: on, state: on ? 'on' : 'off', allowed: true, allow_ips: (i.allow_ips as string[]) ?? [], url: on ? st.tailscale.url : undefined } }
    st.public_url = on ? st.tailscale.url : ''
    return st
  }
  api.override['hosts.list'] = () => ({ hosts: [] })
  await api.install()

  await page.goto('/settings/advanced/network')
  await expect(page.getByTestId('tailnet-url')).toHaveText('https://xbot.tail3a9e.ts.net')
  await expect(page.getByTestId('ts-state')).toHaveText('已連上 tailnet')
  await settle(page)
  await page.screenshot({ path: shot(`${info.project.name}-c35-network-tailnet`), fullPage: true })

  await page.getByRole('switch', { name: '開放外部入口' }).click()
  const dlg = page.getByRole('alertdialog')
  await expect(dlg).toContainText('網際網路上的任何人')
  await settle(page)
  await page.screenshot({ path: shot(`${info.project.name}-c35-funnel-confirm`) })
  await dlg.getByRole('button', { name: '開啟' }).click()
  await expect(page.getByTestId('funnel-state')).toHaveText('開放中')
  await page.getByLabel('IP 白名單').fill('203.0.113.7')
  await page.getByRole('button', { name: '儲存白名單' }).click()
  await expect.poll(() => api.calls.filter((c) => c.op === 'network.funnel_set').at(-1)?.input).toEqual({ enabled: true, allow_ips: ['203.0.113.7'] })
  await settle(page)
  await page.screenshot({ path: shot(`${info.project.name}-c35-network-funnel`), fullPage: true })

  // J12：外部入口開放中、有網址；MCP 的 HTTP 網址是 tailnet 網址。
  api.override['agents.list'] = () => ({ agents: [] })
  api.override['audit.list'] = () => ({ entries: [] })
  await page.goto('/settings/advanced/agents')
  await expect(page.getByTestId('external-entry')).toContainText('開放中')
  await expect(page.getByTestId('external-entry')).toContainText('https://xbot.tail3a9e.ts.net/mcp')
  await settle(page)
  await page.screenshot({ path: shot(`${info.project.name}-c35-j12-external`), fullPage: true })

  // J05：電腦與裝置上的入口
  await page.goto('/settings/computers')
  await expect(page.getByText('用 Tailscale 連手機')).toBeVisible()
  await settle(page)
  await page.screenshot({ path: shot(`${info.project.name}-c35-j05-entry`), fullPage: true })
})
