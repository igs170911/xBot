import { expect, test, type Page } from '@playwright/test'
import { MockServer, settle } from './mock'

// C34：連接應用程式（I01）、應用程式詳細（I02）、登入（I03）、自訂連接（I04）、網站登入（I05）與分享登入（E09）。
// 用假服務測流程並截圖，和畫板並排比對。
const shot = (name: string) => `e2e-results/screens/${name}.png`

test.beforeEach(async ({ page }, info) => {
  if (info.project.name === 'desktop') await page.setViewportSize({ width: 1440, height: 960 })
})

type Conn = Record<string, unknown> & { id: string; app_id: string; status: string }

const app = (id: string, title: string, description: string, icon: string, auth = 'oauth2', featured = true) => ({
  id, title, description, category: '工作', site: `${id}.com`, icon, transport: 'http', url: `https://mcp.${id}.com/mcp`, auth, featured,
})

function conn(id: string, appId: string, title: string, status: string, extra: Record<string, unknown> = {}): Conn {
  return {
    id, app_id: appId, name: appId, title, description: '', icon: 'plug', custom: false, transport: 'http', url: '', command: '', args: [],
    auth: 'oauth2', account: '', status, error: '', secret_hint: '', header_names: [], env_names: [], missing_env: [], tools: [],
    disabled_tools: [], all_bots: true, bot_ids: [], enabled: true, checked_at: Date.now() - 300_000, last_used_at: 0, created_at: 1, ...extra,
  }
}

