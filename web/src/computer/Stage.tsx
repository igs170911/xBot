import { useCallback, useState, type ReactNode, type RefObject } from 'react'
import { Eye, Hand, LoaderCircle, MousePointerClick, Power, RotateCw, TriangleAlert } from 'lucide-react'
import type { Bot } from '../data/bots'
import { Avatar } from '../ui/Avatar'
import { Button } from '../ui/Button'
import { Icon } from '../ui/Icon'
import { cx } from '../ui/cx'
import { boxState, controlOf } from './hooks'
import { useWake, type Screen } from './screenData'
import { statusText, type VncHandle, type VncStatus } from './vnc'
import { VncScreen } from './VncScreen'

/** 畫面框：即時畫面（或電腦狀態）、左下角的膠囊、連線問題的提示列、「你在看」。 */
export function Stage({
  screen,
  bot,
  vnc,
  fit = true,
  className,
  capsule = true,
  onTake,
  taking,
  onStatus,
  recording,
  children,
}: {
  screen: Screen
  bot?: Bot
  vnc: RefObject<VncHandle | null>
  fit?: boolean
  className?: string
  capsule?: boolean
  onTake?: () => void
  taking?: boolean
  onStatus?: (s: VncStatus) => void
  /** 示範教學中（B32、E04）：紅框＋左下角「錄製中 · 密碼只會記成 ••••」。 */
  recording?: 'recording' | 'paused'
  children?: ReactNode
}) {
  const [status, setStatus] = useState<VncStatus>({ kind: 'connecting' })
  const off = boxState(screen)
  const who = controlOf(screen)
  const control = who === 'you'
  const report = useCallback(
    (s: VncStatus) => {
      setStatus(s)
      onStatus?.(s)
    },
    [onStatus],
  )
  const others = Math.max(0, screen.viewers - 1)
  const { wake, busy: waking, error: wakeError } = useWake(screen.bot_id)
  const canWake = (screen.box === 'sleeping' || screen.box === 'none') && screen.host_status === 'online'
  return (
    <div
      className={cx(
        'relative overflow-hidden rounded-[10px] bg-[#111111]',
        control && 'ring-2 ring-accent',
        screen.request && !screen.takeover && 'ring-2 ring-warning',
        recording === 'recording' && 'ring-2 ring-danger',
        recording === 'paused' && 'ring-2 ring-check-off',
        className,
      )}
      data-testid="computer-stage"
      data-state={off ? screen.box : status.kind}
    >
      {off ? (
        <div className="flex h-full min-h-[180px] flex-col items-center justify-center gap-2 p-6 text-center">
          <span className={cx('grid size-12 place-items-center rounded-full bg-control', off.tone === 'danger' ? 'text-danger' : 'text-ink-2')}>
            <Icon icon={off.icon} size={22} className={off.spin ? 'animate-spin' : undefined} />
          </span>
          <p className="m-0 text-title">{off.title}</p>
          <p className="m-0 max-w-[340px] text-sub text-ink-2">{off.sub}</p>
          {canWake && (
            <Button icon={Power} loading={waking} onClick={wake} className="mt-1">
              {screen.box === 'none' ? '現在開機' : '叫醒'}
            </Button>
          )}
          {wakeError && <p className="m-0 text-sub text-danger">{wakeError}</p>}
        </div>
      ) : (
        <>
          <VncScreen ref={vnc} path={screen.vnc} control={control} fit={fit} label={`${screen.name}的電腦畫面`} className="absolute inset-0" onStatus={report} />
          {status.kind !== 'connected' && (
            <div className="absolute inset-x-0 top-0 flex items-center gap-2 bg-black/70 px-3 py-2 text-sub" role="status">
              <Icon icon={status.kind === 'failed' ? TriangleAlert : LoaderCircle} size={16} className={cx(status.kind === 'failed' ? 'text-warning' : 'animate-spin text-ink-2')} />
              <span className={cx('min-w-0 flex-1', status.kind === 'failed' && 'text-warning-ink')}>{statusText(status)}</span>
              {status.kind === 'failed' && (
                <Button size="sm" variant="primary" icon={RotateCw} onClick={() => vnc.current?.reconnect()}>
                  重新連線
                </Button>
              )}
            </div>
          )}
          {status.kind === 'connected' && (
            <span className="absolute right-2.5 top-2.5 inline-flex items-center gap-1.5 rounded-full bg-black/70 px-2.5 py-1 text-meta text-ink" data-testid="watching">
              <Icon icon={Eye} size={13} />
              {others > 0 ? `你和另 ${others} 人在看` : '你在看'}
            </span>
          )}
          {capsule && (
            <div className="absolute bottom-2.5 left-2.5 flex max-w-[calc(100%-20px)] items-center gap-2 rounded-full bg-black/80 py-1 pl-1.5 pr-1 text-sub shadow-dialog" data-testid="capsule">
              {recording ? (
                <span className="flex items-center gap-1.5 px-2.5 py-1" data-testid="rec-label">
                  <span className={cx('size-2 rounded-full', recording === 'paused' ? 'bg-check-off' : 'animate-pulse bg-danger')} />
                  {recording === 'paused' ? '已暫停錄製' : '錄製中 · 密碼只會記成 ••••'}
                </span>
              ) : who === 'you' ? (
                <span className="flex items-center gap-1.5 rounded-full bg-accent px-2.5 py-1 text-white">
                  <Icon icon={MousePointerClick} size={14} />
                  你正在操作 · {screen.name}已暫停
                </span>
              ) : who === 'other' ? (
                <span className="px-2 py-1">{screen.takeover?.by_name} 正在操作 · {screen.name}已暫停</span>
              ) : (
                <>
                  {bot && <Avatar shape={bot.shape} color={bot.color} state={screen.working ? 'working' : 'idle'} dot="none" size={20} ring="#000" />}
                  <span className="truncate">
                    {screen.name} {screen.working ? '正在使用電腦' : '閒著'}
                  </span>
                  {onTake && (
                    <button type="button" onClick={onTake} disabled={taking} className="inline-flex flex-none items-center gap-1 rounded-full bg-control px-2.5 py-1 hover:bg-hover disabled:opacity-50">
                      <Icon icon={Hand} size={14} />
                      接手操作
                    </button>
                  )}
                </>
              )}
            </div>
          )}
        </>
      )}
      {children}
    </div>
  )
}
