import { useState } from 'react'
import { chatDeleteThread } from '../api/generated'
import { ConfirmDialog } from '../ui/Dialog'
import { useToast } from '../ui/toast-context'
import { forgetThread } from './assemble'
import { chatStore } from './chatStore'

// 刪除對話串（xbot.chat.delete_thread）：訊息刪了無法復原；Bot 的電腦、檔案、記憶不受影響。
export function DeleteThreadDialog({ threadId, open, onClose, onDeleted }: { threadId?: string; open: boolean; onClose: () => void; onDeleted: () => void }) {
  const toast = useToast()
  const [busy, setBusy] = useState(false)
  const remove = async () => {
    if (!threadId) return
    setBusy(true)
    try {
      await chatDeleteThread({ thread_id: threadId })
      chatStore.set((s) => forgetThread(s, threadId))
      onClose()
      onDeleted()
      toast.show({ kind: 'done', text: '已刪除對話串' })
    } catch (e) {
      toast.show({ kind: 'error', text: e instanceof Error ? e.message : String(e) })
    } finally {
      setBusy(false)
    }
  }
  return (
    <ConfirmDialog
      open={open}
      danger
      busy={busy}
      title="刪除這個對話串？"
      description="裡面的訊息會刪掉，無法復原。Bot 的電腦、檔案和記憶不受影響。"
      confirmText="刪除"
      onConfirm={() => void remove()}
      onCancel={onClose}
    />
  )
}
