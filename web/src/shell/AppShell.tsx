import { useCallback, useRef, type ReactNode } from 'react'
import { drawerStore } from '../data/ui'
import { useStore } from '../data/store'
import { useFocusTrap } from '../ui/useFocusTrap'
import { goBack } from '../router'
import { PhoneBar, TabBar } from '../mobile/Phone'
import { backFallback, isTabPath, ownsPhoneHeader, useEdgeSwipe, usePhone } from '../mobile/nav'
import { Sidebar } from './Sidebar'

// 版型：桌面側欄 300 固定在左；手機寬度（< 768）照 L 畫板：分頁首頁下面是分頁列，
// 其他頁有自己的頁首（或共用的返回列），從左邊緣往右滑可以返回；分頁首頁往右滑打開側欄（抽屜）。
export function AppShell({ path, title, children }: { path: string; title: string; children: ReactNode }) {
  const phone = usePhone()
  const drawer = useStore(drawerStore)
  const panel = useRef<HTMLDivElement>(null)
  const page = useRef<HTMLDivElement>(null)
  const close = () => drawerStore.set(false)
  const onKeyDown = useFocusTrap(panel, drawer, close)
  const tab = isTabPath(path)
  const onSwipe = useCallback(() => (tab ? drawerStore.set(true) : goBack(backFallback(path))), [tab, path])
  useEdgeSwipe(page, phone && !drawer, onSwipe)

  return (
    <div className="flex min-h-dvh bg-app">
      <div className="sticky top-0 hidden h-dvh w-[301px] flex-none border-r border-edge bg-panel md:block">
        <Sidebar path={path} />
      </div>

      <div ref={page} data-phone-page="1" className={tab ? 'flex min-w-0 flex-1 flex-col max-md:pb-[calc(57px+env(safe-area-inset-bottom))]' : 'flex min-w-0 flex-1 flex-col'}>
        {phone && !ownsPhoneHeader(path) && <PhoneBar title={title} back={backFallback(path)} heading={false} />}
        {children}
      </div>
      {phone && tab && <TabBar path={path} />}

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
