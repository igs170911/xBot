import { useEffect, useRef, useState } from 'react'
import { CircleCheck, Copy, ExternalLink, KeyRound, RefreshCw } from 'lucide-react'
import {
  botsGet,
  botsUpdate,
  enginesCheck,
  enginesLoginCancel,
  enginesLoginCode,
  enginesLoginKey,
  enginesLoginStart,
  enginesLoginStatus,
  enginesLogout,
  enginesSwitch,
  enginesUnlinkCredentials,
  type BotsGetOutput,
  type EnginesLoginStatusOutput,
  type EnginesStatusOutput,
  type EnginesSwitchInput,
} from '../../api/generated'
import { upsertBot } from '../../data/bots'
import { Button } from '../../ui/Button'
import { ConfirmDialog, Dialog } from '../../ui/Dialog'
import { TextInput } from '../../ui/Field'
import { Icon } from '../../ui/Icon'
import { Select } from '../../ui/Menu'
import { StatusChip } from '../../ui/Pill'
import { useToast } from '../../ui/toast-context'
import { cx } from '../../ui/cx'
import { EffortPicker } from '../../chat/ModelPicker'
import { ModelChooser } from '../ModelChooser'
import { Block, Group } from '../parts'
import { ACCOUNTS, ENGINE_LABEL, PLAN_NAME } from '../botModes'
import { errText, isPending } from '../format'
import { splitRef, useModelData } from '../modelData'

// 進階 › AI 帳號與模型（2026-10-20 改版）：一個地方決定 Bot「用哪個帳號、哪個模型」。
// 帳號：內建（Hermes，用「AI 模型」設定的金鑰）或訂閱 CLI（Claude Code、Codex、Grok、OpenCode、Gemini CLI）。
// 訂閱 CLI 由每個 Bot 自己在網頁上登入（xbot.engines.login_*），登入的帳號只給這個 Bot 用；登入後列出這個帳號能用的模型。
// 點一列只是展開看；按「改用」才換（xbot.engines.switch：引擎和模型一起換，登入資料跟著搬）。

type Bot = BotsGetOutput
type EngineState = EnginesStatusOutput['bots'][number]
type Account = NonNullable<EngineState['accounts']>[number]
type Engine = EnginesSwitchInput['engine']
type Cli = Exclude<Engine, 'hermes'>

/** 換引擎與模型（xbot.engines.switch），再讀回 Bot（頁面上的「使用中」跟著換）。link：同意用這台電腦的登入。 */
async function switchTo(bot: Bot, engine: Engine, model: string, onBot: (b: Bot) => void, link = false): Promise<void> {
  await enginesSwitch({ bot_id: bot.id, engine, model, link })
  const b = await botsGet({ id: bot.id })
  onBot(b)
  upsertBot(b)
}