function seed(api: MockServer) {
  const xm = api.addBot({ name: '小秘', title: '幕僚長', shape: 'cursor', color: 'blue' })
  const az = api.addBot({ name: '阿哲', title: '選品研究員', shape: 'blob', color: 'orange' })
  const ak = api.addBot({ name: '阿凱', title: '業務開發', shape: 'star', color: 'green' })
  api.addBot({ name: '帳房', title: '費用管理', shape: 'square', color: 'purple' })
  const catalog = [
    app('notion', 'Notion', 'Notion 工作區的頁面與資料庫', 'notebook'),
    app('linear', 'Linear', '查詢、建立與更新議題和專案', 'list-todo'),
    app('github', 'GitHub', '程式碼、議題與 Pull Request', 'git-branch', 'api_key'),
    app('atlassian', 'Jira 與 Confluence', 'Atlassian 的 Jira 議題與 Confluence 頁面', 'list-checks'),
    app('figma', 'Figma', '讀取設計稿、元件與 Code Connect', 'pen-tool'),
    app('canva', 'Canva', '建立、搜尋與管理 Canva 設計', 'palette'),
    app('dropbox', 'Dropbox', '搜尋、讀取與管理 Dropbox 檔案', 'folder'),
    app('todoist', 'Todoist', '管理 Todoist 任務與專案', 'check-square'),
    app('context7', 'Context7', '查詢套件最新、對應版本的技術文件', 'book-open', 'none'),
    app('slack', 'Slack', '讀頻道、找訊息、發訊息（發送前先問你）', 'hash'),
    app('airtable', 'Airtable', '讀寫 Airtable 的資料表與紀錄', 'table'),
    app('calendly', 'Calendly', '預約連結、活動與受邀者', 'calendar-days'),
    app('stripe', 'Stripe', '付款、客戶與發票', 'credit-card'),
    app('sentry', 'Sentry', '錯誤議題、堆疊追蹤與上下文', 'bug'),
  ]
  const conns: Conn[] = [
    conn('c-notion', 'notion', 'Notion', 'ok', { account: '陳家選物', last_used_at: Date.now() - 3_600_000,
      tools: [{ name: 'search', description: '搜尋頁面' }, { name: 'create-page', description: '建立頁面' }, { name: 'delete-page', description: '刪除頁面' }] }),
    conn('c-notion2', 'notion', 'Notion', 'expired', { account: '個人工作區', error: '' }),
    conn('c-linear', 'linear', 'Linear', 'needs_signin', { all_bots: false, bot_ids: [xm.id, az.id] }),
    conn('c-context7', 'context7', 'Context7', 'ok', { auth: 'none' }),
    conn('c-shop', 'custom', 'shop-inventory', 'ok', { name: 'shop-inventory', custom: true, auth: 'api_key', url: 'https://mcp.chenhome.tw/inventory',
      secret_hint: '7f3a', header_names: ['X-Region', 'X-Shop-Id'], icon: 'plug',
      tools: ['list_products', 'get_stock', 'update_stock', 'list_orders', 'get_order', 'search_sku'].map((name) => ({ name, description: '' })) }),
    conn('c-crm', 'custom', 'crm-notes', 'needs_signin', { name: 'crm-notes', custom: true, all_bots: false, bot_ids: [ak.id] }),
    conn('c-report', 'custom', 'report-builder', 'disabled', { name: 'report-builder', custom: true, transport: 'stdio', command: 'npx', args: ['-y', '@acme/report'],
      auth: 'none', env_names: ['REPORT_API_KEY'], missing_env: ['REPORT_API_KEY'], icon: 'terminal' }),
  ]
  const state = { conns, signedIn: false }
  const view = () => ({
    apps: catalog.map((a) => {
      const mine = state.conns.filter((c) => c.app_id === a.id)
      return { ...a, state: mine[0]?.status ?? 'available', connections: mine.map((c) => c.id) }
    }),
    categories: ['工作', '設計', '開發', '商務', '生活'],
    connected: new Set(state.conns.map((c) => (c.custom ? c.id : c.app_id))).size,
    total: catalog.length,
  })
  const uses = [
    { id: 'u1', connection_id: 'c-notion', bot_id: xm.id, tool: 'search', ok: true, created_at: Date.now() - 3_600_000 },
    { id: 'u2', connection_id: 'c-notion', bot_id: az.id, tool: 'create-page', ok: true, created_at: Date.now() - 7_200_000 },
    { id: 'u3', connection_id: 'c-notion', bot_id: ak.id, tool: 'search', ok: false, created_at: Date.now() - 90_000_000 },
  ]
  const jars: Record<string, { site: string; cookies: number }[]> = {
    [xm.id]: [
      { site: 'shopee.tw', cookies: 14 },
      { site: 'threads.net', cookies: 9 },
      { site: 'google.com', cookies: 7 },
      { site: 'canva.com', cookies: 3 },
    ],
  }
  Object.assign(api.override, {
    'apps.catalog': () => view(),
    'apps.list': () => ({ connections: state.conns }),
    'apps.get': (i: Record<string, unknown>) => ({ ...state.conns.find((c) => c.id === i.id), uses: i.id === 'c-notion' ? uses : [] }),
    'apps.connect': (i: Record<string, unknown>) => {
      const a = catalog.find((x) => x.id === i.app_id)!
      if (a.auth === 'none') {
        const c = conn('c-' + a.id, a.id, a.title, 'ok', { auth: 'none' })
        state.conns.push(c)
        return { connection: c }
      }
      const c = conn('c-' + a.id, a.id, a.title, 'needs_signin')
      state.conns.push(c)
      return { connection: { ...c, status: 'signing_in' }, signin: { flow_id: 'f1', auth_url: 'https://auth.example/authorize?client_id=cid&state=s1', redirect_uri: 'http://127.0.0.1/api/v1/apps/oauth/callback', expires_at: Date.now() + 900_000 } }
    },
    'apps.signin_status': () => ({ status: state.signedIn ? 'done' : 'pending' }),
    'apps.finish_signin': (i: Record<string, unknown>) => {
      state.signedIn = String(i.url).includes('code=')
      const c = state.conns.find((x) => x.app_id === 'figma')
      if (c) c.status = 'ok'
      return { status: 'done', connection: c }
    },
    'apps.cancel_signin': () => ({ canceled: true }),
    'apps.update': (i: Record<string, unknown>) => Object.assign(state.conns.find((c) => c.id === i.id)!, i.disabled_tools ? { disabled_tools: i.disabled_tools } : {}),
    'apps.grant_to_bot': (i: Record<string, unknown>) => Object.assign(state.conns.find((c) => c.id === i.id)!, i.all_bots ? { all_bots: true, bot_ids: [] } : {}),
    'apps.revoke_from_bot': (i: Record<string, unknown>) => Object.assign(state.conns.find((c) => c.id === i.id)!, { all_bots: false, bot_ids: [] }),
    'apps.test': (i: Record<string, unknown>) => state.conns.find((c) => c.id === i.id),
    'apps.disconnect': (i: Record<string, unknown>) => {
      const c = state.conns.find((x) => x.id === i.id)!
      state.conns = state.conns.filter((x) => x !== c)
      return { ...c, status: 'removed' }
    },
    'logins.list': (i: Record<string, unknown>) => ({ bot_id: i.bot_id, sites: jars[String(i.bot_id)] ?? [] }),
    'logins.share': (i: Record<string, unknown>) => ({ share_id: 'sh1', sites: i.sites, targets: i.to_bot_ids, copied: {} }),
    'logins.undo_share': () => ({ share_id: 'sh1', sites: [], targets: [], copied: {} }),
  })
  return { xm, az, ak, state }
}

