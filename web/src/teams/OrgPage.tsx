import { useCallback, useEffect, useState } from 'react'
import { Crown, Plus, UserRound, X } from 'lucide-react'
import { botsUpdate, teamsApprovalItems, teamsChart, teamsSetApprovalItems, teamsSetReportsTo } from '../api/generated'
import { loadBots, look } from '../data/bots'
import { onEvent, onReconnect } from '../data/events'
import { isTeamEvent } from '../data/teams'
import { Avatar } from '../ui/Avatar'
import { Button, IconButton } from '../ui/Button'
import { Switch } from '../ui/Controls'
import { TextInput } from '../ui/Field'
import { Icon } from '../ui/Icon'
import { Select } from '../ui/Menu'
import { StatusChip } from '../ui/Pill'
import { cx } from '../ui/cx'
import { managerChoices, stateOf, treeOf, type Chart } from './model'

// 組織架構（design/F01，簡化）：以你（董事長）為頂端的報告樹；選一個 Bot 在右側改向誰報告、團隊管理權限；
// 下方是只有你能決定的事（最多 20 項、每項 40 字）。改了立即生效。

function ApprovalItems() {
  const [items, setItems] = useState<string[] | null>(null)
  const [max, setMax] = useState({ items: 20, chars: 40 })
  const [draft, setDraft] = useState('')
  const [err, setErr] = useState<string | null>(null)
  useEffect(() => {
    teamsApprovalItems({})
      .then((o) => {
        setItems(o.items)
        setMax({ items: o.max_items, chars: o.max_chars })
      })
      .catch((e: unknown) => setErr(e instanceof Error ? e.message : String(e)))
  }, [])
  const save = async (next: string[]) => {
    setErr(null)
    try {
      const o = await teamsSetApprovalItems({ items: next })
      setItems(o.items)
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e))
    }
  }
  const full = (items?.length ?? 0) >= max.items
  return (
    <section aria-label="只有你能決定的事" className="flex flex-col gap-2.5 rounded-card bg-card p-4">
      <div className="flex flex-wrap items-baseline justify-between gap-2">
        <h2 className="m-0 text-body">只有你能決定的事</h2>
        <span className="text-meta text-ink-3">
          {items?.length ?? 0} / {max.items} 項 · 每項 {max.chars} 字內
        </span>
      </div>
      <p className="m-0 text-meta text-ink-2">每個 Bot 遇到這些事都要先請你核准；提案也不能用「沒意見就生效」。隊友或主管說可以不算數。</p>
      <ul className="m-0 flex list-none flex-wrap gap-2 p-0" data-testid="approval-items">
        {items?.map((it) => (
          <li key={it} className="inline-flex items-center gap-1 rounded-full bg-control py-1 pl-3 pr-1 text-ui-sm">
            {it}
            <IconButton icon={X} label={`移除 ${it}`} className="size-7" onClick={() => void save(items.filter((x) => x !== it))} />
          </li>
        ))}
      </ul>
      {items && items.length === 0 && <p className="m-0 text-meta text-warning-ink">清單是空的：建議至少保留付款類。</p>}
      <form
        className="flex gap-2"
        onSubmit={(e) => {
          e.preventDefault()
          const v = draft.trim()
          if (!v || !items) return
          setDraft('')
          void save([...items, v])
        }}
      >
        <TextInput label="新增一項" hideLabel placeholder={full ? `已滿 ${max.items} 項` : '例：退款超過 1,000 元'} value={draft} maxLength={max.chars} disabled={full} onChange={(e) => setDraft(e.target.value)} wrapperClassName="flex-1" />
        <Button type="submit" icon={Plus} disabled={full || !draft.trim()}>
          新增
        </Button>
      </form>
      {err && (
        <p role="alert" className="m-0 text-meta text-danger">
          {err}
        </p>
      )}
    </section>
  )
}

