import { useEffect, useState } from 'react'
import { ChevronLeft, ChevronRight, Moon, Plus, TriangleAlert } from 'lucide-react'
import { routinesCalendar, type RoutinesCalendarOutput } from '../api/generated'
import { botsStore, type Bot, look } from '../data/bots'
import { useStore } from '../data/store'
import { Avatar } from '../ui/Avatar'
import { COLORS, resolveColor } from '../ui/avatar-data'
import { Button, IconButton } from '../ui/Button'
import { SegmentedControl } from '../ui/Controls'
import { Icon } from '../ui/Icon'
import { Select } from '../ui/Menu'
import { StatusChip } from '../ui/Pill'
import { cx } from '../ui/cx'
import { RUN_STATUS, TRIGGER_LABEL, clockText, dateText, openNewRoutine, routinesStore } from './data'

// 行事曆（H01 週、L16 手機日）：07:00–21:00，定時任務用 Bot 的顏色；過去的是執行紀錄，未來的是排程。

type Item = RoutinesCalendarOutput['items'][number]

const START_H = 7
const END_H = 21
const HOUR_PX = 48
const WD = ['一', '二', '三', '四', '五', '六', '日']

function weekStart(d: Date): Date {
  const x = new Date(d.getFullYear(), d.getMonth(), d.getDate())
  x.setDate(x.getDate() - ((x.getDay() + 6) % 7))
  return x
}

function addDays(d: Date, n: number): Date {
  const x = new Date(d)
  x.setDate(x.getDate() + n)
  return x
}

const sameDay = (a: Date, b: Date) => a.getFullYear() === b.getFullYear() && a.getMonth() === b.getMonth() && a.getDate() === b.getDate()

function useNarrow(): boolean {
  const q = '(max-width: 767px)'
  const [narrow, setNarrow] = useState(() => typeof window !== 'undefined' && window.matchMedia?.(q).matches)
  useEffect(() => {
    const m = window.matchMedia?.(q)
    if (!m) return
    const on = () => setNarrow(m.matches)
    m.addEventListener('change', on)
    return () => m.removeEventListener('change', on)
  }, [])
  return !!narrow
}

/** 同一天同一個定時任務太多次（例：每 5 分鐘）只畫第一格，標「＋N 次」。 */
function collapse(items: Item[]): Array<Item & { more: number }> {
  const out: Array<Item & { more: number }> = []
  const seen = new Map<string, Item & { more: number }>()
  for (const it of items) {
    const d = new Date(it.at)
    const key = `${it.routine_id}|${d.toDateString()}|${it.status === 'scheduled' ? 's' : 'r'}`
    const prev = seen.get(key)
    const count = items.filter((x) => x.routine_id === it.routine_id && new Date(x.at).toDateString() === d.toDateString()).length
    if (count > 6 && prev) {
      prev.more++
      continue
    }
    const v = { ...it, more: 0 }
    seen.set(key, v)
    out.push(v)
  }
  return out
}

function EventBlock({ it, bot, compact }: { it: Item & { more: number }; bot?: Bot; compact?: boolean }) {
  const d = new Date(it.at)
  const color = COLORS[resolveColor(bot?.color)]
  const st = RUN_STATUS[it.status] ?? RUN_STATUS.scheduled
  const failed = it.status === 'failed'
  const past = it.status !== 'scheduled'
  return (
    <div
      data-testid="cal-event"
      data-status={it.status}
      title={`${clockText(d)} · ${it.title}${bot ? ' · ' + bot.name : ''}`}
      className={cx('flex h-full min-w-0 flex-col overflow-hidden rounded-[8px] px-2 py-0.5 text-[12px] leading-[1.3] text-ink', failed && 'outline outline-[1.5px] outline-danger')}
      style={{ background: `color-mix(in srgb, ${color} ${past ? 22 : 34}%, var(--color-panel))`, borderLeft: `3px solid ${color}` }}
    >
      <span className="truncate">{it.title}</span>
      {!compact && (
        <span className="truncate text-[11px] text-ink-2">
          {clockText(d)}
          {past && ` · ${failed ? '失敗' : st.label}`}
          {it.more > 0 && ` · ＋${it.more} 次`}
        </span>
      )}
    </div>
  )
}

