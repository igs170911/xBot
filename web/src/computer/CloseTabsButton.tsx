import { useState } from 'react'
import { PanelTopClose } from 'lucide-react'
import { computersCloseTabs } from '../api/generated'
import { Button } from '../ui/Button'
import { useToast } from '../ui/toast-context'

// 關掉 Bot 電腦瀏覽器裡的分頁（查完網頁沒關的），只留一個空白分頁；登入不受影響。
export function CloseTabsButton({ botId, className, disabled }: { botId: string; className?: string; disabled?: boolean }) {
  const toast = useToast()
  const [busy, setBusy] = useState(false)
  const run = async () => {
    setBusy(true)
    try {
      const out = await computersCloseTabs({ bot_id: botId })
      toast.show({ kind: 'done', text: out.closed > 0 ? `關掉了 ${out.closed} 個分頁` : '沒有要關的分頁' })
    } catch (err) {
      toast.show({ kind: 'error', text: err instanceof Error ? err.message : String(err) })
    } finally {
      setBusy(false)
    }
  }
  return (
    <Button size="sm" variant="outline" icon={PanelTopClose} loading={busy} disabled={disabled} onClick={() => void run()} className={className}>
      關閉瀏覽器分頁
    </Button>
  )
}
