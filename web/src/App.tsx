import { lazy, Suspense, useEffect, useState, type ComponentType } from 'react'
import { Construction } from 'lucide-react'
import { authStatus } from './api/generated'
import { navigate, usePath } from './router'
import { startPause } from './data/pause'
import { useNarrow } from './files/useNarrow'
import { SettingsModal } from './shell/SettingsModal'
import { AppShell } from './shell/AppShell'
import { InboxPage } from './pages/InboxPage'
import { LoginPage } from './pages/LoginPage'
import { ChatPage } from './chat/ChatPage'
import { registerActivityCard, registerPeerCard } from './chat/peer'
import { registerChatCards } from './files/register'
import { APPS_PATH, BOT_FILES_PATH, ROUTINES_PATH, VOICE_PATH } from './paths'
import { startTeams, newTeamDialog } from './data/teams'
import { roomsStore, startRooms, newRoomDialog } from './data/rooms'
import { startChat } from './chat/chatStore'
import { botsStore, startBots } from './data/bots'
import { startEvents } from './data/events'
import { inboxStore, startInbox } from './data/inbox'
import { newBotDialog } from './data/ui'
import { useStore, type Store } from './data/store'
import { ToastProvider } from './ui/Toast'
import { Icon } from './ui/Icon'
import { WelcomePage as FirstBotPage } from './onboard/Welcome'
import { loadOnboard, onboardStore } from './onboard/state'
import { newRoutineDialog } from './routines/data'
import { teachStartStore } from './teaching/teachingData'
import { importBotDialog } from './settings/bot/import-store'
import { ScrollDiag } from './shell/ScrollDiag'
import { startAppUpdate, UpdateBanner } from './shell/appUpdate'

// v1.0 效能：待我處理與對話（最常開的兩頁）跟著主程式載入；其他頁面與對話框按需載入（React.lazy），
// 第一次打開時才下載那一頁的程式。
type PropsOf<C> = C extends (props: infer P) => unknown ? P : never
function page<M, K extends keyof M>(load: () => Promise<M>, name: K) {
  return lazy(() => load().then((m) => ({ default: m[name] as ComponentType<PropsOf<M[K]>> })))
}
const SettingsPage = page(() => import('./settings/SettingsPage'), 'SettingsPage')
const RoutinesPage = page(() => import('./routines/RoutinesPage'), 'RoutinesPage')
const NewRoutineDialog = page(() => import('./routines/NewRoutineDialog'), 'NewRoutineDialog')
const ComputerPage = page(() => import('./computer/ComputerPage'), 'ComputerPage')
const OfficePage = page(() => import('./computer/OfficePage'), 'OfficePage')
const BotFilesPage = page(() => import('./files/BotFilesPage'), 'BotFilesPage')
const FilesPage = page(() => import('./files/FilesPage'), 'FilesPage')
const NewBotDialog = page(() => import('./pages/NewBotDialog'), 'NewBotDialog')
const DevComponentsPage = page(() => import('./pages/DevComponentsPage'), 'DevComponentsPage')
const NewRoomDialog = page(() => import('./rooms/NewRoomDialog'), 'NewRoomDialog')
const RoomPage = page(() => import('./rooms/RoomPage'), 'RoomPage')
const NewTeamDialog = page(() => import('./teams/NewTeamDialog'), 'NewTeamDialog')
const TeamMapPage = page(() => import('./teams/TeamMapPage'), 'TeamMapPage')
const OrgPage = page(() => import('./teams/OrgPage'), 'OrgPage')
const TeachStartDialog = page(() => import('./teaching/TeachStartDialog'), 'TeachStartDialog')
const AgendaPage = page(() => import('./agenda/AgendaPage'), 'AgendaPage')
const AppsPage = page(() => import('./apps/AppsPage'), 'AppsPage')
const McpPage = page(() => import('./apps/McpPage'), 'McpPage')
const LoginsPage = page(() => import('./apps/LoginsPage'), 'LoginsPage')
const Onboarding = page(() => import('./onboard/Onboarding'), 'Onboarding')
const Tour = page(() => import('./onboard/Tour'), 'Tour')
const ImportBotDialog = page(() => import('./settings/bot/share'), 'ImportBotDialog')
const ChatListPage = page(() => import('./mobile/ChatListPage'), 'ChatListPage')
const NotificationsPage = page(() => import('./mobile/NotificationsPage'), 'NotificationsPage')
const PairPage = page(() => import('./mobile/PairPage'), 'PairPage')
const BotProfilePage = page(() => import('./mobile/BotProfilePage'), 'BotProfilePage')
const NewBotPage = page(() => import('./mobile/NewPages'), 'NewBotPage')
const NewRoomPage = page(() => import('./mobile/NewPages'), 'NewRoomPage')
const SharePage = page(() => import('./mobile/SharePage'), 'SharePage')
const CallPage = page(() => import('./voice/CallPage'), 'CallPage')
const VoiceSetupPage = page(() => import('./voice/VoiceSetupPage'), 'VoiceSetupPage')
const WelcomePage = page(() => import('./mobile/WelcomePage'), 'WelcomePage')

