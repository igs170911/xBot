// Bot 做的網頁、儀表板（.html）在對話和檔案預覽裡直接顯示（2026-10-24 使用者：「可以做網頁跟 dashboard 在對話視窗裡顯示，跟 Grok bot 一樣」）。
//
// 網頁是 Bot 寫的，裡面的程式可能是從網路上抓來的：一律關在沙盒 iframe 裡（沒有 allow-same-origin，自己一個不透明的來源），
// 讀不到 XBot 的登入、cookie、localStorage，呼叫 XBot 的 API 也會被伺服器擋掉（Origin: null）。另外在網頁最前面加 CSP：
// 只准連 https（圖表用的 CDN、外部資料），本機的 XBot、內網一律連不到，也不能送出表單。
// 不提供「在新分頁開」：用 blob 網址開會變成和 XBot 同一個來源，網頁裡的程式就拿得到你的登入。

/** 網頁最多讀多少（xbot.files.read 的上限）；更大的請下載。 */
export const PAGE_MAX_BYTES = 1 << 20

/** 檔名是不是網頁。 */
export function isPage(name: string): boolean {
  return /\.html?$/i.test(name)
}

/** 網頁的 CSP：程式、樣式、圖片、字型、資料只准從 https 或網頁自己裡面來。 */
export const PAGE_CSP =
  "default-src 'none'; script-src 'unsafe-inline' 'unsafe-eval' https:; style-src 'unsafe-inline' https:; img-src data: blob: https:; " +
  "font-src data: https:; media-src data: blob: https:; connect-src https:; frame-src https:; form-action 'none'; base-uri 'none'"

/** iframe 的沙盒：可以跑程式、開外部連結（新分頁是外部網站自己的來源），不能和 XBot 同源、不能換掉整個 XBot 頁面。 */
export const PAGE_SANDBOX = 'allow-scripts allow-popups allow-popups-to-escape-sandbox allow-forms'

/** 把 CSP 放在網頁最前面（任何程式之前）：有 <!doctype> 放在它後面，沒有就放最開頭；瀏覽器會把它收進 <head>。 */
export function pageDoc(html: string): string {
  const meta = `<meta http-equiv="Content-Security-Policy" content="${PAGE_CSP}">`
  const doctype = /^\s*<!doctype[^>]*>/i.exec(html)
  if (doctype) return html.slice(0, doctype[0].length) + meta + html.slice(doctype[0].length)
  return meta + html
}
