import { render, screen, within } from '@testing-library/react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { botsStore, resetBots } from '../data/bots'
import { fakeApi } from '../test-api'
import { ToastProvider } from '../ui/Toast'
import { EditTeamDialog } from './EditTeamDialog'

const bot = (id: string, name: string, extra: object = {}) => ({ id, name, title: '', color: 'blue', shape: 'cursor', archived: false, primary: false, team_id: '', ...extra })
const TEAM = { id: 't1', name: '行銷團隊', about: '', chief_id: 'b2', member_ids: ['b2'], memory: [], position: 1, room_id: '', template: '', created_at: 1, created_by: 'owner' }

beforeEach(() => {
  fakeApi({ 'bots.list': () => ({ bots: [] }), 'teams.list': () => ({ teams: [TEAM] }) })
})
afterEach(() => {
  resetBots()
  vi.unstubAllGlobals()
})

it('大總管不在團隊成員的選項裡（大總管不加入團隊）', () => {
  botsStore.set({ bots: [bot('b1', '水哥哥', { primary: true }), bot('b2', '行銷長', { team_id: 't1' }), bot('b3', '研究員')] as never, activity: {} })
  render(
    <ToastProvider>
      <EditTeamDialog team={TEAM as never} onClose={() => {}} onDelete={() => {}} />
    </ToastProvider>,
  )
  const list = within(screen.getByRole('group', { name: '成員' }))
  expect(list.getByText('行銷長')).toBeInTheDocument()
  expect(list.getByText('研究員')).toBeInTheDocument()
  expect(list.queryByText('水哥哥')).toBeNull()
})
