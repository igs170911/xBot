import { BrainCircuit, Monitor, Settings2, SlidersHorizontal, Wallet, type LucideIcon } from 'lucide-react'

export type NavItem = { to: string; label: string; icon?: LucideIcon; sub?: boolean }

export const SETTINGS_NAV: NavItem[] = [
  { to: '/settings/general', label: '一般', icon: Settings2 },
  { to: '/settings/models', label: 'AI 模型', icon: BrainCircuit },
  { to: '/settings/computers', label: '電腦與裝置', icon: Monitor },
  { to: '/settings/usage', label: '用量與花費', icon: Wallet },
  { to: '/settings/advanced', label: '進階', icon: SlidersHorizontal },
  { to: '/settings/advanced/limits', label: '限制', sub: true },
  { to: '/settings/advanced/backup', label: '備份與更新', sub: true },
  { to: '/settings/advanced/people', label: '成員與紀錄', sub: true },
  { to: '/settings/advanced/lab', label: '技能庫與實驗', sub: true },
  { to: '/settings/advanced/network', label: '網路與 Tailscale', sub: true },
  { to: '/settings/advanced/agents', label: '外部代理與 MCP', sub: true },
]

const TITLES: Record<string, string> = Object.fromEntries(SETTINGS_NAV.map((n) => [n.to, n.label]))

/** 設定裡一頁的標題（手機頁首、返回鈕的字）。 */
export function settingsTitle(path: string): string {
  if (path.startsWith('/settings/bots/')) return 'Bot 設定'
  if (path === '/settings/computers/add') return '新增主機'
  if (path.startsWith('/settings/computers/hosts/')) return '主機'
  return TITLES[path] ?? (path.startsWith('/settings/models') ? 'AI 模型' : '設定')
}

/** 這個網址屬於哪個導覽項目（子頁也算）。Bot 設定不屬於任何一項（從哪裡打開的另外看 botBack）。 */
export function activeNav(path: string): string {
  if (path.startsWith('/settings/bots/')) return ''
  const exact = SETTINGS_NAV.find((n) => n.to === path)
  if (exact) return exact.to
  const hits = SETTINGS_NAV.filter((n) => path.startsWith(n.to + '/'))
  return hits.sort((a, b) => b.to.length - a.to.length)[0]?.to ?? ''
}

const BOT_SETTINGS = /^\/settings\/bots\/([^/]+)/

// Bot 設定是從哪裡打開的：每次從別的地方進來記一次；同一個 Bot 的分頁之間切換、上一頁回到前一個分頁都不變。
let origin: { bot: string; from: string } | null = null

/** path 是 Bot 設定時，回它是從哪一頁打開的（prev：上一個網址；直接打開的是空字串）。 */
export function botOrigin(path: string, prev: string): string {
  const bot = path.match(BOT_SETTINGS)?.[1]
  if (!bot) return ''
  const same = prev.match(BOT_SETTINGS)?.[1] === bot
  if (!same) origin = { bot, from: prev }
  else if (origin?.bot !== bot) origin = { bot, from: '' }
  return origin?.from ?? ''
}

export type BotBack = { to: string; label: string; /** 要亮的設定導覽項目（從設定的某一頁點進來才有） */ nav: string }

/** Bot 設定的返回：從聊天打開的回聊天；從設定的某一頁點進來的回那一頁；其他（直接打開）回「AI 模型」的 Bot 清單。 */
export function botBack(from: string): BotBack {
  if (from.startsWith('/settings/') && !BOT_SETTINGS.test(from)) return { to: from, label: settingsTitle(from), nav: activeNav(from) }
  if (/^\/bots\/[^/]+(\/threads\/[^/]+)?\/?$/.test(from)) return { to: from, label: '回到對話', nav: '' }
  if (/^\/rooms\/[^/]+\/?$/.test(from)) return { to: from, label: '回到群組', nav: '' }
  return { to: '/settings/models', label: 'AI 模型', nav: '' }
}