export function BotAI({ bot, eng, reload, onBot }: { bot: Bot; eng: EngineState | null; reload: () => void; onBot: (b: Bot) => void }) {
  const current = (bot.engine || 'hermes') as Engine
  const [sel, setSel] = useState<Engine>(current)
  const [seen, setSeen] = useState(current)
  if (seen !== current) {
    setSeen(current)
    setSel(current)
  }
  const accounts = eng?.accounts ?? []
  return (
    <Block title="AI 帳號與模型" sub={`${bot.name}用哪個帳號、哪個模型工作。訂閱帳號由每個 Bot 自己登入，只給它用。`}>
      <Group>
        <div role="radiogroup" aria-label="AI 帳號" className="flex flex-col">
          {ACCOUNTS.map((a) => {
            const acc = accounts.find((x) => x.engine === a.value)
            const open = sel === a.value
            const on = current === a.value
            return (
              <div key={a.value} className="border-line not-first:border-t">
                <button
                  type="button"
                  role="radio"
                  aria-checked={open}
                  data-engine={a.value}
                  onClick={() => setSel(a.value as Engine)}
                  className={cx('flex min-h-14 w-full items-center gap-3 bg-transparent px-4 py-2.5 text-left text-ink', open ? 'bg-control/60' : 'hover:bg-control/40')}
                >
                  <span aria-hidden="true" className={cx('flex size-[18px] flex-none items-center justify-center rounded-full border-[1.5px]', on ? 'border-accent' : 'border-check-off')}>
                    {on && <span className="size-2.5 rounded-full bg-accent" />}
                  </span>
                  <span className="min-w-0 flex-1">
                    <span className="block text-ui">{a.title}</span>
                    <span className="block text-[13px] text-ink-2">{a.desc}</span>
                  </span>
                  {on ? (
                    <StatusChip tone="accent" size="sm">
                      使用中
                    </StatusChip>
                  ) : acc?.ready ? (
                    <StatusChip tone="success" size="sm">
                      已登入
                    </StatusChip>
                  ) : a.value !== 'hermes' ? (
                    <StatusChip size="sm">還沒登入</StatusChip>
                  ) : null}
                </button>
                {open && (
                  <div className="px-4 pb-4 pt-1">
                    {a.value === 'hermes' ? (
                      <HermesPanel bot={bot} current={on} onBot={onBot} reload={reload} />
                    ) : (
                      <CliPanel bot={bot} engine={a.value as Cli} current={on} acc={acc} onBot={onBot} reload={reload} />
                    )}
                  </div>
                )}
              </div>
            )
          })}
        </div>
      </Group>
      <EffortPicker
        bot={bot}
        className="mt-0 rounded-card border-t-0 bg-card px-4 pb-4"
        onSaved={(b) => {
          upsertBot(b)
          onBot(b as unknown as Bot)
        }}
      />
    </Block>
  )
}

function HermesPanel({ bot, current, onBot, reload }: { bot: Bot; current: boolean; onBot: (b: Bot) => void; reload: () => void }) {
  const toast = useToast()
  const { models, presets } = useModelData()
  const providers = models?.providers ?? []
  const [pick, setPick] = useState(current ? bot.model : '')
  const [busy, setBusy] = useState(false)
  const ref = (v: string) => (v ? (v.includes('/') ? splitRef(v) : { provider: models?.default?.provider ?? '', model: v }) : null)
  const save = async (patch: { model?: string; fallback?: string }, done: string) => {
    try {
      const b = await botsUpdate({ id: bot.id, ...patch })
      onBot(b)
      upsertBot(b)
      toast.show({ kind: 'done', text: done })
    } catch (e) {
      toast.show({ kind: isPending(e) ? 'info' : 'error', text: errText(e) })
    }
  }
  const use = async () => {
    setBusy(true)
    try {
      await switchTo(bot, 'hermes', pick, onBot)
      toast.show({ kind: 'done', text: `${bot.name}改用內建引擎` })
      reload()
    } catch (e) {
      toast.show({ kind: 'error', text: errText(e) })
    } finally {
      setBusy(false)
    }
  }
  if (!providers.length) {
    return <p className="m-0 text-ui-sm text-ink-2">還沒有設定任何 AI 模型。先到「設定 › AI 模型」加入金鑰或用 ChatGPT 方案登入。</p>
  }
  const none = `用預設模型${models?.default ? `（${models.default.model}）` : ''}`
  return (
    <div className="flex flex-col gap-3">
      {current ? (
        <>
          <ModelChooser
            label="模型"
            value={ref(bot.model)}
            providers={providers}
            presets={presets}
            noneLabel={none}
            onCommit={(v) => void save({ model: v ? `${v.provider}/${v.model}` : '' }, v ? `${bot.name}改用 ${v.model}` : `${bot.name}改用預設模型`)}
          />
          <ModelChooser
            label="備援模型（主要模型出錯或限流時用）"
            value={bot.fallback ? splitRef(bot.fallback) : null}
            providers={providers}
            presets={presets}
            noneLabel={`用全域備援${models?.fallback ? `（${models.fallback.model}）` : ''}`}
            onCommit={(v) => void save({ fallback: v ? `${v.provider}/${v.model}` : '' }, v ? `備援改成 ${v.model}` : '改用全域備援')}
          />
        </>
      ) : (
        <>
          <ModelChooser label="模型" value={ref(pick)} providers={providers} presets={presets} noneLabel={none} onCommit={(v) => setPick(v ? `${v.provider}/${v.model}` : '')} />
          <Button variant="primary" className="self-start" loading={busy} onClick={use}>
            改用內建引擎
          </Button>
        </>
      )}
    </div>
  )
}

