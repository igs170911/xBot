import { useCallback, useEffect, useState } from 'react'
import { Crown, Network, Trash2, UsersRound } from 'lucide-react'
import { teamsChart, teamsDelete } from '../api/generated'
import { onEvent, onReconnect } from '../data/events'
import { isTeamEvent, loadTeams, openNewTeam } from '../data/teams'
import { linkClick } from '../router'
import { Avatar } from '../ui/Avatar'
import { Button, IconButton } from '../ui/Button'
import { ConfirmDialog } from '../ui/Dialog'
import { Icon } from '../ui/Icon'
import { StatusChip } from '../ui/Pill'
import { groupsOf, stateOf, type Chart, type ChartBot } from './model'
import { look } from '../data/bots'

// 團隊地圖（design/F04，簡化）：每個團隊一個框，Bot 節點有頭像、職稱、狀態膠囊、模型；
// 主管戴皇冠，不是向主管報告的成員註明向誰報告。資料來自 xbot.teams.chart。
// 交棒清單與電腦列在辦公室（之後的卡）；拖曳換分區先用組織架構頁改。

function Node({ bot, chiefId }: { bot: ChartBot; chiefId?: string }) {
  const st = stateOf(bot)
  const chief = bot.id === chiefId
  const line = !chief && bot.reports_to_id && bot.reports_to_id !== chiefId ? `向 ${bot.reports_to} 報告` : !chief && !bot.reports_to_id && chiefId ? '直接向你報告' : ''
  return (
    <a
      href={`/bots/${bot.id}`}
      onClick={linkClick(`/bots/${bot.id}`)}
      data-testid="map-node"
      className="flex w-[150px] flex-col gap-1 rounded-group border border-line bg-card px-3 py-2.5 text-ink hover:bg-control/60 max-sm:w-full"
    >
      <span className="flex items-center gap-2">
        <Avatar {...look(bot)} state={st.avatar} dot={st.tone === 'warning' ? 'waiting' : 'none'} size={26} ring="#1A1A1A" />
        <span className="min-w-0 flex-1 truncate text-ui-sm">{bot.name}</span>
        {chief && <Icon icon={Crown} size={14} label="主管" className="text-ink-3" />}
      </span>
      <span className="truncate text-meta text-ink-3">{bot.title || ' '}</span>
      <span className="flex items-center gap-1.5">
        <StatusChip size="sm" tone={st.tone}>
          {st.label}
        </StatusChip>
        <span className="truncate text-[12px] text-ink-3">{bot.model || '預設模型'}</span>
      </span>
      {line && <span className="truncate text-[12px] text-ink-3">{line}</span>}
    </a>
  )
}

