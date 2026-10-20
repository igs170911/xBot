import { useEffect, useState } from 'react'
import { systemClientReport } from '../api/generated'

// 手機捲不動的診斷面板（暫時的，查完就拿掉）：在待我處理頁連點「服務正常 · 版本」5 下開關。
// 記下手指碰到哪裡、有沒有被 preventDefault、哪個元素真的捲動了，截圖給開發者看。

const KEY = 'xbot.diag'

export function diagOn(): boolean {
  try {
    return localStorage.getItem(KEY) === 'scroll'
  } catch {
    return false
  }
}

export function toggleDiag() {
  try {
    if (diagOn()) localStorage.removeItem(KEY)
    else localStorage.setItem(KEY, 'scroll')
  } catch {
    /* 私密瀏覽：沒辦法記 */
  }
  window.dispatchEvent(new Event('xbot-diag'))
}

function name(el: Element | null): string {
  if (!el) return '-'
  const cls = (el.getAttribute('class') ?? '').split(/\s+/).filter(Boolean).slice(0, 3).join('.')
  return el.tagName.toLowerCase() + (el.id ? '#' + el.id : '') + (cls ? '.' + cls : '') + (el.getAttribute('aria-label') ? `[${el.getAttribute('aria-label')?.slice(0, 20)}]` : '')
}

function scroller(el: Element | null): HTMLElement | null {
  for (let e = el as HTMLElement | null; e; e = e.parentElement) {
    const s = getComputedStyle(e)
    if (/(auto|scroll)/.test(s.overflowY) && e.scrollHeight > e.clientHeight + 1) return e
  }
  return null
}

type Info = Record<string, string | number>

/** 手指下面（含被蓋住的）第一個還能往這個方向捲的元素。 */
function scrollableUnder(x: number, y: number, dy: number): HTMLElement | null {
  for (const el of document.elementsFromPoint(x, y)) {
    const e = el as HTMLElement
    const s = getComputedStyle(e)
    if (!/(auto|scroll)/.test(s.overflowY) || e.scrollHeight <= e.clientHeight + 1) continue
    // 手指往上滑（dy<0）＝內容往下捲：要還沒到底；往下滑＝往上捲：要還沒到頂
    const room = dy < 0 ? e.scrollTop + e.clientHeight < e.scrollHeight - 2 : e.scrollTop > 2
    return room ? e : null
  }
  return null
}

const REPORTS_MAX = 5
const SWIPE_MIN = 60
/** 健康摘要：至少滑了這麼多次才送（太少看不出什麼）；滑到這麼多次就先送。 */
const HEALTH_MIN = 5
const HEALTH_AT = 25

/** env(safe-area-inset-*) 實際是多少（iPhone 主畫面 App：狀態列樣式對了，上面才會 > 0）。 */
function inset(side: 'top' | 'bottom'): number {
  const probe = document.createElement('div')
  probe.style.cssText = `position:fixed;visibility:hidden;pointer-events:none;padding-${side}:env(safe-area-inset-${side})`
  document.body.appendChild(probe)
  const v = parseFloat(side === 'top' ? getComputedStyle(probe).paddingTop : getComputedStyle(probe).paddingBottom) || 0
  probe.remove()
  return Math.round(v)
}

const navTop = () => {
  const n = document.querySelector('nav[aria-label="分頁"]')
  return n ? Math.round(n.getBoundingClientRect().top) : null
}

/**
 * 一直開著的偵測（只在觸控裝置）：上下滑了超過 60px、手指下面明明有還能捲的區塊，卻什麼都沒捲動 → 回報到服務日誌。
 * 另外每次打開送一筆健康摘要（phone_health）：滑了幾次、幾次有捲動、幾次卡住、分頁列打開後有沒有移動、
 * 瀏海與底部橫條的安全區——真機上順不順，不用請你截圖就看得到。
 * 只送元素名稱、數字與狀態，不送對話內容；卡住的每次開網頁最多 5 筆，摘要 1 筆。
 */
let watching = false