function CliPanel({ bot, engine, current, acc, onBot, reload }: { bot: Bot; engine: Cli; current: boolean; acc?: Account; onBot: (b: Bot) => void; reload: () => void }) {
  const toast = useToast()
  const label = ENGINE_LABEL[engine]
  const [pick, setPick] = useState(current ? bot.model : '')
  const [busy, setBusy] = useState(false)
  const [login, setLogin] = useState(false)
  const [out, setOut] = useState(false)

  const use = async (link = false) => {
    setBusy(true)
    try {
      await switchTo(bot, engine, pick, onBot, link)
      toast.show({ kind: 'done', text: `${bot.name}改用 ${label}` })
      reload()
    } catch (e) {
      toast.show({ kind: 'error', text: errText(e) })
    } finally {
      setBusy(false)
    }
  }
  const pickModel = async (m: string) => {
    setPick(m)
    if (!current) return
    try {
      const b = await botsUpdate({ id: bot.id, model: m })
      onBot(b)
      upsertBot(b)
      toast.show({ kind: 'done', text: m ? `${bot.name}改用 ${m}` : `${bot.name}改用 ${label} 的預設模型` })
    } catch (e) {
      toast.show({ kind: 'error', text: errText(e) })
    }
  }
  const logout = async () => {
    setBusy(true)
    try {
      if (acc?.source === 'host') await enginesUnlinkCredentials({ bot_id: bot.id })
      else await enginesLogout({ bot_id: bot.id, engine })
      setOut(false)
      toast.show({ kind: 'done', text: `${bot.name}已登出 ${label}` })
      reload()
    } catch (e) {
      toast.show({ kind: 'error', text: errText(e) })
    } finally {
      setBusy(false)
    }
  }

  // 登入視窗和兩種內容並排放在同一層：登入好了、帳號變成已登入時，視窗不會被換掉重開。
  return (
    <>
      {acc?.ready ? (
        <ReadyAccount bot={bot} engine={engine} acc={acc} current={current} pick={pick} busy={busy} onPick={pickModel} onUse={() => void use()} onLogout={() => setOut(true)} reload={reload} />
      ) : (
        <LoginChoices bot={bot} engine={engine} acc={acc} busy={busy} onLogin={() => setLogin(true)} onHost={() => void use(true)} />
      )}
      <ConfirmDialog
        open={out}
        danger
        busy={busy}
        title={`讓${bot.name}登出 ${label}？`}
        description={`會結束它正在跑的 ${label}，清掉它電腦裡的登入。之後要再用得重新登入。`}
        confirmText="登出"
        onConfirm={logout}
        onCancel={() => setOut(false)}
      />
      {login && (acc?.login === 'key' ? <KeyLogin bot={bot} onClose={() => setLogin(false)} onDone={reload} /> : <CliLogin bot={bot} engine={engine} onClose={() => setLogin(false)} onDone={reload} />)}
    </>
  )
}

