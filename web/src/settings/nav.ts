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

/** 這個網址屬於哪個導覽項目（子頁也算）。 */
export function activeNav(path: string): string {
  if (path.startsWith('/settings/bots/')) return '/settings/models'
  const exact = SETTINGS_NAV.find((n) => n.to === path)
  if (exact) return exact.to
  const hits = SETTINGS_NAV.filter((n) => path.startsWith(n.to + '/'))
  return hits.sort((a, b) => b.to.length - a.to.length)[0]?.to ?? ''
}