/** 第一次打開之後才掛上（之後一直掛著，關閉時的焦點歸還照舊）：對話框的程式等你要用時才下載。 */
function MountWhen<T>({ store, open, children }: { store: Store<T>; open: (v: T) => boolean; children: React.ReactNode }) {
  const v = useStore(store)
  const [seen, setSeen] = useState(false)
  if (!seen && open(v)) setSeen(true)
  return seen || open(v) ? <Suspense fallback={null}>{children}</Suspense> : null
}

/** 頁面載入中：保留版面，不閃。 */
function PageLoading() {
  return <main className="flex flex-1" aria-busy="true" />
}

const TITLES: Record<string, string> = {
  '/office': '辦公室',
  '/chats': '對話',
  '/notifications': '通知',
  '/share': '分享給 Bot',
  '/new/bot': '新增 Bot',
  '/new/room': '新增群組',
  '/welcome': '第一次設定',
  '/agenda': '等你的事',
  '/teams': '團隊地圖',
  '/apps': '連接應用程式',
  '/apps/mcp': '自訂連接（MCP）',
  '/apps/logins': '分享登入',
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
  const narrow = useNarrow()
  // 打開設定之前所在的頁面（桌面的設定視窗後面顯示它，關掉就回去）
  const [bgPath, setBgPath] = useState('/')
  if (!(path === '/settings' || path.startsWith('/settings/')) && bgPath !== path) setBgPath(path)
  const bots = useStore(botsStore).bots
  useEffect(() => {
    startEvents()
    startBots()
    startInbox()
    startPause()
    startChat()
    startRooms()
    startTeams()
    startAppUpdate()
    void loadOnboard()
  }, [])
  const rooms = useStore(roomsStore).rooms
  const onboard = useStore(onboardStore)
  const inbox = useStore(inboxStore).items

  const bot = path.match(BOT_PATH)
  const computer = path.match(COMPUTER_PATH)
  const room = path.match(ROOM_PATH)
  const botFiles = path.match(BOT_FILES_PATH)
  const call = path.match(CALL_PATH)
  const voice = path.match(VOICE_PATH)
  const botName = (id: string) => bots?.find((b) => b.id === id)?.name
  const title = path === '/' ? '待我處理' : botFiles ? `${botName(botFiles[1]) ?? 'Bot'}的檔案` : bot ? (botName(bot[1]) ?? '對話') : computer ? `${botName(computer[1]) ?? 'Bot'}的電腦` : call ? `和${botName(call[1]) ?? 'Bot'}通話` : voice ? `${botName(voice[1]) ?? 'Bot'}的聲音` : room ? (rooms?.find((r) => r.id === room[1])?.name ?? '群組對話') : (TITLES[path] ?? (path.startsWith('/settings') ? '設定' : path.startsWith('/calendar') ? '定時任務與行事曆' : 'XBot'))

  // 首次設定（A03、A04）：還沒有任何 Bot、也還沒做完首次設定時，整頁顯示；讀狀態的那一下先不畫，免得閃一下首頁。
  if (onboard.status === null) return null
  if (onboard.status !== 'off' && onboard.status.first_run && !onboard.skipped) {
    return (
      <Suspense fallback={null}>
        <Onboarding onDone={() => onboardStore.set((o) => ({ ...o, skipped: true }))} />
      </Suspense>
    )
  }
  const noBots = !!bots && bots.every((b) => b.archived) && inbox !== null && inbox.length === 0

  const pageFor = (path: string) => {
    const bot = path.match(BOT_PATH)
    const computer = path.match(COMPUTER_PATH)
    const room = path.match(ROOM_PATH)
    const botFiles = path.match(BOT_FILES_PATH)
    const profile = path.match(PROFILE_PATH)
    const call = path.match(CALL_PATH)
    const voice = path.match(VOICE_PATH)
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
    else if (profile) page = <BotProfilePage key={profile[1]} botId={profile[1]} />
    else if (call) page = <CallPage key={call[1]} botId={call[1]} />
    else if (voice) page = <VoiceSetupPage key={voice[1]} botId={voice[1]} />
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
    else if (path === '/settings' || path.startsWith('/settings/')) page = null
    else page = <Placeholder title={TITLES[path] ?? 'XBot'} />
    return page
  }
  const settings = path === '/settings' || path.startsWith('/settings/')
  const settingsPath = path === '/settings/agents' ? '/settings/advanced/agents' : path
  const page = settings ? <SettingsPage path={settingsPath} /> : pageFor(path)

  // 桌面：設定是跳出來的視窗，背景留著原本的頁面（背景對螢幕閱讀器與鍵盤先藏起來）
  const modal = settings && !narrow
  return (
    <AppShell path={modal ? bgPath : path} title={title}>
      <ScrollDiag />
      <UpdateBanner />
      {modal ? (
        <>
          <div aria-hidden="true" inert className="flex min-h-0 min-w-0 flex-1 flex-col">
            <Suspense fallback={<PageLoading />}>{pageFor(bgPath)}</Suspense>
          </div>
          <SettingsModal onClose={() => navigate(bgPath)}>
            <Suspense fallback={<PageLoading />}>
              <SettingsPage path={settingsPath} onClose={() => navigate(bgPath)} />
            </Suspense>
          </SettingsModal>
        </>
      ) : (
        <Suspense fallback={<PageLoading />}>{page}</Suspense>
      )}
      <MountWhen store={newBotDialog} open={(v) => v}>
        <NewBotDialog />
      </MountWhen>
      <MountWhen store={newRoutineDialog} open={(v) => v.open}>
        <NewRoutineDialog />
      </MountWhen>
      <MountWhen store={newRoomDialog} open={(v) => v}>
        <NewRoomDialog />
      </MountWhen>
      <MountWhen store={newTeamDialog} open={(v) => v}>
        <NewTeamDialog />
      </MountWhen>
      <MountWhen store={importBotDialog} open={(v) => v}>
        <ImportBotDialog />
      </MountWhen>
      <MountWhen store={onboardStore} open={(v) => v.tour != null}>
        <Tour />
      </MountWhen>
      <MountWhen store={teachStartStore} open={(v) => v != null}>
        <TeachStartDialog />
      </MountWhen>
    </AppShell>
  )
}

export default function App() {
  const path = usePath()
  const [auth, loggedIn] = useAuth()
  // 手機掃 QR 打開的配對頁（L09）：還沒登入也要能用
  if (path === '/pair')
    return (
      <Suspense fallback={null}>
        <PairPage />
      </Suspense>
    )
  if (path === '/dev/components') {
    return (
      <ToastProvider>
        <Suspense fallback={null}>
          <DevComponentsPage />
        </Suspense>
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
