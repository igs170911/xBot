import { useEffect, useMemo, useState, type ReactNode } from 'react'
import { ArrowLeft, ArrowRight, Asterisk, ChevronDown, ChevronRight, CircleCheck, CodeXml, Cpu, KeyRound, LoaderCircle, Lock, Palette, ShieldCheck, TriangleAlert } from 'lucide-react'
import {
  botsCreate,
  enginesLinkCredentials,
  modelsAddProvider,
  modelsGet,
  modelsPresets,
  onboardStatus,
  type ModelsGetOutput,
  type OnboardStatusOutput,
} from '../api/generated'
import { loadBots, upsertBot } from '../data/bots'
import { navigate } from '../router'
import { Avatar, Mascot } from '../ui/Avatar'
import type { EyeVariant } from '../ui/avatar-data'
import { Button } from '../ui/Button'
import { Checkbox } from '../ui/Controls'
import { TextInput } from '../ui/Field'
import { Icon } from '../ui/Icon'
import { Select } from '../ui/Menu'
import { StatusChip } from '../ui/Pill'
import { cx } from '../ui/cx'
import { useToast } from '../ui/toast-context'
import { errText } from '../settings/format'
import { guessPreset, maskKey, type Preset } from '../settings/modelData'
import { ColorPicker, EyePicker } from '../settings/bot/pickers'
import { updateOnboard } from './state'

// 首次設定（A03、A04；PRD ON-3、§21「6 步 → 2 步」）：一張會變形的卡片。
//   1. 接上 AI：偵測這台電腦上已登入的 Claude Code、Codex 與本機模型（LM Studio、Ollama），每列一個「用這個」；
//      都沒找到才直接顯示貼 API 金鑰（自動認出供應商，OpenCode 也可以）。
//   2. 認識小秘：名字、權限「先問我」都預設好，外觀收在「換個樣子」，下面一行看 Bot 電腦準備好了沒（建電腦）。
// 連手機、導覽改成首頁的「之後再做」卡片（A05）；電腦第一次收到工作時在背景開機（A06）。

type Status = OnboardStatusOutput
type Choice =
  | { kind: 'engine'; engine: 'claude-code' | 'codex'; label: string; plan: string }
  | { kind: 'model'; label: string; model: string }

const ENGINE_ICON = { 'claude-code': Asterisk, codex: CodeXml } as const
const DEFAULT_NAME = '小秘'
const POLL_MS = 5000

function Frame({ children }: { children: ReactNode }) {
  return (
    <div className="flex min-h-dvh flex-col items-center gap-4 bg-app px-4 pb-8 pt-4 text-ink">
      <header className="flex min-h-10 w-full items-center gap-2 px-2">
        <Mascot size={24} />
        <span className="flex-1 text-[16px]">XBot</span>
      </header>
      <main className="flex w-full flex-1 items-center justify-center">{children}</main>
    </div>
  )
}

function Steps({ n, onBack }: { n: 1 | 2; onBack?: () => void }) {
  return (
    <div className="flex items-center gap-2.5 text-meta text-ink-2">
      {onBack && (
        <button type="button" aria-label="上一步" onClick={onBack} className="-ml-1.5 flex size-8 items-center justify-center rounded-full bg-transparent text-ink-2 hover:bg-control hover:text-ink">
          <Icon icon={ArrowLeft} size={18} />
        </button>
      )}
      <span>步驟 {n} / 2</span>
      <span aria-hidden="true" className="flex gap-1">
        <span className="h-[3px] w-7 rounded-full bg-ink" />
        <span className={cx('h-[3px] w-7 rounded-full', n === 2 ? 'bg-ink' : 'bg-control')} />
      </span>
    </div>
  )
}

