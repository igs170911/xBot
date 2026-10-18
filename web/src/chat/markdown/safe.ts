// Bot 的回覆是資料，不是程式（SEC-8）：連結與圖片只放行安全的網址，其他一律當文字。

// 拿掉空白與控制字元再判斷（擋 "java\nscript:" 這類寫法）。
function squash(url: string): string {
  // eslint-disable-next-line no-control-regex
  return url.replace(/[\s\u0000-\u001f\u007f]+/g, '')
}

/** 可以點的連結：http(s)、mailto，或頁內錨點（#…）。其他回 null。 */
export function safeHref(href: string | null | undefined): string | null {
  if (!href) return null
  const h = squash(href)
  if (/^https?:\/\//i.test(h) || /^mailto:/i.test(h)) return h
  if (/^#[\w-]*$/.test(h)) return h
  return null
}

/** 可以顯示的圖片：http(s)，或 data: 的點陣圖（不含 SVG，避免夾帶腳本）。 */
export function safeImageSrc(src: string | null | undefined): string | null {
  if (!src) return null
  const s = squash(src)
  if (/^https?:\/\//i.test(s)) return s
  if (/^data:image\/(png|jpe?g|gif|webp|avif);base64,/i.test(s)) return s
  return null
}

const NAMED: Record<string, string> = { amp: '&', lt: '<', gt: '>', quot: '"', apos: "'", nbsp: ' ', copy: '©', reg: '®', hellip: '…', mdash: '—', ndash: '–', times: '×', divide: '÷', middot: '·' }

/** Markdown 文字裡的 &amp; &#39; 之類還原成字（React 會再自己跳脫，不會變成 HTML）。 */
export function decodeEntities(s: string): string {
  if (!s.includes('&')) return s
  return s.replace(/&(#\d{1,7}|#x[0-9a-f]{1,6}|[a-z]{2,8});/gi, (m, body: string) => {
    if (body[0] === '#') {
      const code = body[1] === 'x' || body[1] === 'X' ? parseInt(body.slice(2), 16) : parseInt(body.slice(1), 10)
      return code > 0 && code <= 0x10ffff ? String.fromCodePoint(code) : m
    }
    return NAMED[body.toLowerCase()] ?? m
  })
}