async function open(page: Page, path: string) {
  const api = new MockServer(page)
  const s = seed(api)
  await page.addInitScript(() => {
    window.open = () => null
  })
  await api.install()
  await page.goto(path)
  return { api, ...s }
}

test('I01 連接應用程式：已連接在上、常用一鍵連接、篩選與搜尋', async ({ page }, info) => {
  await open(page, '/apps')
  await expect(page.getByRole('heading', { level: 1, name: '連接應用程式' })).toBeVisible()
  const done = page.getByRole('region', { name: '已連接' })
  await expect(done.getByText('Notion', { exact: true })).toBeVisible()
  await expect(done.getByText('可以使用 · 2 個帳號')).toBeVisible()
  await expect(done.getByText('需要登入', { exact: true })).toBeVisible()
  await expect(done.getByText('shop-inventory')).toBeVisible()
  await expect(page.getByRole('radio', { name: '已連接 6' })).toBeVisible()
  await settle(page)
  await page.screenshot({ path: shot(`${info.project.name}-i01-apps`), fullPage: true })

  // 搜尋找不到 → 引導到自訂連接。
  await page.getByRole('textbox', { name: '搜尋應用程式' }).fill('蝦皮')
  await expect(page.getByText('找不到「蝦皮」')).toBeVisible()
  await expect(page.getByRole('button', { name: '自訂連接（MCP）' }).last()).toBeVisible()
  await page.getByRole('textbox', { name: '搜尋應用程式' }).fill('')
  await page.getByRole('button', { name: '進階設定' }).click()
  await expect(page.getByText('登入權杖加密存在這台主機')).toBeVisible()
})

test('I02 應用程式詳細：帳號、誰能用、最近使用、工具', async ({ page }, info) => {
  await open(page, '/apps')
  await page.getByRole('button', { name: 'Notion 詳細' }).click()
  const sheet = page.getByRole('dialog')
  await expect(sheet.getByText('陳家選物')).toBeVisible()
  await expect(sheet.getByText(/授權已過期・重新連接後才能用/)).toBeVisible()
  await expect(sheet.getByRole('radio', { name: '所有 Bot' })).toHaveAttribute('aria-checked', 'true')
  await expect(sheet.getByText('小秘 用了')).toBeVisible()
  await sheet.getByRole('button', { name: '進階設定' }).click()
  await expect(sheet.getByRole('switch', { name: 'delete-page' })).toBeVisible()
  await settle(page)
  await sheet.screenshot({ path: shot(`${info.project.name}-i02-sheet`) })
  // 中斷最後一個帳號＝移除應用程式，要確認。
  await sheet.getByRole('button', { name: '移除 Notion' }).click()
  await expect(page.getByRole('alertdialog', { name: '移除 Notion？' })).toBeVisible()
})

