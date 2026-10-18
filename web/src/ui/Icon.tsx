import type { LucideIcon } from 'lucide-react'

export type IconProps = {
  icon: LucideIcon
  /** 預設 20；選單 18；行內 14–16。 */
  size?: number
  /** 預設 1.75（全站統一）。 */
  strokeWidth?: number
  /** 有意義的圖示才給 label；裝飾用的會對輔助技術隱藏。 */
  label?: string
  className?: string
  color?: string
}

// Lucide 圖示：線寬 1.75、20 px（N03）。
export function Icon({ icon: Glyph, size = 20, strokeWidth = 1.75, label, className, color }: IconProps) {
  return (
    <Glyph
      size={size}
      strokeWidth={strokeWidth}
      color={color}
      className={className}
      style={{ flex: 'none', display: 'block' }}
      aria-hidden={label ? undefined : true}
      aria-label={label}
      role={label ? 'img' : undefined}
      focusable="false"
    />
  )
}
