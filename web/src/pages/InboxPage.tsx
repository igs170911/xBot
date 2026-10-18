import { useEffect, useState, type ReactNode } from 'react'
import { ArrowRight, Check, Clock, Hand, RotateCcw, ShieldCheck, SquareTerminal, TriangleAlert, Undo2 } from 'lucide-react'
import { Avatar } from '../ui/Avatar'
import { Button } from '../ui/Button'
import { Card } from '../ui/Card'
import { cx } from '../ui/cx'
import { Icon } from '../ui/Icon'
import { SimplifiedTag, StatusChip, type Tone } from '../ui/Pill'
import { useToast } from '../ui/toast-context'
import { BOTS } from '../shell/sample'
import { fetchHealth, type Health } from '../health'
import { ApiError, approvalsDecide, chatAskAgain, inboxDismiss, inboxUndo, routinesRunNow } from '../api/generated'
import { botsStore, type Bot } from '../data/bots'
import { inboxStore, loadInbox, type InboxItem } from '../data/inbox'
import { useStore } from '../data/store'
import { AskCard } from '../chat/AskCard'
import { askFromItem } from '../chat/ask'
import { AgendaCard } from '../agenda/AgendaCard'
import { linkClick } from '../router'

// 待我處理（design/Main、F08、C08～C10、L02）：真的卡片來自 xbot.inbox.list。
//   - Bot 在對話裡的提問（核准指令、補充問題、密碼）：和對話裡同一張 AskCard，回覆走 xbot.chat.respond。
//   - 操作的核准（MCP-9，例：代理要刪 Bot）：允許這次／退回走 xbot.approvals.decide，退回後 10 分鐘內可以復原。
//   - 其他（預算暫停…）：知道了＝xbot.inbox.dismiss，也可以復原。
// 右欄的團隊現況還是 W01 的示範內容（辦公室、Bot 之間在之後的卡接上）。

function errText(err: unknown): string {
  if (err instanceof ApiError) return err.message
  return err instanceof Error ? err.message : String(err)
}

function sinceLabel(ms: number, now = Date.now()): string {
  const min = Math.round((now - ms) / 60_000)
  if (min < 1) return '剛剛'
  if (min < 60) return `${min} 分鐘前`
  const h = Math.round(min / 60)
  return h < 24 ? `${h} 小時前` : `${Math.round(h / 24)} 天前`
}

type CardBot = { name: string; shape?: string; color?: string }

function cardBot(item: InboxItem, bots: Bot[] | null): CardBot {
  const b = bots?.find((x) => x.id === item.bot_id)
  const p = (item.payload ?? {}) as { bot_name?: string }
  return { name: b?.name ?? p.bot_name ?? (item.bot_id ? 'Bot' : 'XBot'), shape: b?.shape, color: b?.color }
}

function useUndoToast() {
  const toast = useToast()
  return (text: string, inboxId: string) =>
    toast.show({
      kind: 'undo',
      text,
      duration: 10_000,
      action: {
        label: '復原',
        onClick: () => {
          inboxUndo({ inbox_id: inboxId })
            .then(() => loadInbox())
            .catch((err: unknown) => toast.show({ kind: 'error', text: errText(err) }))
        },
      },
    })
}

function Head({ bot, chip, tone, sub, aside }: { bot: CardBot; chip: ReactNode; tone: Tone; sub: ReactNode; aside?: ReactNode }) {
  return (
    <div className="flex items-center gap-3">
      <Avatar shape={bot.shape} color={bot.color} state="notifying" dot="waiting" ring="#262626" size={32} />
      <div className="flex min-w-0 flex-1 flex-col">
        <span className="flex items-center gap-2">
          <span className="text-body">{bot.name}</span>
          <StatusChip size="sm" tone={tone}>
            {chip}
          </StatusChip>
        </span>
        <span className="text-[13px] text-ink-2">{sub}</span>
      </div>
      {aside}
    </div>
  )
}

