import { useEffect, useRef, useState } from 'react'
import { cx } from '../ui/cx'

const BARS = [6, 12, 18, 10, 16, 8, 14, 6]

/** 試聽中、說話中的小聲波（K02、L18）：固定的高度，live 時一高一低地動。 */
export function Wave({ live = true, bars = BARS, color = 'var(--color-accent-ink)', className }: { live?: boolean; bars?: number[]; color?: string; className?: string }) {
  return (
    <span className={cx('flex h-6 flex-none items-center gap-[3px]', className)} aria-hidden="true">
      {bars.map((h, i) => (
        <span key={i} className={cx('w-[3px] rounded-full', live && 'animate-pulse')} style={{ height: h, background: color, animationDelay: `${(i % 4) * 120}ms` }} />
      ))}
    </span>
  )
}

/** 跟著麥克風音量動的聲波（錄音中、正在聽）。level 每一格畫面讀一次（0～1）。 */
export function LevelMeter({ level, bars = 24, color = 'var(--color-ink)', height = 28, className }: { level: () => number; bars?: number; color?: string; height?: number; className?: string }) {
  const [hist, setHist] = useState<number[]>(() => Array(bars).fill(0))
  const fn = useRef(level)
  useEffect(() => {
    fn.current = level
  }, [level])
  useEffect(() => {
    let raf = 0
    let last = 0
    const tick = (t: number) => {
      if (t - last > 70) {
        last = t
        const v = Math.min(1, fn.current() * 6)
        setHist((h) => [...h.slice(1), v])
      }
      raf = requestAnimationFrame(tick)
    }
    raf = requestAnimationFrame(tick)
    return () => cancelAnimationFrame(raf)
  }, [])
  return (
    <span className={cx('flex min-w-0 items-center gap-[3px] overflow-hidden', className)} style={{ height }} aria-hidden="true">
      {hist.map((v, i) => (
        <span key={i} className="w-[3px] flex-none rounded-full" style={{ height: Math.max(4, v * height), background: color, opacity: 0.45 + v * 0.55 }} />
      ))}
    </span>
  )
}
