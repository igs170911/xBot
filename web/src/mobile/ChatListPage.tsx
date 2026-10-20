import { useMemo, useState, type ReactNode } from 'react'
import { Bot as BotIcon, CalendarClock, CalendarDays, Ellipsis, FolderOpen, KeyRound, LayoutGrid, MessagesSquare, Network, OctagonPause, Plus, Puzzle, Search, Settings, UsersRound, WifiOff, X } from 'lucide-react'
import { botsStore } from '../data/bots'
import { useLastThreads } from '../data/lastThreads'
import { roomsStore } from '../data/rooms'
import { useStore } from '../data/store'
import { openNewTeam, sectionsOf, teamsStore } from '../data/teams'
import { drawerStore } from '../data/ui'
import { navigate } from '../router'
import { Mascot } from '../ui/Avatar'
import { Button, IconButton } from '../ui/Button'
import { Icon } from '../ui/Icon'
import { MenuButton } from '../ui/Menu'
import { cx } from '../ui/cx'
import { BotListRow, ChiefCard, RoomListRow } from '../shell/BotList'
import { usePauseAll } from '../shell/PauseBanner'
import { useRowMenu, type MenuBind, type MoreOpener } from '../shell/useRowMenu'
import { TabHeader } from './Phone'
import { useOnline } from './pwa'

// L01 手機的對話分頁：和側欄同一種排版（2026-10-20 改版 A）：大總管的大卡片在最上面，其他 Bot 照團隊分區，群組在後面；
// 列上有時間、最後一句話、未讀藍點。不常用的頁面收進右上「設定與更多」。

function Group({ title, children, menu, more }: { title: string; children: ReactNode; menu?: MenuBind; more?: MoreOpener }) {
  return (
    <section aria-label={title || undefined} className="flex flex-col">
      {title && (
        <div className="flex items-center pr-3">
          <h2 {...menu} className={cx('m-0 flex-1 px-5 pb-1 pt-3 text-meta text-ink-2', menu && 'select-none [-webkit-touch-callout:none]')}>
            {title}
          </h2>
          {more && <IconButton icon={Ellipsis} label={`${title}的選單`} className="mt-1 size-11 text-ink-3" onClick={(e) => more(e.currentTarget)} />}
        </div>
      )}
      {children}
    </section>
  )
}

const MORE = [
  { to: '/calendar', label: '行事曆與定時任務', icon: CalendarDays },
  { to: '/agenda', label: '提案與討論', icon: CalendarClock },
  { to: '/files', label: '全部檔案', icon: FolderOpen },
  { to: '/apps/logins', label: '分享登入', icon: KeyRound },
  { to: '/apps', label: '連接應用程式', icon: Puzzle },
  { to: '/teams', label: '團隊地圖', icon: LayoutGrid },
  { to: '/org', label: '組織架構', icon: Network },
]