export function startScrollWatch(version: () => string) {
  if (watching || typeof window === 'undefined' || !window.matchMedia?.('(pointer: coarse)').matches) return
  watching = true
  let sent = 0
  const health = { ok: 0, stuck: 0, edge: 0 }
  // 打開後的時間線：iPhone 主畫面 App 剛打開時畫面高度會少算狀態列，分頁列先浮在上面、之後才掉下去
  // （2026-10-20 真機：2.5 秒時分頁列在 702，應該在 761）。記下高度何時、怎麼變回來，才修得準。
  const t0 = performance.now()
  const timeline: string[] = []
  // lvh／dvh 實際是幾 px：網路上的回報說主畫面 App 剛打開時 dvh 少算狀態列、lvh 是對的，用真機確認
  const unit = (css: string) => {
    const probe = document.createElement('div')
    probe.style.cssText = `position:absolute;visibility:hidden;pointer-events:none;top:0;height:${css}`
    document.body.appendChild(probe)
    const h = probe.offsetHeight
    probe.remove()
    return h
  }
  const sample = (label: string) => {
    const vv = window.visualViewport
    timeline.push(`${label}:in${window.innerHeight}/vv${Math.round(vv?.height ?? 0)}/ch${document.documentElement.clientHeight}/lvh${unit('100lvh')}/dvh${unit('100dvh')}/nav${navTop() ?? '-'}${document.documentElement.style.getPropertyValue('--app-h') ? '/kb' : ''}`)
  }
  for (const ms of [150, 500, 1000, 2500, 5000, 10000]) window.setTimeout(() => sample(String(ms)), ms)
  let resized = 0
  const onResize = () => resized++ < 8 && sample(`resize@${Math.round(performance.now() - t0)}`) // 含鍵盤彈出、收起
  window.addEventListener('resize', onResize)
  window.visualViewport?.addEventListener('resize', onResize)
  window.addEventListener('touchstart', () => timeline.length < 20 && !timeline.some((x) => x.startsWith('touch')) && sample(`touch@${Math.round(performance.now() - t0)}`), { passive: true, once: true })
  let summarized = false
  const summary = () => {
    // 滑得太少看不出順不順，但打開後 10 秒的時間線齊了就照樣送（要看分頁列有沒有先高後低）
    if (summarized || (health.ok + health.stuck + health.edge < HEALTH_MIN && !timeline.some((x) => x.startsWith('10000:')))) return
    summarized = true
    sample('end')
    const data = [
      `swipes ok=${health.ok} stuck=${health.stuck} edge=${health.edge}`,
      `screen ${window.screen.height} | ${timeline.join(' ')}`, // 打開後各時間點的高度與分頁列位置（沒有分頁列的頁面是 -）
      `inset top=${inset('top')} bottom=${inset('bottom')}`,
      `view in=${window.innerHeight}x${window.innerWidth} docW=${document.documentElement.scrollWidth}`,
      `standalone=${(navigator as Navigator & { standalone?: boolean }).standalone ?? matchMedia('(display-mode: standalone)').matches}`,
      `ua=${navigator.userAgent.slice(0, 140)}`,
    ].join(' | ')
    void systemClientReport({ kind: 'phone_health', version: version(), data }).catch(() => {})
  }
  document.addEventListener('visibilitychange', () => document.visibilityState === 'hidden' && summary())
  let g: {
    x: number
    y: number
    dy: number
    moves: number
    prevented: number
    scrolled: string
    cancel: boolean
    target: Element | null
    sel: number
    cancelable: string
  } | null = null
  window.addEventListener(
    'touchstart',
    (e) => {
      const t = e.touches[0]
      // sel：手指放下時已經選取了多少字（iPhone 在選取範圍上拖是在改選取，不會捲）
      g =
        e.touches.length === 1 && t
          ? { x: t.clientX, y: t.clientY, dy: 0, moves: 0, prevented: 0, scrolled: '', cancel: false, target: e.target as Element, sel: window.getSelection()?.toString().length ?? 0, cancelable: '' }
          : null
    },
    { passive: true },
  )
  window.addEventListener(
    'touchmove',
    (e) => {
      const t = e.touches[0]
      if (!g || !t) return
      g.moves++
      // 前幾個 touchmove 的 cancelable：一直是 true 表示瀏覽器始終沒開始捲（手勢被別的東西接走）
      if (g.moves <= 3) g.cancelable += e.cancelable ? 'y' : 'n'
      if (e.defaultPrevented) g.prevented++
      g.dy = t.clientY - g.y
    },
    { passive: true },
  )
  document.addEventListener('scroll', (e) => g && !g.scrolled && (g.scrolled = e.target === document ? 'document' : name(e.target as Element)), { capture: true, passive: true })
  window.addEventListener('touchcancel', () => g && (g.cancel = true), { passive: true })
  window.addEventListener(
    'touchend',
    () => {
      const done = g
      if (!done || Math.abs(done.dy) < SWIPE_MIN) return
      // 等一下慣性捲動的事件
      window.setTimeout(() => {
        if (done.cancel) return
        const box = done.scrolled ? null : scrollableUnder(done.x, done.y, done.dy)
        if (done.scrolled) health.ok++
        else if (box) health.stuck++
        else health.edge++ // 本來就捲到底了，或手指下面沒有能捲的
        if (health.ok + health.stuck + health.edge === HEALTH_AT) summary()
        if (!box || sent >= REPORTS_MAX) return
        const top = document.elementFromPoint(done.x, done.y)
        const covered = top && !box.contains(top) ? name(top) : ''
        sent++
        const vv = window.visualViewport
        const data = [
          `target=${name(done.target)}`,
          `top=${name(top)}`,
          covered ? `COVERED_BY=${covered}` : 'covered=no',
          `box=${name(box)} ${Math.round(box.scrollTop)}/${box.scrollHeight}-${box.clientHeight} ta=${getComputedStyle(box).touchAction}`,
          `dy=${Math.round(done.dy)} moves=${done.moves} prevented=${done.prevented} cancelable=${done.cancelable} sel=${done.sel}`,
          `view in=${window.innerHeight} vv=${Math.round(vv?.height ?? 0)}@${Math.round(vv?.offsetTop ?? 0)} app-h=${document.documentElement.style.getPropertyValue('--app-h') || '-'}`,
          `body-ov=${document.body.style.overflow || '-'} dialogs=${document.querySelectorAll('[role=dialog],[role=alertdialog]').length}`,
          `standalone=${(navigator as Navigator & { standalone?: boolean }).standalone ?? matchMedia('(display-mode: standalone)').matches} path=${location.pathname.replace(/[0-9a-f]{32}/g, ':id')}`,
          `ua=${navigator.userAgent.slice(0, 140)}`,
        ].join(' | ')
        void systemClientReport({ kind: 'scroll_stuck', version: version(), data: data.slice(0, 3900) }).catch(() => {})
      }, 400)
    },
    { passive: true },
  )
}

