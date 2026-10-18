import { useEffect, useState } from 'react'
import { Cloud, House, Moon, Monitor, Server, WifiOff } from 'lucide-react'
import { ApiError, computersStatus, computersWake, hostsList, type ComputersStatusOutput, type HostsListOutput } from '../api/generated'
import { botsStore, type Bot } from '../data/bots'
import { onEvent, onReconnect } from '../data/events'
import { useStore } from '../data/store'
import { navigate } from '../router'
import { Avatar } from '../ui/Avatar'
import { Button, IconButton } from '../ui/Button'
import { Icon } from '../ui/Icon'
import { useToast } from '../ui/toast-context'
import { cx } from '../ui/cx'
import { TabHeader } from './Phone'
import { rowTime } from './format'

// L15 辦公室（/office）：所有 Bot 的電腦依主機分組，每台一列縮圖＋頭像＋狀態＋接手／叫醒。
// 狀態來自 xbot.hosts.list 與每個 Bot 的 xbot.computers.status（B13）；即時畫面縮圖要等 B14，先用狀態圖示。

type Host = HostsListOutput['hosts'][number]
type Comp = ComputersStatusOutput

function stateOf(bot: Bot, comp: Comp | undefined, act: string | undefined, hostOffline: boolean): { text: string; warn: boolean; asleep: boolean } {
  const waiting = act === 'waiting_input' || act === 'waiting_approval'
  if (hostOffline) return { text: '主機離線，工作先排隊，主機回來自動重連', warn: false, asleep: true }
  if (waiting) return { text: act === 'waiting_approval' ? '等你允許' : '等你回覆', warn: true, asleep: false }
  const running = !!comp?.computer.running
  if (comp?.computer.state === 'starting' || act === 'waiting_box') return { text: '開機中…', warn: false, asleep: false }
  if (!running) return { text: '待命 · 電腦休眠中', warn: false, asleep: true }
  if (act && act !== 'done') return { text: '工作中' + (bot.title ? ' · ' + bot.title : ''), warn: false, asleep: false }
  return { text: '待命 · 電腦開著', warn: false, asleep: false }
}

function Thumb({ asleep, offline }: { asleep: boolean; offline: boolean }) {
  if (offline)
    return (
      <span aria-hidden="true" className="flex h-[52px] w-[88px] flex-none items-center justify-center rounded-seg border border-dashed border-line text-ink-3">
        <Icon icon={WifiOff} size={18} />
      </span>
    )
  if (asleep)
    return (
      <span aria-hidden="true" className="flex h-[52px] w-[88px] flex-none flex-col items-center justify-center gap-0.5 rounded-seg border border-line bg-inset text-[11px] text-ink-3">
        <Icon icon={Moon} size={14} />
        休眠中
      </span>
    )
  // B14 接上後換成電腦畫面縮圖
  return (
    <span aria-hidden="true" className="flex h-[52px] w-[88px] flex-none flex-col overflow-hidden rounded-seg bg-[#F4F5F7]">
      <span className="flex h-2 items-center gap-0.5 bg-[#E5E7EB] px-1">
        <span className="size-1 rounded-full bg-[#FF5F57]" />
        <span className="size-1 rounded-full bg-[#FEBC2E]" />
        <span className="size-1 rounded-full bg-[#28C840]" />
      </span>
      <span className="flex flex-1 items-center justify-center text-[#6B7280]">
        <Icon icon={Monitor} size={16} />
      </span>
    </span>
  )
}

function Capacity({ running, max, offline }: { running: number; max: number; offline: boolean }) {
  if (offline || max <= 0) return null
  const full = running >= max
  return (
    <span aria-label={`開機 ${running}/${max}`} className="flex gap-1">
      {Array.from({ length: Math.min(max, 8) }, (_, i) => (
        <span key={i} className={cx('h-1.5 w-4 rounded-full', i < running ? (full ? 'bg-warning' : 'bg-success') : 'bg-line')} />
      ))}
    </span>
  )
}

