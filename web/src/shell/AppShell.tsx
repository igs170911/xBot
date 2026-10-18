import { useRef, useState, type ReactNode } from 'react'
import { Bell, Menu } from 'lucide-react'
import { IconButton } from '../ui/Button'
import { Mascot } from '../ui/Avatar'
import { useFocusTrap } from '../ui/useFocusTrap'
import { Sidebar } from './Sidebar'

// 版型：桌面側欄 300 固定在左；手機寬度（< 768）側欄變抽屜，上方一條標題列。
export function AppShell({ path, title, children }: { path: string; title: string; children: ReactNode }) {
  const [drawer, setDrawer] = useState(false)
  const panel = useRef<HTMLDivElement>(null)
  const close = () => setDrawer(false)
  const onKeyDown = useFocusTrap(panel, drawer, close)

  return (
    <div className="flex min-h-dvh bg-app">
      <div className="sticky top-0 hidden h-dvh w-[301px] flex-none border-r border-edge bg-panel md:block">
        <Sidebar path={path} />
      </div>

      <div className="flex min-w-0 flex-1 flex-col">
        <header className="sticky top-0 z-30 flex h-14 items-center gap-1 border-b border-edge bg-app/95 px-2 pt-[env(safe-area-inset-top)] backdrop-blur md:hidden">
          <IconButton icon={Menu} label="打開側欄" aria-expanded={drawer} onClick={() => setDrawer(true)} />
          <Mascot size={24} />
          <span className="min-w-0 flex-1 truncate pl-2 text-title">{title}</span>
          <IconButton icon={Bell} label="通知" />
        </header>
        {children}
      </div>

      {drawer && (
        <div className="fixed inset-0 z-50 md:hidden">
          <div className="absolute inset-0 bg-black/60" onClick={close} aria-hidden="true" />
          <div
            ref={panel}
            role="dialog"
            aria-modal="true"
            aria-label="側欄"
            tabIndex={-1}
            onKeyDown={onKeyDown}
            className="absolute inset-y-0 left-0 w-[min(300px,85vw)] border-r border-edge bg-panel pb-[env(safe-area-inset-bottom)] pt-[env(safe-area-inset-top)] shadow-dialog outline-none"
          >
            <Sidebar path={path} onNavigate={close} onClose={close} />
          </div>
        </div>
      )}
    </div>
  )
}
