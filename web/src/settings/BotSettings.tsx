import { useCallback, useEffect, useState, type ReactNode } from 'react'
import { Check, ChevronLeft, ChevronRight, Crown, KeyRound, Share2, ShieldAlert, ShieldCheck, TriangleAlert } from 'lucide-react'
import {
  botsGet,
  botsUpdate,
  enginesLinkCredentials,
  enginesStatus,
  enginesUnlinkCredentials,
  skillsList,
  type BotsGetOutput,
  type BotsUpdateInput,
  type EnginesStatusOutput,
} from '../api/generated'
import { botsStore, upsertBot, look } from '../data/bots'
import { useStore } from '../data/store'
import { linkClick } from '../router'
import { Avatar } from '../ui/Avatar'
import { Button } from '../ui/Button'
import { Checkbox, Switch } from '../ui/Controls'
import { ConfirmDialog, Dialog } from '../ui/Dialog'
import { Icon } from '../ui/Icon'
import { TextInput } from '../ui/Field'
import { Select } from '../ui/Menu'
import { StatusChip } from '../ui/Pill'
import { useToast } from '../ui/toast-context'
import { cx } from '../ui/cx'
import { ModelChooser } from './ModelChooser'
import { MemoryTab, SkillsTab } from './BotCapabilities'
import { Block, Group, Row } from './parts'
import { BotCaps } from './UsageSection'
import { BasicTab } from './bot/BasicTab'
import { AccessTab } from './bot/AccessTab'
import { HistoryTab, VoiceTab } from './bot/HistoryTab'
import { ShareBotDialog } from './bot/share'
import { ENGINES, ENGINE_LABEL, MODES, MODE_LABEL, PLAN_NAME, subscriptionMarker } from './botModes'
import { errText, isPending } from './format'
import { splitRef, useModelData } from './modelData'

// 每個 Bot 的設定（G02～G13）：基本 · 能力 · 權限與花費 · 進階；引擎、訂閱、模型、權限等級（EN-2b、BT-5、SEC-2）。

type Bot = BotsGetOutput
type Engine = NonNullable<BotsUpdateInput['engine']>
type EngineState = EnginesStatusOutput['bots'][number]
type HostCli = EnginesStatusOutput['host'][number]

/** 各個 Bot 的引擎一覽（AI 模型 › 各個 Bot）。 */
export function BotsEngineList() {
  const bots = useStore(botsStore).bots
  const [st, setSt] = useState<EnginesStatusOutput | null>(null)
  useEffect(() => {
    enginesStatus({})
      .then(setSt)
      .catch(() => setSt(null))
  }, [])
  const live = (bots ?? []).filter((b) => !b.archived)
  return (
    <Block title="各個 Bot 用哪個" sub="點一個 Bot 換方案；用你的訂閱要逐個同意。">
      {bots && live.length === 0 && <p className="m-0 rounded-group bg-card px-4 py-3 text-ui-sm text-ink-2">還沒有 Bot。</p>}
      {live.length > 0 && (
        <Group label="Bot 清單">
          {live.map((b) => {
            const e = st?.bots.find((x) => x.bot_id === b.id)
            // 這裡要換的是引擎與模型：直接開「進階 › 模型」
            const to = `/settings/bots/${encodeURIComponent(b.id)}/advanced`
            return (
              <a key={b.id} href={to} onClick={linkClick(to)} className="flex min-h-14 items-center gap-3 border-line px-4 py-2.5 text-ink not-first:border-t hover:bg-control/50">
                <Avatar {...look(b)} size={32} blink={false} />
                <span className="min-w-0 flex-1">
                  <span className="block text-ui">{b.name}</span>
                  <span className="block truncate text-[13px] text-ink-2">
                    {ENGINE_LABEL[b.engine] ?? b.engine} · {b.model || '預設模型'} · {MODE_LABEL[b.approval_mode] ?? b.approval_mode}
                  </span>
                </span>
                {e?.linked && (
                  <StatusChip tone="accent" size="sm" icon={KeyRound}>
                    用你的訂閱
                  </StatusChip>
                )}
                {e?.needs_link && !e.linked && (
                  <StatusChip tone="warning" size="sm">
                    要同意
                  </StatusChip>
                )}
                <span className="text-ink-3">
                  <Icon icon={ChevronRight} size={18} />
                </span>
              </a>
            )
          })}
        </Group>
      )}
    </Block>
  )
}

