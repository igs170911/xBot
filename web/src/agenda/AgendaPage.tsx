import { useEffect } from 'react'
import { ChevronRight, Inbox, Info } from 'lucide-react'
import { agendaStore, loadAgenda, resultLabel, startAgenda, whenLabel } from '../data/agenda'
import { botsStore } from '../data/bots'
import { useStore } from '../data/store'
import { linkClick } from '../router'
import { Icon } from '../ui/Icon'
import { AgendaCard } from './AgendaCard'

// 等你的事：提案與討論（design/H04，簡化）。左邊是等你的提案與討論、最近 7 天處理過的；
// 右邊是接下來一週的行程。定時任務、每週檢討與學習、行事曆訂閱（ICS）在 C32 接上。

function sinceLabel(ms: number, now = Date.now()): string {
  const min = Math.round((now - ms) / 60_000)
  if (min < 60) return min < 1 ? '剛剛' : `${min} 分鐘前`
  const h = Math.round(min / 60)
  return h < 24 ? `${h} 小時前` : `${Math.round(h / 24)} 天前`
}

export function AgendaPage() {
  const { data, error } = useStore(agendaStore)
  const bots = useStore(botsStore).bots
  useEffect(() => {
    startAgenda()
    void loadAgenda()
  }, [])
  const bot = (id: string) => bots?.find((b) => b.id === id)
  const pending = data?.pending ?? []
  const approvals = data?.approvals.length ?? 0
  return (
    <div className="grid flex-1 grid-cols-[minmax(0,1fr)_340px] content-start items-start gap-5 px-6 pb-6 pt-4 max-[1100px]:grid-cols-1 max-sm:px-4">
      <main aria-label="等你的事" className="flex min-w-0 flex-col gap-3">
        <header className="flex flex-col gap-0.5">
          <h1 className="max-md:sr-only-text m-0 flex items-center gap-2 text-[22px]">
            <Icon icon={Inbox} size={22} color="var(--color-accent)" />
            等你的事
          </h1>
          <span className="text-sub text-ink-2">Bot 想請你決定或聊一下的事。提案你沒意見，時間到就自動生效。</span>
        </header>
        <div className="flex flex-wrap items-center gap-2 rounded-card bg-card px-4 py-2.5 text-ui-sm text-ink-2">
          <Icon icon={Info} size={16} />
          <span className="flex-1">每個 Bot 提案＋討論最多 {data?.open_max ?? 3} 件，待你核定另計最多 10 件。</span>
          {approvals > 0 && (
            <a href="/" onClick={linkClick('/')} className="inline-flex items-center gap-1 rounded-full bg-warning-soft px-3 py-1 text-meta text-warning-ink">
              {approvals} 件待你核定
              <Icon icon={ChevronRight} size={14} />
            </a>
          )}
        </div>
        {error && !data && (
          <p role="alert" className="m-0 text-sub text-danger">
            讀不到：{error}
          </p>
        )}
        {pending.map((it) => (
          <AgendaCard key={it.id} item={it} bot={bot(it.bot_id)} sub={sinceLabel(it.created_at)} />
        ))}
        {data && pending.length === 0 && (
          <p className="m-0 rounded-card bg-card px-6 py-8 text-center text-sub text-ink-2" data-testid="agenda-empty">
            沒有等你的提案或討論。Bot 有把握的事會先提案，你沒意見就照做。
          </p>
        )}
        {!!data?.decided.length && (
          <section aria-label="最近 7 天處理過" className="flex flex-col pt-2">
            <h2 className="m-0 pb-1.5 text-sub text-ink-2">最近 7 天處理過</h2>
            {data.decided.map((it) => (
              <div key={it.id} className="flex items-center gap-3 border-t border-line px-2 py-2.5 text-ui-sm" data-testid="agenda-decided">
                <span className="min-w-0 flex-1 truncate">{it.subject}</span>
                <span className={it.state === 'accepted' || it.state === 'auto' ? 'text-success' : 'text-ink-2'}>{resultLabel(it)}</span>
                <span className="w-12 text-right text-meta text-ink-3">{it.decided_at ? `${new Date(it.decided_at).getMonth() + 1}/${new Date(it.decided_at).getDate()}` : ''}</span>
              </div>
            ))}
          </section>
        )}
      </main>
      <aside aria-label="接下來一週" className="flex min-w-0 flex-col gap-3">
        <section className="flex flex-col gap-2 rounded-card bg-card p-4">
          <h2 className="m-0 text-body">接下來一週</h2>
          {data?.calendar.length ? (
            data.calendar.map((c) => (
              <div key={c.id} className="flex items-baseline gap-2.5 text-ui-sm" data-testid="agenda-calendar">
                <span className="w-[120px] flex-none text-meta text-ink-3">{whenLabel(c.at)}</span>
                <span className="min-w-0 flex-1 truncate">
                  {c.kind === 'propose' ? `${c.bot_name} 的提案生效：` : `與 ${c.bot_name} 討論：`}
                  {c.title}
                </span>
              </div>
            ))
          ) : (
            <p className="m-0 text-meta text-ink-3">這一週沒有排定的事。</p>
          )}
        </section>
        <section className="flex flex-col gap-1.5 rounded-card bg-card p-4">
          <h2 className="m-0 text-body">定時任務與學習</h2>
          <p className="m-0 text-meta text-ink-3">
            定時任務在「
            <a href="/calendar/routines" onClick={linkClick('/calendar/routines')}>
              定時任務
            </a>
            」；每週檢討與學習、行事曆訂閱網址還沒開放。
          </p>
        </section>
      </aside>
    </div>
  )
}
