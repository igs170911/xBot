import { useState } from 'react'
import { hostsRemove } from '../api/generated'
import { ConfirmDialog } from '../ui/Dialog'
import { useToast } from '../ui/toast-context'
import { errText } from './format'
import { loadHosts, type HostView } from './hostsData'

/** 移除主機的確認（confirm 級、不能代為核准）。 */
export function useRemoveHost(after?: () => void) {
  const toast = useToast()
  const [target, setTarget] = useState<HostView | null>(null)
  const [busy, setBusy] = useState(false)
  const confirm = async () => {
    if (!target) return
    setBusy(true)
    try {
      await hostsRemove({ id: target.id })
      toast.show({ kind: 'done', text: `已移除 ${target.name}` })
      setTarget(null)
      void loadHosts()
      after?.()
    } catch (err) {
      toast.show({ kind: 'error', text: errText(err) })
    } finally {
      setBusy(false)
    }
  }
  const dialog = (
    <ConfirmDialog
      open={!!target}
      title={target ? `移除 ${target.name}？` : ''}
      description={
        target && target.bots.length > 0
          ? `上面還有 ${target.bots.map((b) => b.name).join('、')}。有電腦的 Bot 要先搬走或刪掉電腦才能移除。`
          : 'XBot 會斷線並忘記這台主機。主機上的 xbot 帳號和 Docker 都會留著，要清掉請自己在那台處理。'
      }
      confirmText="移除"
      danger
      busy={busy}
      onConfirm={confirm}
      onCancel={() => setTarget(null)}
    />
  )
  return { ask: setTarget, dialog }
}
