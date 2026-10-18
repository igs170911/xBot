import { useCallback, useEffect, useState } from 'react'
import { LoaderCircle, RefreshCw } from 'lucide-react'
import { computersUpdate, computersUpdateStatus, type ComputersUpdateOutput } from '../api/generated'
import { onEvent } from '../data/events'
import { Button } from '../ui/Button'
import { ConfirmDialog } from '../ui/Dialog'
import { Icon } from '../ui/Icon'
import { useToast } from '../ui/toast-context'
import { errText } from './format'
import { loadHosts } from './hostsData'
import { Note } from './parts'

// 設定 › 電腦與裝置的「更新電腦」（BX-15、B13）：make box 換了映像、或 XBot 更新了電腦的安全設定以後，
// 主機清單標出「映像過期」、電腦標出要更新。這裡用 xbot.computers.update 的預演看要更新哪些，按一下在背景更新
// （一台主機做完再做下一台，正在工作的 Bot 跳過），進度看 computers.update 事件與 xbot.computers.update_status。

export type UpdatePlan = ComputersUpdateOutput

/** 要更新幾台電腦、幾台主機要先傳新映像。 */
export function planSummary(p: UpdatePlan): { bots: number; images: number; names: string[] } {
  const hosts = p.hosts.filter((h) => h.state !== 'skipped')
  return {
    bots: p.total,
    images: hosts.filter((h) => h.image_outdated).length,
    names: hosts.flatMap((h) => h.bots.map((b) => b.name)),
  }
}

/** 「更新電腦」的狀態：預演的計畫，或進行中的進度（事件帶來的）。 */
export function useComputerUpdate() {
  const [plan, setPlan] = useState<UpdatePlan | null>(null)
  const [progress, setProgress] = useState<UpdatePlan | null>(null)
  const reload = useCallback(() => {
    computersUpdate({}, { dryRun: true })
      .then(setPlan)
      .catch(() => setPlan(null))
  }, [])
  useEffect(() => {
    reload()
    // 已經在更新（例如 make update 叫的，或別的分頁按的）：先顯示目前的進度。
    computersUpdateStatus({})
      .then((p) => {
        if (p.running) setProgress(p)
      })
      .catch(() => {})
    return onEvent((e) => {
      if (e.kind !== 'computers.update') return
      const p = e.payload as UpdatePlan | undefined
      if (!p) return
      setProgress(p)
      if (!p.running) {
        reload()
        void loadHosts()
      }
    })
  }, [reload])
  return { plan, progress, setProgress, reload }
}

export function UpdateComputers() {
  const toast = useToast()
  const { plan, progress, setProgress } = useComputerUpdate()
  const [ask, setAsk] = useState(false)
  const [busy, setBusy] = useState(false)
  const running = !!progress?.running

  if (running && progress) {
    return (
      <Note icon={<Icon icon={LoaderCircle} size={18} className="animate-spin" />}>
        <span data-testid="update-progress">
          正在更新電腦 {progress.done}/{progress.total}
          {progress.current ? ` · 正在處理${progress.current}` : ''}。正在工作的 Bot 會跳過，做完再更新一次。
        </span>
      </Note>
    )
  }
  const done = progress && !progress.running ? progress : null
  const sum = plan ? planSummary(plan) : null
  if (!sum || (sum.bots === 0 && sum.images === 0)) {
    if (!done) return null
    const skipped = done.hosts.flatMap((h) => h.bots).filter((b) => b.state === 'skipped' || b.state === 'failed')
    return (
      <Note icon={<Icon icon={RefreshCw} size={18} />}>
        <span data-testid="update-done">電腦更新好了{skipped.length > 0 ? `；${skipped.map((b) => `${b.name}（${b.reason || '跳過'}）`).join('、')}沒有更新` : ''}。</span>
      </Note>
    )
  }

  const go = async () => {
    setBusy(true)
    try {
      setProgress(await computersUpdate({}))
      setAsk(false)
    } catch (err) {
      toast.show({ kind: 'error', text: errText(err) })
    } finally {
      setBusy(false)
    }
  }
  const what = [sum.bots > 0 ? `${sum.bots} 台電腦要更新` : '', sum.images > 0 ? `${sum.images} 台主機的電腦映像過期` : ''].filter(Boolean).join('，')
  return (
    <>
      <Note
        tone="warning"
        icon={<Icon icon={RefreshCw} size={18} />}
        action={
          <Button size="sm" variant="primary" icon={RefreshCw} onClick={() => setAsk(true)}>
            更新電腦
          </Button>
        }
      >
        <span data-testid="update-needed">
          {what}。更新會用新的映像重建電腦，檔案和瀏覽器登入都保留。
        </span>
      </Note>
      <ConfirmDialog
        open={ask}
        title="更新電腦？"
        description={`${sum.names.length > 0 ? `會重建：${sum.names.join('、')}。` : ''}${sum.images > 0 ? '先把新映像傳到主機。' : ''}一台主機做完再做下一台；正在工作的 Bot 會跳過。檔案和登入都保留。`}
        confirmText="開始更新"
        busy={busy}
        onConfirm={() => void go()}
        onCancel={() => setAsk(false)}
      />
    </>
  )
}
