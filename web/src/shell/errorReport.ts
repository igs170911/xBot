import { systemClientReport } from '../api/generated'

// 網頁自己出錯（例外、沒接住的 Promise）時記進服務日誌（xbot.system.client_report，kind=js_error），
// 手機上的錯誤不用等使用者截圖（2026-10-21 檢查 bug 時發現網頁的錯誤從來沒有回報）。
// 只送錯誤訊息、出錯位置與頁面路徑，不送對話內容；同一則只送一次，每次開網頁最多 MAX 則。
const MAX = 5

export function errorReportData(message: string, where: string, stack: string, path: string): string {
  return [`msg=${message.slice(0, 300)}`, `at=${where}`, `path=${path}`, `stack=${stack.replace(/\s+/g, ' ').slice(0, 1500)}`].join(' | ')
}

export function startErrorReport(version: () => string) {
  const sent = new Set<string>()
  const send = (message: string, where: string, stack: string) => {
    if (sent.size >= MAX || sent.has(message)) return
    sent.add(message)
    const data = errorReportData(message, where, stack, location.pathname)
    void systemClientReport({ kind: 'js_error', version: version(), data }).catch(() => {})
  }
  window.addEventListener('error', (e) => {
    if (!e.message) return // 圖片、腳本載入失敗也會觸發 error，沒有訊息的不算
    send(e.message, `${e.filename}:${e.lineno}:${e.colno}`, e.error instanceof Error ? (e.error.stack ?? '') : '')
  })
  window.addEventListener('unhandledrejection', (e) => {
    const r = e.reason
    send(r instanceof Error ? `${r.name}: ${r.message}` : String(r), 'promise', r instanceof Error ? (r.stack ?? '') : '')
  })
}
