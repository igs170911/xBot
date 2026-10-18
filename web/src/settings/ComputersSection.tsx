import { useState } from 'react'
import { ChevronDown, Cloud, House, Plus, RefreshCw, Server, ShieldCheck, Smartphone, TriangleAlert, Truck, WifiOff } from 'lucide-react'
import { hostsCheck, hostsSetDefault } from '../api/generated'
import { linkClick, navigate } from '../router'
import { Avatar } from '../ui/Avatar'
import { Button } from '../ui/Button'
import { SegmentedControl } from '../ui/Controls'
import { Icon } from '../ui/Icon'
import { StatusChip, type Tone } from '../ui/Pill'
import { useToast } from '../ui/toast-context'
import { cx } from '../ui/cx'
import { errText } from './format'
import { Block, Group, Note, Row, SectionHeader } from './parts'
import { DevicesEntry } from './NetworkPage'
import { STATUS_LABEL, hostSub, loadHosts, useHosts, type HostView, type HostsState } from './hostsData'
import { PairPhoneDialog } from '../mobile/PhoneSettings'
import { connectionKind } from '../mobile/pwa'
import { useRemoveHost } from './removeHost'
import { look } from '../data/bots'
import { EgressSummary, MoveAllDialog, MovesBlock } from './MoveAll'
import { moveTargets } from '../computer/moveData'

// 設定 › 電腦與裝置（J06，PRD §16 CL-1、CL-3、CL-6、CL-8）：主機清單（狀態、資源、上面的 Bot、開機台數／上限）、
// 新增主機（J11）、新 Bot 預設放哪台、整台搬家（CL-5、16.1a）與「從家裡出去」（CL-7）的狀態。

const TONE: Record<string, Tone> = { online: 'success', offline: 'neutral', updating: 'accent', failed: 'danger', unknown: 'neutral' }

export function HostStatusChip({ h }: { h: HostView }) {
  return (
    <StatusChip tone={TONE[h.status] ?? 'neutral'} size="sm" icon={h.status === 'offline' ? WifiOff : undefined}>
      {STATUS_LABEL[h.status] ?? h.status}
    </StatusChip>
  )
}

export function HostIcon({ h }: { h: HostView }) {
  const icon = h.kind === 'local' ? House : h.status === 'online' || h.status === 'updating' ? Cloud : Server
  return (
    <span className="flex size-9 flex-none items-center justify-center rounded-field bg-control text-ink">
      <Icon icon={icon} size={18} />
    </span>
  )
}

function Capacity({ h }: { h: HostView }) {
  if (h.status !== 'online' && h.status !== 'updating' && h.kind !== 'local') return <span className="w-[96px] text-ui-sm text-ink-3 max-sm:hidden">—</span>
  const max = h.max_running
  const pct = max > 0 ? Math.min(100, (h.running / max) * 100) : h.running > 0 ? 30 : 0
  const full = max > 0 && h.running >= max
  return (
    <span className="flex w-[96px] flex-col gap-1.5 max-sm:w-full" title={max > 0 ? `同時開機上限 ${max} 台` : '不限台數'}>
      <span className={cx('text-[13px] leading-none', full ? 'text-warning-ink' : 'text-ink')}>
        開機 {h.running}/{max > 0 ? max : '不限'}
      </span>
      <span className="h-1 overflow-hidden rounded-full bg-line" aria-hidden="true">
        <span className={cx('block h-full rounded-full', full ? 'bg-warning' : 'bg-accent')} style={{ width: `${pct}%` }} />
      </span>
    </span>
  )
}

function BotFaces({ h }: { h: HostView }) {
  if (h.bots.length === 0) return <span className="text-ui-sm text-ink-3">沒有 Bot</span>
  const shown = h.bots.slice(0, 4)
  return (
    <span className="flex items-center" title={h.bots.map((b) => b.name).join('、')}>
      {shown.map((b, i) => (
        <span key={b.id} className={cx('rounded-full', i > 0 && '-ml-1.5')}>
          <Avatar {...look(b)} size={24} blink={false} state={h.status === 'offline' ? 'sleeping' : b.running ? 'working' : 'idle'} />
        </span>
      ))}
      {h.bots.length > shown.length && <span className="ml-1.5 text-meta text-ink-2">+{h.bots.length - shown.length}</span>}
    </span>
  )
}

