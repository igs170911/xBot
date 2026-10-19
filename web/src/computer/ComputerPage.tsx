import { useRef, useState, type RefObject } from 'react'
import { ArrowLeft, Check, ChevronDown, Clipboard, Ellipsis, Expand, GraduationCap, Hand, Keyboard, Maximize, RotateCw, Settings, ShieldCheck, X } from 'lucide-react'
import { botsStore, look } from '../data/bots'
import { useStore } from '../data/store'
import { navigate } from '../router'
import { Avatar } from '../ui/Avatar'
import { Button, IconButton } from '../ui/Button'
import { Dialog } from '../ui/Dialog'
import { Icon } from '../ui/Icon'
import { MenuButton } from '../ui/Menu'
import { cx } from '../ui/cx'
import { isRecording, teachStartStore, useLesson } from '../teaching/teachingData'
import { TeachSection } from '../teaching/TeachPanel'
import { controlOf, useNarrow, useTakeover } from './hooks'
import { useScreen } from './screenData'
import { Stage } from './Stage'
import { CloseTabsButton } from './CloseTabsButton'
import { ComputerActions, NetworkAllow, useComputer } from './ComputerActions'
import { KEY, statusText, type VncHandle, type VncStatus } from './vnc'

const DEFAULT_NOTE = '我剛剛在電腦上操作過，請從目前畫面繼續。'