function LoginChoices({ bot, engine, acc, busy, onLogin, onHost }: { bot: Bot; engine: Cli; acc?: Account; busy: boolean; onLogin: () => void; onHost: () => void }) {
  const label = ENGINE_LABEL[engine]
  const plan = PLAN_NAME[engine]
  const kind = acc?.login ?? 'host'
  return (
    <div className="flex flex-col gap-3">
      <p className="m-0 text-ui-sm text-ink-2">
        {kind === 'host' ? (
          <>
            {label} 要在這台電腦的終端機登入（網頁上還做不到）：執行 <code className="font-mono text-meta">gemini</code>，選 Login with Google。
          </>
        ) : (
          `${bot.name}還沒登入 ${label}。登入的 ${plan} 帳號只給${bot.name}用，它的工作會用掉這個帳號的額度；換成別的 AI 時會收起來。`
        )}
      </p>
      <div className="flex flex-wrap gap-2">
        {kind !== 'host' && (
          <Button variant="primary" icon={KeyRound} onClick={onLogin}>
            {kind === 'key' ? `輸入 ${plan} 金鑰` : `登入 ${plan} 帳號`}
          </Button>
        )}
        {acc?.host_logged_in && (
          <Button variant={kind === 'host' ? 'primary' : 'secondary'} loading={busy} onClick={onHost}>
            用這台電腦的登入，改用 {label}
          </Button>
        )}
      </div>
    </div>
  )
}

function ReadyAccount({
  bot,
  engine,
  acc,
  current,
  pick,
  busy,
  onPick,
  onUse,
  onLogout,
  reload,
}: {
  bot: Bot
  engine: Cli
  acc: Account
  current: boolean
  pick: string
  busy: boolean
  onPick: (m: string) => Promise<void>
  onUse: () => void
  onLogout: () => void
  reload: () => void
}) {
  const toast = useToast()
  const label = ENGINE_LABEL[engine]
  const plan = PLAN_NAME[engine]
  const models = acc.models ?? []
  const [typed, setTyped] = useState(pick)
  const [checking, setChecking] = useState(false)
  const check = async () => {
    setChecking(true)
    try {
      const r = await enginesCheck({ bot_id: bot.id, engine })
      toast.show(r.ok ? { kind: 'done', text: `${label} 登入有效，模型清單已更新` } : { kind: 'error', text: r.message || `${label} 開不起來` })
      reload()
    } catch (e) {
      toast.show({ kind: 'error', text: errText(e) })
    } finally {
      setChecking(false)
    }
  }
  const options = [
    { value: '', label: acc.default ? `${label} 預設（${acc.default}）` : `${label} 預設` },
    ...(pick && !models.some((m) => m.value === pick) ? [{ value: pick, label: pick }] : []),
    ...models.map((m) => ({ value: m.value, label: m.name && m.name !== m.value ? `${m.name}（${m.value}）` : m.value })),
  ]
  return (
    <div className="flex flex-col gap-3">
      <div className="flex items-end gap-2">
        {models.length > 0 ? (
          <Select label="模型" className="min-w-0 flex-1" value={pick} options={options} onChange={(v) => void onPick(v)} />
        ) : (
          <TextInput
            label="模型"
            wrapperClassName="min-w-0 flex-1"
            value={typed}
            placeholder={acc.default ? `留空用預設（${acc.default}）` : `留空用 ${label} 的預設`}
            onChange={(e) => setTyped(e.target.value)}
            onBlur={() => typed.trim() !== pick && void onPick(typed.trim())}
            onKeyDown={(e) => {
              if (e.key !== 'Enter' || e.nativeEvent.isComposing) return
              e.preventDefault()
              ;(e.target as HTMLInputElement).blur()
            }}
          />
        )}
        <Button icon={RefreshCw} loading={checking} onClick={check}>
          重新讀取
        </Button>
      </div>
      {!models.length && <p className="m-0 text-meta text-ink-3">還沒讀到這個帳號能用的模型：按「重新讀取」，或直接輸入名稱。</p>}
      <div className="flex flex-wrap items-center gap-2">
        {!current && (
          <Button variant="primary" loading={busy} onClick={onUse}>
            改用 {label}
          </Button>
        )}
        <span className="min-w-0 flex-1 text-meta text-ink-2">{acc.source === 'host' ? `用這台電腦的 ${label} 登入` : `${bot.name}自己登入的 ${plan} 帳號`}</span>
        <Button variant="ghost" size="sm" onClick={onLogout}>
          登出
        </Button>
      </div>
    </div>
  )
}