function HostRow({ h, s, onRemove, onMove }: { h: HostView; s: HostsState; onRemove: (h: HostView) => void; onMove: (h: HostView) => void }) {
  const toast = useToast()
  const [busy, setBusy] = useState(false)
  const to = `/settings/computers/hosts/${encodeURIComponent(h.id)}`
  const reconnect = async () => {
    setBusy(true)
    try {
      const r = await hostsCheck({ id: h.id })
      toast.show({ kind: r.host.status === 'online' ? 'done' : 'error', text: r.host.status === 'online' ? `${h.name} 已重新連上` : `${h.name} 還是連不上：${r.host.status_error || '稍後會自動再試'}` })
      void loadHosts()
    } catch (err) {
      toast.show({ kind: 'error', text: errText(err) })
    } finally {
      setBusy(false)
    }
  }
  const offline = h.status === 'offline' || h.status === 'failed'
  return (
    <div className="flex min-h-16 items-center gap-3 border-line px-4 py-3 not-first:border-t max-sm:flex-wrap" data-testid="host-row">
      <HostIcon h={h} />
      <a href={to} onClick={linkClick(to)} className="min-w-0 flex-1 text-ink hover:opacity-90">
        <span className="flex flex-wrap items-center gap-2">
          <span className="text-ui">{h.name}</span>
          {h.control_center && (
            <StatusChip tone="accent" size="sm">
              控制中心
            </StatusChip>
          )}
          <HostStatusChip h={h} />
        </span>
        <span className="mt-0.5 block truncate text-[13px] text-ink-2">{hostSub(h, s)}</span>
      </a>
      <span className="w-[120px] flex-none max-md:hidden">
        <BotFaces h={h} />
      </span>
      <Capacity h={h} />
      <span className="flex flex-none items-center gap-2 max-sm:w-full max-sm:justify-end">
        {offline && (
          <Button size="sm" icon={RefreshCw} onClick={reconnect} loading={busy}>
            重新連線
          </Button>
        )}
        {!offline && (
          <Button
            size="sm"
            icon={Truck}
            disabled={h.bots.length === 0 || moveTargets(h, s.hosts ?? []).length === 0}
            title={h.bots.length === 0 ? '這台上面沒有 Bot' : '把這台上的 Bot 全部搬到另一台'}
            onClick={() => onMove(h)}
          >
            搬家
          </Button>
        )}
        <Button size="sm" variant="danger" disabled={h.control_center} className={h.control_center ? 'max-sm:hidden' : undefined} onClick={() => onRemove(h)} title={h.control_center ? '控制中心所在的主機不能移除' : undefined}>
          移除
        </Button>
      </span>
    </div>
  )
}

function DefaultHost({ hosts }: { hosts: HostView[] }) {
  const toast = useToast()
  const current = hosts.find((h) => h.default)?.id ?? hosts.find((h) => h.control_center)?.id ?? ''
  const usable = hosts.filter((h) => h.control_center || h.status !== 'failed')
  const pick = async (id: string) => {
    try {
      await hostsSetDefault({ id: hosts.find((h) => h.id === id)?.control_center ? '' : id })
      void loadHosts()
    } catch (err) {
      toast.show({ kind: 'error', text: errText(err) })
    }
  }
  const sel = hosts.find((h) => h.id === current)
  const desc = !sel
    ? ''
    : sel.control_center
      ? '新 Bot 的電腦放在這台電腦；這台滿了會自動放到有空位的主機。'
      : `新 Bot 的電腦放在 ${sel.name}；它滿了或離線時會自動放到有空位的主機。`
  return (
    <Row title="新 Bot 放在" sub={desc} wrap>
      <SegmentedControl
        label="新 Bot 放在"
        value={current}
        onChange={pick}
        options={usable.map((h) => ({ value: h.id, label: h.control_center ? '這台電腦' : h.name }))}
        className="min-w-[260px] max-sm:min-w-0"
      />
    </Row>
  )
}