/** /bots/:id/computer：全視窗看與接手（design/E02；手機 L04）。 */
export function ComputerPage({ botId }: { botId: string }) {
  const bots = useStore(botsStore).bots
  const bot = bots?.find((b) => b.id === botId)
  const { screen, error, setScreen } = useScreen(botId)
  const name = bot?.name ?? screen?.name ?? 'Bot'
  const { take, back, busy } = useTakeover(botId, name, setScreen)
  const vnc = useRef<VncHandle>(null)
  const [note, setNote] = useState('')
  const [sent, setSent] = useState<string | null>(null)
  const [fit, setFit] = useState(true)
  const [status, setStatus] = useState<VncStatus>({ kind: 'connecting' })
  const [paste, setPaste] = useState(false)
  const [more, setMore] = useState(false)
  const narrow = useNarrow()
  const who = controlOf(screen)
  const you = who === 'you'
  const { lesson } = useLesson(botId)
  const teaching = lesson && isRecording(lesson) ? lesson : null
  const computer = useComputer(botId)

  if (bots && !bot && !screen) {
    return (
      <main className="flex flex-1 flex-col items-center justify-center gap-3 p-8 text-center">
        <h1 className="m-0 text-page">找不到這個 Bot</h1>
        <p className="m-0 text-sub text-ink-2">{error ?? '它可能被封存或刪除了。'}</p>
      </main>
    )
  }
  if (!screen) {
    return (
      <main className="flex flex-1 flex-col items-center justify-center gap-3 p-8 text-center" aria-busy={!error || undefined}>
        <h1 className="m-0 text-page">{name}的電腦</h1>
        <p className="m-0 text-sub text-ink-2">{error ?? '讀取中…'}</p>
      </main>
    )
  }

  const handBack = async () => {
    const out = await back(note)
    if (out) {
      setSent(out.note)
      setNote('')
    }
  }
  const title = you ? `你正在操作${name}的電腦` : `${name}的電腦`
  const sub = you
    ? `${name}已暫停，你的滑鼠和鍵盤會直接送進這台電腦`
    : who === 'other'
      ? `${screen.takeover?.by_name} 正在操作，${name}已暫停`
      : screen.working
        ? `${name} 正在使用電腦，你可以看著它做，或直接接手`
        : `${name} 現在沒在用電腦`
  const primary = teaching ? null : you ? (
    <Button variant="primary" icon={Check} loading={busy === 'back'} onClick={() => void handBack()} className={cx(narrow && 'h-12 w-full text-body')}>
      完成，交還給{name}
    </Button>
  ) : (
    <Button
      variant="primary"
      icon={Hand}
      loading={busy === 'take'}
      disabled={who === 'other' || screen.box !== 'running'}
      onClick={() => {
        setSent(null)
        void take()
      }}
      className={cx(narrow && 'h-12 w-full text-body')}
    >
      接手操作
    </Button>
  )
  // 教它一件事（E05）：沒有人在操作、也沒有在示範時才能開始。
  const teach =
    !teaching && !lesson && who === 'bot' ? (
      <Button icon={GraduationCap} onClick={() => teachStartStore.set({ botId })} className={cx(narrow && 'h-12 w-full text-body')}>
        教它一件事
      </Button>
    ) : null

  const keys = <SpecialKeys vnc={vnc} disabled={!you} />

  return (
    <main className="flex h-[calc(100dvh-56px)] min-w-0 flex-1 flex-col bg-app md:h-dvh" aria-label={title} data-testid="computer-page">
      <header className="flex min-h-[60px] flex-none flex-wrap items-center gap-x-3 gap-y-2 border-b border-edge px-4 py-2 max-md:px-3 max-md:pt-[max(8px,env(safe-area-inset-top))]">
        {narrow ? (
          <IconButton icon={X} label="回到對話" onClick={() => navigate(`/bots/${botId}`)} />
        ) : (
          <Button variant="ghost" size="sm" icon={ArrowLeft} onClick={() => navigate(`/bots/${botId}`)}>
            回到對話
          </Button>
        )}
        {bot && <Avatar {...look(bot)} state={you ? 'idle' : screen.working ? 'working' : 'idle'} dot="none" size={28} ring="#070707" />}
        <div className="flex min-w-0 flex-1 flex-col">
          <h1 className="m-0 truncate text-title">{title}</h1>
          <p className="m-0 truncate text-meta text-ink-2">{narrow ? `${screen.host_name || '這台主機'}${status.kind === 'connected' && status.width ? ` · ${status.width}×${status.height}` : ''}` : sub}</p>
        </div>
        {!narrow && (
          <>
            {teach}
            {primary}
          </>
        )}
      </header>

      {narrow && (
        <div className="flex flex-none items-center gap-2 px-3 py-2">
          {you ? (
            <span className="inline-flex items-center gap-1.5 rounded-full bg-warning-soft px-2.5 py-1 text-meta text-warning-ink">
              <Icon icon={Hand} size={14} />
              你正在操作 · {name}在等你
            </span>
          ) : (
            <span className="text-meta text-ink-2">{sub}</span>
          )}
          <span className="flex-1" />
        </div>
      )}

      <div className="flex min-h-0 flex-1 gap-4 p-4 max-md:flex-col max-md:gap-2.5 max-md:p-3 max-md:pt-0">
        <div className="flex min-h-0 min-w-0 flex-1 flex-col gap-2.5">
          {screen.request && !you && (
            <div className="flex items-start gap-2.5 rounded-group bg-warning-soft px-3.5 py-3 text-sub" role="alert" data-testid="takeover-request">
              <Icon icon={Hand} size={16} className="mt-0.5 text-warning" />
              <span className="min-w-0 flex-1">
                <strong className="block font-normal text-warning-ink">{name}需要你協助</strong>
                {screen.request.reason}
              </span>
            </div>
          )}
          {sent && !screen.takeover && (
            <div className="flex items-start gap-2.5 rounded-group bg-success-soft px-3.5 py-3 text-sub" role="status" data-testid="handed-back">
              <Icon icon={Check} size={16} className="mt-0.5 text-success" />
              <span className="min-w-0 flex-1">
                <strong className="block font-normal text-success">已交還，{name}回到工作中</strong>
                你說：「{sent}」
              </span>
            </div>
          )}
          <TeachSection lesson={lesson} botName={name} className="flex-none" />
          <Stage
            screen={screen}
            bot={bot}
            vnc={vnc}
            fit={fit}
            recording={teaching ? (teaching.state === 'paused' ? 'paused' : 'recording') : undefined}
            onStatus={setStatus}
            capsule={!narrow}
            onTake={() => void take()}
            taking={busy === 'take'}
            className={cx('min-h-[220px] flex-1', narrow && 'aspect-[16/10] flex-none')}
          />
          {!narrow && (
            <div className="flex flex-none flex-wrap items-center gap-2">
              <Button size="sm" variant="outline" icon={Clipboard} disabled={!you} onClick={() => setPaste(true)}>
                貼上文字
              </Button>
              {keys}
              <CloseTabsButton botId={botId} disabled={screen.box !== 'running'} />
              <Button size="sm" variant="outline" icon={Maximize} onClick={() => setFit((f) => !f)} aria-pressed={!fit}>
                {fit ? '原始大小' : '符合視窗'}
              </Button>
              <span className="flex-1" />
              <span className="flex items-center gap-1.5 text-meta text-ink-2" data-testid="vnc-status">
                <span className={cx('size-1.5 rounded-full', status.kind === 'connected' ? 'bg-success' : status.kind === 'failed' ? 'bg-danger' : 'bg-warning')} />
                {statusText(status)}
              </span>
            </div>
          )}
          {narrow && <PhoneControls vnc={vnc} disabled={!you} onPaste={() => setPaste(true)} />}
        </div>

        {narrow ? (
          <div className="flex flex-none flex-col gap-2.5 pb-[max(8px,env(safe-area-inset-bottom))]">
            {you && !teaching && <p className="m-0 text-center text-meta text-ink-2">說明選填。不填時會告訴{name}：「{DEFAULT_NOTE}」</p>}
            {primary}
            {teach}
          </div>
        ) : (
          <aside className="flex w-[340px] flex-none flex-col gap-3 overflow-y-auto max-lg:hidden">
            {screen.request && (
              <section className="flex flex-col gap-2 rounded-card bg-panel px-4 py-3.5">
                <h2 className="m-0 flex items-center gap-2 text-ui">
                  {bot && <Avatar {...look(bot)} state="notifying" dot="none" size={22} ring="#111111" />}
                  {name}需要你協助
                </h2>
                <p className="m-0 text-sub text-ink-2">{screen.request.reason}</p>
              </section>
            )}
            {you && (
              <section className="flex flex-col gap-2 rounded-card bg-panel px-4 py-3.5">
                <h2 className="m-0 text-ui">
                  交還時要說什麼 <span className="text-meta text-ink-3">選填</span>
                </h2>
                <textarea
                  aria-label="交還時要說什麼"
                  value={note}
                  onChange={(e) => setNote(e.target.value)}
                  rows={3}
                  maxLength={2000}
                  placeholder={DEFAULT_NOTE}
                  className="resize-none rounded-field border border-line bg-inset px-3 py-2 text-ui outline-none focus:border-focus"
                />
              </section>
            )}
            <button
              type="button"
              aria-expanded={more}
              onClick={() => setMore((m) => !m)}
              className="flex h-10 items-center gap-2 rounded-card bg-panel px-4 text-left text-ui text-ink hover:bg-control"
            >
              <Icon icon={Settings} size={16} className="text-ink-2" />
              <span className="flex-1">電腦設定</span>
              <Icon icon={ChevronDown} size={16} className={cx('text-ink-2 transition-transform', more && 'rotate-180')} />
            </button>
            {more && (
              <>
                <section className="flex flex-col gap-2 rounded-card bg-panel px-4 py-3.5">
                  <h2 className="m-0 flex items-center gap-2 text-ui">
                    <Icon icon={ShieldCheck} size={16} />
                    接手時
                  </h2>
                  <ul className="m-0 flex flex-col gap-1.5 pl-5 text-sub text-ink-2">
                    <li>{name}暫停，不跟你搶滑鼠</li>
                    <li>你輸入的密碼不會存進對話</li>
                    <li>30 分鐘沒人操作會自動交還</li>
                    <li>網站登入會保留在這台電腦</li>
                  </ul>
                </section>
                {!you && <ComputerActions botId={botId} botName={name} state={computer} />}
                {!you && <NetworkAllow botId={botId} botName={name} state={computer} />}
              </>
            )}
          </aside>
        )}
      </div>
      <PasteDialog open={paste} onClose={() => setPaste(false)} onPaste={(t) => vnc.current?.paste(t)} />
    </main>
  )
}