function TimeGrid({ days, items, bots, now, onEmpty }: { days: Date[]; items: Item[]; bots: Bot[]; now: Date; onEmpty: (d: Date) => void }) {
  const height = (END_H - START_H) * HOUR_PX
  const top = (d: Date) => {
    const m = (d.getHours() - START_H) * 60 + d.getMinutes()
    return Math.max(0, Math.min(height - 22, (m / 60) * HOUR_PX))
  }
  return (
    <div className="flex min-w-0 flex-col overflow-hidden rounded-card bg-panel">
      <div className="grid border-b border-divider" style={{ gridTemplateColumns: `52px repeat(${days.length}, minmax(0, 1fr))` }}>
        <span />
        {days.map((d) => {
          const today = sameDay(d, now)
          return (
            <div key={d.toISOString()} className={cx('flex items-baseline gap-1.5 px-2 py-2.5', today && 'bg-inset')}>
              <span className="text-meta text-ink-3">週{WD[(d.getDay() + 6) % 7]}</span>
              <span className={cx('text-ui', today && 'text-accent-ink')}>{d.getDate()}</span>
            </div>
          )
        })}
      </div>
      <div className="relative grid" style={{ gridTemplateColumns: `52px repeat(${days.length}, minmax(0, 1fr))`, height }}>
        <div className="relative">
          {Array.from({ length: END_H - START_H }, (_, i) => (
            <span key={i} className="absolute right-2 -translate-y-1/2 text-[11px] text-ink-3" style={{ top: i * HOUR_PX }}>
              {i === 0 ? '' : `${String(START_H + i).padStart(2, '0')}:00`}
            </span>
          ))}
        </div>
        {days.map((d) => {
          const today = sameDay(d, now)
          const inDay = items.filter((it) => sameDay(new Date(it.at), d))
          const inRange = (it: Item) => {
            const h = new Date(it.at).getHours()
            return h >= START_H && h < END_H
          }
          const mine = collapse(inDay.filter(inRange))
          const outside = inDay.length - inDay.filter(inRange).length
          // 同一時段多個：並排縮窄（每格算 30 分鐘、一格高）。
          const lanes: number[] = []
          const lane = mine.map((it) => {
            let i = lanes.findIndex((end) => end <= it.at)
            if (i < 0) i = lanes.push(0) - 1
            lanes[i] = it.at + 30 * 60_000
            return i
          })
          const width = 100 / Math.max(1, lanes.length)
          return (
            <div
              key={d.toISOString()}
              data-testid="cal-day"
              className={cx('relative border-l border-divider', today && 'bg-inset/60')}
              onClick={(e) => {
                if (e.target !== e.currentTarget) return
                const y = e.clientY - e.currentTarget.getBoundingClientRect().top
                const mins = Math.round(((y / HOUR_PX) * 60) / 30) * 30
                const at = new Date(d)
                at.setHours(START_H, 0, 0, 0)
                at.setMinutes(mins)
                onEmpty(at)
              }}
            >
              {Array.from({ length: END_H - START_H }, (_, i) => (
                <span key={i} className="pointer-events-none absolute inset-x-0 border-t border-divider/70" style={{ top: i * HOUR_PX }} />
              ))}
              {today && now.getHours() >= START_H && now.getHours() < END_H && (
                <span role="img" aria-label="現在" className="pointer-events-none absolute inset-x-0 z-10 flex items-center" style={{ top: top(now) }}>
                  <span className="-ml-1 size-2 rounded-full bg-danger" />
                  <span className="h-[1.5px] flex-1 bg-danger" />
                </span>
              )}
              {mine.map((it, i) => (
                <div
                  key={(it.run_id ?? '') + it.routine_id + it.at}
                  className="absolute px-0.5"
                  style={{ top: top(new Date(it.at)), height: HOUR_PX / 2 - 1, left: `${lane[i] * width}%`, width: `${width}%` }}
                >
                  <EventBlock it={it} bot={bots.find((b) => b.id === it.bot_id)} compact />
                </div>
              ))}
              {outside > 0 && (
                <span className="pointer-events-none absolute left-1 right-1 top-1 truncate text-[11px] text-ink-3">07:00 前、21:00 後還有 {outside} 件</span>
              )}
            </div>
          )
        })}
      </div>
    </div>
  )
}

