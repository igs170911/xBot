import { useEffect, useState } from 'react'
import { Construction } from 'lucide-react'
import { authStatus } from './api/generated'
import { usePath } from './router'
import { AppShell } from './shell/AppShell'
import { InboxPage } from './pages/InboxPage'
import { DevComponentsPage } from './pages/DevComponentsPage'
import { LoginPage } from './pages/LoginPage'
import { NewBotDialog } from './pages/NewBotDialog'
import { SettingsPage } from './settings/SettingsPage'
import { NewRoutineDialog } from './routines/NewRoutineDialog'
import { ROUTINES_PATH, RoutinesPage } from './routines/RoutinesPage'
import { ChatPage } from './chat/ChatPage'
import { ComputerPage } from './computer/ComputerPage'
import { OfficePage } from './computer/OfficePage'
import { registerActivityCard, registerPeerCard } from './chat/peer'
import { registerChatCards } from './files/register'
import { BOT_FILES_PATH, BotFilesPage } from './files/BotFilesPage'
import { FilesPage } from './files/FilesPage'
import { NewRoomDialog } from './rooms/NewRoomDialog'
import { RoomPage } from './rooms/RoomPage'
import { NewTeamDialog } from './teams/NewTeamDialog'
import { TeamMapPage } from './teams/TeamMapPage'
import { OrgPage } from './teams/OrgPage'
import { AgendaPage } from './agenda/AgendaPage'
import { APPS_PATH, AppsPage } from './apps/AppsPage'
import { McpPage } from './apps/McpPage'
import { LoginsPage } from './apps/LoginsPage'
import { startTeams } from './data/teams'
import { roomsStore, startRooms } from './data/rooms'
import { startChat } from './chat/chatStore'
import { botsStore, startBots } from './data/bots'
import { startEvents } from './data/events'
import { inboxStore, startInbox } from './data/inbox'
import { useStore } from './data/store'
import { ToastProvider } from './ui/Toast'
import { Icon } from './ui/Icon'
import { Onboarding } from './onboard/Onboarding'
import { WelcomePage as FirstBotPage } from './onboard/Welcome'
import { Tour } from './onboard/Tour'
import { loadOnboard, onboardStore } from './onboard/state'
import { ImportBotDialog } from './settings/bot/share'
import { ChatListPage } from './mobile/ChatListPage'
import { NotificationsPage } from './mobile/NotificationsPage'
import { PairPage } from './mobile/PairPage'
import { BotProfilePage } from './mobile/BotProfilePage'
import { NewBotPage, NewRoomPage } from './mobile/NewPages'
import { SharePage } from './mobile/SharePage'
import { ImportPage } from './onboard/ImportPage'
import { CallPage } from './mobile/CallPage'
import { WelcomePage } from './mobile/WelcomePage'

const TITLES: Record<string, string> = {
  '/office': '辦公室',
  '/chats': '對話',
  '/notifications': '通知',
  '/share': '分享給 Bot',
  '/new/bot': '新增 Bot',
  '/new/room': '新增群組',
  '/welcome': '第一次設定',
  '/import': '帶入舊設定',
  '/agenda': '等你的事',
  '/teams': '團隊地圖',
  '/apps': '連接應用程式',
  '/apps/mcp': '自訂連接（MCP）',
  '/apps/logins': '網站登入',
  '/org': '組織架構',
  '/files': '全部檔案',
  '/settings': '設定',
  '/settings/agents': '已連線的代理',
}

const BOT_PATH = /^\/bots\/([^/]+)(?:\/threads\/([^/]+))?\/?$/
const COMPUTER_PATH = /^\/bots\/([^/]+)\/computer\/?$/
const ROOM_PATH = /^\/rooms\/([^/]+)\/?$/
const PROFILE_PATH = /^\/bots\/([^/]+)\/profile\/?$/
const CALL_PATH = /^\/bots\/([^/]+)\/call\/?$/

// 對話裡 kind=peer 的那一行（和隊友的往來，C13）。
registerPeerCard()
registerActivityCard()
// 對話裡 kind=card 的卡片（Bot 發的資訊、核准、檔案卡，C11、C33）。
registerChatCards()

function Placeholder({ title }: { title: string }) {
  return (
    <main className="flex flex-1 flex-col items-center justify-center gap-3 p-8 text-center">
      <span className="text-ink-3">
        <Icon icon={Construction} size={32} />
      </span>
      <h1 className="m-0 text-page">{title}</h1>
      <p className="m-0 text-sub text-ink-2">這個畫面還在做。</p>
    </main>
  )
}

type Auth = 'checking' | 'ok' | 'login'

