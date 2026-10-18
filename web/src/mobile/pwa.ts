import { useSyncExternalStore } from 'react'
import { createStore, useStore } from '../data/store'
import { navigate } from '../router'

// PWA（W31、MB-1）：註冊 service worker、安裝到主畫面、網路狀態、推播訂閱。

// ── 網路狀態 ─────────────────────────────────────────

function subscribeOnline(fn: () => void) {
  window.addEventListener('online', fn)
  window.addEventListener('offline', fn)
  return () => {
    window.removeEventListener('online', fn)
    window.removeEventListener('offline', fn)
  }
}

/** 瀏覽器覺得有沒有網路（L01「網路已離線，恢復後會自動重新連線」）。 */
export function useOnline(): boolean {
  return useSyncExternalStore(subscribeOnline, () => navigator.onLine, () => true)
}

// ── service worker ──────────────────────────────────

/** 註冊 /sw.js（只在建置後的版本；開發模式不註冊，免得快取舊檔）。點通知時 SW 會叫我們換頁。 */
export function registerServiceWorker() {
  if (!('serviceWorker' in navigator) || !import.meta.env.PROD) return
  navigator.serviceWorker.addEventListener('message', (e: MessageEvent) => {
    const d = e.data as { type?: string; url?: string } | null
    if (d?.type === 'navigate' && d.url) {
      const u = new URL(d.url, window.location.origin)
      if (u.origin === window.location.origin) navigate(u.pathname + u.search)
    }
  })
  const go = () => void navigator.serviceWorker.register('/sw.js', { scope: '/' }).catch(() => {})
  if (document.readyState === 'complete') go()
  else window.addEventListener('load', go, { once: true })
}

// ── 安裝到主畫面 ─────────────────────────────────────

type InstallEvent = Event & { prompt: () => Promise<void>; userChoice: Promise<{ outcome: 'accepted' | 'dismissed' }> }

export type InstallState = {
  /** Android／桌面 Chrome：可以直接跳出安裝視窗。 */
  canPrompt: boolean
  /** 已經是從主畫面打開的（standalone）。 */
  installed: boolean
  /** iOS Safari：要自己按「分享 → 加入主畫面」。 */
  ios: boolean
}

let deferred: InstallEvent | null = null

function standalone(): boolean {
  const nav = navigator as Navigator & { standalone?: boolean }
  return nav.standalone === true || (typeof window.matchMedia === 'function' && window.matchMedia('(display-mode: standalone)').matches)
}

function isIOS(): boolean {
  const ua = navigator.userAgent
  return /iPhone|iPad|iPod/.test(ua) || (ua.includes('Macintosh') && navigator.maxTouchPoints > 1)
}

export const installStore = createStore<InstallState>({ canPrompt: false, installed: false, ios: false })

/** 開始聽安裝事件（main.tsx 呼叫一次）。 */
export function watchInstall() {
  installStore.set({ canPrompt: false, installed: standalone(), ios: isIOS() && !standalone() })
  window.addEventListener('beforeinstallprompt', (e) => {
    e.preventDefault() // 不讓瀏覽器自己跳，改在設定頁與 L01 由使用者按
    deferred = e as InstallEvent
    installStore.set((s) => ({ ...s, canPrompt: true }))
  })
  window.addEventListener('appinstalled', () => {
    deferred = null
    installStore.set((s) => ({ ...s, canPrompt: false, installed: true }))
  })
}

export function useInstall(): InstallState {
  return useStore(installStore)
}

/** 跳出安裝視窗；回傳使用者有沒有按安裝。 */
export async function promptInstall(): Promise<boolean> {
  if (!deferred) return false
  const ev = deferred
  deferred = null
  installStore.set((s) => ({ ...s, canPrompt: false }))
  await ev.prompt()
  const choice = await ev.userChoice
  return choice.outcome === 'accepted'
}

// ── 推播 ────────────────────────────────────────────

