import { useCallback, useEffect, useState } from 'react'
import { ChevronLeft, ChevronRight, KeyRound, ShieldAlert, ShieldCheck, TriangleAlert } from 'lucide-react'
import {
  botsGet,
  botsUpdate,
  enginesLinkCredentials,
  enginesStatus,
  enginesUnlinkCredentials,
  type BotsGetOutput,
  type BotsUpdateInput,
  type EnginesStatusOutput,
} from '../api/generated'
import { botsStore, upsertBot } from '../data/bots'
import { useStore } from '../data/store'
import { linkClick } from '../router'
import { Avatar } from '../ui/Avatar'
import { Button } from '../ui/Button'
import { Checkbox, SegmentedControl } from '../ui/Controls'
import { ConfirmDialog, Dialog } from '../ui/Dialog'
import { Icon } from '../ui/Icon'
import { StatusChip } from '../ui/Pill'
import { useToast } from '../ui/toast-context'
import { cx } from '../ui/cx'
import { ModelChooser } from './ModelChooser'
import { MemoryTab, SkillsTab, SoulTab } from './BotCapabilities'
import { Block, Group } from './parts'
import { ENGINE_LABEL, MODES, MODE_LABEL, PLAN_NAME, subscriptionMarker } from './botModes'
import { errText, isPending } from './format'
import { splitRef, useModelData } from './modelData'

