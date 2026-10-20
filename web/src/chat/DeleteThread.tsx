import { useState } from 'react'
import { chatDeleteThread } from '../api/generated'
import { ConfirmDialog } from '../ui/Dialog'
import { useToast } from '../ui/toast-context'
import { forgetThread } from './assemble'
import { chatStore } from './chatStore'
import type { Thread } from './ThreadMenu'

// 刪除對話串（xbot.chat.delete_thread）：訊息刪了無法復原；Bot 的電腦、檔案、記憶不受影響。
// 可以刪任何一條（對話串選單每一條的垃圾桶），確認時寫出是哪一條。
export function DeleteThreadDialog({ thread, onClose, onDeleted }: { thread: Pick<Thread, 'id' | 'title'> | null; onClose: () => void; onDeleted: (id: string) => void }) {
  const toast = useToast()
  const [busy, setBusy] = useState(false)
  const remove = async () => {
    if (!thread) return
    setBusy(true)
    try {
      await chatDeleteThread({ thread_id: thread.id })
      chatStore.set((s) => forgetThread(s, thread.id))
      onClose()
      onDeleted(thread.id)
      toast.show({ kind: 'done', text: '已刪除對話串' })
    } catch (e) {
      toast.show({ kind: 'error', text: e instanceof Error ? e.message : String(e) })
    } finally {
      setBusy(false)
    }
  }
  return (
    <ConfirmDialog
      open={!!thread}
      danger
      busy={busy}
      title={`刪除「${thread?.title || '新的對話'}」？`}
      description="裡面的訊息會刪掉，無法復原。Bot 的電腦、檔案和記憶不受影響。"
      confirmText="刪除"
      onConfirm={() => void remove()}
      onCancel={onClose}
    />
  )
}