/** 操作的核准（MCP-9）：允許這次／退回。刪除類用危險樣式（F08）。 */
function ApprovalItem({ item, bot }: { item: InboxItem; bot: CardBot }) {
  const [busy, setBusy] = useState<'allow' | 'deny' | null>(null)
  const [error, setError] = useState<string | null>(null)
  const undoToast = useUndoToast()
  const p = (item.payload ?? {}) as { op?: string; actor?: string; actor_name?: string; via?: string; request_id?: string; delegable?: boolean }
  const op = p.op ?? ''
  const danger = /\.(delete|remove)$/.test(op)
  const byAgent = !!p.actor?.startsWith('agent:')
  const decide = async (decision: 'allow' | 'deny') => {
    if (!item.ref) return
    setBusy(decision)
    setError(null)
    try {
      const out = await approvalsDecide({ approval_id: item.ref, decision })
      if (out.error) setError(out.error.message)
      if (decision === 'deny') undoToast(`已退回 · ${item.title}`, item.id)
      await loadInbox()
    } catch (err) {
      setError(errText(err))
    } finally {
      setBusy(null)
    }
  }
  return (
    <Card data-testid="inbox-item" data-kind="approval">
      <Head
        bot={bot}
        chip="等你允許"
        tone="warning"
        sub={`${sinceLabel(item.created_at)} · ${byAgent ? `透過 ${p.via === 'mcp' || !p.via ? 'MCP' : p.via.toUpperCase()} · ` : ''}${p.delegable && !danger ? '可以由有授權的代理代為核准' : '只有你能決定'}`}
      />
      <p className="m-0 text-body">{item.title}</p>
      {byAgent && op && (
        <code data-testid="approval-op" className="block truncate font-mono text-[12.5px] text-ink-3">
          {op}
          {p.request_id ? ` · request_id ${p.request_id}` : ''}
        </code>
      )}
      {item.body && <p className="m-0 whitespace-pre-wrap rounded-menu bg-inset px-3.5 py-2.5 text-ui-sm text-ink-2">{item.body}</p>}
      {danger && (
        <p className="m-0 flex items-center gap-2 rounded-menu bg-danger/15 px-3.5 py-2.5 text-ui-sm text-danger">
          <Icon icon={TriangleAlert} size={16} />
          這個動作做了就無法復原。
        </p>
      )}
      {error && (
        <p role="alert" className="m-0 text-sub text-danger">
          {error}
        </p>
      )}
      <div className="flex flex-wrap justify-end gap-2">
        <Button variant="outline" onClick={() => void decide('deny')} loading={busy === 'deny'} disabled={!!busy}>
          退回
        </Button>
        <Button variant={danger ? 'danger' : 'primary'} icon={danger ? undefined : Check} onClick={() => void decide('allow')} loading={busy === 'allow'} disabled={!!busy}>
          {danger ? '允許刪除' : '允許這次'}
        </Button>
      </div>
    </Card>
  )
}

function minutesLeft(until: number, now: number): number {
  return Math.max(1, Math.ceil((until - now) / 60_000))
}

/** 現在時間，每 15 秒更新一次（復原倒數用）。 */
function useNow(): number {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    const t = window.setInterval(() => setNow(Date.now()), 15_000)
    return () => window.clearInterval(t)
  }, [])
  return now
}