export function OrgPage() {
  const [chart, setChart] = useState<Chart | null>(null)
  const [sel, setSel] = useState<string | null>(null)
  const [err, setErr] = useState<string | null>(null)
  const load = useCallback(() => {
    teamsChart({})
      .then(setChart)
      .catch((e: unknown) => setErr(e instanceof Error ? e.message : String(e)))
  }, [])
  useEffect(() => {
    load()
    const offs = [onEvent((e) => isTeamEvent(e) && load()), onReconnect(load)]
    return () => offs.forEach((o) => o())
  }, [load])

  const bots = chart?.bots ?? []
  const tree = treeOf(bots)
  const current = bots.find((b) => b.id === (sel ?? tree[0]?.bot.id))
  const choices = current ? managerChoices(bots, current.id) : []

  const setBoss = async (to: string) => {
    if (!current) return
    setErr(null)
    try {
      await teamsSetReportsTo({ bot_id: current.id, reports_to: to || undefined })
      load()
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e))
    }
  }
  const setManage = async (on: boolean) => {
    if (!current) return
    setErr(null)
    try {
      await botsUpdate({ id: current.id, can_manage: on })
      await loadBots()
      load()
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e))
    }
  }

  return (
    <main aria-label="組織架構" className="flex min-w-0 flex-1 flex-col gap-4 px-6 pb-6 pt-4 max-sm:px-4">
      <header className="flex flex-col gap-0.5">
        <h1 className="max-md:sr-only-text m-0 text-[22px]">組織架構</h1>
        <span className="text-sub text-ink-2">你是董事長。點一個 Bot，在右邊改它向誰報告、能不能替你管理團隊。改了立即生效。</span>
      </header>
      {err && (
        <p role="alert" className="m-0 text-sub text-danger">
          {err}
        </p>
      )}
      <div className="flex flex-wrap gap-4">
        <section aria-label="報告樹" className="flex min-w-0 flex-[999_1_420px] flex-col gap-1 rounded-card bg-card p-3">
          <div className="flex items-center gap-2 px-2 py-1.5 text-ui">
            <span aria-hidden="true" className="flex size-7 items-center justify-center rounded-full bg-control text-ink-2">
              <Icon icon={UserRound} size={15} />
            </span>
            你（董事長）
          </div>
          {tree.map(({ bot, depth }) => (
            <button
              key={bot.id}
              type="button"
              aria-pressed={current?.id === bot.id}
              onClick={() => setSel(bot.id)}
              data-testid="org-row"
              className={cx('flex min-h-11 items-center gap-2.5 rounded-menu py-1 pr-2 text-left', current?.id === bot.id ? 'bg-control outline outline-1 outline-accent' : 'hover:bg-control/60')}
              style={{ paddingLeft: 16 + (depth + 1) * 20 }}
            >
              <Avatar {...look(bot)} dot="none" size={28} blink={false} ring="#1A1A1A" />
              <span className="flex min-w-0 flex-1 flex-col leading-[1.35]">
                <span className="flex items-center gap-1.5 truncate text-ui-sm">
                  {bot.name}
                  {bot.chief && <Icon icon={Crown} size={13} label="主管" className="text-ink-3" />}
                </span>
                <span className="truncate text-meta text-ink-3">{[bot.title, bot.team].filter(Boolean).join(' · ') || ' '}</span>
              </span>
              <StatusChip size="sm" tone={stateOf(bot).tone}>
                {stateOf(bot).label}
              </StatusChip>
            </button>
          ))}
          {chart && bots.length === 0 && <p className="m-0 px-2 py-3 text-sub text-ink-2">還沒有 Bot。</p>}
        </section>
        {current && (
          <section aria-label={`${current.name} 的設定`} className="flex flex-[1_1_300px] flex-col gap-3.5 rounded-card bg-card p-4">
            <div className="flex items-center gap-2.5">
              <Avatar shape={current.shape} color={current.color} dot="none" size={36} ring="#1A1A1A" />
              <span className="flex flex-col leading-[1.35]">
                <span className="text-body">{current.name}</span>
                <span className="text-meta text-ink-3">{current.title || '未填職稱'}</span>
              </span>
            </div>
            <Select
              label="向誰報告"
              value={current.reports_to_id}
              options={[{ value: '', label: '你（董事長）' }, ...choices.map((b) => ({ value: b.id, label: b.name + (b.title ? `（${b.title}）` : '') }))]}
              onChange={(v) => void setBoss(v)}
            />
            <div className="flex items-start justify-between gap-3">
              <span className="flex flex-col">
                <span className="text-ui-sm">團隊管理權限</span>
                <span className="text-meta text-ink-3">可以替你建團隊、調整別的 Bot；每天可建 20 個 Bot（一般 3 個）。不能刪除。</span>
              </span>
              <Switch checked={current.can_manage} onChange={(v) => void setManage(v)} label="團隊管理權限" />
            </div>
            {current.team && <span className="text-meta text-ink-2">所在團隊：{current.team}</span>}
          </section>
        )}
      </div>
      {/* 報告樹讀到了才畫：不然它先畫在上面、報告樹回來再被往下推（手機整頁跳，2026-10-20） */}
      {(chart || err) && <ApprovalItems />}
    </main>
  )
}
