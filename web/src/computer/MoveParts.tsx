import { useCallback, useEffect, useState } from 'react'
import { Check, CircleDashed, House, LoaderCircle, Truck, X } from 'lucide-react'
import { computersEgressStatus, computersMove, computersMoveCancel, computersSetEgress, computersStatus, type ComputersEgressStatusOutput } from '../api/generated'
import { onEvent } from '../data/events'
import { useHosts, loadHosts, type HostView } from '../settings/hostsData'
import { Button } from '../ui/Button'
import { SegmentedControl, Switch } from '../ui/Controls'
import { Dialog } from '../ui/Dialog'
import { Icon } from '../ui/Icon'
import { useToast } from '../ui/toast-context'
import { cx } from '../ui/cx'
import { hostLabel, isActive, isRecent, jobOf, loadMoves, moveDetail, moveStep, moveSteps, useMoves, type MoveJob } from './moveData'

function errText(err: unknown): string {
  return err instanceof Error ? err.message : String(err)
}

function hostNote(h: HostView | undefined, home: boolean): string {
  if (!h) return ''
  if (h.kind === 'local') return '家裡 · 用你家裡的網路與登入過的網站；這台關機時不會跑'
  if (h.status !== 'online') return `${h.name} 目前離線 · 工作先排隊，回來後自動重連`
  return `${h.name} · 24 小時開著` + (home ? ' · 連外從家裡出去' : ' · 用機房的網路連外')
}

/** 搬家中的卡片（E10「搬家中 · 換到另一台主機」）：4 步、進度條、取消。 */
export function MoveCard({ job, compact }: { job: MoveJob; compact?: boolean }) {
  const toast = useToast()
  const [busy, setBusy] = useState(false)
  const step = moveStep(job)
  const active = isActive(job)
  const failed = job.state === 'failed' || job.state === 'canceled'
  const cancel = async () => {
    setBusy(true)
    try {
      await computersMoveCancel({ bot_id: job.bot_id })
    } catch (err) {
      toast.show({ kind: 'error', text: errText(err) })
    } finally {
      setBusy(false)
      void loadMoves()
    }
  }
  return (
    <section className="flex flex-col gap-2.5 rounded-group bg-panel px-4 py-3.5" data-testid="move-card" aria-label={`${job.bot_name}搬家`}>
      <div className="flex items-center gap-2">
        <Icon icon={Truck} size={16} className={failed ? 'text-warning' : 'text-ink-2'} />
        <h3 className="m-0 min-w-0 flex-1 truncate text-ui">
          {job.bot_name}：{job.from_name} → {job.to_name}
        </h3>
        {!failed && <span className="flex-none text-meta text-success">登入狀態跟著走</span>}
      </div>
      {!compact && active && (
        <ol className="m-0 grid list-none grid-cols-2 gap-x-4 gap-y-1.5 p-0 text-sub" aria-label="搬家的步驟">
          {moveSteps(job).map((label, i) => {
            const done = i < step
            const now = i === step && job.state === 'running'
            return (
              <li key={label} className={cx('flex items-center gap-1.5', done || now ? 'text-ink' : 'text-ink-3')} aria-current={now ? 'step' : undefined}>
                <Icon icon={done ? Check : now ? LoaderCircle : CircleDashed} size={14} className={cx(done && 'text-success', now && 'animate-spin text-accent-ink')} />
                {label}
              </li>
            )
          })}
        </ol>
      )}
      {(active || job.state === 'done') && (
        <span className="h-1 overflow-hidden rounded-full bg-line" role="progressbar" aria-valuemin={0} aria-valuemax={100} aria-valuenow={job.percent} aria-label="搬家進度">
          <span className="block h-full rounded-full bg-accent transition-[width]" style={{ width: `${job.percent}%` }} />
        </span>
      )}
      <p className={cx('m-0 text-meta', failed ? 'text-warning-ink' : 'text-ink-2')}>{moveDetail(job)}</p>
      {active && (
        <div className="flex justify-end">
          <Button size="sm" variant="outline" icon={X} loading={busy} onClick={() => void cancel()}>
            取消搬家
          </Button>
        </div>
      )}
    </section>
  )
}