/** 舊網址的分頁（soul、skills、memory）也接受，對到新的 4 個分頁。 */
export type BotTab = 'abilities' | 'permissions' | 'advanced' | 'soul' | 'skills' | 'memory'

// §21「Bot 設定：12 個分頁 → 4 個」：基本 · 能力 · 權限與花費 · 進階（G02～G12）。改了就自動儲存。
const TABS: { tab?: 'abilities' | 'permissions' | 'advanced'; label: string }[] = [
  { label: '基本' },
  { tab: 'abilities', label: '能力' },
  { tab: 'permissions', label: '權限與花費' },
  { tab: 'advanced', label: '進階' },
]

const SUBS = {
  abilities: [
    { sub: '', label: '技能' },
    { sub: 'memory', label: '記憶' },
    { sub: 'access', label: '應用程式與電腦' },
  ],
  advanced: [
    { sub: '', label: '模型' },
    { sub: 'voice', label: '聲音、通知、誰看得到' },
    { sub: 'history', label: '歷史' },
  ],
} as const

function normalize(tab?: BotTab, sub?: string): { main: 'basic' | 'abilities' | 'permissions' | 'advanced'; sub: string; openSoul: boolean } {
  if (tab === 'soul') return { main: 'basic', sub: '', openSoul: true }
  if (tab === 'skills') return { main: 'abilities', sub: '', openSoul: false }
  if (tab === 'memory') return { main: 'abilities', sub: 'memory', openSoul: false }
  if (tab === 'abilities' && sub === 'skills') return { main: 'abilities', sub: '', openSoul: false }
  return { main: tab ?? 'basic', sub: sub ?? '', openSoul: false }
}

function TabLink({ to, on, children, small }: { to: string; on: boolean; children: ReactNode; small?: boolean }) {
  return (
    <a
      href={to}
      onClick={linkClick(to)}
      aria-current={on ? 'page' : undefined}
      className={cx(
        'flex items-center justify-center whitespace-nowrap',
        small ? 'h-9 rounded-full px-3.5 text-ui-sm' : 'h-9 flex-1 rounded-field px-4 text-ui-sm max-sm:px-2',
        on ? 'bg-control text-ink' : 'text-ink-2 hover:text-ink',
      )}
    >
      {children}
    </a>
  )
}