/** 首次設定。onDone 在建好小秘（或跳過）之後呼叫。 */
export function Onboarding({ onDone }: { onDone: () => void }) {
  const [step, setStep] = useState<1 | 2>(1)
  const [choice, setChoice] = useState<Choice | null>(null)
  const [status, setStatus] = useState<Status | null>(null)
  const [models, setModels] = useState<ModelsGetOutput | null>(null)
  const [keyFirst, setKeyFirst] = useState(false)

  useEffect(() => {
    onboardStatus({})
      .then(setStatus)
      .catch(() => setStatus({ first_run: true, bots: 0, state: { completed: false, tour_done: false, dismissed: [] }, engines: [], local: [], computer: { docker: false, image: '', ready: false }, legacy: { found: false, sources: 0 } }))
    modelsGet({})
      .then(setModels)
      .catch(() => setModels(null))
  }, [])

  // 第 2 步：電腦映像還沒好就每 5 秒再看一次（make box 跑完會自己變綠）。
  useEffect(() => {
    if (step !== 2 || !status || status.computer.ready) return
    const t = setInterval(() => {
      onboardStatus({})
        .then(setStatus)
        .catch(() => {})
    }, POLL_MS)
    return () => clearInterval(t)
  }, [step, status])

  const skip = async () => {
    await updateOnboard({ completed: true })
    onDone()
  }

  return (
    <Frame>
      {step === 1 ? (
        <ConnectAI
          status={status}
          models={models}
          openKey={keyFirst}
          onPick={(c) => {
            setChoice(c)
            setStep(2)
          }}
          onSkip={() => void skip()}
        />
      ) : (
        <MeetBot
          choice={choice}
          status={status}
          models={models}
          onBack={() => setStep(1)}
          onAddKey={() => {
            setKeyFirst(true)
            setStep(1)
          }}
          onDone={onDone}
        />
      )}
    </Frame>
  )
}

// ── 步驟 1：接上 AI（A03）─────────────────────────────

type Row = { key: string; icon: typeof Cpu; title: string; sub: string; choice: Choice; setup?: () => Promise<void> }

function ConnectAI({ status, models, openKey, onPick, onSkip }: { status: Status | null; models: ModelsGetOutput | null; openKey?: boolean; onPick: (c: Choice) => void; onSkip: () => void }) {
  const toast = useToast()
  const [busy, setBusy] = useState<string | null>(null)
  const [keyOpen, setKeyOpen] = useState<boolean | null>(openKey ? true : null)
  const detecting = !status

  const rows = useMemo<Row[]>(() => {
    if (!status) return []
    const out: Row[] = []
    for (const e of status.engines) {
      if (!e.logged_in || (e.engine !== 'claude-code' && e.engine !== 'codex')) continue
      out.push({ key: e.engine, icon: ENGINE_ICON[e.engine], title: e.label, sub: `已登入 · 用你的 ${e.plan} 訂閱`, choice: { kind: 'engine', engine: e.engine, label: e.label, plan: e.plan } })
    }
    for (const l of status.local) {
      const model = l.models[0] ?? ''
      out.push({
        key: 'local-' + l.id,
        icon: Cpu,
        title: '本機模型',
        sub: `${l.label} · 找到 ${l.models.length} 個模型，不用連網`,
        choice: { kind: 'model', label: `${l.label} · ${model}`, model },
        setup: async () => {
          await modelsAddProvider(
            l.preset
              ? { preset: l.preset, default_model: model || undefined }
              : { id: l.id, label: l.label, base_url: l.base_url, transport: 'chat_completions', default_model: model || undefined },
          )
        },
      })
    }
    if (models?.ready && models.default) {
      const d = models.default
      const p = models.providers.find((x) => x.id === d.provider)
      out.push({ key: 'configured', icon: KeyRound, title: p?.label || d.provider, sub: `已設定 · ${d.model}`, choice: { kind: 'model', label: d.model, model: d.model } })
    }
    return out
  }, [status, models])

  const found = rows.length > 0
  const showKey = keyOpen ?? (!detecting && !found)

  const use = async (r: Row) => {
    setBusy(r.key)
    try {
      await r.setup?.()
      onPick(r.choice)
    } catch (e) {
      toast.show({ kind: 'error', text: errText(e) })
    } finally {
      setBusy(null)
    }
  }

  return (
    <section aria-label="接上 AI" className="flex w-full max-w-[620px] flex-col gap-4 rounded-card border border-divider bg-panel px-8 pb-6 pt-8 max-sm:px-5">
      <Steps n={1} />
      <div className="flex flex-col gap-1">
        <h1 className="m-0 text-page">接上 AI</h1>
        <p className="m-0 text-ui-sm text-ink-2">Bot 用你已經登入的 AI 來工作。挑一個就好，之後可以再加。</p>
      </div>

      {detecting ? (
        <div className="flex flex-col gap-2" aria-busy="true">
          <span className="flex items-center gap-2 text-meta text-ink-2">
            <Icon icon={LoaderCircle} size={14} className="animate-spin" />
            正在找這台電腦上的 AI 工具…
          </span>
          {[0, 1, 2].map((i) => (
            <span key={i} className="block h-16 animate-pulse rounded-group bg-card" />
          ))}
        </div>
      ) : (
        found && (
          <div className="flex flex-col gap-2">
            <span className="text-meta text-ink-2">在這台電腦上找到 {rows.length} 個</span>
            <ul aria-label="找到的 AI" className="m-0 flex list-none flex-col gap-2 p-0">
              {rows.map((r, i) => (
                <li key={r.key} className="flex min-h-16 items-center gap-3 rounded-group bg-card px-3.5 py-2.5">
                  <span className="flex size-9 flex-none items-center justify-center rounded-field bg-control text-ink">
                    <Icon icon={r.icon} size={18} />
                  </span>
                  <span className="min-w-0 flex-1">
                    <span className="flex items-center gap-2 text-ui">
                      {r.title}
                      {i === 0 && (
                        <StatusChip tone="accent" size="sm">
                          建議
                        </StatusChip>
                      )}
                    </span>
                    <span className="block truncate text-[13px] text-ink-2">{r.sub}</span>
                  </span>
                  <Button variant={i === 0 ? 'primary' : 'secondary'} loading={busy === r.key} disabled={!!busy && busy !== r.key} onClick={() => void use(r)} aria-label={`用這個：${r.title}`}>
                    用這個
                  </Button>
                </li>
              ))}
            </ul>
            {rows.length === 1 && rows[0].key.startsWith('local-') && <p className="m-0 text-meta text-ink-3">只有本機模型也可以用；速度看這台電腦。</p>}
          </div>
        )
      )}

      {!detecting && found && (
        <button
          type="button"
          aria-expanded={showKey}
          onClick={() => setKeyOpen(!showKey)}
          className="flex h-11 items-center gap-2.5 rounded-field bg-transparent px-1 text-left text-ui-sm text-ink-2 hover:text-ink"
        >
          <Icon icon={KeyRound} size={16} />
          <span className="flex-1">改用 API 金鑰</span>
          <Icon icon={showKey ? ChevronDown : ChevronRight} size={16} />
        </button>
      )}
      {showKey && <KeyForm hasDefault={!!models?.default} onDone={onPick} />}

      <div className="flex items-center gap-2 border-t border-divider pt-4 text-meta text-ink-2">
        <Icon icon={Lock} size={14} />
        <span className="flex-1">金鑰只存在這台主機，不會傳給 Bot 的電腦。</span>
        <button type="button" onClick={onSkip} className="bg-transparent p-0 text-meta text-ink-3 underline-offset-2 hover:text-ink-2 hover:underline">
          先跳過
        </button>
      </div>
    </section>
  )
}

