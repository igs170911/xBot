import {
  Activity,
  Bed,
  BookOpen,
  Brain,
  Briefcase,
  Bug,
  Calculator,
  CalendarDays,
  ChartCandlestick,
  ChartLine,
  Cloud,
  Code,
  Compass,
  Contact,
  CreditCard,
  Database,
  Folder,
  GitBranch,
  Globe,
  Hash,
  Headphones,
  Image,
  Kanban,
  ListChecks,
  ListTodo,
  Mail,
  Mic,
  Mountain,
  Notebook,
  Palette,
  PenTool,
  Phone,
  Plane,
  Plug,
  Presentation,
  Puzzle,
  Rocket,
  Send,
  SquareCheck,
  Store,
  Table,
  Terminal,
  type LucideIcon,
} from 'lucide-react'
import { Icon } from '../ui/Icon'
import { cx } from '../ui/cx'


// 目錄的圖示名稱 → Lucide 圖示（中性圓形圖示，不放品牌標誌，I01）。
const ICONS: Record<string, LucideIcon> = {
  activity: Activity, bed: Bed, 'book-open': BookOpen, brain: Brain, briefcase: Briefcase, bug: Bug, calculator: Calculator,
  'calendar-days': CalendarDays, 'chart-candlestick': ChartCandlestick, 'chart-line': ChartLine, cloud: Cloud, code: Code,
  compass: Compass, contact: Contact, 'credit-card': CreditCard, database: Database, folder: Folder, 'git-branch': GitBranch,
  globe: Globe, hash: Hash, headphones: Headphones, image: Image, kanban: Kanban, 'list-checks': ListChecks, 'list-todo': ListTodo,
  mail: Mail, mic: Mic, mountain: Mountain, notebook: Notebook, palette: Palette, 'pen-tool': PenTool, phone: Phone, plane: Plane,
  plug: Plug, presentation: Presentation, rocket: Rocket, send: Send, 'check-square': SquareCheck, store: Store, table: Table,
  terminal: Terminal,
}

export function AppIcon({ name, size = 44 }: { name?: string; size?: number }) {
  return (
    <span
      aria-hidden="true"
      className="inline-flex flex-none items-center justify-center rounded-full bg-control text-ink"
      style={{ width: size, height: size }}
    >
      <Icon icon={ICONS[name ?? ''] ?? Puzzle} size={Math.round(size * 0.45)} />
    </span>
  )
}

/** 分段控制（只有一組，I01：全部／已連接）。 */
export function Segmented<T extends string>({
  label,
  value,
  options,
  onChange,
  className,
}: {
  label: string
  value: T
  options: { value: T; label: string }[]
  onChange: (v: T) => void
  className?: string
}) {
  return (
    <div role="radiogroup" aria-label={label} className={cx('inline-flex rounded-[12px] bg-inset p-1', className)}>
      {options.map((o) => (
        <button
          key={o.value}
          type="button"
          role="radio"
          aria-checked={value === o.value}
          onClick={() => onChange(o.value)}
          className={cx(
            'h-8 flex-1 cursor-pointer rounded-[9px] border-0 px-3.5 text-ui-sm whitespace-nowrap',
            value === o.value ? 'bg-control text-ink' : 'bg-transparent text-ink-2 hover:text-ink',
          )}
        >
          {o.label}
        </button>
      ))}
    </div>
  )
}