function SpecialKeys({ vnc, disabled }: { vnc: RefObject<VncHandle | null>; disabled: boolean }) {
  const send = (k: number, mods?: number[]) => () => vnc.current?.key(k, mods)
  return (
    <MenuButton
      label="特殊按鍵"
      items={[
        { label: 'Esc', onSelect: send(KEY.Escape) },
        { label: 'Tab', onSelect: send(KEY.Tab) },
        { label: 'Enter', onSelect: send(KEY.Return) },
        { label: 'Ctrl+A（全選）', onSelect: send(0x61, [KEY.Control]) },
        { label: 'Ctrl+C（複製）', onSelect: send(0x63, [KEY.Control]) },
        { label: 'Ctrl+Alt+Del', onSelect: send(0xffff, [KEY.Control, KEY.Alt]) },
      ]}
      trigger={(p) => (
        <Button {...p} size="sm" variant="outline" icon={Keyboard} disabled={disabled}>
          特殊按鍵
        </Button>
      )}
    />
  )
}

/** 手機的操控列（L04）：鍵盤、貼上、更多（特殊鍵、方向鍵、全螢幕、重新連線）。Ctrl／Alt 會停住等下一鍵。 */
function PhoneControls({ vnc, disabled, onPaste }: { vnc: RefObject<VncHandle | null>; disabled: boolean; onPaste: () => void }) {
  const [more, setMore] = useState(false)
  const [mods, setMods] = useState<number[]>([])
  const input = useRef<HTMLInputElement>(null)
  const press = (k: number) => {
    vnc.current?.key(k, mods)
    setMods([])
  }
  const toggle = (m: number) => setMods((cur) => (cur.includes(m) ? cur.filter((x) => x !== m) : [...cur, m]))
  const big = 'flex h-12 flex-1 items-center justify-center gap-2 rounded-field text-ui disabled:opacity-40'
  return (
    <div className="flex flex-none flex-col gap-2" data-testid="phone-controls">
      <div className="flex gap-1 rounded-group bg-panel p-1">
        <button type="button" className={cx(big, 'hover:bg-control')} disabled={disabled} onClick={() => input.current?.focus()}>
          <Icon icon={Keyboard} size={18} />
          鍵盤
        </button>
        <button type="button" className={cx(big, 'hover:bg-control')} disabled={disabled} onClick={onPaste}>
          <Icon icon={Clipboard} size={18} />
          貼上
        </button>
        <button type="button" className={cx(big, more ? 'bg-control' : 'hover:bg-control')} aria-expanded={more} onClick={() => setMore((m) => !m)}>
          <Icon icon={Ellipsis} size={18} />
          更多
        </button>
      </div>
      {/* 手機的系統鍵盤：打的字逐字送進電腦（不留在這個輸入框）。 */}
      <input
        ref={input}
        aria-label="輸入到電腦"
        className="sr-only-text"
        autoCapitalize="off"
        autoComplete="off"
        autoCorrect="off"
        onKeyDown={(e) => {
          const special: Record<string, number> = { Enter: KEY.Return, Backspace: KEY.BackSpace, Tab: KEY.Tab, Escape: KEY.Escape }
          if (special[e.key]) {
            e.preventDefault()
            press(special[e.key])
          }
        }}
        onChange={(e) => {
          const text = e.target.value
          e.target.value = ''
          if (!text) return
          if (mods.length) {
            for (const ch of text) press(ch.codePointAt(0) ?? 0)
          } else vnc.current?.type(text)
        }}
      />
      {more && (
        <div className="flex flex-col gap-2 rounded-group bg-panel p-3">
          <p className="m-0 text-meta text-ink-2">特殊按鍵（按一下送出，Ctrl／Alt 會停住等下一鍵）</p>
          <div className="grid grid-cols-4 gap-2">
            {[
              ['Esc', () => press(KEY.Escape)],
              ['Tab', () => press(KEY.Tab)],
            ].map(([l, f]) => (
              <Key key={l as string} label={l as string} disabled={disabled} onClick={f as () => void} />
            ))}
            <Key label="Ctrl" disabled={disabled} pressed={mods.includes(KEY.Control)} onClick={() => toggle(KEY.Control)} />
            <Key label="Alt" disabled={disabled} pressed={mods.includes(KEY.Alt)} onClick={() => toggle(KEY.Alt)} />
            <Key label="←" aria="左" disabled={disabled} onClick={() => press(KEY.Left)} />
            <Key label="↑" aria="上" disabled={disabled} onClick={() => press(KEY.Up)} />
            <Key label="↓" aria="下" disabled={disabled} onClick={() => press(KEY.Down)} />
            <Key label="→" aria="右" disabled={disabled} onClick={() => press(KEY.Right)} />
          </div>
          <div className="grid grid-cols-2 gap-2">
            <Button variant="outline" icon={Expand} onClick={() => void document.documentElement.requestFullscreen?.().catch(() => {})}>
              全螢幕
            </Button>
            <Button variant="outline" icon={RotateCw} onClick={() => vnc.current?.reconnect()}>
              重新連線
            </Button>
          </div>
        </div>
      )}
    </div>
  )
}