export function ScrollDiag() {
  const [on, setOn] = useState(diagOn)
  const [info, setInfo] = useState<Info>({})
  useEffect(() => {
    const f = () => setOn(diagOn())
    window.addEventListener('xbot-diag', f)
    return () => window.removeEventListener('xbot-diag', f)
  }, [])
  useEffect(() => {
    if (!on) return
    const d: Info = { start: 0, move: 0, prevented: 0, end: 0, cancel: 0, scrolls: 0 }
    let sc: HTMLElement | null = null
    let frame = 0
    const flush = () => {
      cancelAnimationFrame(frame)
      frame = requestAnimationFrame(() => {
        const vv = window.visualViewport
        const root = document.documentElement
        setInfo({
          ...d,
          view: `in ${window.innerHeight} vv ${Math.round(vv?.height ?? 0)}@${Math.round(vv?.offsetTop ?? 0)} app-h ${root.style.getPropertyValue('--app-h') || '-'}`,
          body: `ov ${document.body.style.overflow || '-'} doc ${document.scrollingElement?.scrollTop ?? 0}/${root.scrollHeight}`,
          box: sc ? `${name(sc)} ${Math.round(sc.scrollTop)}/${sc.scrollHeight}-${sc.clientHeight} ta ${getComputedStyle(sc).touchAction}` : '-',
          standalone: String((navigator as Navigator & { standalone?: boolean }).standalone ?? matchMedia('(display-mode: standalone)').matches),
        })
      })
    }
    const start = (e: TouchEvent) => {
      const t = e.touches[0]
      d.start = Number(d.start) + 1
      d.move = 0
      d.prevented = 0
      d.scrolls = 0
      d.target = name(e.target as Element)
      const top = t ? document.elementFromPoint(t.clientX, t.clientY) : null
      d.top = top === e.target ? '同上' : name(top)
      d.at = t ? `${Math.round(t.clientX)},${Math.round(t.clientY)}` : '-'
      d.touchAction = getComputedStyle(e.target as Element).touchAction
      sc = scroller(e.target as Element)
      flush()
    }
    const move = (e: TouchEvent) => {
      d.move = Number(d.move) + 1
      if (e.defaultPrevented) d.prevented = Number(d.prevented) + 1
      flush()
    }
    const end = () => {
      d.end = Number(d.end) + 1
      flush()
    }
    const cancel = () => {
      d.cancel = Number(d.cancel) + 1
      flush()
    }
    const scroll = (e: Event) => {
      d.scrolls = Number(d.scrolls) + 1
      d.scrolled = e.target === document ? 'document' : name(e.target as Element)
      flush()
    }
    // 掛在 window 的冒泡階段（最後才跑），才看得到別人有沒有 preventDefault
    window.addEventListener('touchstart', start, { passive: true })
    window.addEventListener('touchmove', move, { passive: true })
    window.addEventListener('touchend', end, { passive: true })
    window.addEventListener('touchcancel', cancel, { passive: true })
    document.addEventListener('scroll', scroll, { capture: true, passive: true })
    flush()
    return () => {
      cancelAnimationFrame(frame)
      window.removeEventListener('touchstart', start)
      window.removeEventListener('touchmove', move)
      window.removeEventListener('touchend', end)
      window.removeEventListener('touchcancel', cancel)
      document.removeEventListener('scroll', scroll, { capture: true })
    }
  }, [on])
  if (!on) return null
  return (
    <div
      aria-hidden="true"
      className="pointer-events-none fixed inset-x-2 top-[max(8px,env(safe-area-inset-top))] z-[200] whitespace-pre-wrap break-all rounded-menu bg-black/80 p-2 font-mono text-[10.5px] leading-[1.35] text-[#7CFC9A]"
    >
      {Object.entries(info)
        .map(([k, v]) => `${k}: ${v}`)
        .join('\n')}
    </div>
  )
}
