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
import { ChatPage } from './chat/ChatPage'
import { startChat } from './chat/chatStore'
import { botsStore, startBots } from './data/bots'
import { startEvents } from './data/events'
import { startInbox } from './data/inbox'
import { useStore } from './data/store'
import { ToastProvider } from './ui/Toast'
import { Icon } from './ui/Icon'

const TITLES: Record<string, string> = {
  '/office': '辦公室',
  '/calendar': '定時任務與行事曆',
  '/apps': '連接應用程式',
  '/org': '組織架構',
  '/settings': '設定',
  '/settings/agents': '已連線的代理',
}

const BOT_PATH = /^\/bots\/([^/]+)(?:\/threads\/([^/]+))?\/?$/
const COMPUTER_PATH = /^\/bots\/([^/]+)\/computer\/?$/

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
  }, [])

  const bot = path.match(BOT_PATH)
  const computer = path.match(COMPUTER_PATH)
  const botName = (id: string) => bots?.find((b) => b.id === id)?.name
  const title = path === '/' ? '待我處理' : bot ? (botName(bot[1]) ?? '對話') : computer ? `${botName(computer[1]) ?? 'Bot'}的電腦` : path.startsWith('/rooms/') ? '群組對話' : (TITLES[path] ?? (path.startsWith('/settings') ? '設定' : 'XBot'))

  let page
  if (path === '/') page = <InboxPage />
  else if (bot) page = <ChatPage key={bot[1]} botId={bot[1]} threadParam={bot[2]} />
  else if (path === '/settings' || path.startsWith('/settings/'))
    page = <SettingsPage path={path === '/settings/agents' ? '/settings/advanced/agents' : path} />
  else page = <Placeholder title={title} />

  return (
    <AppShell path={path} title={title}>
      {page}
      <NewBotDialog />
    </AppShell>
  )
}

export default function App() {
  const path = usePath()
  const [auth, loggedIn] = useAuth()
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