/** /settings/bots/:id[/abilities|permissions|advanced[/子分頁]]（舊網址 soul、skills、memory 也可以）。 */
export function BotSettingsPage({ botId, tab, sub, back = { to: '/settings/models', label: 'AI 模型' } }: { botId: string; tab?: BotTab; sub?: string; /** 返回鈕：從哪裡打開的就回哪裡（設定頁算好給的）。 */ back?: { to: string; label: string } }) {
  const toast = useToast()
  const [bot, setBot] = useState<Bot | null>(null)
  const [err, setErr] = useState<string | null>(null)
  const [eng, setEng] = useState<EngineState | null>(null)
  const [host, setHost] = useState<HostCli[]>([])
  const [share, setShare] = useState(false)
  const [skillCount, setSkillCount] = useState<number | null>(null)
  const view = normalize(tab, sub)

  const loadEngine = useCallback(() => {
    enginesStatus({ bot_id: botId })
      .then((s) => {
        setEng(s.bots.find((b) => b.bot_id === botId) ?? null)
        setHost(s.host)
      })
      .catch(() => setEng(null))
  }, [botId])

  useEffect(() => {
    botsGet({ id: botId })
      .then(setBot)
      .catch((e) => setErr(errText(e)))
    loadEngine()
  }, [botId, loadEngine])

  useEffect(() => {
    if (view.main !== 'abilities') return
    skillsList({ bot_id: botId })
      .then((s) => setSkillCount(s.skills.length))
      .catch(() => setSkillCount(null))
  }, [botId, view.main])

  const onSaved = useCallback((b: Bot) => {
    setBot(b)
    upsertBot(b)
  }, [])

  const update = async (patch: Omit<BotsUpdateInput, 'id'>, done: string) => {
    if (!bot) return
    try {
      const b = await botsUpdate({ id: bot.id, ...patch })
      onSaved(b)
      toast.show({ kind: 'done', text: done })
      loadEngine()
    } catch (e) {
      toast.show({ kind: isPending(e) ? 'info' : 'error', text: errText(e) })
    }
  }

  if (err) {
    return (
      <p role="alert" className="m-0 text-ui-sm text-danger">
        讀不到這個 Bot：{err}
      </p>
    )
  }
  if (!bot) return <p className="m-0 text-ui-sm text-ink-2">讀取中…</p>

  const base = `/settings/bots/${encodeURIComponent(bot.id)}`
  const engine = (bot.engine || 'hermes') as Engine
  const subs = view.main === 'abilities' || view.main === 'advanced' ? SUBS[view.main] : null

  return (
    <>
      <a href={back.to} onClick={linkClick(back.to)} className="-mb-2 flex w-fit items-center gap-1 text-meta text-ink-2 hover:text-ink max-md:hidden" data-testid="bot-settings-back">
        <Icon icon={ChevronLeft} size={16} />
        {back.label}
      </a>
      <div className="flex items-center gap-3 max-sm:flex-wrap">
        <Avatar {...look(bot)} size={40} blink={false} />
        <div className="min-w-0 flex-1">
          <h2 className="m-0 flex items-center gap-1.5 text-[18px] leading-[1.4]">
            {bot.name}
            {bot.primary && <Icon icon={Crown} size={16} color="var(--color-accent-ink)" label="主要 Bot" />}
          </h2>
          <p className="m-0 truncate text-sub text-ink-2">{[bot.title, bot.description].filter(Boolean).join(' · ') || 'Bot 設定'}</p>
        </div>
        {eng?.linked && <SubscriptionMarker engine={eng.linked_engine || engine} marker={eng.marker} />}
        <Button icon={Share2} onClick={() => setShare(true)}>
          分享
        </Button>
      </div>

      <nav aria-label="Bot 設定分頁" className="flex w-full max-w-[520px] gap-1 rounded-group bg-card p-1">
        {TABS.map((t) => (
          <TabLink key={t.label} to={base + (t.tab ? '/' + t.tab : '')} on={(t.tab ?? 'basic') === view.main}>
            {t.label}
          </TabLink>
        ))}
      </nav>

      {subs && (
        <nav aria-label={view.main === 'abilities' ? '能力分頁' : '進階分頁'} className="-mt-1 flex flex-wrap gap-1">
          {subs.map((s) => (
            <TabLink key={s.label} small to={`${base}/${view.main}${s.sub ? '/' + s.sub : ''}`} on={s.sub === view.sub}>
              {s.label}
              {view.main === 'abilities' && !s.sub && skillCount !== null && <span className="ml-1.5 text-ink-3">{skillCount}</span>}
            </TabLink>
          ))}
        </nav>
      )}

      {view.main === 'basic' && <BasicTab key={bot.id} bot={bot} onSaved={onSaved} openSoul={view.openSoul} />}
      {view.main === 'abilities' && view.sub === '' && <SkillsTab bot={bot} />}
      {view.main === 'abilities' && view.sub === 'memory' && <MemoryTab bot={bot} onBot={onSaved} />}
      {view.main === 'abilities' && view.sub === 'access' && <AccessTab bot={bot} onSaved={onSaved} />}
      {view.main === 'permissions' && (
        <>
          <Permissions bot={bot} onPick={(m) => void update({ approval_mode: m.value }, `${bot.name}的權限改成「${m.label}」`)} />
          <BotCaps botId={bot.id} name={bot.name} />
          <Block title="可以找誰" sub={`${bot.name}可以交辦、詢問哪些 Bot`}>
            <Group>
              <Row title="團隊管理權限" sub={`開了以後${bot.name}可以幫你建隊友、組團隊、調整誰向誰報告（每天能建幾個 Bot 在「設定 → 進階 → 限制」調）。刪除 Bot 一定要你核准。`}>
                <Switch
                  label="團隊管理權限"
                  checked={!!bot.can_manage}
                  onChange={(on) => void update({ can_manage: on }, on ? `${bot.name}可以管理團隊了` : `已關閉${bot.name}的團隊管理權限`)}
                />
              </Row>
              <Row title="同團隊與你指定的匯報線" sub="在「團隊地圖」調整團隊與向誰報告；Bot 之間同一條往來的次數上限在「設定 → 進階 → 限制」調。">
                <a href="/teams" onClick={linkClick('/teams')} className="text-ui-sm">
                  團隊地圖
                </a>
              </Row>
            </Group>
          </Block>
        </>
      )}
      {view.main === 'advanced' && view.sub === '' && <EngineAndModel bot={bot} engine={engine} eng={eng} host={host} update={update} reload={loadEngine} />}
      {view.main === 'advanced' && view.sub === 'voice' && <VoiceTab bot={bot} />}
      {view.main === 'advanced' && view.sub === 'history' && <HistoryTab bot={bot} />}

      {share && <ShareBotDialog bot={bot} open onClose={() => setShare(false)} />}
    </>
  )
}

