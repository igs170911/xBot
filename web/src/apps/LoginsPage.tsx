import { useEffect, useState } from 'react'
import { ArrowLeft, CircleCheck, Globe, History, ShieldCheck, X } from 'lucide-react'
import { loginsForget, loginsList, loginsShare, loginsUndoShare, type LoginsListOutput } from '../api/generated'
import { botsStore } from '../data/bots'
import { useStore } from '../data/store'
import { navigate, useSearch } from '../router'
import { Avatar } from '../ui/Avatar'
import { Button, IconButton } from '../ui/Button'
import { Checkbox } from '../ui/Controls'
import { ConfirmDialog, Dialog } from '../ui/Dialog'
import { Icon } from '../ui/Icon'
import { Select } from '../ui/Menu'
import { cx } from '../ui/cx'
import { useToast } from '../ui/toast-context'
import { errText } from '../settings/format'

type Site = LoginsListOutput['sites'][number]

const LETTER_COLORS = ['#ff6b35', '#1084fe', '#16a34a', '#0ea5a4', '#8b5cf6', '#e11d48', '#5a5a5a']

function SiteBadge({ site }: { site: string }) {
  const n = [...site].reduce((a, c) => a + c.charCodeAt(0), 0)
  return (
    <span aria-hidden="true" className="inline-flex size-[26px] flex-none items-center justify-center rounded-full text-meta text-white" style={{ background: LETTER_COLORS[n % LETTER_COLORS.length] }}>
      {site[0]?.toUpperCase()}
    </span>
  )
}