/** 代理代為核准的紀錄卡（MCP-10，C16、F08）：做了什麼、今天第幾次；可以復原的話 10 分鐘內能按「復原」。 */
function DelegatedItem({ item }: { item: InboxItem }) {
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const p = (item.payload ?? {}) as { actor_name?: string; via?: string; category_name?: string; reversible?: boolean }
  const now = useNow()
  const undone = item.resolution === 'undone'
  const undoUntil = item.undo_until ?? 0
  const canUndo = !undone && undoUntil > now
  const time = new Date(item.created_at)
  const hhmm = `${String(time.getHours()).padStart(2, '0')}:${String(time.getMinutes()).padStart(2, '0')}`
  const undo = async () => {
    setBusy(true)
    setError(null)
    try {
      await inboxUndo({ inbox_id: item.id })
      await loadInbox()
    } catch (err) {
      setError(errText(err))
    } finally {
      setBusy(false)
    }
  }
  return (
    <section
      aria-label="代為核准的紀錄"
      data-testid="inbox-delegated"
      data-undone={undone || undefined}
      className={cx('flex flex-wrap items-center gap-x-3 gap-y-2 rounded-card bg-card py-3 pl-[18px] pr-3.5', undone && 'opacity-70')}
    >
      <span className="flex size-8 flex-none items-center justify-center rounded-field bg-control text-ink-2">
        <Icon icon={SquareTerminal} size={16} />
      </span>
      <span className="flex min-w-0 flex-[999_1_260px] flex-col gap-0.5">
        <span className={cx('text-ui', undone && 'line-through')}>{item.title}</span>
        <span className="text-[13px] text-ink-2">
          {item.body} · {hhmm}
        </span>
        {error && (
          <span role="alert" className="text-sub text-danger">
            {error}
          </span>
        )}
      </span>
      {undone ? (
        <span className="inline-flex flex-none items-center gap-1.5 text-sub text-success">
          <Icon icon={Check} size={16} />
          已復原
        </span>
      ) : canUndo ? (
        <span className="flex flex-none items-center gap-2">
          <span className="text-meta text-ink-3">還能復原 {minutesLeft(undoUntil, now)} 分鐘</span>
          <Button size="sm" icon={Undo2} loading={busy} onClick={() => void undo()}>
            復原
          </Button>
        </span>
      ) : (
        <span className="flex-none text-meta text-ink-3">{p.reversible ? '已超過 10 分鐘，不能復原' : '這個動作做了就不能復原'}</span>
      )}
    </section>
  )
}

/** 不用決定、看過就好的卡（預算暫停、失敗的排程…）。 */
function InfoItem({ item, bot }: { item: InboxItem; bot: CardBot }) {
  const [busy, setBusy] = useState(false)
  const undoToast = useUndoToast()
  const toast = useToast()
  const chip = item.kind === 'budget' ? '預算用完，已暫停' : item.kind === 'routine_failed' ? '定時任務沒有完成' : '等你處理'
  // 定時任務失敗卡（C32）：重跑一次，然後收掉這張卡。
  const rerun = async () => {
    setBusy(true)
    try {
      await routinesRunNow({ id: (item.payload as { routine_id: string }).routine_id })
      await inboxDismiss({ inbox_id: item.id })
      toast.show({ kind: 'done', text: '已開始重跑' })
      await loadInbox()
    } catch (err) {
      toast.show({ kind: 'error', text: errText(err) })
    } finally {
      setBusy(false)
    }
  }
  const dismiss = async () => {
    setBusy(true)
    try {
      await inboxDismiss({ inbox_id: item.id })
      undoToast(`已略過 · ${item.title}`, item.id)
      await loadInbox()
    } catch (err) {
      toast.show({ kind: 'error', text: errText(err) })
    } finally {
      setBusy(false)
    }
  }
  return (
    <Card data-testid="inbox-item" data-kind={item.kind} layout="flex flex-wrap items-center gap-x-3 gap-y-2.5 py-3 pl-[18px] pr-3.5">
      <div className="flex min-w-0 flex-[999_1_300px] flex-col gap-1">
        <Head bot={bot} chip={chip} tone="warning" sub={item.title} />
        {item.body && <p className="m-0 pl-11 text-sub text-ink-2">{item.body}</p>}
      </div>
      {item.kind === 'routine_failed' && typeof (item.payload as { routine_id?: unknown } | null)?.routine_id === 'string' && (
        <Button icon={RotateCcw} onClick={() => void rerun()} loading={busy}>
          重跑一次
        </Button>
      )}
      <Button onClick={() => void dismiss()} loading={busy}>
        知道了
      </Button>
    </Card>
  )
}