test('I03 登入：開登入頁等你允許；回呼打不開時貼網址完成', async ({ page }, info) => {
  await open(page, '/apps')
  await page.getByRole('button', { name: '看全部' }).click().catch(() => {})
  await page.getByRole('button', { name: '連接 Figma' }).click()
  const dlg = page.getByRole('dialog', { name: '登入 Figma' })
  await expect(dlg.getByText('等你登入…完成後會自動繼續')).toBeVisible()
  await expect(dlg.getByRole('button', { name: '開啟登入頁' })).toBeVisible()
  await settle(page)
  await dlg.screenshot({ path: shot(`${info.project.name}-i03-signin`) })
  await dlg.getByRole('textbox', { name: '登入後的網址' }).fill('http://127.0.0.1/api/v1/apps/oauth/callback?code=abc&state=s1')
  await dlg.getByRole('button', { name: '完成登入' }).click()
  await expect(page.getByText('已連接 Figma，所有 Bot 都能用')).toBeVisible()
})

test('I04 自訂連接：清單＋編輯器、Token 只顯示末四碼、缺環境變數', async ({ page }, info) => {
  await open(page, '/apps/mcp?id=c-shop')
  await expect(page.getByRole('heading', { level: 1, name: '自訂連接（MCP）' })).toBeVisible()
  const list = page.getByRole('complementary', { name: '已加入的自訂連接' })
  await expect(list.getByText('還缺 REPORT_API_KEY，補上就能啟用')).toBeVisible()
  await expect(list.getByRole('button', { name: /shop-inventory/ })).toHaveAttribute('aria-current', 'true')
  const editor = page.getByRole('region', { name: '編輯自訂連接' })
  await expect(editor.getByRole('textbox', { name: 'Token' })).toHaveAttribute('placeholder', '已存 · 7f3a')
  await expect(editor.getByText('get_stock')).toBeVisible()
  await editor.getByRole('button', { name: '測試連線' }).click()
  await expect(editor.getByText(/連線成功/)).toBeVisible()
  await settle(page)
  await page.screenshot({ path: shot(`${info.project.name}-i04-mcp`), fullPage: true })
})

test('I05＋E09 網站登入：列出網站、分享給其他 Bot、可以復原', async ({ page }, info) => {
  await open(page, '/apps/logins')
  await expect(page.getByRole('heading', { level: 1, name: '網站登入' })).toBeVisible()
  await expect(page.getByText('shopee.tw')).toBeVisible()
  await settle(page)
  await page.screenshot({ path: shot(`${info.project.name}-i05-logins`), fullPage: true })
  await page.getByRole('button', { name: '分享登入' }).click()
  const dlg = page.getByRole('dialog')
  await expect(dlg.getByRole('button', { name: '分享登入' })).toBeDisabled()
  await dlg.getByText('shopee.tw').click()
  await dlg.getByText('threads.net').click()
  await dlg.getByText('阿哲').click()
  await dlg.getByText('阿凱').click()
  await expect(dlg.getByText('2 個網站 → 2 個 Bot')).toBeVisible()
  await dlg.getByRole('button', { name: '分享登入' }).click()
  await expect(dlg.getByText('已把 2 個網站的登入分享給阿哲、阿凱')).toBeVisible()
  await settle(page)
  await dlg.screenshot({ path: shot(`${info.project.name}-e09-share`) })
  await dlg.getByRole('button', { name: '復原' }).click()
  await expect(dlg.getByText('已復原，對方換回原本的登入')).toBeVisible()
})
