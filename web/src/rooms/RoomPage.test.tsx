import { render, screen } from '@testing-library/react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { fakeApi } from '../test-api'
import App from '../App'
import { resetBots } from '../data/bots'

const member = (bot_id: string, name: string) => ({ bot_id, name, title: '', color: 'blue', shape: 'cursor', seat: 'idle' })
const ROOM = { id: 'r1', name: '發表會', auto_name: false, responder_rule: 'auto', lead_id: 'b1', bulletin: '', created_by: 'owner', members: [member('b1', '小秘'), member('b2', '小寫')], preview: '', last_message_at: 1, created_at: 1 }

let messages: unknown[]
beforeEach(() => {
  messages = []
  fakeApi({
    'collab.room_read': () => ({ room: ROOM, messages, has_more: false }),
    'limits.get': () => ({ values: { room_rounds: 10 } }),
  })
  window.history.replaceState(null, '', '/rooms/r1')
})
afterEach(() => {
  resetBots()
  vi.unstubAllGlobals()
})

it('群組裡 Bot 回覆的檔案：只顯示檔名、點了打開那個 Bot 的檔案，下面有檔案卡', async () => {
  messages = [{ id: 'm1', seq: 1, sender: 'bot:b2', speaker_name: '小寫', content: '寫好了，在 `/workspace/reports/新聞稿.docx`。', mentions: [], created_at: Date.now() }]
  render(<App />)
  const link = await screen.findByRole('link', { name: '新聞稿.docx' })
  // 檔案在發言的小寫電腦裡，不是主責小秘
  expect(link).toHaveAttribute('data-href', `/bots/b2/files?path=${encodeURIComponent('/workspace/reports/新聞稿.docx')}`)
  expect(screen.queryByText(/\/workspace\/reports/)).toBeNull()
  expect(screen.getByRole('link', { name: '預覽 新聞稿.docx' })).toBeInTheDocument()
  expect(screen.getByRole('link', { name: '下載 新聞稿.docx' })).toBeInTheDocument()
})
