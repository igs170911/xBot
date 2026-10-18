import { SquareTerminal } from 'lucide-react'
import { Icon } from './Icon'
import { cx } from './cx'

/** 「透過 MCP」這種出口名稱：mcp → MCP；空白也當成 MCP。 */
function viaLabel(via?: string): string {
  return !via || via === 'mcp' ? 'MCP' : via.toUpperCase()
}

/** 代理透過 MCP 做的事（MCP-13，design/C16）：「由 Claude Code（MCP）修改」。 */
export function AgentMark({ name, via, verb = '修改', className }: { name: string; via?: string; verb?: string; className?: string }) {
  return (
    <span data-testid="agent-mark" className={cx('inline-flex items-center gap-1.5 text-meta text-ink-3', className)}>
      <Icon icon={SquareTerminal} size={14} />
      由 {name}（{viaLabel(via)}）{verb}
    </span>
  )
}