/** 搬家確認（切換「在哪裡跑」或按「搬到這台主機」時）。 */
export function MoveDialog({ botId, botName, from, to, onClose }: { botId: string; botName: string; from?: HostView; to: HostView | null; onClose: () => void }) {
  const toast = useToast()
  const [busy, setBusy] = useState(false)
  if (!to) return null
  const go = async () => {
    setBusy(true)
    try {
      await computersMove({ bot_id: botId, host_id: to.id })
      void loadMoves()
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
      title={`把${botName}的電腦搬到 ${to.name}？`}
      description="切換主機就是搬家：家目錄、工作資料夾、引擎紀錄和瀏覽器登入都會跟著搬過去，不是重新建立。"
      actions={
        <>
          <Button variant="secondary" onClick={onClose} disabled={busy}>
            取消
          </Button>
          <Button variant="primary" icon={Truck} loading={busy} onClick={() => void go()}>
            開始搬家
          </Button>
        </>
      }
    >
      <ol className="m-0 flex flex-col gap-1.5 rounded-group bg-card px-4 py-3 pl-8 text-sub text-ink-2">
        <li>停止{from ? `在 ${from.name} 上的` : ''}電腦（正在做的事要先停下）</li>
        <li>打包 home／workspace／hermes，經加密連線傳到 {to.name}</li>
        <li>在 {to.name} 還原、逐一核對檔案，原本開著的會在那邊開機</li>
      </ol>
      <p className="m-0 text-meta text-ink-3">
        途中失敗或取消時留在原主機，原本的資料不動。搬好後原主機上的舊資料保留 24 小時才刪。
        {to.kind !== 'local' && ' 雲端主機用機房的網路連外，常被購物網站要求驗證：可以開「從家裡出去」。'}
      </p>
    </Dialog>
  )
}

/** 「在哪裡跑」（E01）：這個 Bot 的電腦放在哪台主機；換主機＝搬家。 */
export function WhereSection({ botId, botName, hostId }: { botId: string; botName: string; hostId: string }) {
  const hs = useHosts()
  const moves = useMoves()
  const [target, setTarget] = useState<HostView | null>(null)
  const egress = useEgress(botId)
  const hosts = hs.hosts ?? []
  const current = hosts.find((h) => h.id === hostId) ?? hosts.find((h) => h.control_center)
  const job = jobOf(moves, botId)
  const moving = job && isActive(job)
  const shown = moving ? hosts.find((h) => h.id === job.to) : current
  const recent = job && !moving && isRecent(job) ? job : undefined
  if (!hs.hosts || hosts.length === 0) return null
  return (
    <section className="flex flex-col gap-2.5 rounded-group bg-panel px-4 py-3.5" aria-label="在哪裡跑">
      <div>
        <h3 className="m-0 text-ui">在哪裡跑</h3>
        <p className="m-0 mt-0.5 text-meta text-ink-2">這個 Bot 的電腦放在哪一台主機。換主機會連登入狀態一起搬過去。</p>
      </div>
      {hosts.length > 1 && (
        <SegmentedControl
          label="在哪裡跑"
          value={shown?.id ?? ''}
          onChange={(id) => {
            const h = hosts.find((x) => x.id === id)
            if (!h || h.id === current?.id || moving) return
            setTarget(h)
          }}
          options={hosts.map((h) => ({ value: h.id, label: hostLabel(h) }))}
        />
      )}
      <p className="m-0 text-meta text-ink-2">{hostNote(shown, egress.home)}</p>
      {job && (moving || recent) && <MoveCard job={job} compact={!moving} />}
      {current && <EgressRow botId={botId} botName={botName} host={current} state={egress} />}
      <MoveDialog
        botId={botId}
        botName={botName}
        from={current}
        to={target}
        onClose={() => {
          setTarget(null)
          void loadHosts()
        }}
      />
    </section>
  )
}

type EgressState = { home: boolean; status: ComputersEgressStatusOutput | null; reload: () => void }

function useEgress(botId: string): EgressState {
  const [home, setHome] = useState(false)
  const [status, setStatus] = useState<ComputersEgressStatusOutput | null>(null)
  const reload = useCallback(() => {
    computersStatus({ bot_id: botId })
      .then((v) => setHome(v.home))
      .catch(() => {})
    computersEgressStatus({})
      .then(setStatus)
      .catch(() => {})
  }, [botId])
  useEffect(() => {
    reload()
    return onEvent((e) => {
      if ((e.kind === 'computer.status' && e.bot_id === botId) || e.kind === 'computers.move') reload()
    })
  }, [botId, reload])
  return { home, status, reload }
}

/** 「從家裡出去」（CL-7）：這個 Bot 的電腦連外經過家裡那台電腦（Tailscale exit node）。 */
function EgressRow({ botId, botName, host, state }: { botId: string; botName: string; host: HostView; state: EgressState }) {
  const toast = useToast()
  const [ask, setAsk] = useState(false)
  const [busy, setBusy] = useState(false)
  const st = state.status
  const hostState = st?.hosts.find((h) => h.host_id === host.id)
  const off = async () => {
    setBusy(true)
    try {
      await computersSetEgress({ bot_id: botId, home: false })
      toast.show({ kind: 'done', text: `${botName}的電腦改回直接從 ${host.name} 連外` })
      state.reload()
    } catch (err) {
      toast.show({ kind: 'error', text: errText(err) })
    } finally {
      setBusy(false)
    }
  }
  let sub = host.kind === 'local' ? '這台主機本來就在家裡，通常不用開。' : '雲端主機的機房 IP 常被蝦皮、Threads 要求驗證；開了以後瀏覽器經家裡的網路連外。'
  if (state.home) {
    sub = hostState?.backend === 'Running' ? `經 ${st?.exit_node || '家裡的電腦'} 連外` + (hostState.exit_node_online ? '' : '（家裡那台現在離線）') : `經 ${st?.exit_node || '家裡的電腦'} 連外 · 正在連線 Tailscale`
    if (hostState?.backend === 'NeedsLogin') sub = 'Tailscale 登入金鑰無效或過期，請重新設定'
  }
  return (
    <div className="flex items-start gap-3 border-t border-line pt-3" data-testid="egress-row">
      <Icon icon={House} size={16} className="mt-0.5 text-ink-2" />
      <span className="min-w-0 flex-1">
        <span className="block text-sub">從家裡出去</span>
        <span className="block text-meta text-ink-2">{sub}</span>
      </span>
      <Switch label="從家裡出去" checked={state.home} disabled={busy} onChange={(on) => (on ? setAsk(true) : void off())} />
      {ask && (
        <EgressDialog
          botId={botId}
          botName={botName}
          status={st}
          onClose={(done) => {
            setAsk(false)
            if (done) state.reload()
          }}
        />
      )}
    </div>
  )
}

function EgressDialog({ botId, botName, status, onClose }: { botId: string; botName: string; status: ComputersEgressStatusOutput | null; onClose: (done: boolean) => void }) {
  const toast = useToast()
  const [node, setNode] = useState(status?.exit_node ?? '')
  const [key, setKey] = useState('')
  const [busy, setBusy] = useState(false)
  const needKey = !status?.has_auth_key
  const go = async () => {
    setBusy(true)
    try {
      const out = await computersSetEgress({ bot_id: botId, home: true, exit_node: node.trim() || undefined, auth_key: key.trim() || undefined })
      toast.show({
        kind: 'done',
        text: out.pending ? `已開啟，${botName}做完手上的工作、下次開機後生效` : out.rebuilt ? `已開啟，${botName}的電腦重開好了` : '已開啟「從家裡出去」',
      })
      onClose(true)
    } catch (err) {
      toast.show({ kind: 'error', text: errText(err) })
    } finally {
      setBusy(false)
    }
  }
  const field = 'h-10 rounded-field border border-line bg-inset px-3 text-ui outline-none focus:border-focus'
  return (
    <Dialog
      open
      onClose={() => onClose(false)}
      busy={busy}
      title={`讓${botName}從家裡出去？`}
      description="這個 Bot 的瀏覽器與工具會經過家裡那台電腦（Tailscale exit node）連外，網站看到的是你家裡的網路。電腦開著時會重開一次，資料和登入都保留。"
      actions={
        <>
          <Button variant="secondary" onClick={() => onClose(false)} disabled={busy}>
            取消
          </Button>
          <Button variant="primary" loading={busy} disabled={!node.trim() || (needKey && !key.trim())} onClick={() => void go()}>
            開啟
          </Button>
        </>
      }
    >
      <label className="flex flex-col gap-1">
        <span className="text-meta text-ink-2">家裡那台電腦在 Tailscale 上的名稱</span>
        <input className={field} value={node} onChange={(e) => setNode(e.target.value)} placeholder="例：home-pc 或 100.101.7.23" aria-label="家裡那台電腦" />
      </label>
      <label className="flex flex-col gap-1">
        <span className="text-meta text-ink-2">Tailscale 登入金鑰{needKey ? '' : '（已存，不換可以留空）'}</span>
        <input className={field} type="password" value={key} onChange={(e) => setKey(e.target.value)} placeholder="tskey-auth-…" aria-label="Tailscale 登入金鑰" autoComplete="off" />
      </label>
      <ul className="m-0 flex flex-col gap-1 rounded-group bg-card px-4 py-3 pl-8 text-meta text-ink-2">
        <li>家裡那台要在 Tailscale 設成 exit node，而且要開著。</li>
        <li>登入金鑰請加上標籤 tag:xbot-egress，並在 Tailscale 存取控制只允許它連網際網路（autogroup:internet），這樣 Bot 碰不到你家裡的區網。</li>
        <li>只對認得 proxy 設定的程式有效（瀏覽器、Python、curl）；其他程式仍從主機直接連外。</li>
      </ul>
    </Dialog>
  )
}