// 每個 Bot 的引擎、訂閱、模型、權限等級（G08／G09；EN-2b、BT-5、SEC-2）。

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
    <Block title="各個 Bot 的引擎與權限" sub="每個 Bot 可以用不同的引擎；用你的訂閱要逐個 Bot 同意。">
      {bots && live.length === 0 && <p className="m-0 rounded-group bg-card px-4 py-3 text-ui-sm text-ink-2">還沒有 Bot。</p>}
      {live.length > 0 && (
        <Group label="Bot 清單">
          {live.map((b) => {
            const e = st?.bots.find((x) => x.bot_id === b.id)
            const to = `/settings/bots/${encodeURIComponent(b.id)}`
            return (
              <a key={b.id} href={to} onClick={linkClick(to)} className="flex min-h-14 items-center gap-3 border-line px-4 py-2.5 text-ink not-first:border-t hover:bg-control/50">
                <Avatar shape={b.shape} color={b.color} size={32} blink={false} />
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

export type BotTab = 'soul' | 'skills' | 'memory'

const TABS: { tab?: BotTab; label: string }[] = [{ label: '引擎與權限' }, { tab: 'soul', label: '人設' }, { tab: 'skills', label: '技能' }, { tab: 'memory', label: '記憶' }]

/** /settings/bots/:id[/soul|skills|memory]：引擎與權限（G08／G09）、人設、技能（G05）、記憶（G06）。 */
export function BotSettingsPage({ botId, tab }: { botId: string; tab?: BotTab }) {
  const toast = useToast()
  const { models, presets } = useModelData()
  const [bot, setBot] = useState<Bot | null>(null)
  const [err, setErr] = useState<string | null>(null)
  const [eng, setEng] = useState<EngineState | null>(null)
  const [host, setHost] = useState<HostCli[]>([])

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

  const update = async (patch: Omit<BotsUpdateInput, 'id'>, done: string) => {
    if (!bot) return
    try {
      const b = await botsUpdate({ id: bot.id, ...patch })
      setBot(b)
      upsertBot(b)
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

  const providers = models?.providers ?? []
  const modelRef = bot.model ? (bot.model.includes('/') ? splitRef(bot.model) : { provider: models?.default?.provider ?? '', model: bot.model }) : null
  const fallbackRef = bot.fallback ? splitRef(bot.fallback) : null
  const engine = (bot.engine || 'hermes') as Engine

  return (
    <>
      <a
        href="/settings/models/bots"
        onClick={linkClick('/settings/models/bots')}
        className="-mb-2 flex w-fit items-center gap-1 text-meta text-ink-2 hover:text-ink max-md:hidden"
      >
        <Icon icon={ChevronLeft} size={16} />
        各個 Bot
      </a>
      <div className="flex items-center gap-3 max-sm:flex-wrap">
        <Avatar shape={bot.shape} color={bot.color} size={40} blink={false} />
        <div className="min-w-0 flex-1">
          <h2 className="m-0 text-[18px] leading-[1.4]">{bot.name}</h2>
          <p className="m-0 text-sub text-ink-2">{bot.title || 'Bot 設定'}</p>
        </div>
        {eng?.linked && <SubscriptionMarker engine={eng.linked_engine || engine} marker={eng.marker} />}
      </div>

      <nav aria-label="Bot 設定分頁" className="flex w-fit gap-1 rounded-group bg-card p-1 max-sm:w-full">
        {TABS.map((t) => {
          const to = `/settings/bots/${encodeURIComponent(bot.id)}${t.tab ? '/' + t.tab : ''}`
          const on = t.tab === tab
          return (
            <a
              key={t.label}
              href={to}
              onClick={linkClick(to)}
              aria-current={on ? 'page' : undefined}
              className={cx(
                'flex h-9 items-center justify-center rounded-field px-4 text-ui-sm max-sm:flex-1 max-sm:px-2',
                on ? 'bg-control text-ink' : 'text-ink-2 hover:text-ink',
              )}
            >
              {t.label}
            </a>
          )
        })}
      </nav>

      {tab === 'soul' && <SoulTab key={bot.id} bot={bot} onSaved={setBot} />}
      {tab === 'skills' && <SkillsTab bot={bot} />}
      {tab === 'memory' && <MemoryTab bot={bot} onBot={setBot} />}
      {!tab && (
        <>
          <Block title="引擎" sub="Hermes 是內建引擎，用「AI 模型」設定的模型；Claude Code、Codex 用你在這台電腦上登入的訂閱。">
            <Group>
              <div className="flex flex-col gap-3 px-4 py-3">
                <SegmentedControl
                  label="引擎"
                  value={engine}
                  onChange={(v) => void update({ engine: v }, `${bot.name}改用 ${ENGINE_LABEL[v]}`)}
                  options={[
                    { value: 'hermes', label: 'Hermes（內建）' },
                    { value: 'claude-code', label: 'Claude Code' },
                    { value: 'codex', label: 'Codex' },
                  ]}
                />
                {engine !== 'hermes' && <Subscription bot={bot} engine={engine} state={eng} host={host.find((h) => h.engine === engine)} onChanged={loadEngine} />}
              </div>
            </Group>
          </Block>

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

          <Block title="權限等級" sub="這個 Bot 可以自己做多少。放寬權限要你本人決定。">
            <Group>
              <div role="radiogroup" aria-label="權限等級" className="flex flex-col">
                {MODES.map((m) => {
                  const on = bot.approval_mode === m.value
                  return (
                    <button
                      key={m.value}
                      type="button"
                      role="radio"
                      aria-checked={on}
                      onClick={() => !on && void update({ approval_mode: m.value }, `${bot.name}的權限改成「${m.label}」`)}
                      className={cx(
                        'flex min-h-14 items-center gap-3 border-line bg-transparent px-4 py-2.5 text-left text-ink not-first:border-t',
                        on ? 'bg-control/60' : 'hover:bg-control/40',
                      )}
                    >
                      <span
                        aria-hidden="true"
                        className={cx('flex size-[18px] flex-none items-center justify-center rounded-full border-[1.5px]', on ? 'border-accent' : 'border-check-off')}
                      >
                        {on && <span className="size-2.5 rounded-full bg-accent" />}
                      </span>
                      <span className="min-w-0 flex-1">
                        <span className="block text-ui">{m.label}</span>
                        <span className="block text-[13px] text-ink-2">{m.desc}</span>
                      </span>
                    </button>
                  )
                })}
              </div>
              <div className="flex items-start gap-2.5 border-t border-line px-4 py-3 text-[13px] text-ink-2">
                <span className="pt-px text-success">
                  <Icon icon={ShieldCheck} size={16} />
                </span>
                <span>就算選「完全放手」，危險的動作（刪除 Bot 或資料、付款、提高花費上限、做了無法挽回的事）也一定會先到「待我處理」等你核准，不會自己允許。</span>
              </div>
            </Group>
          </Block>
        </>
      )}
    </>
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
      await enginesLinkCredentials({ bot_id: bot.id, engine: engine as 'claude-code' | 'codex' })
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
