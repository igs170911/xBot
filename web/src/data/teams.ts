import { teamsList, teamsUpdate, type TeamsListOutput, type XBotEvent } from '../api/generated'
import type { Bot } from './bots'
import { loadBots } from './bots'
import { onEvent, onReconnect } from './events'
import { createStore } from './store'

// 團隊（X03，TM-1）：側欄依團隊分區、團隊地圖、建立團隊。
export type Team = TeamsListOutput['teams'][number]

export type TeamsState = { teams: Team[] | null; error?: string }

export const teamsStore = createStore<TeamsState>({ teams: null })

/** 「建立團隊」對話框開著沒（側欄、團隊地圖都能打開）。 */
export const newTeamDialog = createStore(false)
export const openNewTeam = () => newTeamDialog.set(true)

let inflight: Promise<void> | null = null
let again = false

export function loadTeams(): Promise<void> {
  if (inflight) {
    again = true
    return inflight
  }
  inflight = teamsList({})
    .then((out) => teamsStore.set({ teams: out.teams }))
    .catch((err: unknown) => teamsStore.set((s) => ({ teams: s.teams ?? [], error: err instanceof Error ? err.message : String(err) })))
    .finally(() => {
      inflight = null
      if (again) {
        again = false
        void loadTeams()
      }
    })
  return inflight
}

/** 團隊或報告線變了（team.updated 事件、xbot.teams.* 操作）。建團隊會新建 Bot，所以 Bot 清單也要重讀。 */
export function isTeamEvent(e: XBotEvent): boolean {
  if (e.kind === 'team.updated') return true
  if (e.kind !== 'op') return false
  const p = (e.payload ?? {}) as { op?: string; status?: string }
  return typeof p.op === 'string' && p.op.startsWith('xbot.teams.') && p.status === 'ok'
}

export type BotSection = { key: string; title: string; team?: Team; bots: Bot[] }

/**
 * 側欄分區：每個團隊一區（主管在最前面），沒有團隊的 Bot 放最後一區。
 * 沒有任何團隊時只有一區「Bot」。
 */
export function sectionsOf(bots: Bot[], teams: Team[] | null): BotSection[] {
  const live = bots.filter((b) => !b.archived)
  const list = teams ?? []
  const out: BotSection[] = []
  const placed = new Set<string>()
  for (const t of list) {
    const members = live.filter((b) => b.team_id === t.id)
    members.sort((a, b) => Number(b.id === t.chief_id) - Number(a.id === t.chief_id))
    members.forEach((b) => placed.add(b.id))
    out.push({ key: t.id, title: t.name, team: t, bots: members })
  }
  const rest = live.filter((b) => !placed.has(b.id))
  if (rest.length || out.length === 0) out.push({ key: 'none', title: out.length ? '未分區' : 'Bot', bots: rest })
  return out
}

let unsubs: Array<() => void> | null = null
let timer: ReturnType<typeof setTimeout> | null = null

export function startTeams() {
  if (unsubs) return
  void loadTeams()
  unsubs = [
    onEvent((e) => {
      if (!isTeamEvent(e) || timer) return
      timer = setTimeout(() => {
        timer = null
        void loadTeams()
        void loadBots()
      }, 100)
    }),
    onReconnect(() => void loadTeams()),
  ]
}

export function resetTeams() {
  unsubs?.forEach((u) => u())
  unsubs = null
  teamsStore.set({ teams: null })
}

/**
 * 把 Bot 換到另一個團隊（to 空白＝移出團隊、回到未分區）。先從原本的團隊移出、再加進新的：
 * 它原本是那個團隊的主管時，原團隊的主管和報告線由後端一起調整（teams.update remove_ids）。
 */
export async function moveBot(bot: Pick<Bot, 'id' | 'team_id'>, to: string): Promise<void> {
  if (bot.team_id === to) return
  try {
    if (bot.team_id) await teamsUpdate({ id: bot.team_id, remove_ids: [bot.id] })
    if (to) await teamsUpdate({ id: to, add_ids: [bot.id] })
  } finally {
    await Promise.all([loadTeams(), loadBots()])
  }
}