export function OfficePage() {
  const bots = useStore(botsStore)
  const toast = useToast()
  const [hosts, setHosts] = useState<Host[] | null>(null)
  const [comps, setComps] = useState<Record<string, Comp>>({})
  const [waking, setWaking] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)
  const live = (bots.bots ?? []).filter((b) => !b.archived)
  const ids = live.map((b) => b.id).join(',')

  const [tick, setTick] = useState(0)
  useEffect(() => {
    const load = () => {
    hostsList({})
      .then((o) => setHosts(o.hosts))
      .catch((e: unknown) => setError(e instanceof Error ? e.message : String(e)))
    ids
      .split(',')
      .filter(Boolean)
      .forEach((id) =>
        computersStatus({ bot_id: id })
          .then((c) => setComps((s) => ({ ...s, [id]: c })))
          .catch(() => {}),
      )
    }
    load()
    let t: ReturnType<typeof setTimeout> | null = null
    const offs = [
      onEvent((e) => {
        if (!(e.kind.startsWith('computer.') || e.kind.startsWith('host.') || e.kind === 'task.state') || t) return
        t = setTimeout(() => {
          t = null
          load()
        }, 300)
      }),
      onReconnect(load),
    ]
    return () => {
      offs.forEach((o) => o())
      if (t) clearTimeout(t)
    }
  }, [ids, tick])

  const wake = async (b: Bot) => {
    setWaking(b.id)
    try {
      const c = await computersWake({ bot_id: b.id })
      setComps((s) => ({ ...s, [b.id]: c }))
      setTick((n) => n + 1)
    } catch (e) {
      toast.show({ kind: 'error', text: e instanceof ApiError || e instanceof Error ? e.message : String(e) })
    } finally {
      setWaking(null)
    }
  }

  const hostOf = (b: Bot): string => comps[b.id]?.computer.host_id || b.host_id || hosts?.find((h) => h.kind === 'local')?.id || ''
  const groups = (hosts ?? []).map((h) => ({ host: h, bots: live.filter((b) => hostOf(b) === h.id) })).filter((g) => g.bots.length > 0 || g.host.kind === 'local')
  const unplaced = live.filter((b) => !hosts?.some((h) => h.id === hostOf(b)))
  const on = Object.values(comps).filter((c) => c.computer.running).length
  const waitingN = live.filter((b) => ['waiting_input', 'waiting_approval'].includes(bots.activity[b.id]?.state ?? '')).length
  const offlineN = (hosts ?? []).filter((h) => h.status === 'offline' || h.status === 'failed').length

  return (
    <main className="flex min-h-0 flex-1 flex-col" aria-label="辦公室">
      <TabHeader
        title="辦公室"
        sub={hosts ? [`${on} 台開機`, waitingN ? `${waitingN} 台等你接手` : '', offlineN ? `${offlineN} 台主機離線` : ''].filter(Boolean).join(' · ') : '讀取中…'}
        right={<IconButton icon={Server} label="主機" className="-mr-2 size-11" onClick={() => navigate('/settings/computers')} />}
      />
      {error && <p className="m-0 px-5 text-sub text-danger">{error}</p>}
      <div className="mx-auto flex w-full max-w-[880px] flex-col gap-1 px-3 pb-4">
        {groups.map(({ host, bots: list }) => {
          const offline = host.status === 'offline' || host.status === 'failed'
          const local = host.kind === 'local'
          return (
            <section key={host.id} aria-label={host.name} className="flex flex-col gap-2 pt-2">
              <h2 className="m-0 flex items-center gap-2 px-2 text-ui text-ink">
                <Icon icon={offline ? WifiOff : local ? House : Cloud} size={17} className={offline ? 'text-danger' : 'text-ink-2'} />
                <span>{local ? '這台主機' : host.name}</span>
                <span className={cx('text-sub', offline ? 'text-danger' : 'text-ink-2')}>{offline ? '· 離線' : `· 開機 ${host.running}/${host.max_running}`}</span>
                <span className="ml-auto">{offline ? <span className="text-meta text-ink-3">最後上線 {rowTime(host.health?.since ?? 0)}</span> : <Capacity running={host.running} max={host.max_running} offline={offline} />}</span>
              </h2>
              <div className="flex flex-col overflow-hidden rounded-card bg-panel">
                {list.map((b) => {
                  const act = bots.activity[b.id]?.state
                  const st = stateOf(b, comps[b.id], act, offline)
                  return (
                    <div key={b.id} data-testid="office-row" className="flex items-center gap-3 border-divider px-3 py-2.5 not-first:border-t">
                      <Thumb asleep={st.asleep} offline={offline} />
                      <div className="flex min-w-0 flex-1 flex-col gap-0.5">
                        <span className="flex items-center gap-1.5 text-name">
                          <Avatar shape={b.shape} color={b.color} state={st.warn ? 'notifying' : st.asleep ? 'sleeping' : act && act !== 'done' ? 'working' : 'idle'} dot={st.warn ? 'waiting' : 'none'} size={22} ring="#111111" />
                          <span className="truncate">{b.name}</span>
                        </span>
                        <span className={cx('truncate text-sub', st.warn ? 'text-warning-ink' : 'text-ink-2')}>{st.text}</span>
                      </div>
                      {!offline &&
                        (st.asleep ? (
                          <Button className="min-h-11 rounded-full px-4" variant="outline" loading={waking === b.id} onClick={() => void wake(b)}>
                            叫醒
                          </Button>
                        ) : (
                          <Button className="min-h-11 rounded-full px-4" variant={st.warn ? 'primary' : 'secondary'} onClick={() => navigate(`/bots/${b.id}/computer`)}>
                            接手
                          </Button>
                        ))}
                    </div>
                  )
                })}
                {list.length === 0 && <p className="m-0 px-4 py-3 text-sub text-ink-3">這台主機上還沒有 Bot 的電腦。</p>}
              </div>
            </section>
          )
        })}
        {unplaced.length > 0 && hosts && (
          <p className="m-0 px-2 pt-2 text-sub text-ink-3">
            還沒開過電腦的 Bot：{unplaced.map((b) => b.name).join('、')}（第一次交辦時自動擺到有空的主機）
          </p>
        )}
      </div>
    </main>
  )
}