function KeyForm({ hasDefault, onDone }: { hasDefault: boolean; onDone: (c: Choice) => void }) {
  const [key, setKey] = useState('')
  const [presets, setPresets] = useState<Preset[]>([])
  const [picked, setPicked] = useState('')
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string | null>(null)
  useEffect(() => {
    modelsPresets({})
      .then((p) => setPresets(p.presets.filter((x) => !x.local)))
      .catch(() => setPresets([]))
  }, [])
  const guessed = guessPreset(key, presets)
  const preset = presets.find((p) => p.id === (picked || guessed?.id))
  const model = preset?.default_model ?? preset?.models[0] ?? ''

  const add = async () => {
    if (!preset || !key.trim()) return
    setBusy(true)
    setErr(null)
    try {
      await modelsAddProvider({ preset: preset.id, api_key: key.trim(), default_model: model || undefined })
      onDone({ kind: 'model', label: `${preset.label}${model ? ` · ${model}` : ''}`, model })
    } catch (e) {
      setErr(errText(e))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="flex flex-col gap-3 rounded-group bg-card px-4 py-3.5">
      <TextInput
        label="API 金鑰"
        icon={KeyRound}
        type="password"
        autoComplete="off"
        spellCheck={false}
        value={key}
        onChange={(e) => setKey(e.target.value)}
        placeholder="貼上金鑰，例：sk-…"
        className="font-mono"
        error={err ?? undefined}
      />
      {key.trim() && (
        <p className="m-0 text-meta text-ink-2" aria-live="polite">
          {guessed ? (
            <>
              <Icon icon={CircleCheck} size={14} color="var(--color-success)" className="mr-1 inline-block align-[-2px]" />
              認出是 {guessed.label}（{maskKey(key)}）{model && ` · 建議模型 ${model}`}
            </>
          ) : (
            '認不出是哪一家，請選供應商（OpenCode 的金鑰選 OpenCode Go 或 Zen）。'
          )}
        </p>
      )}
      {key.trim() && !guessed && (
        <Select label="供應商" value={picked} placeholder="選供應商" options={presets.map((p) => ({ value: p.id, label: p.label }))} onChange={setPicked} />
      )}
      <p className="m-0 text-meta text-ink-3">所有 Bot 合計預設每月最多花 $20，到了會先停下來問你；可在「設定 → 用量與花費」改。</p>
      <div className="flex justify-end">
        <Button variant="primary" loading={busy} disabled={!preset || !key.trim()} onClick={() => void add()}>
          {hasDefault ? '加入並用這個' : '用這把金鑰'}
        </Button>
      </div>
    </div>
  )
}

// ── 步驟 2：認識小秘（A04）＋電腦在背景準備 ───────────────

function MeetBot({
  choice,
  status,
  models,
  onBack,
  onAddKey,
  onDone,
}: {
  choice: Choice | null
  status: Status | null
  models: ModelsGetOutput | null
  onBack: () => void
  onAddKey: () => void
  onDone: () => void
}) {
  const toast = useToast()
  const [name, setName] = useState(DEFAULT_NAME)
  const [color, setColor] = useState<string>('blue')
  const [eyes, setEyes] = useState<EyeVariant>('happy')
  const [looks, setLooks] = useState(false)
  const [busy, setBusy] = useState(false)
  const shown = name.trim() || DEFAULT_NAME
  const engine = choice?.kind === 'engine' ? choice : null
  // EN-2b：訂閱憑證要逐 Bot 明確同意，預設不勾；不同意就改用已設定的模型（Hermes）。
  const [agree, setAgree] = useState(false)
  const useSub = !!engine && agree
  const hasModel = !!models?.ready
  const blocked = !!engine && !agree && !hasModel

  const start = async () => {
    setBusy(true)
    try {
      const bot = await botsCreate({
        name: shown,
        title: '幕僚長',
        description: '每天整理重點，把工作交給對的隊友',
        soul: `你是${shown}，使用者的幕僚長。說話簡短、先講結論，用台灣用語。不確定的事先問，付款、對外發送、刪除這類事做之前一定先問。`,
        shape: 'cursor',
        color,
        eyes,
        engine: useSub ? engine.engine : undefined,
      })
      upsertBot(bot)
      if (useSub) {
        // 你勾了同意（EN-2b 逐 Bot 同意）才把訂閱連給這個 Bot
        await enginesLinkCredentials({ bot_id: bot.id, engine: engine.engine }).catch((e: unknown) =>
          toast.show({ kind: 'error', text: `${shown}還不能用你的 ${engine.plan} 訂閱：${errText(e)}` }),
        )
      }
      await updateOnboard({ completed: true })
      void loadBots()
      onDone()
      navigate(`/bots/${bot.id}`)
    } catch (e) {
      toast.show({ kind: 'error', text: errText(e) })
      setBusy(false)
    }
  }

  return (
    <section aria-label={`認識${shown}`} className="flex w-full max-w-[520px] flex-col gap-4 rounded-card border border-divider bg-panel px-8 pb-6 pt-8 max-sm:px-5">
      <Steps n={2} onBack={onBack} />
      <div className="flex flex-col items-center gap-2 text-center">
        <Avatar shape="cursor" color={color} eyes={eyes} dot="none" size={80} label={`${shown}的頭像`} />
        <h1 className="m-0 text-page">認識{shown}</h1>
        <p className="m-0 max-w-[400px] text-ui-sm text-ink-2">
          {shown}是你的幕僚長。你交代事情給它，它會自己做，或分給之後加入的 Bot。
        </p>
      </div>
      <TextInput label="名字" value={name} maxLength={40} onChange={(e) => setName(e.target.value)} placeholder={DEFAULT_NAME} />
      <div className="flex items-start gap-2.5 rounded-group bg-card px-3.5 py-3">
        <span className="pt-0.5 text-success">
          <Icon icon={ShieldCheck} size={18} />
        </span>
        <span className="min-w-0 flex-1">
          <span className="block text-ui">先問我</span>
          <span className="block text-[13px] text-ink-2">付款、對外發送、刪除這類事，做之前先問你。</span>
        </span>
        <span className="text-meta text-ink-3">之後可改</span>
      </div>
      <div className="rounded-group border border-line">
        <button type="button" aria-expanded={looks} onClick={() => setLooks((x) => !x)} className="flex h-12 w-full items-center gap-2.5 bg-transparent px-3.5 text-left text-ui-sm text-ink-2 hover:text-ink">
          <Icon icon={Palette} size={18} />
          <span className="flex-1">換個樣子</span>
          <Icon icon={looks ? ChevronDown : ChevronRight} size={16} />
        </button>
        {looks && (
          <div className="flex flex-col gap-3 border-t border-line px-3.5 pb-3.5 pt-3">
            <span className="text-meta text-ink-2">顏色</span>
            <ColorPicker value={color} onChange={setColor} />
            <span className="text-meta text-ink-2">眼睛 · 只有眼睛，沒有嘴巴</span>
            <EyePicker value={{ shape: 'cursor', color, eyes }} onChange={setEyes} />
            <span className="text-meta text-ink-3">形狀預設游標；之後可以在外觀換 10 種形狀。</span>
          </div>
        )}
      </div>
      <ComputerLine status={status} name={shown} />
      {engine && (
        <div data-testid="consent" className="flex flex-col gap-2 rounded-group border border-line px-3.5 py-3 text-[13px] text-ink-2">
          <span className="flex items-start gap-2">
            <Icon icon={KeyRound} size={14} className="mt-0.5 flex-none" />
            <span>
              同意後，會把這台電腦上 {engine.label} 的登入憑證<strong className="font-normal text-ink">唯讀</strong>放進<strong className="font-normal text-ink">{shown}的電腦</strong>，只有這個 Bot 拿得到；它的工作會用掉你的 {engine.plan} 方案額度，電腦裡的程式也讀得到這份憑證。隨時可以在設定收回。
            </span>
          </span>
          <Checkbox checked={agree} onChange={setAgree}>
            我同意讓{shown}使用我的 {engine.plan} 訂閱
          </Checkbox>
          {!agree &&
            (hasModel ? (
              <span className="text-ink-3">不同意也可以：{shown}先用你設定好的模型{models?.default ? `（${models.default.model}）` : ''}。</span>
            ) : (
              <span className="flex flex-wrap items-center gap-2 text-warning-ink">
                還沒有其他可用的模型：勾選同意，或改貼 API 金鑰。
                <Button size="sm" icon={KeyRound} onClick={onAddKey}>
                  改用 API 金鑰
                </Button>
              </span>
            ))}
        </div>
      )}
      {choice?.kind === 'model' && <p className="m-0 text-meta text-ink-3">{shown}會用 {choice.label}。</p>}
      <Button variant="primary" loading={busy} disabled={blocked} onClick={() => void start()} className="h-12 w-full text-[16px] max-sm:h-12">
        開始
        <Icon icon={ArrowRight} size={18} />
      </Button>
      <p className="m-0 text-center text-meta text-ink-3">名字、樣子、權限之後都能在{shown}的設定裡改。</p>
    </section>
  )
}

/** 「建電腦」那一步縮成一行：映像好了沒、Docker 有沒有開；還沒好時每 5 秒自己更新。 */
function ComputerLine({ status, name }: { status: Status | null; name: string }) {
  const c = status?.computer
  let icon = LoaderCircle
  let tone = 'text-ink-2'
  let text = '正在看 Bot 的電腦準備好了沒…'
  if (c?.ready) {
    icon = CircleCheck
    tone = 'text-success'
    text = `${name}的電腦已備好 · 第一次交代工作時開機（最多約 75 秒）`
  } else if (c) {
    icon = TriangleAlert
    tone = 'text-warning-ink'
    text = c.hint || '電腦還沒準備好'
  }
  return (
    <div data-testid="computer-line" className="flex items-start gap-2.5 rounded-group bg-inset px-3.5 py-2.5 text-[13px]">
      <span className={cx('pt-0.5', tone)}>
        <Icon icon={c ? icon : LoaderCircle} size={16} className={!c ? 'animate-spin' : undefined} />
      </span>
      <span className="min-w-0 flex-1 text-ink-2">
        {text}
        {c && !c.ready && <span className="block text-ink-3">不用等：沒有電腦時，{name}先用不需要電腦的方式工作。</span>}
      </span>
    </div>
  )
}