function ListView({ items, bots }: { items: Item[]; bots: Bot[] }) {
  const routines = useStore(routinesStore).routines ?? []
  const groups = new Map<string, Item[]>()
  for (const it of collapse(items)) {
    const k = new Date(it.at).toDateString()
    groups.set(k, [...(groups.get(k) ?? []), it])
  }
  if (groups.size === 0) return <p className="m-0 rounded-card bg-panel px-5 py-8 text-center text-sub text-ink-2">這段時間沒有定時任務。</p>
  return (
    <div className="flex flex-col gap-1 rounded-card bg-panel px-5 py-3" data-testid="cal-list">
      {[...groups.entries()].map(([k, list]) => (
        <section key={k} className="flex flex-col">
          <h3 className="m-0 border-b border-divider py-2.5 text-meta font-normal text-ink-3">{dateText(new Date(list[0].at))}</h3>
          {list.map((it) => {
            const bot = bots.find((b) => b.id === it.bot_id)
            const r = routines.find((x) => x.id === it.routine_id)
            const st = RUN_STATUS[it.status] ?? RUN_STATUS.scheduled
            return (
              <div key={(it.run_id ?? '') + it.routine_id + it.at} className="flex items-center gap-3 border-b border-divider/60 py-2.5 last:border-b-0">
                <span className="w-[88px] flex-none text-sub text-ink-2">{clockText(new Date(it.at))}</span>
                {bot && <Avatar {...look(bot)} size={28} dot="none" />}
                <span className="flex min-w-0 flex-1 flex-col">
                  <span className="truncate text-ui">{it.title}</span>
                  <span className="truncate text-meta text-ink-3">
                    {[bot?.name, r?.when, it.trigger && it.trigger !== 'schedule' ? TRIGGER_LABEL[it.trigger] : null].filter(Boolean).join(' · ')}
                  </span>
                </span>
                <StatusChip size="sm" tone={st.tone === 'accent' ? 'accent' : st.tone}>
                  {st.label}
                </StatusChip>
              </div>
            )
          })}
        </section>
      ))}
    </div>
  )
}

