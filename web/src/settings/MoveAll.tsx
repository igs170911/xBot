import { useEffect, useState } from 'react'
import { House, Truck } from 'lucide-react'
import { computersEgressStatus, computersMoveAll, type ComputersEgressStatusOutput } from '../api/generated'
import { MoveCard } from '../computer/MoveParts'
import { isActive, isRecent, loadMoves, moveTargets, useMoves } from '../computer/moveData'
import { onEvent } from '../data/events'
import { Avatar } from '../ui/Avatar'
import { Button } from '../ui/Button'
import { SegmentedControl } from '../ui/Controls'
import { Dialog } from '../ui/Dialog'
import { Icon } from '../ui/Icon'
import { useToast } from '../ui/toast-context'
import { errText } from './format'
import { Block, Group, Row } from './parts'
import { loadHosts, type HostView } from './hostsData'

// 設定 › 電腦與裝置（J06）的搬家：「全部搬到…」（16.1a：從驗證主機換到專用主機）、搬家進度、「從家裡出去」的狀態。

/** 「全部搬到…」：把一台主機上的 Bot 依序搬到另一台。 */
export function MoveAllDialog({ from, hosts, onClose }: { from: HostView | null; hosts: HostView[]; onClose: () => void }) {
  const toast = useToast()
  const targets = from ? moveTargets(from, hosts) : []
  const [to, setTo] = useState('')
  const [busy, setBusy] = useState(false)
  if (!from) return null
  const pick = to || targets[0]?.id || ''
  const target = targets.find((h) => h.id === pick)
  const go = async () => {
    if (!target) return
    setBusy(true)
    try {
      const out = await computersMoveAll({ from_host_id: from.id, to_host_id: target.id })
      const n = out.jobs.filter((j) => isActive(j) && j.from === from.id).length
      toast.show({ kind: 'done', text: n ? `開始把 ${n} 個 Bot 搬到 ${target.name}，一個做完再做下一個` : '沒有要搬的 Bot' })
      void loadMoves()
      void loadHosts()
      onClose()
    } catch (err) {
      toast.show({ kind: 'error', text: errText(err) })
    } finally {
      setBusy(false)
    }
  }
  return (
    <Dialog
      open
      onClose={onClose}
      busy={busy}
      width={520}
      title={`把 ${from.name} 上的 Bot 全部搬走`}
      description="每個 Bot 的家目錄、工作資料夾、引擎紀錄和瀏覽器登入都會跟著搬過去。一個搬完再搬下一個；正在工作的 Bot 輪到時會先跳過。"
      actions={
        <>
          <Button variant="secondary" onClick={onClose} disabled={busy}>
            取消
          </Button>
          <Button variant="primary" icon={Truck} loading={busy} disabled={!target || from.bots.length === 0} onClick={() => void go()}>
            全部搬到 {target?.name ?? '…'}
          </Button>
        </>
      }
    >
      {targets.length === 0 ? (
        <p className="m-0 text-ui-sm text-warning-ink">沒有其他在線上的主機可以搬過去。先新增或接回一台主機。</p>
      ) : (
        <SegmentedControl label="搬到" value={pick} onChange={setTo} options={targets.map((h) => ({ value: h.id, label: h.kind === 'local' ? '這台主機' : h.name }))} />
      )}
      <ul className="m-0 flex list-none flex-col gap-1.5 rounded-group bg-card p-3" aria-label="要搬的 Bot">
        {from.bots.length === 0 && <li className="text-ui-sm text-ink-2">這台上面沒有 Bot。</li>}
        {from.bots.map((b) => (
          <li key={b.id} className="flex items-center gap-2.5 text-ui-sm">
            <Avatar shape={b.shape} color={b.color} size={22} blink={false} />
            <span className="flex-1">{b.name}</span>
            <span className="text-meta text-ink-3">{b.running ? '開機中，會先停止' : '休眠'}</span>
          </li>
        ))}
      </ul>
      <p className="m-0 text-meta text-ink-3">途中失敗的 Bot 會留在 {from.name}，原本的資料不動。搬好後 {from.name} 上的舊資料保留 24 小時才刪。</p>
    </Dialog>
  )
}

/** 搬家進度：進行中、排隊中，以及 24 小時內做完的（看得到舊資料保留到什麼時候）。 */
export function MovesBlock() {
  const s = useMoves()
  const jobs = (s?.jobs ?? []).filter((j) => isRecent(j)).slice(0, 6)
  if (jobs.length === 0) return null
  return (
    <Block title="搬家" aside={<span className="text-[13px] text-ink-2 max-md:hidden">一次搬一個；登入狀態跟著走</span>}>
      <div className="flex flex-col gap-2" data-testid="moves">
        {jobs.map((j) => (
          <MoveCard key={j.id} job={j} compact={!isActive(j)} />
        ))}
      </div>
    </Block>
  )
}

/** 「從家裡出去」的狀態（每個 Bot 在電腦面板的「在哪裡跑」開關）。 */
export function EgressSummary() {
  const [st, setSt] = useState<ComputersEgressStatusOutput | null>(null)
  useEffect(() => {
    const load = () =>
      void computersEgressStatus({})
        .then(setSt)
        .catch(() => {})
    load()
    return onEvent((e) => {
      if (e.kind === 'computer.status') load()
    })
  }, [])
  if (!st) return null
  const n = st.bots.length
  const bad = st.hosts.filter((h) => h.backend && h.backend !== 'Running')
  const sub = !st.exit_node
    ? '雲端主機的機房 IP 常被購物網站要求驗證。在 Bot 的電腦面板打開「從家裡出去」，它的瀏覽器就經家裡那台電腦（Tailscale exit node）連外。'
    : `經 ${st.exit_node} 連外 · ${n ? `${n} 個 Bot 開著` : '目前沒有 Bot 開著'}` +
      (bad.length ? ` · ${bad.length} 台主機的 Tailscale 還沒連上` : '') +
      '。Tailscale 存取控制請只允許 tag:xbot-egress 連網際網路，Bot 才碰不到家裡的區網。'
  return (
    <Group>
      <Row title={<span className="inline-flex items-center gap-2"><Icon icon={House} size={16} />從家裡出去</span>} sub={sub} />
    </Group>
  )
}
