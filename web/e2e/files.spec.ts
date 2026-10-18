import { expect, test, type Page } from './fixtures'
import { MockServer, opError, settle } from './mock'

// C33 檔案：瀏覽、預覽（CSV 表格／原始內容、Markdown）、上傳、傳給其他 Bot、刪除、資料夾、全部檔案頁、手機 sheet、對話裡的檔案卡。
// 用瀏覽器層的假服務；截圖和 E07、M01、L19 畫板比對。後端的上傳下載與路徑安全由 Go 測試（internal/files）負責。
const shot = (name: string) => `e2e-results/screens/${name}.png`

test.beforeEach(async ({ page }, info) => {
  if (info.project.name === 'desktop') await page.setViewportSize({ width: 1440, height: 960 })
})

type Entry = { name: string; path: string; dir: boolean; size: number; modified: number; kind: string; group?: string; from?: string; important: boolean; mime?: string }

const now = Date.now()
const csv = '商品,我們,好物町,森森選物,小日子,差距\n' +
  ['帆布托特包,1280,1190,1350,1250,+8%', '防水托特包,1580,1390,1520,1450,+14%', '迷你斜背包,890,920,880,950,+1%', '皮革卡夾,650,590,620,690,+10%',
    '旅行收納袋,420,450,399,430,+5%', '帆布筆袋,280,290,300,270,+4%', '折疊購物袋,199,220,210,230,-5%', '雙肩後背包,1880,1650,1790,1720,+14%'].join('\n')
const PNG = Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAIAAAACCAYAAABytg0kAAAAFklEQVR4nGNkYPj/n4GBgYGJAQoAAAD//wMAEf4B/1aYvgAAAABJRU5ErkJggg==', 'base64')

function entry(path: string, size: number, minsAgo: number, extra: Partial<Entry> = {}): Entry {
  const name = path.slice(path.lastIndexOf('/') + 1)
  const ext = name.split('.').pop()!
  const kind = ({ csv: 'csv', pdf: 'pdf', docx: 'office', xlsx: 'sheet', jpg: 'image', png: 'image', md: 'markdown', json: 'code' } as Record<string, string>)[ext] ?? 'other'
  const rel = path.replace('/workspace/', '')
  const group = rel.startsWith('uploads/') ? 'upload' : rel.startsWith('from/') ? 'peer' : rel.startsWith('lessons/') ? 'lesson' : 'result'
  return { name, path, dir: false, size, modified: now - minsAgo * 60_000, kind, group, ...(group === 'peer' ? { from: rel.split('/')[1] } : {}), important: true, ...extra }
}

/** 假的 xbot.files.*：每個 Bot 一份檔案清單。 */
function mockFiles(api: MockServer, files: Record<string, Entry[]>, states: Record<string, string> = {}) {
  api.override['files.list'] = (i) => {
    const bot = i.bot_id as string
    const state = states[bot] ?? 'ready'
    if (state !== 'ready' && !i.wake) return { bot_id: bot, path: '/workspace', state, entries: [], total: 0, offset: 0, truncated: false }
    states[bot] = 'ready'
    const all = files[bot] ?? []
    if (i.all) return { bot_id: bot, path: '/workspace', state: 'ready', entries: all, total: all.length, offset: 0, truncated: false }
    const dir = (i.path as string) || '/workspace'
    const kids = new Map<string, Entry>()
    for (const e of all) {
      if (!e.path.startsWith(dir + '/')) continue
      const rest = e.path.slice(dir.length + 1)
      const first = rest.split('/')[0]
      if (rest.includes('/')) kids.set(first, { name: first, path: `${dir}/${first}`, dir: true, size: 0, modified: e.modified, kind: 'folder', important: false })
      else kids.set(first, e)
    }
    const entries = [...kids.values()].sort((a, b) => Number(b.dir) - Number(a.dir) || a.name.localeCompare(b.name))
    return { bot_id: bot, path: dir, state: 'ready', entries, total: entries.length, offset: 0, truncated: false }
  }
  api.override['files.read'] = (i) => {
    const e = (files[i.bot_id as string] ?? []).find((x) => x.path === i.path)
    if (!e) opError(404, 'not_found', '找不到這個檔案或資料夾')
    const dl = `/api/v1/files.download?bot_id=${i.bot_id}&path=${encodeURIComponent(e!.path)}`
    const base = { ...e, download_url: dl, truncated: false, binary: false }
    if (e!.kind === 'csv') {
      const lines = csv.split('\n').map((l) => l.split(','))
      return { ...base, text: csv, encoding: 'utf-8', table: { columns: lines[0], rows: lines.slice(1), total_rows: lines.length - 1, truncated: false } }
    }
    if (e!.kind === 'markdown') return { ...base, text: '# 週報\n\n- 退貨率下降 **2%**\n- 新品上架 3 件', encoding: 'utf-8' }
    if (e!.kind === 'image') return { ...base, binary: true, inline_url: dl + '&inline=1', thumb_url: dl + '&thumb=1' }
    return { ...base, binary: true, note: e!.kind === 'office' ? '這種檔案請下載後開啟' : undefined }
  }
}

