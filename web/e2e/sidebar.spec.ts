import { expect, test } from './fixtures'
import { MockServer } from './mock'

// 縮小的側欄：「⋯」選單要蓋在頁面上面，不能被右邊的頁面蓋住（2026-10-19 使用者回報：縮小後選單被切掉）。
test('縮小側欄的「⋯」選單整個看得到、點得到', async ({ page }, info) => {
  test.skip(info.project.name === 'mobile', '手機沒有縮小的側欄')
  await page.setViewportSize({ width: 1280, height: 800 })
  const m = new MockServer(page)
  m.addBot({ name: '小秘', title: '幕僚長', color: 'blue', shape: 'cursor' })
  await m.install()
  await page.goto('/office')
  const nav = page.getByRole('navigation', { name: '側欄' })
  await nav.getByRole('button', { name: '縮小側欄' }).click()
  await nav.getByRole('button', { name: '更多' }).click()
  const item = page.getByRole('menuitem', { name: '組織架構' })
  await expect(item).toBeVisible()
  // 選單項目的右半邊（在側欄外面）最上層要是選單本身
  const onTop = await item.evaluate((el) => {
    const r = el.getBoundingClientRect()
    const hit = document.elementFromPoint(r.right - 12, r.top + r.height / 2)
    return !!hit && el.contains(hit)
  })
  expect(onTop).toBe(true)
  await item.click()
  await expect(page).toHaveURL(/\/org$/)
  await nav.getByRole('button', { name: '展開側欄' }).click()
})

// 2026-10-20 側欄改版（design 稿 A「總管置頂」）：大總管的大卡片在最上面，其他 Bot 一列一個（職稱、最後一句話、未讀），
// 新增收在＋，設定和不常用的頁面收在左下角；手機的對話分頁同一種排版。
test('側欄 A：大總管卡片、每列有最後一句話；＋和「設定與更多」選單', async ({ page }, info) => {
  const m = new MockServer(page)
  const chief = m.addBot({ name: '水哥哥', title: '幕僚長', color: 'teal', shape: 'star', primary: true } as never)
  const dev = m.addBot({ name: '研究員', title: '產業研究', color: 'blue', shape: 'blob' })
  m.addBot({ name: '帶貨分析', title: '績效追蹤', color: 'orange', shape: 'squircle' })
  const t = m.addThread(dev.id, '佣金率')
  m.addMessage(t.id, { sender: 'owner', content: '查三家佣金率' })
  m.addMessage(t.id, { sender: 'bot:' + dev.id, content: '**比較表**做好了，在 /workspace/reports' })
  t.unread = 1
  m.addRoom([chief.id, dev.id], '帶貨研究團隊')
  await m.install()
  const phone = info.project.name !== 'desktop'
  await page.goto(phone ? '/chats' : '/office')
  const scope = phone ? page.getByRole('main', { name: '對話' }) : page.getByRole('navigation', { name: '側欄' })
  const card = scope.getByTestId('chief-card')
  await expect(card).toHaveAccessibleName(/水哥哥（大總管）/)
  await expect(scope.getByRole('link', { name: /水哥哥/ })).toHaveCount(1)
  const row = scope.getByRole('link', { name: /研究員/ })
  await expect(row).toContainText('產業研究')
  await expect(row).toContainText('比較表做好了，在 /workspace/reports')
  await expect(row.getByRole('img', { name: '有新訊息' })).toBeVisible()
  await expect(scope.getByRole('group', { name: '群組' }).or(scope.getByRole('region', { name: '群組' })).getByRole('link', { name: /帶貨研究團隊/ })).toBeVisible()
  await page.screenshot({ path: `e2e-results/screens/${info.project.name}-sidebar-a.png` })
  // 新增：一顆＋裡有 Bot、群組、團隊
  const add = phone ? page.getByRole('button', { name: '新增', exact: true }) : scope.getByRole('button', { name: '新增', exact: true })
  await add.click()
  await expect(page.getByRole('menuitem')).toHaveText(['新增 Bot', '新增群組', '建立團隊'])
  await page.keyboard.press('Escape')
  // 設定和不常用的頁面
  await page.getByRole('button', { name: '設定與更多' }).click()
  await expect(page.getByRole('menuitem', { name: '設定' })).toBeVisible()
  await page.getByRole('menuitem', { name: '全部檔案' }).click()
  await expect(page).toHaveURL(/\/files$/)
})

