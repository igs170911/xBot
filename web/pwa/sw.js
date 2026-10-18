/* XBot service worker（W31、MB-1）。
 *
 * 建置時由 vite.config.ts 的 xbotServiceWorker 外掛填入版本與要先存的檔案清單，輸出成 dist/sw.js。
 *
 * 快取規則：
 *   - 安裝時先存好 app 殼：首頁（/）、入口 JS／CSS、圖示、manifest。
 *   - /assets/*（檔名含雜湊、不會變）：先看快取，沒有再上網並存起來。
 *   - 頁面導覽：先上網（拿最新版），離線才用存好的首頁（/，即 index.html）。
 *   - /api/*、/mcp、/model/、/hooks/：一律直接上網，絕不存進快取（回應可能含金鑰、登入資料、檔案內容）。
 *   - 其他同站 GET（圖示、manifest）：先看快取，背景更新。
 *
 * 另外處理：推播（push、notificationclick）與 Android 分享目標（POST /share）。
 */
const VERSION = '__XBOT_VERSION__'
const PRECACHE = __XBOT_PRECACHE__
const SHELL = 'xbot-shell-' + VERSION
const ASSETS = 'xbot-assets'
const SHARE = 'xbot-share'
const NEVER = ['/api/', '/mcp', '/model/', '/hooks/', '/healthz']

self.addEventListener('install', (event) => {
  event.waitUntil(
    caches
      .open(SHELL)
      .then((c) => c.addAll(PRECACHE))
      .then(() => self.skipWaiting()),
  )
})

self.addEventListener('activate', (event) => {
  event.waitUntil(
    caches
      .keys()
      .then((keys) => Promise.all(keys.filter((k) => k.startsWith('xbot-shell-') && k !== SHELL).map((k) => caches.delete(k))))
      .then(() => self.clients.claim()),
  )
})

self.addEventListener('message', (event) => {
  if (event.data === 'skip-waiting') self.skipWaiting()
})

function never(url) {
  return NEVER.some((p) => url.pathname === p.replace(/\/$/, '') || url.pathname.startsWith(p))
}

self.addEventListener('fetch', (event) => {
  const req = event.request
  const url = new URL(req.url)
  if (url.origin !== self.location.origin) return
  if (req.method === 'POST' && url.pathname === '/share') {
    event.respondWith(receiveShare(req))
    return
  }
  if (req.method !== 'GET' || never(url)) return // 交給瀏覽器直接上網，不經快取
  if (req.mode === 'navigate') {
    event.respondWith(
      fetch(req).catch(() => caches.match('/', { cacheName: SHELL }).then((r) => r || caches.match('/'))),
    )
    return
  }
  if (url.pathname.startsWith('/assets/')) {
    event.respondWith(
      caches.match(req).then(
        (hit) =>
          hit ||
          fetch(req).then((res) => {
            if (res.ok) {
              const copy = res.clone()
              caches.open(ASSETS).then((c) => c.put(req, copy))
            }
            return res
          }),
      ),
    )
    return
  }
  event.respondWith(
    caches.match(req).then((hit) => {
      const net = fetch(req)
        .then((res) => {
          if (res.ok && res.type === 'basic') {
            const copy = res.clone()
            caches.open(SHELL).then((c) => c.put(req, copy))
          }
          return res
        })
        .catch(() => hit)
      return hit || net
    }),
  )
})

// ── Android 分享目標：把分享進來的文字、網址、檔案先存起來，再帶去 /share 頁選 Bot ──
async function receiveShare(req) {
  try {
    const form = await req.formData()
    const cache = await caches.open(SHARE)
    const keys = await cache.keys()
    await Promise.all(keys.map((k) => cache.delete(k)))
    const meta = { title: form.get('title') || '', text: form.get('text') || '', url: form.get('url') || '', files: [], at: Date.now() }
    let i = 0
    for (const f of form.getAll('files')) {
      if (!(f instanceof File) || !f.size) continue
      const key = '/__share/file-' + i++
      meta.files.push({ key, name: f.name, type: f.type, size: f.size })
      await cache.put(key, new Response(f, { headers: { 'Content-Type': f.type || 'application/octet-stream' } }))
    }
    await cache.put('/__share/meta', new Response(JSON.stringify(meta), { headers: { 'Content-Type': 'application/json' } }))
    return Response.redirect('/share?received=1', 303)
  } catch {
    return Response.redirect('/share?failed=1', 303)
  }
}

// ── 推播（xbot.push.*）：服務端送來 {title, body, url, tag, kind} ──
self.addEventListener('push', (event) => {
  let data = {}
  try {
    data = event.data ? event.data.json() : {}
  } catch {
    data = { title: 'XBot', body: event.data ? event.data.text() : '' }
  }
  const title = data.title || 'XBot'
  event.waitUntil(
    self.registration.showNotification(title, {
      body: data.body || '',
      tag: data.tag || undefined,
      renotify: !!data.tag,
      icon: '/icons/icon-192.png',
      badge: '/icons/badge-96.png',
      lang: 'zh-Hant-TW',
      data: { url: data.url || '/' },
    }),
  )
})

self.addEventListener('notificationclick', (event) => {
  event.notification.close()
  const target = new URL((event.notification.data && event.notification.data.url) || '/', self.location.origin).href
  event.waitUntil(
    self.clients.matchAll({ type: 'window', includeUncontrolled: true }).then((wins) => {
      for (const w of wins) {
        if (new URL(w.url).origin === self.location.origin && 'focus' in w) {
          w.postMessage({ type: 'navigate', url: target })
          return w.focus()
        }
      }
      return self.clients.openWindow(target)
    }),
  )
})
