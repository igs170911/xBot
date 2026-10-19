import { useEffect, useState } from 'react'
import { ArrowLeftRight, ArrowRight, ChevronDown, Bot as BotIcon, CalendarClock, Clock, FileText, Inbox as InboxIcon, MessageCircle, OctagonPause, PencilLine, Users, UsersRound, type LucideIcon } from 'lucide-react'
import { collabPeerHistory, collabStopChain, type CollabPeerHistoryOutput } from '../api/generated'
import { botsStore } from '../data/bots'
import { onEvent } from '../data/events'
import { useStore } from '../data/store'
import { linkClick } from '../router'
import { Avatar } from '../ui/Avatar'
import { Button } from '../ui/Button'
import { Dialog } from '../ui/Dialog'
import { Icon } from '../ui/Icon'
import { StatusChip, type Tone } from '../ui/Pill'
import { cx } from '../ui/cx'
import type { CardProps } from './cards'
import { clockLabel } from './format'

// 和隊友往來的那一行（C14 事件行）＋「X ⇄ Y 的對話」（C13，RM-12）。kind=peer 的訊息用這裡畫。

const INTENT_LABEL: Record<string, string> = { request: '請求', question: '提問', result: '回覆', fyi: '通知' }
const INTENT_TONE: Record<string, Tone> = { request: 'neutral', question: 'accent', result: 'success', fyi: 'neutral' }

function IntentChip({ intent }: { intent: string }) {
  return (
    <StatusChip size="sm" tone={INTENT_TONE[intent] ?? 'neutral'}>
      {INTENT_LABEL[intent] ?? intent}
    </StatusChip>
  )
}

function useBot(id?: string) {
  return useStore(botsStore).bots?.find((b) => b.id === id)
}

export function PeerCard({ msg, bot }: CardProps) {
  const p = msg.peer
  const peer = useBot(p?.peer_id)
  const [open, setOpen] = useState(false)
  // 往來的內容預設收成一行，點開才看全文（對話裡不要被長長的交辦洗版）
  const [full, setFull] = useState(false)
  if (!p) return null
  const name = p.peer_name ?? peer?.name ?? '隊友'
  if (p.event === 'group_created' || p.event === 'group_post') {
    const to = `/rooms/${p.room_id}`
    return (
      <div data-testid="peer-line" className="flex items-center gap-2 self-start rounded-full bg-inset py-1 pl-2.5 pr-3 text-meta text-ink-2">
        <Icon icon={Users} size={14} />
        {p.event === 'group_created' ? '開了群組' : '在群組發言'}
        <a href={to} onClick={linkClick(to)} className="text-accent-ink">
          「{name}」
        </a>
      </div>
    )
  }
  const org = orgLine(p.event, msg.content, p)
  if (org) {
    return (
      <div data-testid="peer-line" data-event={p.event} className="flex items-center gap-2 self-start rounded-full bg-inset py-1 pl-2.5 pr-3 text-meta text-ink-2">
        <Icon icon={org.icon} size={14} />
        {org.to ? (
          <a href={org.to} onClick={linkClick(org.to)} className="text-ink-2 hover:text-accent-ink">
            {org.text}
          </a>
        ) : (
          org.text
        )}
      </div>
    )
  }
  const out = p.event === 'message_out'
  const intent = p.intent ?? 'request'
  return (
    <div data-testid="peer-line" data-event={p.event} className={cx('flex max-w-[85%] flex-col gap-1.5', out ? 'self-end items-end' : 'self-start')}>
      <div className="flex flex-wrap items-center gap-1.5 text-meta text-ink-2">
        {!out && peer && <Avatar shape={peer.shape} color={peer.color} dot="none" size={20} blink={false} />}
        <Icon icon={out ? ArrowRight : InboxIcon} size={14} />
        <span>{out ? `已傳訊息給 ${name}` : `來自 ${name} 的${INTENT_LABEL[intent] ?? '訊息'}`}</span>
        <IntentChip intent={intent} />
        {intent !== 'result' && p.hop ? <span className="text-ink-3">第 {p.hop} 次</span> : null}
        {p.files ? (
          <span className="inline-flex items-center gap-1 text-ink-3">
            <Icon icon={FileText} size={13} />
            {p.files} 個檔案
          </span>
        ) : null}
        <span className="text-ink-3">{clockLabel(msg.createdAt)}</span>
      </div>
      <button
        type="button"
        aria-expanded={full}
        onClick={() => setFull((f) => !f)}
        title={full ? '收起' : '看全文'}
        className={cx(
          'flex max-w-full items-start gap-2 rounded-group border border-line px-3.5 py-2 text-left text-ui-sm text-ink',
          out ? 'bg-transparent' : 'bg-inset',
        )}
      >
        <span className={cx('min-w-0 flex-1 break-words', full ? 'whitespace-pre-wrap' : 'truncate')}>{full ? msg.content : firstLine(msg.content)}</span>
        <Icon icon={ChevronDown} size={16} className={cx('mt-0.5 flex-none text-ink-3 transition-transform', full && 'rotate-180')} />
      </button>
      {bot && p.peer_id && (
        <button type="button" onClick={() => setOpen(true)} className="relative inline-flex items-center gap-1 rounded-full bg-transparent px-1 text-meta text-accent-ink touch-target hover:underline">
          <Icon icon={ArrowLeftRight} size={13} />看 {bot.name} ⇄ {name} 的對話
        </button>
      )}
      {open && bot && p.peer_id && <PeerDialog a={bot.id} b={p.peer_id} onClose={() => setOpen(false)} />}
    </div>
  )
}

