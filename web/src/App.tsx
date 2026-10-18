import { Construction } from 'lucide-react'
import { usePath } from './router'
import { AppShell } from './shell/AppShell'
import { InboxPage } from './pages/InboxPage'
import { DevComponentsPage } from './pages/DevComponentsPage'
import { ToastProvider } from './ui/Toast'
import { Icon } from './ui/Icon'
import { BOTS } from './shell/sample'

const TITLES: Record<string, string> = {
  '/office': '辦公室',
  '/calendar': '定時任務與行事曆',
  '/apps': '連接應用程式',
  '/org': '組織架構',
  '/settings': '設定',
}

function titleFor(path: string): string {
  if (path === '/') return '待我處理'
  const bot = path.match(/^\/bots\/([^/]+)/)
  if (bot) return BOTS[bot[1]]?.name ?? '對話'
  if (path.startsWith('/rooms/')) return '群組對話'
  return TITLES[path] ?? 'XBot'
}

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

export default function App() {
  const path = usePath()
  if (path === '/dev/components') {
    return (
      <ToastProvider>
        <DevComponentsPage />
      </ToastProvider>
    )
  }
  const title = titleFor(path)
  return (
    <ToastProvider>
      <AppShell path={path} title={title}>
        {path === '/' ? <InboxPage /> : <Placeholder title={title} />}
      </AppShell>
    </ToastProvider>
  )
}
