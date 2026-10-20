import { useEffect } from 'react'
import { CalendarDays, Plus, Repeat } from 'lucide-react'
import { linkClick } from '../router'
import { Button, IconButton } from '../ui/Button'
import { PhoneBar } from '../mobile/Phone'
import { usePhone } from '../mobile/nav'
import { Icon } from '../ui/Icon'
import { cx } from '../ui/cx'
import { CalendarView } from './CalendarView'
import { openNewRoutine, startRoutinesFeed } from './data'
import { RoutineDrawer, RoutineList } from './RoutineList'
import { ROUTINES_PATH } from '../paths'

// 定時任務與行事曆（H01、H03；手機 L16）。路徑：/calendar（行事曆）、/calendar/routines（清單）、/calendar/routines/<id>（含執行紀錄）。


function Tab({ to, on, icon, children }: { to: string; on: boolean; icon: typeof Repeat; children: string }) {
  return (
    <a
      href={to}
      onClick={linkClick(to)}
      aria-current={on ? 'page' : undefined}
      className={cx('inline-flex h-8 items-center gap-1.5 rounded-seg px-3 text-ui-sm max-sm:h-10', on ? 'bg-control text-ink' : 'text-ink-2 hover:text-ink')}
    >
      <Icon icon={icon} size={16} />
      {children}
    </a>
  )
}

export function RoutinesPage({ path }: { path: string }) {
  useEffect(() => startRoutinesFeed(), [])
  const m = path.match(ROUTINES_PATH)
  const list = path.startsWith('/calendar/routines')
  const selected = m?.[1]
  const phone = usePhone()
  return (
    <div className="flex min-w-0 flex-1">
      <main className="min-w-0 flex-1 px-6 pb-6 pt-4 max-md:px-4 max-md:pt-0">
        {phone && list && <PhoneBar title="定時任務" back="/calendar" className="-mx-4" right={<IconButton icon={Plus} label="新增定時任務" className="size-11" onClick={() => openNewRoutine()} />} />}
        {/* 行事曆不再是手機的分頁（2026-10-20）：自己畫一條有返回的頁首 */}
        {phone && !list && <PhoneBar title="行事曆" heading={false} back="/chats" className="-mx-4" right={<IconButton icon={Plus} label="新增定時任務" className="size-11" onClick={() => openNewRoutine()} />} />}
        <div className="mx-auto flex max-w-[1180px] flex-col gap-3">
          <header className="flex flex-wrap items-start justify-between gap-3 max-md:hidden">
            <div className="flex flex-col gap-0.5">
              <h1 className="m-0 flex items-center gap-2.5 text-[22px] font-normal">
                <span className="text-accent">
                  <Icon icon={list ? Repeat : CalendarDays} size={24} />
                </span>
                {list ? '定時任務' : '行事曆'}
              </h1>
              <p className="m-0 text-sub text-ink-2">{list ? '也可以直接在對話裡說：「每天早上 9 點整理新聞給我」。' : 'Bot 照排程自己開工，結果貼回和它的對話。'}</p>
            </div>
            <Button variant="primary" icon={Plus} onClick={() => openNewRoutine()}>
              新增定時任務
            </Button>
          </header>
          <nav aria-label="定時任務分頁" className="flex self-start rounded-field bg-inset p-[3px] max-md:hidden">
            <Tab to="/calendar" on={!list} icon={CalendarDays}>
              行事曆
            </Tab>
            <Tab to="/calendar/routines" on={list} icon={Repeat}>
              定時任務
            </Tab>
          </nav>
          {list ? <RoutineList selectedId={selected} /> : <CalendarView />}
        </div>
      </main>
      {list && <RoutineDrawer selectedId={selected} />}
    </div>
  )
}