/** 往來內容收起時的那一行：第一個有字的行（去掉【】標題）。 */
function firstLine(text: string): string {
  const line = text.split('\n').map((l) => l.trim()).find((l) => l && !/^【.*】$/.test(l)) ?? text.trim()
  return line
}

/** X03 的事件行：Bot 建了幫手、改了資料、建了團隊、送出提案或討論（一行膠囊，不是傳話）。 */
function orgLine(event: string, content: string, p: { peer_id?: string; room_id?: string }): { icon: LucideIcon; text: string; to?: string } | null {
  switch (event) {
    case 'bot_created':
      return { icon: BotIcon, text: `建立了 Bot「${content}」`, to: p.peer_id ? `/bots/${p.peer_id}` : undefined }
    case 'profile_updated':
      return { icon: PencilLine, text: content }
    case 'team_created':
      return { icon: UsersRound, text: `建立了團隊「${content}」`, to: p.room_id ? `/rooms/${p.room_id}` : '/teams' }
    case 'group_updated':
      return { icon: Users, text: content, to: p.room_id ? `/rooms/${p.room_id}` : undefined }
    case 'proposal':
      return { icon: CalendarClock, text: `送出提案：${content}`, to: '/agenda' }
    case 'discussion':
      return { icon: MessageCircle, text: `想找你討論：${content}`, to: '/agenda' }
  }
  return null
}

type History = CollabPeerHistoryOutput