/** 權限四選一（G08）：新 Bot 預設「先問我」；安全底線說明一直顯示。 */
function Permissions({ bot, onPick }: { bot: Bot; onPick: (m: (typeof MODES)[number]) => void }) {
  return (
    <Block title="權限" sub={`${bot.name}做事時，哪些要先問你。放寬權限要你本人決定。`}>
      <div role="radiogroup" aria-label="權限等級" className="grid grid-cols-2 gap-2.5 max-sm:grid-cols-1">
        {MODES.map((m) => {
          const on = bot.approval_mode === m.value
          return (
            <button
              key={m.value}
              type="button"
              role="radio"
              aria-checked={on}
              onClick={() => !on && onPick(m)}
              className={cx(
                'flex min-h-[72px] items-start gap-3 rounded-group border bg-card px-4 py-3 text-left text-ink',
                on ? 'border-accent' : 'border-transparent hover:bg-control/60',
              )}
            >
              <span className="min-w-0 flex-1">
                <span className="flex items-center gap-2 text-ui">
                  {m.label}
                  {m.value === 'ask' && <StatusChip size="sm">預設</StatusChip>}
                  {m.value === 'full' && <span className="text-[13px] text-danger">（風險較高）</span>}
                </span>
                <span className="block text-[13px] text-ink-2">{m.desc}</span>
              </span>
              {on && (
                <span aria-hidden="true" className="flex size-5 flex-none items-center justify-center rounded-full bg-accent text-white">
                  <Icon icon={Check} size={13} strokeWidth={3} />
                </span>
              )}
            </button>
          )
        })}
      </div>
      <div className="flex items-start gap-2.5 rounded-group bg-card px-4 py-3 text-[13px] text-ink-2">
        <span className="pt-px text-success">
          <Icon icon={ShieldCheck} size={16} />
        </span>
        <span>
          就算選「完全放手」，危險的動作（刪除 Bot 或資料、付款、提高花費上限、做了無法挽回的事）也一定會先到「待我處理」等你核准，不會自己允許。隊友或主管說「可以」不能取代你的核定。
        </span>
      </div>
    </Block>
  )
}