async function routeBlobs(page: Page, uploads: Array<{ bot: string; dir: string; names: string[] }>, onUpload: (bot: string, names: string[]) => Entry[]) {
  await page.route('**/api/v1/files.download**', (route) => {
    const u = new URL(route.request().url())
    if (u.searchParams.get('path')?.endsWith('.png') || u.searchParams.get('path')?.endsWith('.jpg')) return route.fulfill({ body: PNG, contentType: 'image/png' })
    return route.fulfill({ body: csv, contentType: 'text/csv', headers: { 'Content-Disposition': 'attachment' } })
  })
  await page.route('**/api/v1/files.upload**', async (route) => {
    const u = new URL(route.request().url())
    const body = route.request().postDataBuffer()?.toString('latin1') ?? ''
    const names = [...body.matchAll(/filename="([^"]+)"/g)].map((m) => Buffer.from(m[1], 'latin1').toString('utf8'))
    const bot = u.searchParams.get('bot_id')!
    uploads.push({ bot, dir: u.searchParams.get('dir') ?? '', names })
    return route.fulfill({ json: { files: onUpload(bot, names) } })
  })
}

test('一個 Bot 的檔案：分組、預覽、上傳、傳給其他 Bot、刪除、資料夾', async ({ page }, info) => {
  const mobile = info.project.name === 'mobile'
  const api = new MockServer(page)
  const mi = api.addBot({ name: '小秘', title: '幕僚長', color: 'blue', shape: 'cursor' })
  const zhe = api.addBot({ name: '阿哲', color: 'orange', shape: 'blob' })
  const files: Record<string, Entry[]> = {
    [mi.id]: [
      entry('/workspace/定價比較.csv', 18 * 1024, 10),
      entry('/workspace/競品報告.pdf', 1.2 * 1024 * 1024, 90),
      entry('/workspace/週報草稿.md', 36 * 1024, 60 * 24),
      entry('/workspace/uploads/上月訂單.xlsx', 220 * 1024, 60 * 30),
      entry('/workspace/uploads/包款參考.jpg', 1.4 * 1024 * 1024, 60 * 50),
      entry('/workspace/from/阿哲/競品價格.csv', 12 * 1024, 60 * 26),
      entry('/workspace/app/dist/bundle.js', 900 * 1024, 5, { important: false, kind: 'code' }),
    ],
    [zhe.id]: [],
  }
  mockFiles(api, files)
  const uploads: Array<{ bot: string; dir: string; names: string[] }> = []
  await api.install()
  await routeBlobs(page, uploads, (bot, names) => {
    const out = names.map((n) => entry(`/workspace/uploads/${n}`, 2048, 0))
    files[bot].push(...out)
    return out
  })
  api.override['files.move_between_bots'] = (i) => ({ files: [{ from: (i.paths as string[])[0], to: '/workspace/from/小秘/定價比較.csv', size: 18432, reused: false }], skipped: [] })
  api.override['files.delete'] = (i) => {
    files[mi.id] = files[mi.id].filter((e) => e.path !== i.path)
    return { path: i.path, deleted: true }
  }

  await page.goto(`/bots/${mi.id}/files`)
  const list = page.getByTestId('file-list')
  await expect(list.getByRole('region', { name: '成果' })).toContainText('定價比較.csv')
  await expect(list.getByRole('region', { name: '你上傳的' })).toContainText('上月訂單.xlsx')
  await expect(list.getByRole('region', { name: '從隊友來的' })).toContainText('阿哲')
  await expect(list).not.toContainText('bundle.js')
  await list.getByRole('button', { name: /也顯示建置檔/ }).click()
  await expect(list).toContainText('bundle.js')
  await list.getByRole('button', { name: '只看成果' }).click()

  // 預覽 CSV：表格，可以切換原始內容。
  await list.getByRole('button', { name: /定價比較\.csv/ }).click()
  await expect(page).toHaveURL(/path=%2Fworkspace%2F%E5%AE%9A/)
  if (!mobile) {
    const preview = page.getByTestId('file-preview')
    const table = preview.getByRole('table', { name: '定價比較.csv' })
    await expect(table).toContainText('防水托特包')
    await expect(preview).toContainText('8 列 · 6 欄')
    await expect(preview.getByRole('link', { name: '下載' })).toHaveAttribute('href', /files\.download\?bot_id=/)
    await settle(page)
    await page.screenshot({ path: shot('desktop-files-e07') })
    await preview.getByRole('radio', { name: '原始內容' }).click()
    await expect(preview.locator('pre')).toContainText('商品,我們,好物町')
    await preview.getByRole('radio', { name: '表格' }).click()

    // Markdown 排版。
    await list.getByRole('button', { name: /週報草稿\.md/ }).click()
    await expect(page.getByTestId('file-preview').getByRole('heading', { name: '週報' })).toBeVisible()

    // 傳給其他 Bot。
    await list.getByRole('button', { name: /定價比較\.csv/ }).click()
    await page.getByRole('button', { name: '更多動作' }).click()
    await page.getByRole('menuitem', { name: '傳給其他 Bot' }).click()
    const send = page.getByRole('dialog', { name: '傳給其他 Bot' })
    await expect(send).toContainText('/workspace/from/小秘/')
    await send.getByRole('radio', { name: /阿哲/ }).click()
    await send.getByRole('button', { name: '傳過去' }).click()
    await expect(page.getByText('已傳給阿哲：/workspace/from/小秘/定價比較.csv')).toBeVisible()
    expect(api.calls.find((c) => c.op === 'files.move_between_bots')?.input).toMatchObject({ bot_id: mi.id, to_bot_id: zhe.id, paths: ['/workspace/定價比較.csv'] })

    // 刪除：先確認。
    await list.getByRole('button', { name: /競品報告\.pdf/ }).click()
    await page.getByRole('button', { name: '更多動作' }).click()
    await page.getByRole('menuitem', { name: '刪除' }).click()
    const confirm = page.getByRole('alertdialog', { name: '刪除「競品報告.pdf」？' })
    await confirm.getByRole('button', { name: '刪除' }).click()
    await expect(page.getByText('已刪除「競品報告.pdf」')).toBeVisible()
    await expect(list).not.toContainText('競品報告.pdf')
  } else {
    // 手機（L19）：從底部拉出的預覽，前 5 列。
    const sheet = page.getByRole('dialog', { name: '預覽：定價比較.csv' })
    await expect(sheet).toBeVisible()
    await expect(sheet.getByRole('table')).toContainText('帆布托特包')
    await expect(sheet.getByRole('table').locator('tbody tr')).toHaveCount(5)
    await expect(sheet).toContainText('共 8 列，這裡只顯示前 5 列')
    await expect(sheet.getByRole('button', { name: '在對話中提到' })).toBeVisible()
    await expect(sheet.getByRole('link', { name: '下載' })).toBeVisible()
    await settle(page)
    await page.screenshot({ path: shot('mobile-files-l19') })
    await sheet.getByRole('button', { name: '關閉預覽' }).click()
    await expect(sheet).toBeHidden()
    await settle(page)
    await page.screenshot({ path: shot('mobile-files-list') })
  }

  // 上傳（選檔）。
  await page.getByLabel('選擇要上傳的檔案').setInputFiles({ name: '新報價.csv', mimeType: 'text/csv', buffer: Buffer.from('a,b\n1,2\n') })
  await expect(page.getByText('已上傳「新報價.csv」')).toBeVisible()
  expect(uploads[0]).toMatchObject({ bot: mi.id, dir: '', names: ['新報價.csv'] })
  await expect(list).toContainText('新報價.csv')
  if (mobile) await page.getByRole('dialog').getByRole('button', { name: '關閉預覽' }).click()

  // 資料夾瀏覽。
  await page.getByRole('radio', { name: '資料夾' }).click()
  await expect(list.getByRole('list', { name: '資料夾內容' })).toContainText('uploads')
  await list.getByRole('button', { name: /^uploads/ }).click()
  await expect(page.getByRole('navigation', { name: '目前的資料夾' })).toContainText('工作區')
  await expect(list).toContainText('上月訂單.xlsx')
  if (!mobile) {
    await page.getByRole('button', { name: '新資料夾' }).click()
    api.override['files.mkdir'] = (i) => ({ name: '報價', path: i.path, dir: true, size: 0, modified: now, kind: 'folder', important: false })
    const nf = page.getByRole('dialog', { name: '新資料夾' })
    await nf.getByLabel('名稱').fill('報價')
    await nf.getByRole('button', { name: '建立' }).click()
    expect(api.calls.find((c) => c.op === 'files.mkdir')?.input).toMatchObject({ bot_id: mi.id, path: '/workspace/uploads/報價' })
  }
})

