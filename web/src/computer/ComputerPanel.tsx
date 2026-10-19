import { useRef, useState } from 'react'
import { ChevronDown, Cpu, Expand, Globe, Hand, Moon, Server, Settings, Sparkles, X } from 'lucide-react'
import type { Bot } from '../data/bots'
import { navigate } from '../router'
import { Button, IconButton } from '../ui/Button'
import { Icon } from '../ui/Icon'
import { cx } from '../ui/cx'
import { isRecording, useLesson } from '../teaching/teachingData'
import { TeachSection } from '../teaching/TeachPanel'
import { controlOf, useTakeover } from './hooks'
import { ComputerActions, NetworkAllow, useComputer } from './ComputerActions'
import { WhereSection } from './MoveParts'
import { BOX_SPEC, computerPanelStore, IDLE_SLEEP_MIN, useScreen } from './screenData'
import { Stage } from './Stage'
import type { VncHandle } from './vnc'

/** 對話旁的電腦面板（design/E01）：即時畫面、接手／交還、在哪裡跑（搬家、從家裡出去）、休眠／重開／重建／刪除、私有網路例外、電腦資訊。 */
export function ComputerPanel({ bot, className }: { bot: Bot; className?: string }) {
  const { screen, error, setScreen } = useScreen(bot.id)
  const vnc = useRef<VncHandle>(null)
  const [note, setNote] = useState('')
  const [more, setMore] = useState(false)
  const { take, back, busy } = useTakeover(bot.id, bot.name, setScreen)
  const who = controlOf(screen)
  const { lesson } = useLesson(bot.id)
  const teaching = lesson && isRecording(lesson) ? lesson : null
  const computer = useComputer(bot.id)

  return (
    <aside aria-label={`${bot.name}的電腦`} data-testid="computer-panel" className={cx('flex h-dvh w-[540px] flex-none flex-col border-l border-edge bg-app', className)}>
      <header className="flex h-14 flex-none items-center gap-1 px-3">
        <IconButton icon={Settings} label="電腦設定" onClick={() => navigate(`/settings/bots/${encodeURIComponent(bot.id)}`)} />
        <h2 className="m-0 flex-1 text-center text-title">{bot.name}的電腦</h2>
        <IconButton icon={Expand} label="全視窗" onClick={() => navigate(`/bots/${bot.id}/computer`)} />
        <IconButton icon={X} label="關閉電腦面板" onClick={() => computerPanelStore.set(false)} />
      </header>
      <div className="flex min-h-0 flex-1 flex-col gap-3.5 overflow-y-auto px-5 pb-5">
        {error && !screen && <p className="m-0 text-sub text-danger">{error}</p>}
        {screen && (
          <>
            <div className="flex items-center justify-between text-sub text-ink-2">
              <span>即時畫面</span>
              <span className="flex items-center gap-1.5">
                <span className={cx('size-1.5 rounded-full', screen.host_status === 'online' ? 'bg-success' : 'bg-danger')} />
                {(screen.host_name || '這台主機') + (screen.host_status === 'online' ? ' · 已連線' : ' · 離線')}
              </span>
            </div>
            {screen.request && !screen.takeover && (
              <div className="flex items-start gap-2.5 rounded-group bg-warning-soft px-3.5 py-3 text-sub" role="alert">
                <Icon icon={Hand} size={16} className="mt-0.5 text-warning" />
                <span className="min-w-0 flex-1">
                  <strong className="block font-normal text-warning-ink">{bot.name}需要你協助</strong>
                  {screen.request.reason}
                </span>
              </div>
            )}
            <TeachSection lesson={lesson} botName={bot.name} />
            <Stage
              screen={screen}
              bot={bot}
              vnc={vnc}
              className="aspect-[16/10] w-full"
              onTake={() => void take()}
              taking={busy === 'take'}
              recording={teaching ? (teaching.state === 'paused' ? 'paused' : 'recording') : undefined}
            />
            {teaching ? null : who === 'you' ? (
              <div className="flex flex-col gap-2.5">
                <label className="flex flex-col gap-1">
                  <span className="text-meta text-ink-3">交還時要說什麼（選填）</span>
                  <textarea
                    value={note}
                    onChange={(e) => setNote(e.target.value)}
                    rows={2}
                    maxLength={2000}
                    placeholder="我剛剛在電腦上操作過，請從目前畫面繼續。"
                    className="resize-none rounded-field border border-line bg-inset px-3 py-2 text-ui outline-none focus:border-focus"
                  />
                </label>
                <Button
                  variant="primary"
                  icon={Sparkles}
                  loading={busy === 'back'}
                  onClick={() =>
                    void back(note).then((o) => {
                      if (o) setNote('')
                    })
                  }
                >
                  完成，交還給{bot.name}
                </Button>
              </div>
            ) : (
              <div className="flex gap-2.5">
                <Button className="flex-1" icon={Hand} loading={busy === 'take'} disabled={who === 'other' || screen.box !== 'running'} onClick={() => void take()}>
                  {who === 'other' ? `${screen.takeover?.by_name} 正在操作` : '接手操作'}
                </Button>
              </div>
            )}
            <button
              type="button"
              aria-expanded={more}
              onClick={() => setMore((m) => !m)}
              className="flex h-10 items-center gap-2 rounded-group bg-panel px-4 text-left text-ui text-ink hover:bg-control"
            >
              <Icon icon={Settings} size={16} className="text-ink-2" />
              <span className="flex-1">電腦設定</span>
              <Icon icon={ChevronDown} size={16} className={cx('text-ink-2 transition-transform', more && 'rotate-180')} />
            </button>
            {more && (
              <>
                <WhereSection botId={bot.id} botName={bot.name} hostId={screen.host_id} />
                <ComputerActions botId={bot.id} botName={bot.name} state={computer} />
                <NetworkAllow botId={bot.id} botName={bot.name} state={computer} />
                <section className="flex flex-col gap-2.5 rounded-group bg-panel px-4 py-3.5">
                  <h3 className="m-0 text-ui">電腦資訊</h3>
                  <InfoRow icon={Cpu} label={`${BOX_SPEC} 記憶體`} aside={screen.box === 'running' ? '開機中' : screen.box === 'sleeping' ? '休眠中' : undefined} />
                  <InfoRow icon={Moon} label={`閒置 ${computer.view?.idle_minutes || IDLE_SLEEP_MIN} 分鐘休眠`} aside="有新工作自動喚醒" />
                  <InfoRow icon={Server} label={screen.host_name ? `主機：${screen.host_name}` : '主機：第一次開機時自動擺放'} aside={screen.viewers > 0 ? `${screen.viewers} 個畫面連線` : undefined} />
                  <InfoRow icon={Globe} label="瀏覽器登入會保留在這台電腦" />
                </section>
              </>
            )}
          </>
        )}
      </div>
    </aside>
  )
}

function InfoRow({ icon, label, aside }: { icon: typeof Cpu; label: string; aside?: string }) {
  return (
    <div className="flex items-center gap-2.5 text-sub">
      <Icon icon={icon} size={16} className="text-ink-2" />
      <span className="min-w-0 flex-1 truncate">{label}</span>
      {aside && <span className="flex-none text-ink-3">{aside}</span>}
    </div>
  )
}
