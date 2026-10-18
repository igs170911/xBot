// 對話的附件（design/C05「8 · 附件」）：選檔、拖進對話、貼上圖片時，先上傳到 Bot 電腦的 /workspace/uploads/
// （xbot.files.upload），送出時把檔案路徑附在訊息後面，Bot 就知道去哪裡讀。

/** 一則訊息最多附幾個檔案（internal/limits UploadFilesMax，出自 LazyBoy UPLOAD_LIMITS）。 */
export const ATTACH_MAX = 10

/** 上傳到哪個資料夾（/workspace 底下）。 */
export const ATTACH_DIR = 'uploads'

export type Uploaded = { name: string; path: string; size: number }

export type Attachment = {
  id: number
  name: string
  size: number
  /** 圖片的本機預覽網址（URL.createObjectURL）；不是圖片時沒有。 */
  preview?: string
  state: 'uploading' | 'done' | 'error'
  /** 上傳好的完整路徑，/workspace/uploads/… */
  path?: string
  error?: string
}

/** 送出的文字：你打的字，加上每個附件的路徑（一行一個）。 */
export function withAttachments(text: string, paths: string[]): string {
  const t = text.trim()
  if (!paths.length) return t
  const lines = paths.map((p) => `附件：${p}`)
  return t ? `${t}\n\n${lines.join('\n')}` : lines.join('\n')
}

/** 貼上或拖進來的檔案（剪貼簿裡的圖片沒有檔名時補一個）。 */
export function filesFrom(list: FileList | File[] | null | undefined, now = Date.now()): File[] {
  if (!list) return []
  return Array.from(list).map((f, i) => {
    if (f.name && f.name !== 'image.png') return f
    const ext = (f.type.split('/')[1] || 'png').replace('jpeg', 'jpg')
    const d = new Date(now)
    const stamp = `${d.getFullYear()}${String(d.getMonth() + 1).padStart(2, '0')}${String(d.getDate()).padStart(2, '0')}-${String(d.getHours()).padStart(2, '0')}${String(d.getMinutes()).padStart(2, '0')}${String(d.getSeconds()).padStart(2, '0')}`
    return new File([f], `貼上的圖片-${stamp}${i ? `-${i + 1}` : ''}.${ext}`, { type: f.type })
  })
}

/** 拖進來的是不是檔案（不是拖文字或連結）。 */
export function hasFiles(dt: DataTransfer | null): boolean {
  return !!dt && Array.from(dt.types ?? []).includes('Files')
}