/** 這個瀏覽器能不能收推播（iOS 要先加到主畫面才行）。 */
export function pushSupported(): boolean {
  return typeof window !== 'undefined' && 'serviceWorker' in navigator && 'PushManager' in window && 'Notification' in window
}

/** VAPID 公鑰（base64url）→ applicationServerKey。 */
export function urlBase64ToBytes(s: string): Uint8Array<ArrayBuffer> {
  const pad = '='.repeat((4 - (s.length % 4)) % 4)
  const b64 = (s + pad).replace(/-/g, '+').replace(/_/g, '/')
  const raw = atob(b64)
  const out = new Uint8Array(new ArrayBuffer(raw.length))
  for (let i = 0; i < raw.length; i++) out[i] = raw.charCodeAt(i)
  return out
}

/** 這支手機目前的推播訂閱（沒有就是 null）。 */
export async function currentSubscription(): Promise<PushSubscription | null> {
  if (!pushSupported()) return null
  const reg = await navigator.serviceWorker.getRegistration('/')
  return (await reg?.pushManager.getSubscription()) ?? null
}

/** 把 PushSubscription 拆成服務要的欄位。 */
export function subscriptionKeys(sub: PushSubscription): { endpoint: string; p256dh: string; auth: string } {
  const j = sub.toJSON() as { endpoint?: string; keys?: { p256dh?: string; auth?: string } }
  return { endpoint: j.endpoint ?? sub.endpoint, p256dh: j.keys?.p256dh ?? '', auth: j.keys?.auth ?? '' }
}

/** 向瀏覽器要推播權限並訂閱（使用者按開關時才呼叫）。 */
export async function subscribePush(publicKey: string): Promise<PushSubscription> {
  if (!pushSupported()) throw new Error(isIOS() && !standalone() ? 'iPhone 要先「加入主畫面」，從主畫面打開 XBot 才能收推播' : '這個瀏覽器不支援推播')
  const perm = await Notification.requestPermission()
  if (perm !== 'granted') throw new Error('沒有允許通知；請到瀏覽器或系統設定打開 XBot 的通知')
  const reg = (await navigator.serviceWorker.getRegistration('/')) ?? (await navigator.serviceWorker.register('/sw.js', { scope: '/' }))
  await navigator.serviceWorker.ready
  const old = await reg.pushManager.getSubscription()
  if (old) return old
  return reg.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey: urlBase64ToBytes(publicKey) })
}

/** 取消這支手機的推播訂閱（瀏覽器那邊）；回傳原本的 endpoint。 */
export async function unsubscribePush(): Promise<string | null> {
  const sub = await currentSubscription()
  if (!sub) return null
  const ep = sub.endpoint
  await sub.unsubscribe().catch(() => false)
  return ep
}

/** 給推播訂閱一個看得懂的名字：「Android 手機（Chrome）」「iPhone」。 */
export function deviceLabel(): string {
  const ua = navigator.userAgent
  if (/iPhone/.test(ua)) return 'iPhone'
  if (/iPad/.test(ua) || (ua.includes('Macintosh') && navigator.maxTouchPoints > 1)) return 'iPad'
  if (/Android/.test(ua)) return /Mobile/.test(ua) ? 'Android 手機' : 'Android 平板'
  if (/Mac/.test(ua)) return 'Mac'
  if (/Windows/.test(ua)) return 'Windows 電腦'
  return '這個瀏覽器'
}

/** 這個網址是怎麼連進來的（L06「這支手機 · 已連線 · Tailscale」）。 */
export function connectionKind(host = window.location.hostname): string {
  if (host.endsWith('.ts.net') || /^100\.(6[4-9]|[7-9]\d|1[01]\d|12[0-7])\./.test(host)) return 'Tailscale'
  if (host === 'localhost' || host === '127.0.0.1' || host === '::1') return '這台電腦'
  if (/^(10\.|192\.168\.|172\.(1[6-9]|2\d|3[01])\.)/.test(host)) return '同一個 Wi-Fi'
  return 'HTTPS'
}
