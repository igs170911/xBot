import { useEffect, useRef, type ReactNode } from 'react'
import { drawerStore, railStore } from '../data/ui'
import { useStore } from '../data/store'
import { useFocusTrap } from '../ui/useFocusTrap'
import { useLockBody } from '../ui/useLockBody'
import { slide, useDrag } from '../ui/useDrag'
import { useSlideOver } from '../ui/useSlideOver'
import { backTo, goBack } from '../router'
import { PhoneBar, TabBar } from '../mobile/Phone'
import { backFallback, isChatPath, isTabPath, ownsPhoneHeader, usePhone } from '../mobile/nav'
import { Sidebar } from './Sidebar'
import { ResizeHandle } from './ResizeHandle'
import { SIDEBAR, sidebarWidthStore } from './panes'
import { cx } from '../ui/cx'

/** 從左邊緣幾 px 內開始拖才算（避開頁面本身的左右滑動）。 */
const EDGE = 24

// 版型：桌面側欄在左（預設 260，可以拖右邊緣改寬度）（可以縮成 56 的圖示列）；手機寬度（< 768）照 L 畫板：分頁首頁下面是分頁列，
// 其他頁有自己的頁首（或共用的返回列）。手機的手勢都跟著手指走（像原生 App）：
//   - 分頁首頁從左邊緣往右拖：拉出側欄；側欄打開時往左拖：收回去。
//   - 其他頁從左邊緣往右拖：整頁跟著滑，拉夠了放開就回上一頁。
export function AppShell({ path, title, children }: { path: string; title: string; children: ReactNode }) {
  const phone = usePhone()
  const drawer = useStore(drawerStore)
  const rail = useStore(railStore)
  const sideWidth = useStore(sidebarWidthStore)
  const panel = useRef<HTMLDivElement>(null)
  const page = useRef<HTMLDivElement>(null)
  const close = () => drawerStore.set(false)
  const onKeyDown = useFocusTrap(panel, drawer, close)
  const tab = isTabPath(path)
  // 側欄（手機）：分頁首頁從左邊緣拉出來、打開時往左拖收回；位置由 useSlideOver 決定
  const side = useSlideOver({ side: 'left', open: drawer, setOpen: (v) => drawerStore.set(v), enabled: phone && tab, size: () => Math.min(300, window.innerWidth * 0.85) })
  useLockBody(phone && drawer) // 側欄開著：iPhone 上在側欄裡滑，後面的頁面不跟著捲

  // 分頁標題跟著頁面換（WCAG 2.4.2；螢幕閱讀器換頁時會念、瀏覽器分頁與歷史紀錄也看得出是哪一頁）
  useEffect(() => {
    document.title = title && title !== 'XBot' ? `${title} · XBot` : 'XBot'
  }, [title])

  // 返回（其他頁、從左邊緣）：整頁跟著手指滑
  useDrag('document', {
    axis: 'x',
    dir: 1,
    enabled: phone && !tab && !drawer,
    size: () => window.innerWidth,
    canStart: (x) => x <= EDGE,
    onMove: (d) => {
      const p = page.current
      if (!p) return
      p.style.transition = 'none'
      p.style.transform = `translateX(${d}px)`
      p.style.boxShadow = '-12px 0 24px rgba(0,0,0,.45)'
    },
    onEnd: (commit) => {
      const p = page.current
      const reset = () => {
        if (!p) return
        p.style.transition = ''
        p.style.transform = ''
        p.style.boxShadow = ''
      }
      if (commit)
        slide(p, `translateX(${window.innerWidth}px)`, () => {
          if (isChatPath(path)) backTo('/chats')
          else goBack(backFallback(path))
          requestAnimationFrame(reset)
        })
      else slide(p, 'translateX(0)', reset)
    },
  })


  return (
    <div className="flex min-h-dvh bg-app">
      {/* 跳過側欄（WCAG 2.4.1）：按 Tab 第一個出現，按下把焦點移到這一頁的主要內容 */}
      <a
        href="#main"
        onClick={(e) => {
          e.preventDefault()
          const main = page.current?.querySelector<HTMLElement>('main')
          if (!main) return
          if (!main.hasAttribute('tabindex')) main.setAttribute('tabindex', '-1')
          main.focus()
        }}
        className="sr-only-text focus:not-sr-only-text fixed left-3 top-3 z-[60] rounded-full bg-ink px-4 py-2 text-ui text-on-ink"
      >
        跳到主要內容
      </a>
      {/* 側欄（可以拖右邊緣改寬度，雙擊回到 260；縮成圖示列時固定 56）＋ 1 的邊線 */}
      <div className={cx('sticky top-0 hidden h-dvh flex-none border-r border-edge bg-panel md:block', rail && 'w-[57px]')} style={rail ? undefined : { width: sideWidth + 1 }}>
        <Sidebar path={path} rail={rail} />
        {!rail && <ResizeHandle size={SIDEBAR} store={sidebarWidthStore} edge="right" label="側欄寬度" />}
      </div>

      <div ref={page} data-phone-page="1" className={tab ? 'flex min-w-0 flex-1 flex-col bg-app max-md:pb-[calc(57px+env(safe-area-inset-bottom))]' : 'flex min-w-0 flex-1 flex-col bg-app'}>
        {phone && !ownsPhoneHeader(path) && <PhoneBar title={title} back={backFallback(path)} heading={false} />}
        {children}
      </div>
      {phone && tab && <TabBar path={path} />}
      {/* 狀態列（時間、電量）後面的底色：App 蓋滿整個螢幕（black-translucent），捲動的內容不會跑到時間下面 */}
      {phone && <div aria-hidden="true" data-status-bar="1" className="fixed inset-x-0 top-0 z-[35] h-[env(safe-area-inset-top)] bg-app" />}

      {phone && side.mounted && (
        <div className="fixed-screen z-50 md:hidden" data-testid="drawer" aria-hidden={side.hidden || undefined} inert={side.hidden || undefined}>
          <div className="absolute inset-0 bg-black/60" style={side.shadeStyle} onClick={close} aria-hidden="true" />
          <div
            ref={panel}
            role="dialog"
            aria-modal="true"
            aria-label="側欄"
            tabIndex={-1}
            onKeyDown={onKeyDown}
            style={side.panelStyle}
            className="absolute inset-y-0 left-0 w-[min(300px,85vw)] border-r border-edge bg-panel pb-[env(safe-area-inset-bottom)] pt-[env(safe-area-inset-top)] shadow-dialog outline-none"
          >
            <Sidebar path={path} onNavigate={close} onClose={close} />
          </div>
        </div>
      )}
    </div>
  )
}
