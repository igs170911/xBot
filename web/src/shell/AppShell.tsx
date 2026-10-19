import { useEffect, useRef, useState, type ReactNode } from 'react'
import { drawerStore, railStore } from '../data/ui'
import { useStore } from '../data/store'
import { useFocusTrap } from '../ui/useFocusTrap'
import { slide, useDrag } from '../ui/useDrag'
import { goBack } from '../router'
import { PhoneBar, TabBar } from '../mobile/Phone'
import { backFallback, isTabPath, ownsPhoneHeader, usePhone } from '../mobile/nav'
import { Sidebar } from './Sidebar'
import { cx } from '../ui/cx'

/** 從左邊緣幾 px 內開始拖才算（避開頁面本身的左右滑動）。 */
const EDGE = 24

// 版型：桌面側欄 260 固定在左（可以縮成 56 的圖示列）；手機寬度（< 768）照 L 畫板：分頁首頁下面是分頁列，
// 其他頁有自己的頁首（或共用的返回列）。手機的手勢都跟著手指走（像原生 App）：
//   - 分頁首頁從左邊緣往右拖：拉出側欄；側欄打開時往左拖：收回去。
//   - 其他頁從左邊緣往右拖：整頁跟著滑，拉夠了放開就回上一頁。
export function AppShell({ path, title, children }: { path: string; title: string; children: ReactNode }) {
  const phone = usePhone()
  const drawer = useStore(drawerStore)
  const rail = useStore(railStore)
  const panel = useRef<HTMLDivElement>(null)
  const shade = useRef<HTMLDivElement>(null)
  const page = useRef<HTMLDivElement>(null)
  // 拖拉中（側欄還沒真的打開，但要畫出來跟著手指）
  const [peek, setPeek] = useState(false)
  const close = () => drawerStore.set(false)
  const fadeShade = (r: number) => {
    if (!shade.current) return
    shade.current.style.transition = 'opacity 220ms ease-out'
    shade.current.style.opacity = String(r)
  }
  // 點背景：滑出去再關
  const closeSlide = () => {
    fadeShade(0)
    slide(panel.current, 'translateX(-100%)', close)
  }
  const onKeyDown = useFocusTrap(panel, drawer, close)
  const tab = isTabPath(path)
  const width = () => panel.current?.offsetWidth || Math.min(300, window.innerWidth * 0.85)

  // 分頁標題跟著頁面換（WCAG 2.4.2；螢幕閱讀器換頁時會念、瀏覽器分頁與歷史紀錄也看得出是哪一頁）
  useEffect(() => {
    document.title = title && title !== 'XBot' ? `${title} · XBot` : 'XBot'
  }, [title])

  // 側欄開關時（按鈕、手勢放開）動畫到定位
  useEffect(() => {
    if (!phone) return
    const p = panel.current
    if (drawer && p) {
      // 按按鈕打開：從左邊滑進來；拖拉打開：從手指放開的位置接著滑
      if (!p.style.transform) {
        p.style.transition = 'none'
        p.style.transform = 'translateX(-100%)'
        void p.offsetWidth
      }
      slide(p, 'translateX(0)')
      fadeShade(1)
    }
  }, [drawer, phone])

  const shadeTo = (r: number) => {
    if (!shade.current) return
    shade.current.style.transition = 'none'
    shade.current.style.opacity = String(Math.max(0, Math.min(1, r)))
  }

  // 拉出側欄（分頁首頁、從左邊緣）
  useDrag('document', {
    axis: 'x',
    dir: 1,
    enabled: phone && tab && !drawer,
    size: width,
    canStart: (x) => x <= EDGE,
    onMove: (d) => {
      setPeek(true)
      const p = panel.current
      if (p) {
        p.style.transition = 'none'
        p.style.transform = `translateX(${d - width()}px)`
      }
      shadeTo(d / width())
    },
    onEnd: (commit) => {
      if (commit) {
        drawerStore.set(true)
        setPeek(false)
        return
      }
      fadeShade(0)
      slide(panel.current, `translateX(-100%)`, () => setPeek(false))
    },
  })

  // 收回側欄（打開時往左拖，從側欄或背景都可以）
  useDrag('document', {
    axis: 'x',
    dir: -1,
    enabled: phone && drawer,
    size: width,
    onMove: (d) => {
      const p = panel.current
      if (p) {
        p.style.transition = 'none'
        p.style.transform = `translateX(${-d}px)`
      }
      shadeTo(1 - d / width())
    },
    onEnd: (commit) => {
      if (commit) {
        fadeShade(0)
        slide(panel.current, 'translateX(-100%)', close)
      } else {
        slide(panel.current, 'translateX(0)')
        fadeShade(1)
      }
    },
  })

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
          goBack(backFallback(path))
          requestAnimationFrame(reset)
        })
      else slide(p, 'translateX(0)', reset)
    },
  })

  const showDrawer = drawer || peek

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
      <div className={cx('sticky top-0 hidden h-dvh flex-none border-r border-edge bg-panel md:block', rail ? 'w-[57px]' : 'w-[261px]')}>
        <Sidebar path={path} rail={rail} />
      </div>

      <div ref={page} data-phone-page="1" className={tab ? 'flex min-w-0 flex-1 flex-col bg-app max-md:pb-[calc(57px+env(safe-area-inset-bottom))]' : 'flex min-w-0 flex-1 flex-col bg-app'}>
        {phone && !ownsPhoneHeader(path) && <PhoneBar title={title} back={backFallback(path)} heading={false} />}
        {children}
      </div>
      {phone && tab && <TabBar path={path} />}

      {phone && showDrawer && (
        <div className="fixed inset-0 z-50 md:hidden" data-testid="drawer">
          <div ref={shade} className="absolute inset-0 bg-black/60 transition-opacity duration-200" style={{ opacity: 0 }} onClick={closeSlide} aria-hidden="true" />
          <div
            ref={panel}
            role="dialog"
            aria-modal="true"
            aria-label="側欄"
            tabIndex={-1}
            onKeyDown={onKeyDown}
            style={{ transform: drawer ? undefined : 'translateX(-100%)' }}
            className="absolute inset-y-0 left-0 w-[min(300px,85vw)] border-r border-edge bg-panel pb-[env(safe-area-inset-bottom)] pt-[env(safe-area-inset-top)] shadow-dialog outline-none"
          >
            <Sidebar path={path} onNavigate={close} onClose={close} />
          </div>
        </div>
      )}
    </div>
  )
}