type LoginView = EnginesLoginStatusOutput

/** 網頁登入（Codex、Grok：輸入代碼；Claude：登入後把授權碼貼回來）。 */
function CliLogin({ bot, engine, onClose, onDone }: { bot: Bot; engine: Cli; onClose: () => void; onDone: () => void }) {
  const label = ENGINE_LABEL[engine]
  const plan = PLAN_NAME[engine]
  const [v, setV] = useState<LoginView | null>(null)
  const [err, setErr] = useState('')
  const [starting, setStarting] = useState(true)
  const [code, setCode] = useState('')
  const [sending, setSending] = useState(false)
  const [copied, setCopied] = useState(false)
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)
  const alive = useRef(true)
  const finished = useRef(false)
  const done = useRef(onDone)
  useEffect(() => {
    done.current = onDone
  }, [onDone])

  useEffect(() => {
    alive.current = true
    const poll = () => {
      timer.current = setTimeout(async () => {
        if (!alive.current) return
        try {
          const s = await enginesLoginStatus({ bot_id: bot.id })
          if (!alive.current) return
          setV(s)
          if (s.state === 'done') {
            finished.current = true
            done.current()
            return
          }
          if (s.state === 'waiting' || s.state === 'checking') poll()
        } catch (e) {
          if (alive.current) setErr(errText(e))
        }
      }, 2000)
    }
    enginesLoginStart({ bot_id: bot.id, engine: engine as 'claude-code' | 'codex' | 'grok' })
      .then((s) => {
        if (!alive.current) return
        setV(s)
        setStarting(false)
        if (s.state === 'waiting' || s.state === 'checking') poll()
      })
      .catch((e) => {
        if (!alive.current) return
        setErr(errText(e))
        setStarting(false)
      })
    return () => {
      alive.current = false
      clearTimeout(timer.current)
    }
  }, [bot.id, engine])

  const close = () => {
    if (!finished.current && (starting || v?.state === 'waiting')) void enginesLoginCancel({ bot_id: bot.id }).catch(() => undefined)
    onClose()
  }
  const send = async () => {
    setSending(true)
    try {
      setV(await enginesLoginCode({ bot_id: bot.id, code: code.trim() }))
    } catch (e) {
      setErr(errText(e))
    } finally {
      setSending(false)
    }
  }
  const copy = async (c: string) => {
    try {
      await navigator.clipboard.writeText(c)
      setCopied(true)
    } catch {
      setCopied(false)
    }
  }

  const state = v?.state
  return (
    <Dialog
      open
      onClose={close}
      title={`${bot.name}登入 ${plan} 帳號`}
      width={520}
      actions={
        <Button variant={state === 'done' ? 'primary' : 'secondary'} onClick={close}>
          {state === 'done' || state === 'failed' || err ? '關閉' : '取消'}
        </Button>
      }
    >
      <div className="flex flex-col gap-3" aria-live="polite">
        {starting && <p className="m-0 text-ui-sm text-ink-2">正在{bot.name}的電腦裡開 {label} 的登入…（電腦沒開機的話要等一下）</p>}
        {state === 'waiting' && v?.url && (
          <>
            <p className="m-0 text-ui-sm">1. 用你自己的瀏覽器打開登入頁，登入你的 {plan} 帳號：</p>
            <a href={v.url} target="_blank" rel="noreferrer" className="flex items-center gap-1.5 break-all text-ui-sm" data-testid="login-url">
              {v.url.length > 80 ? v.url.slice(0, 80) + '…' : v.url}
              <Icon icon={ExternalLink} size={14} />
            </a>
            {v.code && (
              <>
                <p className="m-0 text-ui-sm">2. 網頁要你輸入代碼時，輸入：</p>
                <div className="flex items-center gap-3">
                  <code data-testid="login-code" className="rounded-field bg-inset px-4 py-2 font-mono text-[22px] tracking-[0.15em]">
                    {v.code}
                  </code>
                  <Button size="sm" icon={Copy} onClick={() => copy(v.code!)}>
                    {copied ? '已複製' : '複製'}
                  </Button>
                </div>
              </>
            )}
            {v.needs_code ? (
              <>
                <p className="m-0 text-ui-sm">2. 登入完網頁會顯示一段授權碼，整段複製貼在這裡：</p>
                <div className="flex items-end gap-2">
                  <TextInput label="授權碼" hideLabel wrapperClassName="min-w-0 flex-1" value={code} placeholder="貼上授權碼" onChange={(e) => setCode(e.target.value)} />
                  <Button variant="primary" disabled={!code.trim()} loading={sending} onClick={send}>
                    送出
                  </Button>
                </div>
              </>
            ) : (
              <p role="status" className="m-0 text-meta text-ink-3">
                等你在瀏覽器完成登入…（這個視窗會自己更新）
              </p>
            )}
          </>
        )}
        {state === 'checking' && <p className="m-0 text-ui-sm text-ink-2">登入好了，正在確認並讀取這個帳號能用的模型…</p>}
        {state === 'done' && (
          <p role="status" className="m-0 flex items-center gap-2 text-ui">
            <Icon icon={CircleCheck} color="var(--color-success)" />
            {v?.message && v.message !== '已登入' ? v.message : `${bot.name}已登入 ${plan} 帳號。選好模型後按「改用 ${label}」。`}
          </p>
        )}
        {(state === 'failed' || state === 'cancelled') && (
          <p role="alert" className="m-0 text-ui-sm text-danger">
            {v?.message || '登入沒有完成'}
          </p>
        )}
        {err && (
          <p role="alert" className="m-0 text-ui-sm text-danger">
            {err}
          </p>
        )}
      </div>
    </Dialog>
  )
}

