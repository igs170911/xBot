import { useCallback, useEffect, useState } from 'react'
import { ChevronRight, CircleAlert, CircleCheck, Plus } from 'lucide-react'
import { appsGet, appsGrantToBot, appsRevokeFromBot, appsUpdate, type AppsGetOutput } from '../api/generated'
import { botsStore } from '../data/bots'
import { useStore } from '../data/store'
import { Avatar } from '../ui/Avatar'
import { Button } from '../ui/Button'
import { Checkbox, Switch } from '../ui/Controls'
import { ConfirmDialog, Dialog } from '../ui/Dialog'
import { Icon } from '../ui/Icon'
import { cx } from '../ui/cx'
import { useToast } from '../ui/toast-context'
import { errText } from '../settings/format'
import { AppIcon, Segmented } from './shared'
import { statusOf, toneText, when, type CatalogApp } from './appsData'

type Detail = AppsGetOutput

// I02：已連接應用程式的詳細。帳號（重新連接／中斷）、誰能用、最近使用、進階（工具，預設全開）。變更自動儲存。
export function AppSheet({
  app,
  ids,
  onClose,
  onReconnect,
  onAddAccount,
  onDisconnect,
  onChanged,
}: {
  app: CatalogApp | null
  ids: string[]
  onClose: () => void
  onReconnect: (id: string) => void
  onAddAccount: () => void
  onDisconnect: (ids: string[]) => Promise<void>
  onChanged: () => void
}) {
  const toast = useToast()
  const bots = (useStore(botsStore).bots ?? []).filter((b) => !b.archived)
  const [accounts, setAccounts] = useState<Detail[]>([])
  const [advanced, setAdvanced] = useState(false)
  const [removing, setRemoving] = useState<string[] | null>(null)
  const [busy, setBusy] = useState(false)
  const key = ids.join(',')

  const load = useCallback(() => {
    if (!key) return
    Promise.all(key.split(',').map((id) => appsGet({ id })))
      .then(setAccounts)
      .catch((e) => toast.show({ kind: 'error', text: errText(e) }))
  }, [key, toast])
  useEffect(load, [load])

  const first = accounts[0]
  const all = accounts.every((a) => a.all_bots)
  const granted = new Set(accounts.flatMap((a) => a.bot_ids))
  const uses = accounts
    .flatMap((a) => a.uses ?? [])
    .sort((x, y) => y.created_at - x.created_at)
    .slice(0, 6)
  const tools = first?.tools ?? []
  const off = new Set(first?.disabled_tools ?? [])

  const save = async (p: Promise<unknown>[]) => {
    try {
      await Promise.all(p)
      load()
      onChanged()
    } catch (e) {
      toast.show({ kind: 'error', text: errText(e) })
    }
  }
  const setAll = (v: 'all' | 'some') => {
    if (v === 'all') void save(accounts.map((a) => appsGrantToBot({ id: a.id, all_bots: true })))
    else void save(accounts.map((a) => appsRevokeFromBot({ id: a.id, all: true })))
  }
  const toggleBot = (botId: string, on: boolean) =>
    void save(accounts.map((a) => (on ? appsGrantToBot({ id: a.id, bot_id: botId }) : appsRevokeFromBot({ id: a.id, bot_id: botId }))))
  const toggleTool = (name: string, on: boolean) => {
    const next = new Set(off)
    if (on) next.delete(name)
    else next.add(name)
    void save(accounts.map((a) => appsUpdate({ id: a.id, disabled_tools: [...next], reset_tools: next.size === 0 })))
  }

  const status = first ? statusOf(first.status) : null
  return (
    <>
      <Dialog
        open={!!app && ids.length > 0}
        onClose={onClose}
        width={640}
        title={
          <span className="flex items-center gap-3">
            <AppIcon name={app?.icon} size={44} />
            <span className="flex min-w-0 flex-col">
              <span>{app?.title}</span>
              <span className="text-sub text-ink-2">
                {status && <span className={toneText[status.tone]}>{status.tone === 'success' ? '已連接' : status.text}</span>} · {app?.description}
              </span>
            </span>
          </span>
        }
        actions={
          <div className="flex w-full items-center gap-3">
            <Button variant="danger" onClick={() => setRemoving(ids)}>
              移除 {app?.title}
            </Button>
            <span className="ml-auto text-sub text-ink-2">變更會自動儲存</span>
            <Button variant="primary" onClick={onClose}>
              完成
            </Button>
          </div>
        }
      >
        <div className="flex flex-col gap-5">
          <section aria-label="帳號">
            <div className="mb-2 flex items-center justify-between">
              <h3 className="m-0 text-name font-normal">帳號</h3>
              <Button size="sm" variant="outline" icon={Plus} onClick={onAddAccount}>
                加一個帳號
              </Button>
            </div>
            <ul className="m-0 list-none rounded-[14px] bg-card p-0">
              {accounts.map((a, i) => {
                const s = statusOf(a.status)
                const ok = a.status === 'ok'
                return (
                  <li key={a.id} className={cx('flex items-center gap-3 px-4 py-3', i > 0 && 'border-0 border-t border-solid border-divider')}>
                    <span className={toneText[s.tone]}>
                      <Icon icon={ok ? CircleCheck : CircleAlert} size={20} />
                    </span>
                    <span className="flex min-w-0 flex-1 flex-col">
                      <span className="truncate text-ui">
                        {a.account || a.name}
                        {a.secret_hint && <span className="ml-2 font-mono text-meta text-ink-2">···{a.secret_hint}</span>}
                      </span>
                      <span className={cx('text-meta', ok ? 'text-ink-2' : toneText[s.tone])}>
                        {ok ? `可以使用${i === 0 ? '・預設帳號' : ''}・上次使用 ${when(a.last_used_at)}` : `${s.text}${a.error ? `・${a.error}` : ''}・重新連接後才能用`}
                      </span>
                    </span>
                    <Button size="sm" onClick={() => onReconnect(a.id)}>
                      重新連接
                    </Button>
                    <Button size="sm" variant="danger" onClick={() => setRemoving([a.id])}>
                      中斷
                    </Button>
                  </li>
                )
              })}
            </ul>
          </section>

          <section aria-label="誰能用" className="flex flex-col gap-2">
            <h3 className="m-0 text-name font-normal">誰能用</h3>
            <Segmented
              label="誰能用"
              value={all ? 'all' : 'some'}
              onChange={setAll}
              className="w-full"
              options={[
                { value: 'all', label: '所有 Bot' },
                { value: 'some', label: '只有指定的 Bot' },
              ]}
            />
            {!all && (
              <div className="grid grid-cols-2 gap-2 max-sm:grid-cols-1">
                {bots.map((b) => (
                  <Checkbox key={b.id} checked={granted.has(b.id)} onChange={(v) => toggleBot(b.id, v)} className="rounded-[12px] bg-card px-3 py-2">
                    <span className="flex items-center gap-2">
                      <Avatar shape={b.shape} color={b.color} size={24} />
                      {b.name}
                    </span>
                  </Checkbox>
                ))}
              </div>
            )}
            <p className="m-0 text-sub text-ink-2">寄出、刪除這類動作照每個 Bot 的權限；「先問我」的 Bot 一定會先問你。</p>
          </section>

          <section aria-label="最近使用" className="flex flex-col gap-1">
            <h3 className="m-0 mb-1 text-name font-normal">最近使用</h3>
            {uses.length === 0 && <p className="m-0 text-sub text-ink-2">還沒有 Bot 用過。</p>}
            {uses.map((u) => {
              const b = bots.find((x) => x.id === u.bot_id)
              return (
                <div key={u.id} className="flex items-center gap-3 border-0 border-b border-solid border-divider py-2 last:border-b-0">
                  {b ? <Avatar shape={b.shape} color={b.color} size={22} /> : <span className="size-[22px]" />}
                  <span className="min-w-0 flex-1 truncate text-ui-sm">
                    {b?.name ?? 'Bot'} 用了 <span className="font-mono text-meta">{u.tool}</span>
                    {!u.ok && <span className="text-danger">（失敗）</span>}
                  </span>
                  <span className="text-meta text-ink-2">{when(u.created_at)}</span>
                </div>
              )
            })}
          </section>

          <section aria-label="進階設定" className="rounded-[14px] border border-solid border-line">
            <button
              type="button"
              aria-expanded={advanced}
              onClick={() => setAdvanced(!advanced)}
              className="flex w-full cursor-pointer items-center gap-2 border-0 bg-transparent px-4 py-3 text-left text-ui text-ink"
            >
              <span className={cx('transition-transform', advanced && 'rotate-90')}>
                <Icon icon={ChevronRight} size={18} />
              </span>
              進階設定
              <span className="ml-auto text-sub text-ink-2">
                {tools.length ? `${tools.length} 個工具${off.size ? `・關了 ${off.size} 個` : '全開'}` : '工具全開'}・結果上限 25,000 字
              </span>
            </button>
            {advanced && (
              <div className="flex flex-col gap-2 px-4 pb-4">
                {tools.length === 0 && <p className="m-0 text-sub text-ink-2">Bot 第一次用的時候會列出工具。</p>}
                {tools.map((t) => (
                  <Switch key={t.name} checked={!off.has(t.name)} onChange={(v) => toggleTool(t.name, v)} label={t.name}>
                    <span className="flex flex-col">
                      <span className="font-mono text-meta">{t.name}</span>
                      {t.description && <span className="text-meta text-ink-2">{t.description}</span>}
                    </span>
                  </Switch>
                ))}
              </div>
            )}
          </section>
        </div>
      </Dialog>
      <ConfirmDialog
        open={!!removing}
        danger
        busy={busy}
        title={removing && removing.length === ids.length ? `移除 ${app?.title}？` : '中斷這個帳號？'}
        description={
          removing && removing.length === ids.length
            ? '登入資料會刪掉，所有 Bot 都不能再用。之後要用得重新連接。'
            : '這個帳號的登入資料會刪掉，Bot 不能再用它。'
        }
        confirmText={removing && removing.length === ids.length ? '移除' : '中斷'}
        onCancel={() => setRemoving(null)}
        onConfirm={async () => {
          if (!removing) return
          setBusy(true)
          try {
            await onDisconnect(removing)
            setRemoving(null)
          } finally {
            setBusy(false)
          }
        }}
      />
    </>
  )
}