/** 進階 › 模型（G09）：引擎、訂閱、模型與備援。 */
function EngineAndModel({
  bot,
  engine,
  eng,
  host,
  update,
  reload,
}: {
  bot: Bot
  engine: Engine
  eng: EngineState | null
  host: HostCli[]
  update: (patch: Omit<BotsUpdateInput, 'id'>, done: string) => Promise<void>
  reload: () => void
}) {
  const { models, presets } = useModelData()
  const providers = models?.providers ?? []
  const modelRef = bot.model ? (bot.model.includes('/') ? splitRef(bot.model) : { provider: models?.default?.provider ?? '', model: bot.model }) : null
  const fallbackRef = bot.fallback ? splitRef(bot.fallback) : null
  return (
    <>
      <Block title="引擎" sub="Hermes 是內建引擎，用「AI 模型」設定的模型；其他引擎用你在這台電腦上登入的訂閱，要逐個 Bot 同意。">
        <Group>
          <EnginePicker
            value={engine}
            host={host}
            linkedEngine={eng?.linked ? eng.linked_engine || engine : undefined}
            onChange={(v) => void update({ engine: v }, `${bot.name}改用 ${ENGINE_LABEL[v]}`)}
          />
          {engine !== 'hermes' && (
            <div className="border-t border-line px-4 py-3">
              <Subscription bot={bot} engine={engine} state={eng} host={host.find((h) => h.engine === engine)} onChanged={reload} />
            </div>
          )}
        </Group>
      </Block>

      {engine !== 'hermes' && <PlanModel bot={bot} engine={engine} eng={eng} onPick={(m) => void update({ model: m }, m ? `${bot.name}改用 ${m}` : `${bot.name}改用 ${ENGINE_LABEL[engine]} 的預設模型`)} />}

      {engine === 'hermes' && (
        <Block title="模型" sub="沒有指定時用預設模型。主要模型出錯或限流時改用備援。">
          <Group>
            <div className="flex flex-col gap-3 px-4 py-3">
              <ModelChooser
                label="這個 Bot 的模型"
                value={modelRef}
                providers={providers}
                presets={presets}
                noneLabel={`用預設模型${models?.default ? `（${models.default.model}）` : ''}`}
                onCommit={(v) => void update({ model: v ? `${v.provider}/${v.model}` : '' }, v ? `${bot.name}改用 ${v.model}` : `${bot.name}改用預設模型`)}
              />
              <ModelChooser
                label="備援模型"
                value={fallbackRef}
                providers={providers}
                presets={presets}
                noneLabel={`用全域備援${models?.fallback ? `（${models.fallback.model}）` : ''}`}
                onCommit={(v) => void update({ fallback: v ? `${v.provider}/${v.model}` : '' }, v ? `備援改成 ${v.model}` : '改用全域備援')}
              />
            </div>
          </Group>
        </Block>
      )}
    </>
  )
}

/** 用訂閱的 Bot 選模型：列出 CLI 開對話時回報的模型；還沒回報（沒聊過、或 CLI 不列）時可以直接打名稱。 */
function PlanModel({ bot, engine, eng, onPick }: { bot: Bot; engine: Engine; eng: EngineState | null; onPick: (model: string) => void }) {
  const models = eng?.engine === engine ? (eng.models ?? []) : []
  const [typed, setTyped] = useState(bot.model)
  const [seen, setSeen] = useState(bot.model)
  if (seen !== bot.model) {
    setSeen(bot.model)
    setTyped(bot.model)
  }
  const options = [
    { value: '', label: eng?.agent_default ? `${ENGINE_LABEL[engine]} 預設（${eng.agent_default}）` : `${ENGINE_LABEL[engine]} 預設` },
    ...(bot.model && !models.some((m) => m.value === bot.model) ? [{ value: bot.model, label: bot.model }] : []),
    ...models.map((m) => ({ value: m.value, label: m.name && m.name !== m.value ? `${m.name}（${m.value}）` : m.value })),
  ]
  return (
    <Block title="模型" sub={models.length ? `${PLAN_NAME[engine] ?? ENGINE_LABEL[engine]} 能用的模型。` : '聊過一次之後會列出能用的模型；也可以直接輸入名稱。'}>
      <Group>
        <div className="flex flex-col gap-3 px-4 py-3">
          {models.length > 0 ? (
            <Select label="這個 Bot 的模型" value={bot.model} options={options} onChange={onPick} />
          ) : (
            <TextInput
              label="這個 Bot 的模型"
              value={typed}
              placeholder={`留空用 ${ENGINE_LABEL[engine]} 的預設`}
              onChange={(e) => setTyped(e.target.value)}
              onBlur={() => typed.trim() !== bot.model && onPick(typed.trim())}
              onKeyDown={(e) => {
                if (e.key !== 'Enter' || e.nativeEvent.isComposing) return
                e.preventDefault()
                ;(e.target as HTMLInputElement).blur()
              }}
            />
          )}
        </div>
      </Group>
    </Block>
  )
}