export function CalendarView() {
  const bots = useStore(botsStore).bots ?? []
  const narrow = useNarrow()
  const [anchor, setAnchor] = useState(() => new Date())
  const [mode, setMode] = useState<'week' | 'list'>('week')
  const [botFilter, setBotFilter] = useState('')
  const [items, setItems] = useState<Item[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const routines = useStore(routinesStore).routines
  const now = new Date()
  const start = narrow ? new Date(anchor.getFullYear(), anchor.getMonth(), anchor.getDate()) : weekStart(anchor)
  const week = weekStart(anchor)
  const startMs = start.getTime()
  const weekMs = week.getTime()

  useEffect(() => {
    let live = true
    routinesCalendar({ from: weekMs, to: addDays(new Date(weekMs), 7).getTime(), bot_id: botFilter || undefined })
      .then((out) => live && (setItems(out.items), setError(null)))
      .catch((e: unknown) => live && setError(e instanceof Error ? e.message : String(e)))
    return () => {
      live = false
    }
  }, [weekMs, botFilter, routines])

  const days = narrow ? [new Date(startMs)] : Array.from({ length: 7 }, (_, i) => addDays(new Date(weekMs), i))
  const failed = (items ?? []).filter((i) => i.status === 'failed').length
  const end = addDays(week, 6)
  const range = `${week.getFullYear()} 年 ${week.getMonth() + 1} 月 ${week.getDate()} – ${week.getMonth() === end.getMonth() ? '' : `${end.getMonth() + 1} 月 `}${end.getDate()} 日`
  const step = narrow ? 1 : 7
  const shownItems = (items ?? []).filter((i) => !narrow || mode === 'list' || sameDay(new Date(i.at), days[0]))

  const dayCount = shownItems.length
  return (
    <div className="flex flex-col gap-3">
      {narrow && (
        // 手機（L16）：月份大標＋「今天」＋「＋」，下面一週日期列
        <div className="flex items-center gap-1 pt-[max(6px,env(safe-area-inset-top))]">
          <h1 className="m-0 min-w-0 flex-1 whitespace-nowrap text-[22px] font-normal" data-testid="cal-range">
            {days[0].getFullYear()} 年 {days[0].getMonth() + 1} 月
          </h1>
          <IconButton icon={ChevronLeft} label="上一週" className="size-11" onClick={() => setAnchor((a) => addDays(a, -7))} />
          <IconButton icon={ChevronRight} label="下一週" className="size-11" onClick={() => setAnchor((a) => addDays(a, 7))} />
          <Button className="min-h-11 rounded-full px-4" onClick={() => setAnchor(new Date())}>
            今天
          </Button>
          <IconButton icon={Plus} label="新增定時任務" className="size-11 rounded-full border border-divider bg-panel text-ink" onClick={() => openNewRoutine()} />
        </div>
      )}
      <div className={cx('flex flex-wrap items-center gap-2.5', narrow && 'hidden')}>
        <div className="flex h-10 items-center rounded-field border border-line px-1">
          <IconButton icon={ChevronLeft} label={narrow ? '前一天' : '上一週'} onClick={() => setAnchor((a) => addDays(a, -step))} />
          <Button variant="ghost" size="sm" className="text-ink" onClick={() => setAnchor(new Date())}>
            今天
          </Button>
          <IconButton icon={ChevronRight} label={narrow ? '後一天' : '下一週'} onClick={() => setAnchor((a) => addDays(a, step))} />
        </div>
        {!narrow && (
          <h2 className="m-0 text-[17px] font-normal" data-testid="cal-range">
            {range}
          </h2>
        )}
        <span className="flex-1" />
        {failed > 0 && (
          <StatusChip tone="danger" icon={TriangleAlert}>
            本週 {failed} 次失敗
          </StatusChip>
        )}
        <Select
          label="篩選 Bot"
          hideLabel
          look="compact"
          value={botFilter}
          options={[{ value: '', label: '所有 Bot' }, ...bots.map((b) => ({ value: b.id, label: b.name }))]}
          onChange={setBotFilter}
          className="w-[150px]"
        />
        <SegmentedControl
          label="檢視"
          value={mode}
          onChange={setMode}
          options={[
            { value: 'week', label: narrow ? '日' : '週' },
            { value: 'list', label: '清單' },
          ]}
        />
      </div>
      {narrow && mode === 'week' && (
        <div className="-mx-4 grid grid-cols-7 gap-1 border-b border-divider px-2 pb-2" role="tablist" aria-label="這一週">
          {Array.from({ length: 7 }, (_, i) => addDays(week, i)).map((d, i) => {
            const on = sameDay(d, days[0])
            const has = (items ?? []).some((it) => sameDay(new Date(it.at), d))
            return (
              <button
                key={d.toISOString()}
                type="button"
                role="tab"
                aria-selected={on}
                onClick={() => setAnchor(d)}
                className="flex min-h-[64px] flex-col items-center gap-1 bg-transparent py-1 text-ink"
              >
                <span className="text-[11px] text-ink-3">{WD[i]}</span>
                <span className={cx('flex size-9 items-center justify-center rounded-full text-[17px]', on ? 'bg-ink text-on-ink' : sameDay(d, now) ? 'text-accent-ink' : 'text-ink-2')}>{d.getDate()}</span>
                <span className={cx('size-1 rounded-full', has ? 'bg-ink-3' : 'bg-transparent')} />
              </button>
            )
          })}
        </div>
      )}
      {narrow && mode === 'week' && (
        <p className="m-0 text-ui">
          {days[0].getMonth() + 1} 月 {days[0].getDate()} 日（{WD[(days[0].getDay() + 6) % 7]}）{sameDay(days[0], now) && ' · 今天'}
          <span className="pl-2 text-sub text-ink-3">{dayCount} 件</span>
        </p>
      )}
      {error && (
        <p role="alert" className="m-0 text-sub text-danger">
          {error}
        </p>
      )}
      {mode === 'week' ? (
        <TimeGrid
          days={days}
          items={shownItems}
          bots={bots}
          now={now}
          onEmpty={(at) => openNewRoutine(`${at.getMonth() + 1}月${at.getDate()}日 ${String(at.getHours()).padStart(2, '0')}:${String(at.getMinutes()).padStart(2, '0')} `)}
        />
      ) : (
        <ListView items={items ?? []} bots={bots} />
      )}
      <div className="flex items-center gap-3 rounded-card bg-panel px-5 py-3.5 text-sub text-ink-2 max-md:hidden">
        <Icon icon={Moon} size={18} />
        這台主機要開著，定時任務才會跑。休眠或關機時錯過的，恢復後合併補跑一次。
      </div>
    </div>
  )
}
