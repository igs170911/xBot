import { readFileSync } from 'node:fs'
import type { BrowserContext, Page, WebSocketRoute } from '@playwright/test'

// 假的 Bot 電腦畫面（make e2e 用）：攔截 /api/v1/computers/vnc 的 WebSocket，回一個最小的 RFB 3.8 伺服器，
// 畫面是 e2e/fixtures 裡從真的 Bot 電腦擷取的 JPEG（在瀏覽器裡解碼成像素）。

export type Frame = { width: number; height: number; rgba: Buffer; jpeg: string }

/** 讀一張 fixture JPEG，解碼成 RGBA 像素（noVNC 要 32 位元、R 在最低位元組，剛好是 canvas 的順序）。 */
export async function loadFrame(context: BrowserContext, file: string): Promise<Frame> {
  const jpeg = readFileSync(new URL(`./fixtures/${file}`, import.meta.url)).toString('base64')
  const p = await context.newPage()
  const out = await p.evaluate(async (b64) => {
    const img = new Image()
    img.src = 'data:image/jpeg;base64,' + b64
    await img.decode()
    const c = document.createElement('canvas')
    c.width = img.naturalWidth
    c.height = img.naturalHeight
    const g = c.getContext('2d')!
    g.drawImage(img, 0, 0)
    const data = g.getImageData(0, 0, c.width, c.height).data
    let s = ''
    for (let i = 0; i < data.length; i += 0x8000) s += String.fromCharCode(...data.subarray(i, i + 0x8000))
    return { width: c.width, height: c.height, rgba: btoa(s) }
  }, jpeg)
  await p.close()
  return { width: out.width, height: out.height, rgba: Buffer.from(out.rgba, 'base64'), jpeg }
}

export type VncLog = { urls: string[]; input: number[] }

/** 攔截畫面 WebSocket；frameFor 依網址（bot、mode）決定畫面。記下連線網址與收到的輸入（鍵盤＝4、滑鼠＝5）。 */
export async function fakeVnc(page: Page, frameFor: (url: URL) => Frame): Promise<VncLog> {
  const log: VncLog = { urls: [], input: [] }
  // Playwright 攔截 WebSocket 時換上的類別，方法放在更上層的 prototype；noVNC 只看物件本身與直接的 prototype
  // （"Raw channel missing property: send"）。把方法複製到直接的 prototype，真的瀏覽器不需要。
  await page.addInitScript(() => {
    const fix = () => {
      const proto = window.WebSocket.prototype
      for (const p of ['send', 'close', 'binaryType', 'onerror', 'onmessage', 'onopen', 'protocol', 'readyState']) {
        if (Object.getOwnPropertyNames(proto).includes(p)) continue
        for (let o = Object.getPrototypeOf(proto); o; o = Object.getPrototypeOf(o)) {
          const d = Object.getOwnPropertyDescriptor(o, p)
          if (d) {
            Object.defineProperty(proto, p, d)
            break
          }
        }
      }
    }
    document.addEventListener('DOMContentLoaded', fix)
  })
  await page.routeWebSocket(/\/api\/v1\/computers\/vnc/, (ws: WebSocketRoute) => {
    const url = new URL(ws.url())
    log.urls.push(url.pathname + url.search)
    const frame = frameFor(url)
    let state = 0
    let buf = Buffer.alloc(0)
    let sent = false
    const send = (b: Buffer) => ws.send(b)
    send(Buffer.from('RFB 003.008\n'))
    ws.onMessage((m) => {
      buf = Buffer.concat([buf, typeof m === 'string' ? Buffer.from(m, 'binary') : m])
      for (;;) {
        if (state === 0) {
          if (buf.length < 12) return
          buf = buf.subarray(12)
          send(Buffer.from([1, 1])) // 一種安全類型：None
          state = 1
        } else if (state === 1) {
          if (buf.length < 1) return
          buf = buf.subarray(1)
          send(Buffer.from([0, 0, 0, 0])) // SecurityResult OK
          state = 2
        } else if (state === 2) {
          if (buf.length < 1) return
          buf = buf.subarray(1)
          const name = Buffer.from('XBot')
          const init = Buffer.alloc(24)
          init.writeUInt16BE(frame.width, 0)
          init.writeUInt16BE(frame.height, 2)
          Buffer.from([32, 24, 0, 1, 0, 255, 0, 255, 0, 255, 16, 8, 0, 0, 0, 0]).copy(init, 4)
          init.writeUInt32BE(name.length, 20)
          send(Buffer.concat([init, name]))
          state = 3
        } else {
          if (buf.length < 1) return
          const t = buf[0]
          const size =
            t === 0 ? 20 : t === 2 ? (buf.length >= 4 ? 4 + 4 * buf.readUInt16BE(2) : 0) : t === 3 ? 10 : t === 4 ? 8 : t === 5 ? 6 : t === 6 ? (buf.length >= 8 ? 8 + Math.abs(buf.readInt32BE(4)) : 0) : t === 150 ? 10 : t === 248 ? (buf.length >= 9 ? 9 + buf[8] : 0) : t === 251 ? (buf.length >= 8 ? 8 + 16 * buf[6] : 0) : t === 255 ? 12 : -1
          if (size < 0) {
            buf = Buffer.alloc(0)
            return
          }
          if (size === 0 || buf.length < size) return
          if (t === 4 || t === 5) log.input.push(t)
          if (t === 3 && !sent) {
            sent = true
            const head = Buffer.alloc(16)
            head.writeUInt8(0, 0)
            head.writeUInt16BE(1, 2)
            head.writeUInt16BE(frame.width, 8)
            head.writeUInt16BE(frame.height, 10)
            head.writeInt32BE(0, 12) // Raw
            send(Buffer.concat([head, frame.rgba]))
          }
          buf = buf.subarray(size)
        }
      }
    })
  })
  return log
}