/** Bot 請你接手電腦（B14：登入、驗證碼）：「接手電腦」直接到全視窗的電腦頁（E02）。 */
function TakeoverItem({ item, bot }: { item: InboxItem; bot: CardBot }) {
  const to = item.bot_id ? `/bots/${item.bot_id}/computer` : '/office'
  return (
    <Card data-testid="inbox-item" data-kind={item.kind} layout="flex flex-wrap items-center gap-x-3 gap-y-2.5 py-3 pl-[18px] pr-3.5">
      <div className="flex min-w-0 flex-[999_1_300px] flex-col gap-1">
        <Head bot={bot} chip="等你接手" tone="warning" sub={`${item.title} · ${sinceLabel(item.created_at)}`} />
        {item.body && <p className="m-0 pl-11 text-sub text-ink-2">{item.body}</p>}
      </div>
      <a href={to} onClick={linkClick(to)} className="inline-flex h-9 flex-none items-center gap-2 rounded-full bg-ink pl-3 pr-3.5 text-ui text-on-ink">
        <Icon icon={Hand} size={16} />
        接手電腦
      </a>
    </Card>
  )
}

/** 過期的提問（L14）：顯示已過期，可以請它再問一次。 */
function ExpiredItem({ item, bot }: { item: InboxItem; bot: CardBot }) {
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const again = async () => {
    setBusy(true)
    setError(null)
    try {
      await chatAskAgain({ inbox_id: item.id })
      await loadInbox()
    } catch (err) {
      setError(errText(err))
    } finally {
      setBusy(false)
    }
  }
  return (
    <Card data-testid="inbox-expired" layout="flex flex-col gap-2 py-3 pl-[18px] pr-3.5">
      <Head bot={bot} chip="已過期" tone="neutral" sub={`${item.title} · 10 分鐘沒回，${bot.name}先跳過了`} />
      <div className="flex flex-wrap items-center gap-3 rounded-menu bg-inset px-3.5 py-2.5">
        <span className="min-w-0 flex-1 text-ui-sm text-ink-2">{item.body}</span>
        <span className="inline-flex items-center gap-1 text-meta text-ink-3">
          <Icon icon={Clock} size={14} />
          已過期
        </span>
        <Button size="sm" icon={RotateCcw} onClick={() => void again()} loading={busy}>
          請它再問一次
        </Button>
      </div>
      {error && (
        <p role="alert" className="m-0 text-sub text-danger">
          {error}
        </p>
      )}
    </Card>
  )
}

/** Bot 的提案或討論（TM-5、H04）：卡片的 payload 帶 agenda_id、kind、due_at、subject。 */
function AgendaItem({ item, bot }: { item: InboxItem; bot: CardBot }) {
  const toast = useToast()
  const p = (item.payload ?? {}) as { agenda_id?: string; kind?: string; due_at?: number; subject?: string }
  const done: Record<string, string> = { accepted: '已同意', vetoed: '已否決', answered: '已回覆', declined: '不用討論了' }
  return (
    <div data-testid="inbox-item" data-kind={item.kind}>
      <AgendaCard
        item={{
          id: p.agenda_id ?? item.ref ?? '',
          kind: p.kind ?? (item.kind === 'proposal' ? 'propose' : 'discuss'),
          subject: p.subject ?? item.title,
          details: item.body,
          due_at: p.due_at ?? item.created_at,
          bot_name: bot.name,
        }}
        bot={bot}
        sub={sinceLabel(item.created_at)}
        onDone={(state) => {
          toast.show({ kind: 'done', text: `${done[state] ?? '已處理'} · ${p.subject ?? item.title}` })
          window.setTimeout(() => void loadInbox(), 1500)
        }}
      />
    </div>
  )
}

function RealItem({ item, bots }: { item: InboxItem; bots: Bot[] | null }) {
  const bot = cardBot(item, bots)
  if (item.kind === 'proposal' || item.kind === 'discussion') return <AgendaItem item={item} bot={bot} />
  if (item.task_id && (item.kind === 'approval' || item.kind === 'question' || item.kind === 'secret')) {
    const p = (item.payload ?? {}) as { thread_title?: string; unattended?: boolean }
    const sub = [bot.name, p.thread_title, sinceLabel(item.created_at)].filter(Boolean).join(' · ')
    return (
      <div data-testid="inbox-item" data-kind={item.kind}>
        <AskCard ask={askFromItem(item)} bot={bot} sub={sub} onDone={() => void loadInbox()} />
      </div>
    )
  }
  if (item.kind === 'approval' && item.ref) return <ApprovalItem item={item} bot={bot} />
  if (item.kind === 'takeover') return <TakeoverItem item={item} bot={bot} />
  return <InfoItem item={item} bot={bot} />
}