// 2026-10-20：刪群組、搬人員要直覺。電腦上：把 Bot 拖到別的團隊分區＝換團隊，拖到群組上＝加進群組；
// 每一列滑過出現「⋯」（不用知道右鍵）；團隊的「⋯」能改名換成員、刪除團隊，刪團隊時可以一起刪它的群組。
test('搬人員與刪除：拖到團隊換團隊、拖到群組加入、「⋯」選單、刪團隊連群組', async ({ page }, info) => {
  test.skip(info.project.name !== 'desktop', '拖曳和滑過的「⋯」是桌面的')
  await page.setViewportSize({ width: 1280, height: 860 })
  const m = new MockServer(page)
  const a = m.addBot({ name: '阿海', title: '主管', color: 'blue', shape: 'blob' })
  const b = m.addBot({ name: '阿筆', title: '寫手', color: 'orange', shape: 'squircle' })
  const c = m.addBot({ name: '小巡', title: '海巡', color: 'pink', shape: 'drop' })
  const room = m.addRoom([a.id, c.id], '內容隊群組')
  const content = m.addTeam('內容隊', [a.id, b.id], room.id)
  const research = m.addTeam('研究隊', [c.id])
  await m.install()
  await page.goto('/office')
  const nav = page.getByRole('navigation', { name: '側欄' })
  const team = (name: string) => nav.getByRole('group', { name })

  // 拖到別的團隊
  await nav.getByRole('link', { name: /阿筆/ }).dragTo(team('研究隊'))
  await expect.poll(() => m.calls.filter((x) => x.op === 'teams.update').map((x) => x.input)).toEqual([
    { id: content.id, remove_ids: [b.id] },
    { id: research.id, add_ids: [b.id] },
  ])
  await expect(team('研究隊').getByRole('link', { name: /阿筆/ })).toBeVisible()
  await expect(page.getByText('已把 阿筆 移到「研究隊」')).toBeVisible()

  // 拖到群組上＝加進群組
  await team('研究隊').getByRole('link', { name: /阿筆/ }).dragTo(nav.getByRole('link', { name: /內容隊群組/ }))
  await expect.poll(() => m.calls.find((x) => x.op === 'rooms.update')?.input).toEqual({ room_id: room.id, member_ids: [a.id, c.id, b.id] })

  // 列上的「⋯」：不用右鍵也找得到（換團隊、刪除）
  const row = team('研究隊').getByRole('link', { name: /小巡/ })
  await row.hover()
  await nav.getByRole('button', { name: '小巡的選單' }).click()
  await expect(page.getByRole('menuitem', { name: '移到「內容隊」' })).toBeVisible()
  await expect(page.getByRole('menuitem', { name: '移出團隊' })).toBeVisible()
  await expect(page.getByRole('menuitem', { name: '設成大總管' })).toBeVisible()
  await page.getByRole('menuitem', { name: '移出團隊' }).click()
  await expect.poll(() => m.calls.filter((x) => x.op === 'teams.update').at(-1)?.input).toEqual({ id: research.id, remove_ids: [c.id] })
  await page.screenshot({ path: `e2e-results/screens/${info.project.name}-sidebar-move.png` })

  // 團隊的「⋯」→ 改名、換成員 → 刪除團隊（連它的群組）
  await team('內容隊').hover()
  await nav.getByRole('button', { name: '內容隊的選單' }).click()
  await page.getByRole('menuitem', { name: '改名、換成員' }).click()
  const dlg = page.getByRole('dialog', { name: '編輯團隊' })
  await expect(dlg.getByRole('checkbox', { name: /阿海/ })).toBeChecked()
  await dlg.getByRole('button', { name: '刪除團隊' }).click()
  const confirm = page.getByRole('alertdialog', { name: '刪除團隊「內容隊」？' })
  await expect(confirm.getByRole('checkbox', { name: /一起刪除團隊的群組「內容隊群組」/ })).toBeChecked()
  await page.screenshot({ path: `e2e-results/screens/${info.project.name}-team-delete.png` })
  await confirm.getByRole('button', { name: '刪除' }).click()
  await expect.poll(() => m.calls.filter((x) => x.op === 'teams.delete' || x.op === 'rooms.delete').map((x) => x.op)).toEqual(['teams.delete', 'rooms.delete'])
  await expect(page.getByText('已刪除團隊「內容隊」和群組「內容隊群組」')).toBeVisible()
})
