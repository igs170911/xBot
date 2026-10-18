import {
  Archive,
  File,
  FileCode,
  FileImage,
  FileMusic,
  FileSpreadsheet,
  FileText,
  FileVideo,
  Folder,
  type LucideIcon,
} from 'lucide-react'
import type { FilesListOutput, FilesReadOutput } from '../api/generated'
import { stampLabel } from '../data/ui'

// 檔案面板（C33）共用的型別與小工具。

export type FileEntry = FilesListOutput['entries'][number]
export type FileKind = FileEntry['kind']
export type FileGroup = NonNullable<FileEntry['group']>
export type Preview = FilesReadOutput
export type Table = NonNullable<FilesReadOutput['table']>

export const GROUPS: { key: FileGroup; label: string }[] = [
  { key: 'result', label: '成果' },
  { key: 'upload', label: '你上傳的' },
  { key: 'lesson', label: '示範教學' },
  { key: 'peer', label: '從隊友來的' },
]

export function groupLabel(g: FileGroup | undefined): string {
  return GROUPS.find((x) => x.key === g)?.label ?? '成果'
}

type KindMeta = { icon: LucideIcon; color: string; label: string }

const KINDS: Record<FileKind, KindMeta> = {
  folder: { icon: Folder, color: '#7FC0FF', label: '資料夾' },
  image: { icon: FileImage, color: '#FFB547', label: '圖片' },
  pdf: { icon: FileText, color: '#FF6B7A', label: 'PDF' },
  csv: { icon: FileSpreadsheet, color: '#38D591', label: 'CSV' },
  sheet: { icon: FileSpreadsheet, color: '#38D591', label: 'Excel' },
  markdown: { icon: FileText, color: '#7FC0FF', label: 'Markdown' },
  code: { icon: FileCode, color: '#C9A4FF', label: '程式碼' },
  text: { icon: FileText, color: '#FCFCFC99', label: '文字' },
  office: { icon: FileText, color: '#5B9BFF', label: '文件' },
  archive: { icon: Archive, color: '#FCFCFC99', label: '壓縮檔' },
  audio: { icon: FileMusic, color: '#FF8AD8', label: '音訊' },
  video: { icon: FileVideo, color: '#FF8AD8', label: '影片' },
  other: { icon: File, color: '#FCFCFC99', label: '檔案' },
}

export function kindMeta(kind: FileKind | string | undefined): KindMeta {
  return KINDS[(kind ?? 'other') as FileKind] ?? KINDS.other
}

/** 由檔名猜種類（對話裡的檔案卡只有路徑）。 */
export function kindFromName(name: string): FileKind {
  const ext = name.toLowerCase().split('.').pop() ?? ''
  if (['png', 'jpg', 'jpeg', 'gif', 'webp', 'svg', 'bmp', 'avif'].includes(ext)) return 'image'
  if (ext === 'pdf') return 'pdf'
  if (ext === 'csv' || ext === 'tsv') return 'csv'
  if (['xlsx', 'xlsm', 'xls', 'ods'].includes(ext)) return 'sheet'
  if (ext === 'md' || ext === 'markdown') return 'markdown'
  if (['doc', 'docx', 'ppt', 'pptx', 'odt', 'odp', 'rtf', 'key', 'pages'].includes(ext)) return 'office'
  if (['zip', 'tar', 'gz', 'tgz', '7z', 'rar'].includes(ext)) return 'archive'
  if (['mp3', 'wav', 'm4a', 'ogg', 'flac'].includes(ext)) return 'audio'
  if (['mp4', 'webm', 'mov', 'mkv'].includes(ext)) return 'video'
  if (['txt', 'log'].includes(ext)) return 'text'
  if (['go', 'py', 'js', 'ts', 'tsx', 'json', 'yaml', 'yml', 'sh', 'html', 'css', 'sql', 'toml', 'xml'].includes(ext)) return 'code'
  return 'other'
}

/** 「320 B」「18 KB」「1.2 MB」 */
export function sizeLabel(n: number): string {
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${Math.round(n / 1024)} KB`
  if (n < 1024 * 1024 * 1024) return `${(n / 1024 / 1024).toFixed(n < 10 * 1024 * 1024 ? 1 : 0)} MB`
  return `${(n / 1024 / 1024 / 1024).toFixed(1)} GB`
}

/** 「剛剛」「10 分鐘前」，再早寫日期。 */
export function whenLabel(ms: number, now = Date.now()): string {
  if (!ms) return ''
  const d = now - ms
  if (d < 60_000) return '剛剛'
  if (d < 3_600_000) return `${Math.floor(d / 60_000)} 分鐘前`
  return stampLabel(ms, now)
}

/** /workspace/a/b.csv → a/b.csv */
export function relPath(p: string): string {
  return p.replace(/^\/workspace\/?/, '')
}

/** 上一層資料夾的完整路徑。 */
export function parentOf(p: string): string {
  const i = p.lastIndexOf('/')
  return i <= '/workspace'.length ? '/workspace' : p.slice(0, i)
}

export function baseName(p: string): string {
  return p.slice(p.lastIndexOf('/') + 1)
}

/** 麵包屑：[{name:'工作區', path:'/workspace'}, {name:'a', path:'/workspace/a'}…] */
export function crumbs(p: string): { name: string; path: string }[] {
  const out = [{ name: '工作區', path: '/workspace' }]
  let cur = '/workspace'
  for (const part of relPath(p).split('/').filter(Boolean)) {
    cur += '/' + part
    out.push({ name: part, path: cur })
  }
  return out
}

/** 依關鍵字過濾（檔名或路徑，不分大小寫）。 */
export function matches(e: FileEntry, q: string): boolean {
  const s = q.trim().toLowerCase()
  return !s || e.name.toLowerCase().includes(s) || e.path.toLowerCase().includes(s)
}

/** 檔案頁的網址：/bots/<id>/files?path=…（path 是檔案時同時打開預覽）。 */
export function filesHref(botId: string, path?: string, opts: { folder?: string } = {}): string {
  const q = new URLSearchParams()
  if (path) q.set('path', path)
  if (opts.folder) q.set('folder', opts.folder)
  const s = q.toString()
  return `/bots/${encodeURIComponent(botId)}/files${s ? '?' + s : ''}`
}

/** 「8 列 · 6 欄」或「共 1,200 列 · 這裡只顯示前 1,000 列」 */
export function tableSummary(t: Table, shown = t.rows.length): string {
  const cols = `${t.columns.length} 欄`
  if (t.total_rows < 0) return `前 ${shown.toLocaleString()} 列 · ${cols} · 檔案很大，沒有數完`
  if (shown < t.total_rows) return `共 ${t.total_rows.toLocaleString()} 列，這裡只顯示前 ${shown.toLocaleString()} 列 · ${cols}`
  return `${t.total_rows.toLocaleString()} 列 · ${cols}`
}