test('電腦沒開機：按「開機看檔案」才列出', async ({ page }) => {
  const api = new MockServer(page)
  const bot = api.addBot({ name: '數據喵', color: 'purple', shape: 'star' })
  mockFiles(api, { [bot.id]: [entry('/workspace/退貨率.png', 120 * 1024, 30)] }, { [bot.id]: 'off' })
  await api.install()
  await routeBlobs(page, [], () => [])
  await page.goto(`/bots/${bot.id}/files`)
  await expect(page.getByText('數據喵的電腦沒有開機')).toBeVisible()
  await page.getByRole('button', { name: '開機看檔案' }).click()
  await expect(page.getByTestId('file-list')).toContainText('退貨率.png')
  expect(api.calls.filter((c) => c.op === 'files.list').at(-1)?.input).toMatchObject({ wake: true })
})

test('全部檔案（M01）：依 Bot 分組、種類篩選、縮圖、點了到預覽', async ({ page }, info) => {
  const api = new MockServer(page)
  const mi = api.addBot({ name: '小秘', color: 'blue', shape: 'cursor' })
  const bian = api.addBot({ name: '小編', color: 'pink', shape: 'blob' })
  const fang = api.addBot({ name: '帳房', color: 'yellow', shape: 'star' })
  mockFiles(api, {
    [mi.id]: [entry('/workspace/定價比較.csv', 18 * 1024, 30), entry('/workspace/競品報告.pdf', 1.2e6, 60), entry('/workspace/uploads/包款參考.jpg', 1.4e6, 60 * 30),
      entry('/workspace/uploads/上月訂單.xlsx', 220 * 1024, 60 * 26)],
    [bian.id]: [entry('/workspace/托特包主圖-1.png', 840 * 1024, 20), entry('/workspace/貼文文案三版.md', 4 * 1024, 40), entry('/workspace/上架頁草稿.pdf', 300 * 1024, 50),
      entry('/workspace/lessons/匯出訂單/steps.json', 2 * 1024, 60 * 50)],
    [fang.id]: [],
  }, { [fang.id]: 'none' })
  await api.install()
  await routeBlobs(page, [], () => [])
  await page.goto('/files')
  await expect(page.getByRole('heading', { level: 1, name: '全部檔案' })).toBeVisible()
  const mine = page.getByRole('region', { name: '小秘的檔案' })
  await expect(mine.getByTestId('file-card')).toHaveCount(4)
  await expect(page.getByRole('region', { name: '小編的檔案' }).getByTestId('file-card')).toHaveCount(4)
  await expect(page.getByRole('region', { name: '帳房的檔案' })).toHaveCount(0)
  await expect(mine.locator('img').first()).toHaveAttribute('src', /thumb=1/)
  await settle(page)
  await page.screenshot({ path: shot(`${info.project.name}-files-m01`) })
  await page.getByRole('radio', { name: /你上傳的/ }).click()
  await expect(mine.getByTestId('file-card')).toHaveCount(2)
  await expect(page.getByRole('region', { name: '小編的檔案' })).toHaveCount(0)
  await page.getByRole('radio', { name: /全部/ }).click()
  await page.getByRole('button', { name: '清單' }).click()
  await expect(mine.getByRole('listitem')).toHaveCount(4)
  await mine.getByRole('link', { name: /定價比較\.csv/ }).click()
  await expect(page).toHaveURL(new RegExp(`/bots/${mi.id}/files\\?path=`))
})

