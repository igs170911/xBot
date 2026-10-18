import type { TeamsChartOutput, TeamsTemplatesOutput } from '../api/generated'
import type { Tone } from '../ui/Pill'

// 團隊地圖、組織架構、建立團隊共用的資料整理（X03）。

export type Chart = TeamsChartOutput
export type ChartBot = Chart['bots'][number]

const STATE: Record<string, { label: string; tone: Tone; avatar: string }> = {
  working: { label: '工作中', tone: 'accent', avatar: 'working' },
  waiting: { label: '等你回覆', tone: 'warning', avatar: 'notifying' },
  queued: { label: '排隊中', tone: 'neutral', avatar: 'idle' },
  idle: { label: '待命', tone: 'neutral', avatar: 'idle' },
}

export function stateOf(b: Pick<ChartBot, 'state' | 'approvals'>) {
  if (b.state === 'idle' && b.approvals.length) return { label: '等你允許', tone: 'warning' as Tone, avatar: 'notifying' }
  return STATE[b.state] ?? STATE.idle
}

/** 依團隊分組：每個團隊（主管在前），最後是未分區。 */
export function groupsOf(chart: Chart) {
  const groups = chart.teams.map((t) => ({
    id: t.id,
    name: t.name,
    team: t,
    bots: t.member_ids.map((id) => chart.bots.find((b) => b.id === id)).filter((b): b is ChartBot => !!b),
  }))
  const placed = new Set(groups.flatMap((g) => g.bots.map((b) => b.id)))
  const rest = chart.bots.filter((b) => !placed.has(b.id))
  return { groups, rest }
}

/** 報告樹：每個 Bot 和它的深度（向你報告的是 0），依樹的順序。 */
export function treeOf(bots: ChartBot[]): Array<{ bot: ChartBot; depth: number }> {
  const ids = new Set(bots.map((b) => b.id))
  const out: Array<{ bot: ChartBot; depth: number }> = []
  const walk = (parent: string, depth: number) => {
    for (const b of bots) {
      const boss = ids.has(b.reports_to_id) ? b.reports_to_id : ''
      if (boss === parent && !out.some((x) => x.bot.id === b.id)) {
        out.push({ bot: b, depth })
        walk(b.id, depth + 1)
      }
    }
  }
  walk('', 0)
  return out
}

/** 可以當 id 主管的 Bot：不是自己，也不是自己的下屬（不能繞成一圈）。 */
export function managerChoices(bots: ChartBot[], id: string): ChartBot[] {
  const below = new Set<string>([id])
  let grew = true
  while (grew) {
    grew = false
    for (const b of bots) {
      if (!below.has(b.id) && below.has(b.reports_to_id)) {
        below.add(b.id)
        grew = true
      }
    }
  }
  return bots.filter((b) => !below.has(b.id))
}

export type Template = TeamsTemplatesOutput['templates'][number]
export type Person = { name: string; title: string; persona: string }

/** 範本的人：主管在第一位。 */
export function peopleOf(t: Template): Person[] {
  return [t.lead, ...t.members].map((p) => ({ ...p }))
}

/** 建立後會不會超過全部 Bot 的上限。 */
export function overLimit(total: number, adding: number, max: number) {
  return total + adding > max
}

/** 名字重複或空白的成員（建立前擋下，伺服器也會再檢查一次）。 */
export function memberProblem(people: Person[], teamMax: number): string | null {
  if (people.length < 2) return '團隊至少要有主管和一位成員'
  if (people.length > teamMax) return `一個團隊最多 ${teamMax} 位（含主管）`
  const names = people.map((p) => p.name.trim())
  if (names.some((n) => !n)) return '每位成員都要有名字'
  const dup = names.find((n, i) => names.indexOf(n) !== i)
  return dup ? `「${dup}」重複了` : null
}