export function TeamMapPage() {
  const [chart, setChart] = useState<Chart | null>(null)
  const [err, setErr] = useState<string | null>(null)
  const [removing, setRemoving] = useState<{ id: string; name: string } | null>(null)
  const [busy, setBusy] = useState(false)
  const load = useCallback(() => {
    teamsChart({})
      .then((c) => {
        setChart(c)
        setErr(null)
      })
      .catch((e: unknown) => setErr(e instanceof Error ? e.message : String(e)))
  }, [])
  useEffect(() => {
    load()
    const offs = [
      onEvent((e) => {
        if (isTeamEvent(e) || e.kind === 'task.state' || (e.kind === 'op' && String((e.payload as { op?: string })?.op ?? '').startsWith('xbot.bots.'))) load()
      }),
      onReconnect(load),
    ]
    return () => offs.forEach((o) => o())
  }, [load])

  const remove = async () => {
    if (!removing) return
    setBusy(true)
    try {
      await teamsDelete({ id: removing.id })
      setRemoving(null)
      await loadTeams()
      load()
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e))
      setRemoving(null)
    } finally {
      setBusy(false)
    }
  }

  const g = chart ? groupsOf(chart) : null
  return (
    <main aria-label="團隊地圖" className="flex min-w-0 flex-1 flex-col gap-3.5 px-5 pb-5 max-sm:px-4">
      <header className="flex min-h-16 flex-wrap items-center gap-2.5">
        <div className="flex min-w-[200px] flex-1 flex-col leading-[1.4]">
          <h1 className="max-md:sr-only-text m-0 text-[18px]">團隊地圖</h1>
          <span className="text-[13px] text-ink-2">{chart ? `${chart.bots.length} 個 Bot · ${chart.teams.length} 個團隊。每個團隊含主管最多 ${chart.team_max} 位。` : '讀取中…'}</span>
        </div>
        <Button variant="secondary" icon={UsersRound} onClick={openNewTeam}>
          建立團隊
        </Button>
        <a href="/org" onClick={linkClick('/org')} className="inline-flex h-10 items-center gap-1.5 rounded-full border border-line px-3.5 text-ui-sm text-ink hover:bg-control/60">
          <Icon icon={Network} size={16} />
          組織架構
        </a>
      </header>
      {err && (
        <p role="alert" className="m-0 text-sub text-danger">
          {err}
        </p>
      )}
      <section aria-label="地圖" className="flex flex-col gap-3 rounded-card border border-line p-4 max-sm:p-2.5" style={{ backgroundImage: 'radial-gradient(#ffffff14 1px, transparent 1px)', backgroundSize: '16px 16px' }}>
        <p className="m-0 flex flex-wrap gap-x-4 text-meta text-ink-3">
          <span>藍色＝工作中</span>
          <span>橘色＝等你</span>
          <span>灰色＝排隊或待命</span>
        </p>
        <div className="flex flex-wrap gap-3">
          {g?.groups.map((t) => (
            <div key={t.id} id={t.id} role="group" aria-label={t.name} data-testid="map-team" className="flex max-w-full flex-col gap-2.5 rounded-group border border-line bg-panel/90 p-3">
              <div className="flex items-center gap-2">
                <span className="text-body">{t.name}</span>
                <span className="text-meta text-ink-3">{t.bots.length} 位 · 各自的電腦</span>
                <IconButton icon={Trash2} label={`刪除團隊 ${t.name}`} className="ml-auto size-8" onClick={() => setRemoving({ id: t.id, name: t.name })} />
              </div>
              {t.team.about && <span className="text-meta text-ink-2">{t.team.about}</span>}
              <div className="flex flex-wrap gap-2.5">
                {t.bots.map((b) => (
                  <Node key={b.id} bot={b} chiefId={t.team.chief_id} />
                ))}
                {t.bots.length === 0 && <span className="text-meta text-ink-3">還沒有成員</span>}
              </div>
            </div>
          ))}
          {g && g.rest.length > 0 && (
            <div role="group" aria-label="未分區" data-testid="map-team" className="flex max-w-full flex-col gap-2.5 rounded-group border border-dashed border-line p-3">
              <span className="text-body text-ink-2">未分區 · {g.rest.length} 位</span>
              <div className="flex flex-wrap gap-2.5">
                {g.rest.map((b) => (
                  <Node key={b.id} bot={b} />
                ))}
              </div>
            </div>
          )}
          {chart && chart.bots.length === 0 && (
            <div className="flex w-full flex-col items-center gap-2 py-10 text-center text-sub text-ink-2">
              還沒有 Bot。用範本一次建好一個有主管的團隊。
              <Button variant="primary" icon={UsersRound} onClick={openNewTeam}>
                建立團隊
              </Button>
            </div>
          )}
        </div>
      </section>
      <ConfirmDialog
        open={!!removing}
        title={`刪除團隊「${removing?.name ?? ''}」？`}
        description="成員回到未分區，Bot、報告線、群組和它們的資料都會保留；團隊記憶會一起刪除。"
        confirmText="刪除團隊"
        danger
        busy={busy}
        onConfirm={() => void remove()}
        onCancel={() => setRemoving(null)}
      />
    </main>
  )
}