/** 引擎選擇器（G08；E31）：每個引擎一列，標出這台電腦上有沒有登入、這個 Bot 有沒有同意使用訂閱。 */
function EnginePicker({ value, host, linkedEngine, onChange }: { value: Engine; host: HostCli[]; linkedEngine?: string; onChange: (v: Engine) => void }) {
  return (
    <div role="radiogroup" aria-label="引擎" className="flex flex-col">
      {ENGINES.map((e) => {
        const on = value === e.value
        const h = host.find((x) => x.engine === e.value)
        return (
          <button
            key={e.value}
            type="button"
            role="radio"
            aria-checked={on}
            data-engine={e.value}
            onClick={() => !on && onChange(e.value as Engine)}
            className={cx(
              'flex min-h-14 items-center gap-3 border-line bg-transparent px-4 py-2.5 text-left text-ink not-first:border-t',
              on ? 'bg-control/60' : 'hover:bg-control/40',
            )}
          >
            <span aria-hidden="true" className={cx('flex size-[18px] flex-none items-center justify-center rounded-full border-[1.5px]', on ? 'border-accent' : 'border-check-off')}>
              {on && <span className="size-2.5 rounded-full bg-accent" />}
            </span>
            <span className="min-w-0 flex-1">
              <span className="block text-ui">
                {ENGINE_LABEL[e.value]}
                {e.value === 'hermes' ? '（內建）' : ''}
              </span>
              <span className="block text-[13px] text-ink-2">{e.desc}</span>
            </span>
            {e.value !== 'hermes' &&
              (linkedEngine === e.value ? (
                <StatusChip tone="accent" size="sm" icon={KeyRound}>
                  用你的訂閱
                </StatusChip>
              ) : h?.logged_in ? (
                <StatusChip tone="success" size="sm">
                  已登入
                </StatusChip>
              ) : h ? (
                <StatusChip tone="warning" size="sm">
                  這台電腦沒登入
                </StatusChip>
              ) : null)}
          </button>
        )
      })}
    </div>
  )
}

export function SubscriptionMarker({ engine, marker }: { engine: string; marker?: string }) {
  return (
    <StatusChip tone="accent" icon={KeyRound}>
      {subscriptionMarker(engine, marker)}
    </StatusChip>
  )
}