function Key({ label, aria, pressed, disabled, onClick }: { label: string; aria?: string; pressed?: boolean; disabled?: boolean; onClick: () => void }) {
  return (
    <button
      type="button"
      aria-label={aria ?? label}
      aria-pressed={pressed}
      disabled={disabled}
      onClick={onClick}
      className={cx('h-11 rounded-field font-mono text-ui disabled:opacity-40', pressed ? 'bg-accent-strong text-white' : 'bg-control hover:bg-hover')}
    >
      {label}
    </button>
  )
}

function PasteDialog({ open, onClose, onPaste }: { open: boolean; onClose: () => void; onPaste: (text: string) => void }) {
  const [text, setText] = useState('')
  return (
    <Dialog
      open={open}
      onClose={onClose}
      title="貼上文字"
      description="會放進電腦的剪貼簿，並在目前的位置按 Ctrl+V。"
      actions={
        <>
          <Button onClick={onClose}>取消</Button>
          <Button
            variant="primary"
            disabled={!text}
            onClick={() => {
              onPaste(text)
              setText('')
              onClose()
            }}
          >
            貼上
          </Button>
        </>
      }
    >
      <textarea
        aria-label="要貼上的文字"
        value={text}
        onChange={(e) => setText(e.target.value)}
        rows={4}
        className="w-full resize-none rounded-field border border-line bg-inset px-3 py-2 text-ui outline-none focus:border-focus"
      />
    </Dialog>
  )
}
