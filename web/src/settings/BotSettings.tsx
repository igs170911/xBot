import { useCallback, useEffect, useState, type ReactNode } from 'react'
import { Check, ChevronLeft, ChevronRight, Crown, KeyRound, Share2, ShieldCheck } from 'lucide-react'
import {
  botsGet,
  botsUpdate,
  enginesStatus,
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
import { Switch } from '../ui/Controls'
import { Icon } from '../ui/Icon'
import { StatusChip } from '../ui/Pill'
import { useToast } from '../ui/toast-context'
import { cx } from '../ui/cx'
import { MemoryTab, SkillsTab } from './BotCapabilities'
import { Block, Group, Row } from './parts'
import { BotCaps } from './UsageSection'
import { BasicTab } from './bot/BasicTab'
import { AccessTab } from './bot/AccessTab'
import { HistoryTab, VoiceTab } from './bot/HistoryTab'
import { ShareBotDialog } from './bot/share'
import { BotAI } from './bot/BotAI'
import { ENGINE_LABEL, MODES, MODE_LABEL, subscriptionMarker } from './botModes'
import { errText, isPending } from './format'

// 每個 Bot 的設定（G02～G13）：基本 · 能力 · 權限與花費 · 進階；引擎、訂閱、模型、權限等級（EN-2b、BT-5、SEC-2）。

type Bot = BotsGetOutput
type Engine = NonNullable<BotsUpdateInput['engine']>
type EngineState = EnginesStatusOutput['bots'][number]

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
  const [share, setShare] = useState(false)
  const [skillCount, setSkillCount] = useState<number | null>(null)
  const view = normalize(tab, sub)

  const loadEngine = useCallback(() => {
    enginesStatus({ bot_id: botId })
      .then((s) => setEng(s.bots.find((b) => b.bot_id === botId) ?? null))
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
        {eng?.linked && (
          // 手機上標示自己一行，不要把名字擠成直的
          <span className="max-sm:order-last max-sm:basis-full">
            <SubscriptionMarker engine={eng.linked_engine || engine} marker={eng.marker} />
          </span>
        )}
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
      {view.main === 'advanced' && view.sub === '' && <BotAI bot={bot} eng={eng} reload={loadEngine} onBot={onSaved} />}
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

export function SubscriptionMarker({ engine, marker }: { engine: string; marker?: string }) {
  return (
    <StatusChip tone="accent" icon={KeyRound}>
      {subscriptionMarker(engine, marker)}
    </StatusChip>
  )
}