function Subscription({ bot, engine, state, host, onChanged }: { bot: Bot; engine: Engine; state: EngineState | null; host?: HostCli; onChanged: () => void }) {
  const toast = useToast()
  const [consent, setConsent] = useState(false)
  const [revoke, setRevoke] = useState(false)
  const [agree, setAgree] = useState(false)
  const [busy, setBusy] = useState(false)
  const label = ENGINE_LABEL[engine]
  const plan = PLAN_NAME[engine]
  const linked = !!state?.linked

  const link = async () => {
    setBusy(true)
    try {
      await enginesLinkCredentials({ bot_id: bot.id, engine: engine as Exclude<Engine, 'hermes'> })
      setConsent(false)
      setAgree(false)
      toast.show({ kind: 'done', text: subscriptionMarker(engine) })
      onChanged()
    } catch (e) {
      toast.show({ kind: 'error', text: errText(e) })
    } finally {
      setBusy(false)
    }
  }
  const unlink = async () => {
    setBusy(true)
    try {
      await enginesUnlinkCredentials({ bot_id: bot.id })
      setRevoke(false)
      toast.show({ kind: 'done', text: `已收回，${bot.name}不再用你的 ${plan} 訂閱` })
      onChanged()
    } catch (e) {
      toast.show({ kind: 'error', text: errText(e) })
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="flex flex-col gap-2.5 rounded-field bg-inset px-3.5 py-3">
      {linked ? (
        <div className="flex items-center gap-3 max-sm:flex-col max-sm:items-stretch">
          <span className="min-w-0 flex-1">
            <SubscriptionMarker engine={engine} marker={state?.marker} />
            <span className="mt-1.5 block text-[13px] text-ink-2">
              {label} 的登入憑證唯讀放在{bot.name}的電腦裡，只有它能用。
            </span>
          </span>
          <Button variant="danger" onClick={() => setRevoke(true)}>
            收回
          </Button>
        </div>
      ) : (
        <div className="flex items-center gap-3 max-sm:flex-col max-sm:items-stretch">
          <span className="min-w-0 flex-1">
            <span className="block text-ui">{state?.pending_clear ? '已收回，等 Bot 的電腦連上時清掉憑證' : `還沒同意讓${bot.name}用你的 ${plan} 訂閱`}</span>
            <span className="block text-[13px] text-ink-2">
              {host && !host.logged_in ? (
                <>
                  這台電腦還沒登入 {label}。先在這台電腦執行 <code className="font-mono text-meta">{host.login}</code>。
                </>
              ) : (
                `同意之前，${bot.name}用 ${label} 不能工作。`
              )}
            </span>
          </span>
          <Button variant="primary" disabled={!!host && !host.logged_in} onClick={() => setConsent(true)}>
            同意使用我的訂閱
          </Button>
        </div>
      )}

      <Dialog
        open={consent}
        onClose={() => setConsent(false)}
        busy={busy}
        width={520}
        title={`讓${bot.name}用你的 ${plan} 訂閱？`}
        actions={
          <>
            <Button onClick={() => setConsent(false)} disabled={busy}>
              取消
            </Button>
            <Button variant="primary" disabled={!agree} loading={busy} onClick={link}>
              同意
            </Button>
          </>
        }
      >
        <ul className="m-0 flex list-none flex-col gap-2 p-0 text-ui-sm">
          <li className="flex gap-2.5">
            <span className="pt-0.5 text-ink-2">
              <Icon icon={KeyRound} size={16} />
            </span>
            <span>
              會把這台電腦上 {label} 的登入憑證<strong className="font-normal text-ink">唯讀</strong>放進<strong className="font-normal text-ink">{bot.name}的電腦</strong>
              ，只有這個 Bot，別的 Bot 拿不到。
            </span>
          </li>
          <li className="flex gap-2.5">
            <span className="pt-0.5 text-warning-ink">
              <Icon icon={TriangleAlert} size={16} />
            </span>
            <span>
              {bot.name}的工作會用掉你的 {plan} 方案額度；它電腦裡的程式也讀得到這份憑證。這是 API 金鑰不進 Bot 電腦這條規則唯一的例外。
            </span>
          </li>
          <li className="flex gap-2.5">
            <span className="pt-0.5 text-ink-2">
              <Icon icon={ShieldAlert} size={16} />
            </span>
            <span>
              隨時可以收回：收回會立刻結束它正在跑的 {label}，並清掉它電腦裡的憑證。介面會一直標示「{subscriptionMarker(engine)}」。
            </span>
          </li>
        </ul>
        <Checkbox checked={agree} onChange={setAgree} className="mt-2">
          我同意讓{bot.name}使用我的 {plan} 訂閱
        </Checkbox>
      </Dialog>

      <ConfirmDialog
        open={revoke}
        danger
        busy={busy}
        title={`收回${bot.name}的訂閱使用權？`}
        description={`會立刻結束它正在跑的 ${label}，並清掉它電腦裡的憑證。之後要再用，得重新同意。`}
        confirmText="收回"
        onConfirm={unlink}
        onCancel={() => setRevoke(false)}
      />
    </div>
  )
}