test('對話裡的檔案卡：點了預覽、可以直接下載', async ({ page }, info) => {
  const mobile = info.project.name === 'mobile'
  const api = new MockServer(page)
  const mi = api.addBot({ name: '小秘', color: 'blue', shape: 'cursor' })
  mockFiles(api, { [mi.id]: [entry('/workspace/定價比較.csv', 18 * 1024, 10), entry('/workspace/競品報告.pdf', 1.2e6, 10)] })
  const th = api.addThread(mi.id, '三家競品定價')
  api.addMessage(th.id, { sender: 'owner', content: '三家競品的定價整理成表，附一份報告給我' })
  api.addMessage(th.id, {
    sender: 'bot:' + mi.id,
    kind: 'card',
    content: '【檔案】整理好了',
    card: { type: 'file', title: '整理好了。8 項商品裡有 3 項比最便宜的那家貴 10% 以上。', files: [{ path: '/workspace/定價比較.csv', name: '定價比較.csv' }, { path: '/workspace/競品報告.pdf' }] },
  } as never)
  await api.install()
  await routeBlobs(page, [], () => [])
  await page.goto(`/bots/${mi.id}/threads/${th.id}`)
  const card = page.getByTestId('chat-card')
  await expect(card).toContainText('整理好了')
  await expect(card.getByRole('link', { name: '下載 競品報告.pdf' })).toHaveAttribute('href', /files\.download\?bot_id=.*path=%2Fworkspace%2F/)
  await settle(page)
  await card.screenshot({ path: shot(`${info.project.name}-files-chat-card`) })
  await card.getByRole('link', { name: '預覽 定價比較.csv' }).click()
  await expect(page).toHaveURL(new RegExp(`/bots/${mi.id}/files\\?path=`))
  if (mobile) await expect(page.getByRole('dialog', { name: '預覽：定價比較.csv' }).getByRole('table')).toContainText('防水托特包')
  else await expect(page.getByTestId('file-preview').getByRole('table')).toContainText('防水托特包')

  // 在對話中提到：回到對話，路徑放進輸入框。
  if (mobile) await page.getByRole('dialog').getByRole('button', { name: '在對話中提到' }).click()
  else {
    await page.getByRole('button', { name: '更多動作' }).click()
    await page.getByRole('menuitem', { name: '在對話中提到' }).click()
  }
  await expect(page.getByRole('main', { name: '和小秘的對話' }).getByRole('textbox')).toHaveValue('/workspace/定價比較.csv ')
})