/** /settings/computers */
export function ComputersSection() {
  const s = useHosts()
  const [open, setOpen] = useState(false)
  const [pairing, setPairing] = useState(false)
  const remove = useRemoveHost()
  const [moveFrom, setMoveFrom] = useState<HostView | null>(null)
  const hosts = s.hosts ?? []
  const local = hosts.find((h) => h.control_center)
  const bots = hosts.flatMap((h) => h.bots.map((b) => ({ ...b, host: h })))
  const running = bots.filter((b) => b.running).length
  return (
    <>
      <SectionHeader title="電腦與裝置" sub="Bot 的電腦放在哪幾台主機。" aside={local && <span className="whitespace-nowrap pt-1 text-[13px] text-ink-2 max-sm:hidden">控制中心在：{local.name}</span>} />
      <Block
        title="主機"
        aside={
          <span className="flex flex-none items-center gap-2">
            <span className="max-md:hidden">
              <StatusChip tone="success" size="sm" icon={ShieldCheck}>
                不開公開埠
              </StatusChip>
            </span>
            <span className="max-md:hidden">
              <StatusChip tone="success" size="sm" icon={ShieldCheck}>
                金鑰只在 XBot 服務
              </StatusChip>
            </span>
            <Button variant="primary" size="sm" icon={Plus} onClick={() => navigate('/settings/computers/add')}>
              新增主機
            </Button>
          </span>
        }
      >
        {s.error && !s.hosts && <p className="m-0 rounded-group bg-card px-4 py-3 text-ui-sm text-danger">讀不到主機清單：{s.error}</p>}
        {!s.hosts && !s.error && <p className="m-0 rounded-group bg-card px-4 py-3 text-ui-sm text-ink-2">讀取中…</p>}
        {s.hosts && (
          <Group label="主機清單">
            {hosts.map((h) => (
              <HostRow key={h.id} h={h} s={s} onRemove={remove.ask} onMove={setMoveFrom} />
            ))}
            <button
              type="button"
              aria-expanded={open}
              onClick={() => setOpen((v) => !v)}
              className="flex h-[38px] items-center justify-center gap-1.5 border-t border-line bg-transparent text-[13px] text-ink-2 hover:text-ink"
            >
              各 Bot 放在哪台主機：{bots.length === 0 ? '還沒有 Bot' : `顯示全部 ${bots.length} 個 Bot（${running} 台開機中）`}
              <Icon icon={ChevronDown} size={16} className={cx('transition-transform', open && 'rotate-180')} />
            </button>
            {open &&
              bots.map((b) => (
                <div key={b.id} className="flex items-center gap-3 border-t border-line px-4 py-2">
                  <Avatar {...look(b)} size={24} blink={false} />
                  <span className="min-w-0 flex-1 text-ui-sm">{b.name}</span>
                  <span className="text-[13px] text-ink-2">
                    {b.host.name} · {b.host.status === 'offline' ? '主機離線' : b.running ? '開機中' : b.state === 'none' ? '還沒開機' : '休眠'}
                  </span>
                </div>
              ))}
          </Group>
        )}
      </Block>
      <MovesBlock />
      {s.hosts && hosts.length > 0 && (
        <Block title="預設值" aside={<span className="text-[13px] text-ink-2 max-md:hidden">同一個電腦映像，每台主機都一樣；主機滿了會排隊</span>}>
          <Group>
            <DefaultHost hosts={hosts} />
            <Row title="每台電腦 2 CPU · 3 GB 記憶體 · 共用記憶體 512 MB" sub="第一次收到工作才開機；每台主機各自有同時開機上限，滿了新工作會排隊「等電腦空出來」。" />
          </Group>
        </Block>
      )}
      <EgressSummary />
      <DevicesEntry />
      <Block title="掃 QR 連手機" aside={<Button variant="primary" size="sm" icon={Smartphone} onClick={() => setPairing(true)}>連手機</Button>}>
        <Group>
          <Row title="不用輸入登入碼" sub="手機裝 Tailscale、登入同一個帳號，掃電腦上的 QR（網址＋一次性配對碼，5 分鐘內有效、只能用一次）就登入了。不用安裝憑證。" />
          <Row title="這個瀏覽器" sub={`連線方式：${connectionKind()}`} />
        </Group>
      </Block>
      <PairPhoneDialog open={pairing} onClose={() => setPairing(false)} />
      <Note tone="warning" icon={<Icon icon={TriangleAlert} size={18} />}>
        XBot 能控制每台主機的 Docker，等於有那台主機的最高權限：建議整台主機專給 XBot。
      </Note>
      {remove.dialog}
      <MoveAllDialog from={moveFrom} hosts={hosts} onClose={() => setMoveFrom(null)} />
    </>
  )
}