/** L01 對話分頁（/chats）。 */
export function ChatListPage() {
  const { bots, error } = useStore(botsStore)
  const rooms = useStore(roomsStore).rooms
  const teams = useStore(teamsStore).teams
  const last = useLastThreads(bots)
  const pauseAll = usePauseAll()
  const online = useOnline()
  const [q, setQ] = useState<string | null>(null)
  const query = (q ?? '').trim()

  const live = useMemo(() => (bots ?? []).filter((b) => !b.archived), [bots])
  const chief = live.find((b) => b.primary)
  const sections = bots ? sectionsOf(live.filter((b) => b !== chief), teams) : []
  const match = (s: string) => !query || s.toLowerCase().includes(query.toLowerCase())
  const rowMenu = useRowMenu()

  return (
    <main className="flex min-h-0 flex-1 flex-col" aria-label="對話">
      <TabHeader
        lead={
          <button type="button" aria-label="打開側欄" onClick={() => drawerStore.set(true)} className="-ml-2 flex size-11 items-center justify-center rounded-full active:bg-control">
            <Mascot size={26} />
          </button>
        }
        title="對話"
        right={
          <span className="-mr-2 flex items-center">
            <IconButton icon={Search} label="搜尋" className="size-11" onClick={() => setQ((v) => (v === null ? '' : null))} />
            <MenuButton
              label="新增"
              align="end"
              items={[
                { label: '新增 Bot', icon: BotIcon, onSelect: () => navigate('/new/bot') },
                { label: '新增群組', icon: MessagesSquare, onSelect: () => navigate('/new/room') },
                { label: '建立團隊', icon: UsersRound, onSelect: openNewTeam },
              ]}
              trigger={(t) => <IconButton {...t} icon={Plus} label="新增" className="size-11" />}
            />
            <MenuButton
              label="設定與更多"
              align="end"
              items={[
                { label: '設定', icon: Settings, onSelect: () => navigate('/settings') },
                ...MORE.map((m, i) => ({ label: m.label, icon: m.icon, separator: i === 0, onSelect: () => navigate(m.to) })),
                { label: '全部停下（所有 Bot）', icon: OctagonPause, separator: true, onSelect: () => void pauseAll() },
              ]}
              trigger={(t) => <IconButton {...t} icon={Ellipsis} label="設定與更多" className="size-11" />}
            />
          </span>
        }
      />
      {q !== null && (
        <div className="flex items-center gap-2 px-4 pb-2">
          <label className="relative flex flex-1 items-center">
            <span className="sr-only-text">搜尋 Bot 與群組</span>
            <span className="pointer-events-none absolute left-3 text-ink-3">
              <Icon icon={Search} size={18} />
            </span>
            <input autoFocus type="search" value={q} onChange={(e) => setQ(e.target.value)} placeholder="搜尋 Bot 與群組" className="h-11 w-full rounded-field bg-control pl-[38px] pr-3 text-ui text-ink outline-none focus-visible:outline-2 focus-visible:outline-focus" />
          </label>
          <IconButton icon={X} label="關閉搜尋" className="size-11" onClick={() => setQ(null)} />
        </div>
      )}
      {!online && (
        <p role="status" className="mx-4 mb-1 flex items-center gap-2 rounded-field bg-warning-soft px-3 py-2 text-sub text-warning-ink">
          <Icon icon={WifiOff} size={16} />
          網路已離線，恢復後會自動重新連線
        </p>
      )}
      <div className="flex flex-col pb-4">
        {!bots && error && <p className="m-0 px-5 py-2 text-sub text-danger">讀不到 Bot 清單：{error}</p>}
        {bots && live.length === 0 && (
          <div className="mx-4 mt-6 flex flex-col items-center gap-3 rounded-card bg-card px-5 py-8 text-center">
            <Mascot size={48} pose="happy" />
            <p className="m-0 text-body">還沒有 Bot</p>
            <p className="m-0 text-sub text-ink-2">建一個，交代它第一件事。</p>
            <Button variant="primary" icon={Plus} onClick={() => navigate('/new/bot')}>
              建立第一個 Bot
            </Button>
          </div>
        )}
        {chief && !query && (
          <div className="flex justify-center px-4 pb-2 pt-1">
            <ChiefCard bot={chief} last={last[chief.id]} menu={rowMenu.bot(chief)} ring="#262626" />
          </div>
        )}
        {sections.map((sec) => {
          const list = sec.bots.filter((b) => match(b.name))
          // 沒有成員的團隊也列出來（長按標題才刪得掉）；搜尋時只看名稱有符合的
          if (!list.length && !(sec.team && sec.bots.length === 0 && match(sec.title))) return null
          return (
            <Group key={sec.key} title={sections.length === 1 && !sec.team ? '' : sec.title} menu={sec.team ? rowMenu.team(sec.team) : undefined} more={sec.team ? rowMenu.more.team(sec.team) : undefined}>
              {list.map((b) => (
                <BotListRow key={b.id} bot={b} last={last[b.id]} crown={!!sec.team && sec.team.chief_id === b.id} menu={rowMenu.bot(b)} look="phone" ring="#070707" />
              ))}
              {list.length === 0 && (
                <p className="m-0 px-5 py-1 text-sub text-ink-3">{chief && sec.team && chief.team_id === sec.team.id ? `成員只有大總管 ${chief.name}（在最上面）。` : '還沒有成員。'}按右邊的「⋯」可以換成員或刪除。</p>
              )}
            </Group>
          )
        })}
        {rooms && rooms.filter((r) => match(r.name)).length > 0 && (
          <Group title="群組">
            {rooms.filter((r) => match(r.name)).map((r) => (
              <RoomListRow key={r.id} room={r} menu={rowMenu.room(r)} look="phone" ring="#070707" />
            ))}
          </Group>
        )}
        {query && bots && ![...live, ...(rooms ?? [])].some((x) => match(x.name)) && <p className="m-0 px-5 py-6 text-center text-sub text-ink-2">找不到「{query}」</p>}
      </div>
      {rowMenu.ui}
    </main>
  )
}