/** 先問服務有沒有登入（本機開啟不用登入）；沒登入就顯示登入頁。 */
function useAuth(): [Auth, () => void] {
  const [auth, setAuth] = useState<Auth>('checking')
  useEffect(() => {
    authStatus()
      .then((s) => setAuth(s.authenticated ? 'ok' : 'login'))
      // 連不到服務：照樣顯示畫面，各頁會顯示連不到
      .catch(() => setAuth('ok'))
  }, [])
  return [auth, () => setAuth('ok')]
}

function Main() {
  const path = usePath()
  const bots = useStore(botsStore).bots
  useEffect(() => {
    startEvents()
    startBots()
    startInbox()
    startChat()
    startRooms()
    startTeams()
    void loadOnboard()
  }, [])
  const rooms = useStore(roomsStore).rooms
  const onboard = useStore(onboardStore)
  const inbox = useStore(inboxStore).items

  const bot = path.match(BOT_PATH)
  const computer = path.match(COMPUTER_PATH)
  const room = path.match(ROOM_PATH)
  const botFiles = path.match(BOT_FILES_PATH)
  const profile = path.match(PROFILE_PATH)
  const call = path.match(CALL_PATH)
  const botName = (id: string) => bots?.find((b) => b.id === id)?.name
  const title = path === '/' ? '待我處理' : botFiles ? `${botName(botFiles[1]) ?? 'Bot'}的檔案` : bot ? (botName(bot[1]) ?? '對話') : computer ? `${botName(computer[1]) ?? 'Bot'}的電腦` : room ? (rooms?.find((r) => r.id === room[1])?.name ?? '群組對話') : (TITLES[path] ?? (path.startsWith('/settings') ? '設定' : path.startsWith('/calendar') ? '定時任務與行事曆' : 'XBot'))

  // 首次設定（A03、A04）：還沒有任何 Bot、也還沒做完首次設定時，整頁顯示；讀狀態的那一下先不畫，免得閃一下首頁。
  if (onboard.status === null) return null
  if (onboard.status !== 'off' && onboard.status.first_run && !onboard.skipped) {
    return <Onboarding onDone={() => onboardStore.set((o) => ({ ...o, skipped: true }))} />
  }
  const noBots = !!bots && bots.every((b) => b.archived) && inbox !== null && inbox.length === 0

  let page
  if (path === '/' && noBots) page = <FirstBotPage />
  else if (path === '/') page = <InboxPage />
  else if (path === '/chats') page = <ChatListPage />
  else if (path === '/office') page = <OfficePage />
  else if (path === '/notifications') page = <NotificationsPage />
  else if (path === '/share') page = <SharePage />
  else if (path === '/new/bot') page = <NewBotPage />
  else if (path === '/new/room') page = <NewRoomPage />
  else if (path === '/welcome') page = <WelcomePage />
  else if (path === '/import') page = <ImportPage />
  else if (profile) page = <BotProfilePage key={profile[1]} botId={profile[1]} />
  else if (call) page = <CallPage key={call[1]} botId={call[1]} />
  else if (computer) page = <ComputerPage key={computer[1]} botId={computer[1]} />
  else if (botFiles) page = <BotFilesPage key={botFiles[1]} botId={botFiles[1]} />
  else if (path === '/files') page = <FilesPage />
  else if (bot) page = <ChatPage key={bot[1]} botId={bot[1]} threadParam={bot[2]} />
  else if (ROUTINES_PATH.test(path)) page = <RoutinesPage path={path} />
  else if (room) page = <RoomPage key={room[1]} roomId={room[1]} />
  else if (path === '/teams') page = <TeamMapPage />
  else if (path === '/org') page = <OrgPage />
  else if (path === '/agenda') page = <AgendaPage />
  else if (APPS_PATH.test(path))
    page = path.startsWith('/apps/mcp') ? <McpPage /> : path.startsWith('/apps/logins') ? <LoginsPage /> : <AppsPage />
  else if (path === '/settings' || path.startsWith('/settings/'))
    page = <SettingsPage path={path === '/settings/agents' ? '/settings/advanced/agents' : path} />
  else page = <Placeholder title={title} />

  return (
    <AppShell path={path} title={title}>
      {page}
      <NewBotDialog />
      <NewRoutineDialog />
      <NewRoomDialog />
      <NewTeamDialog />
      <ImportBotDialog />
      <Tour />
    </AppShell>
  )
}

export default function App() {
  const path = usePath()
  const [auth, loggedIn] = useAuth()
  // 手機掃 QR 打開的配對頁（L09）：還沒登入也要能用
  if (path === '/pair') return <PairPage />
  if (path === '/dev/components') {
    return (
      <ToastProvider>
        <DevComponentsPage />
      </ToastProvider>
    )
  }
  if (auth === 'checking') return null
  if (auth === 'login') return <LoginPage onLoggedIn={loggedIn} />
  return (
    <ToastProvider>
      <Main />
    </ToastProvider>
  )
}