function ScreenThumb({ label, waiting, children }: { label: string; waiting?: boolean; children?: ReactNode }) {
  return (
    <span
      aria-hidden="true"
      className="flex h-[90px] items-center justify-center overflow-hidden rounded-field bg-[#F4F5F7] text-[11px] text-[#6B7280]"
      style={waiting ? { boxShadow: '0 0 0 1.5px #FF9800' } : undefined}
    >
      {children ?? label}
    </span>
  )
}

const WEEK = '日一二三四五六'

export function InboxPage() {
  const [health, setHealth] = useState<Health | null | undefined>(undefined)
  useEffect(() => {
    fetchHealth().then(setHealth)
  }, [])

  const { items, expired, delegated } = useStore(inboxStore)
  const bots = useStore(botsStore).bots
  const real = items ?? []
  const left = real.length
  const now = new Date()
  const today = `${now.getMonth() + 1} 月 ${now.getDate()} 日（${WEEK[now.getDay()]}）`

  return (
    <div className="flex flex-1 flex-wrap">
      <main className="min-w-0 flex-[999_1_560px] px-8 pb-2.5 pt-4 max-sm:px-4">
        <div className="mx-auto flex max-w-[780px] flex-col gap-2">
          <header className="flex flex-wrap items-end justify-between gap-x-6 gap-y-3 pb-1.5">
            <div className="flex flex-col gap-0.5">
              <div className="flex flex-wrap items-center gap-3">
                <h1 className="m-0 text-[24px] leading-[1.35]">{items === null ? '待我處理' : left > 0 ? `早安，有 ${left} 件事等你` : '早安，事情都處理完了'}</h1>
                <SimplifiedTag>簡化：核定 3 步 → 1 步</SimplifiedTag>
              </div>
              <span className="text-sub text-ink-2">{today} · 要你決定的事都在這裡，其他事 Bot 會自己處理</span>
            </div>
            <a href="/usage" className="flex w-[200px] flex-col gap-1.5 text-ink">
              <span className="flex justify-between text-meta text-ink-2">
                <span>本月花費</span>
                <span>$38 / $100</span>
              </span>
              <span className="block h-1 rounded-full bg-line">
                <span className="block h-1 w-[38%] rounded-full bg-accent" />
              </span>
            </a>
          </header>

          {real.map((it) => (
            <RealItem key={it.id} item={it} bots={bots} />
          ))}
          {items !== null && left === 0 && (
            <div className="flex flex-col items-center gap-2 rounded-card bg-card px-6 py-8 text-center" data-testid="inbox-empty">
              <Icon icon={Check} size={28} color="var(--color-success)" />
              <span className="text-body">都處理完了</span>
              <span className="text-sub text-ink-2">Bot 有事要你決定時，會出現在這裡。</span>
            </div>
          )}
          {!!delegated?.length && (
            <section aria-label="最近的決定" className="flex flex-col gap-2 pt-3">
              <h2 className="m-0 inline-flex items-center gap-1.5 text-sub text-ink-2">
                <Icon icon={ShieldCheck} size={15} />
                最近的決定 · 代為核准
              </h2>
              {delegated.map((it) => (
                <DelegatedItem key={it.id} item={it} />
              ))}
            </section>
          )}
          {!!expired?.length && (
            <section aria-label="過期的提問" className="flex flex-col gap-2 pt-3">
              <h2 className="m-0 text-sub text-ink-2">過期的提問</h2>
              {expired.map((it) => (
                <ExpiredItem key={it.id} item={it} bot={cardBot(it, bots)} />
              ))}
            </section>
          )}

          <p className="m-0 pt-2 text-center text-meta text-ink-3" data-testid="health">
            {health === undefined ? '連線中…' : health ? `服務正常 · ${health.version}` : '連不到服務'}
          </p>
        </div>
      </main>

      <aside aria-label="團隊現況" className="flex max-w-full flex-[1_1_340px] flex-col gap-6 border-l border-edge bg-panel px-5 py-[22px] max-[1179px]:border-l-0 max-[1179px]:border-t">
        <section className="flex flex-col gap-2.5">
          <div className="flex items-baseline justify-between">
            <h2 className="m-0 text-body">電腦</h2>
            <a href="/office" className="text-[13px]">
              打開辦公室
            </a>
          </div>
          <div className="grid grid-cols-2 gap-x-2.5 gap-y-3">
            {[
              { bot: 'xiaomi', state: 'working', note: '彙整價差表', thumb: '競品定價.xlsx' },
              { bot: 'azhe', state: 'searching', note: '比價第 3 家', thumb: '購物網站' },
              { bot: 'xiaobian', state: 'notifying', note: '等你接手', thumb: '輸入簡訊驗證碼', waiting: true },
              { bot: 'zhangfang', state: 'notifying', note: '等你允許', thumb: '帳單付款', waiting: true },
            ].map((c) => {
              const b = BOTS[c.bot]
              return (
                <a key={c.bot} href="/office" aria-label={`${b.name}的電腦：${c.thumb}`} className="flex flex-col gap-1.5 text-ink">
                  <ScreenThumb label={c.thumb} waiting={c.waiting} />
                  <span className="flex min-w-0 items-center gap-1.5 text-[13px]">
                    <Avatar shape={b.shape} color={b.color} state={c.state} dot={c.waiting ? 'waiting' : 'none'} size={18} />
                    {b.name}
                    <span className={c.waiting ? 'truncate text-warning-ink' : 'truncate text-ink-2'}>{c.note}</span>
                  </span>
                </a>
              )
            })}
          </div>
        </section>

        <section className="flex flex-col gap-2">
          <h2 className="m-0 text-body">Bot 之間</h2>
          {[
            { from: 'xiaomi', to: 'azhe', tag: '進行中 · 第 1/6 棒', tone: 'accent' as Tone, text: '請求：抓三家競品主力商品的價格' },
            { from: 'shujumiao', to: 'xiaomi', tag: '已回覆', tone: 'success' as Tone, text: '回覆：上週銷售摘要與 2 張圖（附檔）' },
            { from: 'xiaomi', to: 'zhangfang', tag: '等你允許', tone: 'warning' as Tone, text: '請求：結清 10 月廣告費' },
          ].map((h) => {
            const f = BOTS[h.from]
            const t = BOTS[h.to]
            return (
              <div key={h.text} className="flex flex-col gap-1 rounded-menu bg-inset px-3 py-2.5">
                <div className="flex items-center gap-1.5 text-sub">
                  <Avatar shape={f.shape} color={f.color} state="idle" dot="none" size={18} />
                  <span>{f.name}</span>
                  <Icon icon={ArrowRight} size={14} color="var(--color-ink-3)" />
                  <Avatar shape={t.shape} color={t.color} state="idle" dot="none" size={18} />
                  <span>{t.name}</span>
                  <StatusChip size="sm" tone={h.tone} className="ml-auto">
                    {h.tag}
                  </StatusChip>
                </div>
                <span className="text-[13px] text-ink-2">{h.text}</span>
              </div>
            )
          })}
        </section>

        <section className="flex flex-col gap-1.5">
          <h2 className="m-0 mb-0.5 text-body">今天完成</h2>
          {[
            ['10 月對帳單整理', '帳房'],
            ['新品貼文文案三版', '小編'],
            ['開發信草稿 3 封', '阿凱'],
            ['收件匣分類 24 封', '信差'],
          ].map(([t, who]) => (
            <div key={t} className="flex min-h-6 items-center gap-2 text-sub">
              <Icon icon={Check} size={16} strokeWidth={2} color="var(--color-success)" />
              <span className="flex-1">{t}</span>
              <span className="text-ink-3">{who}</span>
            </div>
          ))}
        </section>
      </aside>
    </div>
  )
}
