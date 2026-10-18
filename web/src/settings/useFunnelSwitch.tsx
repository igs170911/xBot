import { useState } from 'react'
import { networkFunnelSet } from '../api/generated'
import { loadNetwork, setNetwork } from '../data/network'
import { ConfirmDialog } from '../ui/Dialog'
import { useToast } from '../ui/toast-context'
import { errText } from './format'

/** Funnel 開關（要確認才開；關閉不用）。網路頁和「外部代理與 MCP」共用。 */
export function useFunnelSwitch() {
  const toast = useToast()
  const [asking, setAsking] = useState(false)
  const [busy, setBusy] = useState(false)
  const set = async (enabled: boolean, allow?: string[]) => {
    const cur = (await loadNetwork())?.funnel.allow_ips ?? []
    setBusy(true)
    try {
      const st = await networkFunnelSet({ enabled, allow_ips: allow ?? cur })
      setNetwork(st)
      toast.show({ kind: 'done', text: enabled ? '外部入口已開啟' : '外部入口已關閉' })
      setAsking(false)
    } catch (e) {
      toast.show({ kind: 'error', text: errText(e) })
    } finally {
      setBusy(false)
    }
  }
  const toggle = (next: boolean) => (next ? setAsking(true) : void set(false))
  const dialog = (
    <ConfirmDialog
      open={asking}
      title="開啟外部入口？"
      description="網際網路上的任何人都連得到這三個路徑：/mcp（一律要代理權杖）、/api/v1/agents.pair（要一次性配對碼）、/hooks/…（要網址金鑰）。網頁、對話與其他 API 不會開放。不用時請關掉。"
      confirmText="開啟"
      danger
      busy={busy}
      onConfirm={() => void set(true)}
      onCancel={() => setAsking(false)}
    />
  )
  return { toggle, set, busy, dialog }
}