/** OpenCode：貼上 OpenCode Go（或 Zen）的金鑰。 */
function KeyLogin({ bot, onClose, onDone }: { bot: Bot; onClose: () => void; onDone: () => void }) {
  const toast = useToast()
  const [goKey, setGoKey] = useState('')
  const [zenKey, setZenKey] = useState('')
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')
  const save = async () => {
    setBusy(true)
    setErr('')
    try {
      await enginesLoginKey({ bot_id: bot.id, go_key: goKey.trim(), zen_key: zenKey.trim() })
      toast.show({ kind: 'done', text: `${bot.name}已登入 OpenCode` })
      onDone()
      onClose()
    } catch (e) {
      setErr(errText(e))
    } finally {
      setBusy(false)
    }
  }
  return (
    <Dialog
      open
      onClose={onClose}
      busy={busy}
      title={`${bot.name}登入 OpenCode`}
      width={520}
      actions={
        <>
          <Button onClick={onClose} disabled={busy}>
            取消
          </Button>
          <Button variant="primary" disabled={!goKey.trim() && !zenKey.trim()} loading={busy} onClick={save}>
            登入
          </Button>
        </>
      }
    >
      <div className="flex flex-col gap-3">
        <p className="m-0 text-ui-sm text-ink-2">到 opencode.ai 的帳號頁複製金鑰。金鑰只放進{bot.name}的電腦給 OpenCode 用，換成別的 AI 時會收起來。</p>
        <TextInput label="OpenCode Go 金鑰" type="password" autoComplete="off" value={goKey} onChange={(e) => setGoKey(e.target.value)} />
        <TextInput label="OpenCode Zen 金鑰（選填）" type="password" autoComplete="off" value={zenKey} onChange={(e) => setZenKey(e.target.value)} />
        {err && (
          <p role="alert" className="m-0 text-ui-sm text-danger">
            {err}
          </p>
        )}
      </div>
    </Dialog>
  )
}