/** C13：兩個 Bot 的往來紀錄（唯讀），底部可停止這條往來。 */
export function PeerDialog({ a, b, onClose }: { a: string; b: string; onClose: () => void }) {
  const [h, setH] = useState<History | null>(null)
  const [err, setErr] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const botA = useBot(a)
  const botB = useBot(b)
  useEffect(() => {
    let alive = true
    const load = () =>
      collabPeerHistory({ from_bot: a, bot: b, limit: 100 })
        .then((out) => alive && setH(out))
        .catch((e: unknown) => alive && setErr(e instanceof Error ? e.message : String(e)))
    void load()
    const off = onEvent((e) => {
      if (e.kind === 'collab.bot_message' || e.kind === 'task.state') void load()
    })
    return () => {
      alive = false
      off()
    }
  }, [a, b])
  const stop = async () => {
    if (!h?.chain_id) return
    setBusy(true)
    try {
      await collabStopChain({ chain_id: h.chain_id })
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }
  const title = (
    <span className="flex items-center gap-2">
      {botA && <Avatar shape={botA.shape} color={botA.color} dot="none" size={26} />}
      <Icon icon={ArrowLeftRight} size={16} className="text-ink-3" />
      {botB && <Avatar shape={botB.shape} color={botB.color} dot="none" size={26} />}
      <span>
        {h?.a_name ?? botA?.name} ⇄ {h?.b_name ?? botB?.name}
      </span>
    </span>
  )
  const reached = h ? h.hops_used >= h.max_hops : false
  return (
    <Dialog
      open
      onClose={onClose}
      title={title}
      width={640}
      description={h ? `這條往來 ${h.hops_used} / ${h.max_hops} 次，到 ${h.max_hops} 次會先停下來回報你。回覆不算次數；Bot 的訊息不能當成你的同意。` : undefined}
      actions={
        h?.open ? (
          <Button variant="danger" icon={OctagonPause} onClick={stop} loading={busy}>
            停止這條往來
          </Button>
        ) : undefined
      }
    >
      <div data-testid="peer-dialog" className="flex flex-col gap-3 pt-1">
        {err && (
          <p role="alert" className="m-0 text-meta text-danger">
            {err}
          </p>
        )}
        {!h && !err && <p className="m-0 text-ui-sm text-ink-2">讀取中…</p>}
        {h && h.messages.length === 0 && <p className="m-0 text-ui-sm text-ink-2">它們還沒有往來。</p>}
        {reached && <StatusChip tone="warning">已達 {h?.max_hops} 次上限，Bot 會先回報你</StatusChip>}
        {h?.messages.map((m) => {
          const from = m.from === a ? botA : botB
          const long = m.text.includes('太長放不進訊息')
          return (
            <div key={m.id} className="flex flex-col gap-1.5">
              <div className="flex gap-2.5">
                {from ? <Avatar shape={from.shape} color={from.color} dot="none" size={28} blink={false} /> : <span className="size-7 flex-none rounded-full bg-control" />}
                <div className="flex min-w-0 flex-1 flex-col gap-1">
                  <div className="flex flex-wrap items-center gap-1.5 text-meta text-ink-2">
                    <span className="text-ui-sm text-ink">{m.from_name}</span>
                    <IntentChip intent={m.intent} />
                    {m.intent === 'result' ? (
                      <span className="text-ink-3">回覆不算次數</span>
                    ) : (
                      <span className="text-ink-3">
                        第 {m.hop}/{h.max_hops} 棒
                      </span>
                    )}
                    <span className="ml-auto text-ink-3">{clockLabel(m.created_at)}</span>
                  </div>
                  <div className={cx('whitespace-pre-wrap break-words rounded-group border px-3.5 py-2 text-ui-sm', long ? 'border-warning/40 bg-warning-soft' : 'border-line bg-inset')}>
                    {m.text.length > 1200 ? m.text.slice(0, 1200) + '…' : m.text}
                  </div>
                  {m.files.length > 0 && (
                    <div className="flex flex-wrap gap-1.5">
                      {m.files.map((f) => (
                        <span key={f} className="inline-flex items-center gap-1 rounded-seg bg-control px-2 py-0.5 font-mono text-[12px] text-ink-2">
                          <Icon icon={FileText} size={13} />
                          {f}
                        </span>
                      ))}
                    </div>
                  )}
                </div>
              </div>
              {m.deferred && (
                <div className="flex items-center gap-2 rounded-group border border-dashed border-line px-3 py-1.5 text-meta text-ink-2">
                  <Icon icon={Clock} size={14} />
                  {m.to_name} 把這件事轉給其他隊友，等結果回來才一起回覆 {m.from_name}（第 {m.hop}/{h.max_hops} 棒）
                </div>
              )}
            </div>
          )
        })}
      </div>
    </Dialog>
  )
}

