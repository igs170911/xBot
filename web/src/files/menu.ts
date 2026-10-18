import { AtSign, FolderInput, Send, Trash2 } from 'lucide-react'
import type { MenuItem } from '../ui/Menu'
import type { FileEntry } from './data'
import { mentionInChat } from './mention'

// 一個檔案（或資料夾）的動作：在對話中提到、傳給其他 Bot、改名或搬移、刪除（C33；E07、L19）。

export type FileDialog = 'send' | 'move' | 'delete' | null

export function fileMenu(botId: string, entry: FileEntry | null, open: (d: FileDialog) => void): MenuItem[] {
  if (!entry) return []
  return [
    { label: '在對話中提到', icon: AtSign, onSelect: () => mentionInChat(botId, entry.path) },
    ...(entry.dir ? [] : [{ label: '傳給其他 Bot', icon: Send, onSelect: () => open('send') }]),
    { label: '改名或搬移', icon: FolderInput, onSelect: () => open('move') },
    { label: '刪除', icon: Trash2, danger: true, separator: true, onSelect: () => open('delete') },
  ]
}