// I05（C34 範圍：Bot 瀏覽器登入了哪些網站，LazyBoy logins.py）＋ E09 分享登入給其他 Bot。
// 帳密保險庫、1Password（SK-7）是之後的卡片。
export function LoginsPage() {
  const toast = useToast()
  const search = useSearch()
  const bots = (useStore(botsStore).bots ?? []).filter((b) => !b.archived)
  const botId = search.get('bot') ?? bots[0]?.id ?? ''
  const bot = bots.find((b) => b.id === botId)
  const [got, setGot] = useState<{ bot: string; tick: number; sites: Site[] | null; err: string | null } | null>(null)
  const [tick, setTick] = useState(0)
  const [sharing, setSharing] = useState<string[] | null>(null)
  const [forgetting, setForgetting] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    if (!botId) return
    let stop = false
    loginsList({ bot_id: botId })
      .then((out) => !stop && setGot({ bot: botId, tick, sites: out.sites, err: null }))
      .catch((e) => !stop && setGot({ bot: botId, tick, sites: null, err: errText(e) }))
    return () => {
      stop = true
    }
  }, [botId, tick])
  const load = () => setTick((n) => n + 1)
  const fresh = got && got.bot === botId && got.tick === tick ? got : null
  const sites = fresh?.sites ?? null
  const err = fresh?.err ?? null
  const loading = !!botId && !fresh

  return (
    <main className="flex min-w-0 flex-1 flex-col overflow-y-auto">
      <div className="mx-auto flex w-full max-w-[1100px] flex-col gap-4 px-7 py-6 max-sm:px-4">
        <header className="flex items-start gap-3">
          <Button variant="ghost" icon={ArrowLeft} onClick={() => navigate('/apps')} aria-label="回到連接應用程式" />
          <div className="min-w-0 flex-1">
            <h1 className="m-0 text-page font-normal">網站登入</h1>
            <p className="m-0 mt-0.5 text-sub text-ink-2">每個 Bot 的瀏覽器登入彼此獨立。登入資料只留在 Bot 的電腦裡，這裡只看得到網站名稱。</p>
          </div>
        </header>
        <div className="flex gap-5 max-md:flex-col">
          <section aria-label="登入的網站" className="flex min-w-0 flex-1 flex-col gap-3 rounded-[18px] bg-card p-4">
            <div className="flex flex-wrap items-center gap-3">
              <Select
                label="看哪個 Bot"
                hideLabel
                value={botId}
                options={bots.map((b) => ({ value: b.id, label: `${b.name}的瀏覽器` }))}
                onChange={(v) => navigate(`/apps/logins?bot=${v}`, { replace: true })}
                className="w-[220px]"
              />
              <span className="ml-auto" />
              <Button disabled={!sites?.length} onClick={() => setSharing([])}>
                分享登入
              </Button>
            </div>
            <table className="w-full border-collapse text-left">
              <thead>
                <tr className="text-meta text-ink-2">
                  <th className="py-2 font-normal">網站</th>
                  <th className="py-2 font-normal">登入資料</th>
                  <th className="py-2 font-normal">
                    <span className="sr-only">動作</span>
                  </th>
                </tr>
              </thead>
              <tbody>
                {(sites ?? []).map((s) => (
                  <tr key={s.site} className="border-0 border-t border-solid border-divider">
                    <td className="py-3">
                      <span className="flex items-center gap-2.5">
                        <SiteBadge site={s.site} />
                        <span className="font-mono text-ui">{s.site}</span>
                      </span>
                    </td>
                    <td className="py-3 text-ui-sm text-ink-2">{s.cookies} 筆</td>
                    <td className="py-3">
                      <span className="flex justify-end gap-2">
                        <Button size="sm" onClick={() => setSharing([s.site])}>
                          分享
                        </Button>
                        <Button size="sm" variant="danger" onClick={() => setForgetting(s.site)}>
                          移除
                        </Button>
                      </span>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
            {loading && <p className="m-0 text-sub text-ink-2" role="status">正在讀{bot?.name ?? ''}的瀏覽器…（電腦沒開機會先開機）</p>}
            {err && (
              <p role="alert" className="m-0 text-ui-sm text-danger">
                {err}
              </p>
            )}
            {sites && sites.length === 0 && (
              <div className="flex flex-col items-center gap-2 px-6 py-8 text-center">
                <span className="text-ink-3">
                  <Icon icon={Globe} size={28} />
                </span>
                <p className="m-0 text-ui">{bot?.name}的瀏覽器還沒有登入任何網站</p>
                <p className="m-0 text-sub text-ink-2">打開它的電腦登入一次，之後就會一直記得。</p>
              </div>
            )}
          </section>
          <aside className="flex w-[300px] flex-none flex-col gap-3 rounded-[18px] bg-inset p-4 text-sub text-ink-2 max-md:w-auto">
            <p className="m-0 flex gap-2">
              <span className="text-success">
                <Icon icon={ShieldCheck} size={18} />
              </span>
              密碼不會被看到，對話與紀錄也不會出現登入資料。
            </p>
            <p className="m-0 flex gap-2">
              <Icon icon={History} size={18} />
              分享後 10 分鐘內可以復原。
            </p>
          </aside>
        </div>
      </div>
      {sharing && bot && (
        <ShareDialog
          from={bot}
          sites={sites ?? []}
          initial={sharing}
          bots={bots.filter((b) => b.id !== bot.id)}
          onClose={() => setSharing(null)}
        />
      )}
      <ConfirmDialog
        open={!!forgetting}
        danger
        busy={busy}
        title={`移除 ${forgetting} 的登入？`}
        description={`${bot?.name ?? 'Bot'}下次打開這個網站要重新登入。其他 Bot 的登入不受影響。`}
        confirmText="移除"
        onCancel={() => setForgetting(null)}
        onConfirm={() => {
          if (!forgetting) return
          setBusy(true)
          loginsForget({ bot_id: botId, sites: [forgetting] })
            .then(() => {
              toast.show({ kind: 'done', text: `已移除 ${forgetting} 的登入` })
              load()
            })
            .catch((e) => toast.show({ kind: 'error', text: errText(e) }))
            .finally(() => {
              setBusy(false)
              setForgetting(null)
            })
        }}
      />
    </main>
  )
}

type B = { id: string; name: string; title: string; shape: string; color: string }

// E09：把一個 Bot 瀏覽器裡選定網站的登入狀態複製給其他 Bot。
function ShareDialog({ from, sites, initial, bots, onClose }: { from: B; sites: Site[]; initial: string[]; bots: B[]; onClose: () => void }) {
  const [picked, setPicked] = useState<Set<string>>(new Set(initial))
  const [to, setTo] = useState<Set<string>>(new Set())
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string | null>(null)
  const [done, setDone] = useState<{ id: string; text: string; undone?: boolean } | null>(null)
  const flip = (s: Set<string>, k: string, on: boolean) => {
    const n = new Set(s)
    if (on) n.add(k)
    else n.delete(k)
    return n
  }
  const share = async () => {
    setBusy(true)
    setErr(null)
    try {
      const out = await loginsShare({ from_bot_id: from.id, to_bot_ids: [...to], sites: [...picked] })
      const names = out.targets.map((id) => bots.find((b) => b.id === id)?.name ?? 'Bot').join('、')
      setDone({ id: out.share_id, text: `已把 ${out.sites.length} 個網站的登入分享給${names}` })
    } catch (e) {
      setErr(errText(e))
    } finally {
      setBusy(false)
    }
  }
  const undo = async () => {
    if (!done) return
    setBusy(true)
    try {
      await loginsUndoShare({ share_id: done.id })
      setDone({ ...done, text: '已復原，對方換回原本的登入', undone: true })
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
      width={560}
      title={
        <span className="flex items-center gap-3">
          <Avatar shape={from.shape} color={from.color} size={32} />
          分享{from.name}的登入
        </span>
      }
      description={`把${from.name}瀏覽器裡的登入複製給其他 Bot，它們打開同一個網站就不用再登入。密碼不會被看到，只會帶走登入狀態；對方原本的登入會被取代。`}
      actions={
        <div className="flex w-full items-center gap-3">
          <span className="text-sub text-ink-2">
            {picked.size} 個網站 → {to.size} 個 Bot
          </span>
          <span className="ml-auto" />
          <Button onClick={onClose}>{done ? '完成' : '取消'}</Button>
          {!done && (
            <Button variant="primary" loading={busy} disabled={!picked.size || !to.size} onClick={() => void share()}>
              分享登入
            </Button>
          )}
        </div>
      }
    >
      <div className="flex flex-col gap-4">
        <section aria-label="要分享的網站" className="flex flex-col gap-2">
          <h3 className="m-0 text-ui-sm font-normal text-ink-2">要分享的網站</h3>
          <div className="rounded-[14px] bg-card">
            {sites.map((s, i) => (
              <Checkbox
                key={s.site}
                checked={picked.has(s.site)}
                onChange={(v) => setPicked(flip(picked, s.site, v))}
                note={`${s.cookies} 筆`}
                className={cx('px-3.5 py-3', i > 0 && 'border-0 border-t border-solid border-divider')}
              >
                <span className="flex items-center gap-2.5">
                  <SiteBadge site={s.site} />
                  <span className="font-mono">{s.site}</span>
                </span>
              </Checkbox>
            ))}
          </div>
        </section>
        <section aria-label="分享給" className="flex flex-col gap-2">
          <h3 className="m-0 text-ui-sm font-normal text-ink-2">分享給</h3>
          <div className="grid grid-cols-2 gap-2 max-sm:grid-cols-1">
            {bots.map((b) => (
              <Checkbox
                key={b.id}
                checked={to.has(b.id)}
                onChange={(v) => setTo(flip(to, b.id, v))}
                className={cx('rounded-[12px] px-3 py-2.5', to.has(b.id) ? 'bg-control' : 'bg-card')}
              >
                <span className="flex items-center gap-2.5">
                  <Avatar shape={b.shape} color={b.color} size={30} />
                  <span className="flex flex-col">
                    <span>{b.name}</span>
                    {b.title && <span className="text-meta text-ink-2">{b.title}</span>}
                  </span>
                </span>
              </Checkbox>
            ))}
          </div>
        </section>
        {err && (
          <p role="alert" className="m-0 text-ui-sm text-danger">
            {err}
          </p>
        )}
        {done && (
          <div role="status" className="flex items-center gap-3 rounded-[14px] bg-card px-4 py-3">
            <span className="text-success">
              <Icon icon={CircleCheck} size={20} />
            </span>
            <span className="flex-1 text-ui-sm">{done.text}</span>
            {!done.undone && (
              <Button size="sm" onClick={() => void undo()}>
                復原
              </Button>
            )}
            <IconButton icon={X} label="關閉提示" onClick={() => setDone(null)} />
          </div>
        )}
      </div>
    </Dialog>
  )
}
