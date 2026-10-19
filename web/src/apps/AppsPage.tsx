import { useCallback, useEffect, useState } from 'react'
import { ArrowRight, ChevronRight, CircleCheck, KeyRound, Plug, RefreshCw, Search, Sparkles } from 'lucide-react'
import { appsCatalog, appsConnect, appsDisconnect, appsList, appsSignin, type AppsCatalogOutput } from '../api/generated'
import { botsStore } from '../data/bots'
import { useStore } from '../data/store'
import { linkClick, navigate } from '../router'
import { Button, IconButton } from '../ui/Button'
import { TextInput } from '../ui/Field'
import { Icon } from '../ui/Icon'
import { cx } from '../ui/cx'
import { useToast } from '../ui/toast-context'
import { errText } from '../settings/format'
import { AppSheet } from './AppSheet'
import { KeyDialog, SigninDialog, type Signin } from './SigninDialog'
import { AppIcon, Segmented } from './shared'
import { statusOf, toneText, whoCanUse, type CatalogApp, type Conn } from './appsData'


// I01：連接應用程式。已連接的放上面（含需要重新登入、授權過期），常用應用程式一鍵「連接」；只有一組篩選（全部／已連接）。
export function AppsPage() {
  const toast = useToast()
  const bots = (useStore(botsStore).bots ?? []).filter((b) => !b.archived)
  const [data, setData] = useState<AppsCatalogOutput | null>(null)
  const [conns, setConns] = useState<Conn[]>([])
  const [err, setErr] = useState<string | null>(null)
  const [q, setQ] = useState('')
  const [filter, setFilter] = useState<'all' | 'connected'>('all')
  const [showAll, setShowAll] = useState(false)
  const [advanced, setAdvanced] = useState(false)
  const [sheet, setSheet] = useState<CatalogApp | null>(null)
  const [signin, setSignin] = useState<{ app: CatalogApp; id: string; signin: Signin } | null>(null)
  const [keyFor, setKeyFor] = useState<{ app: CatalogApp; connectionId?: string } | null>(null)
  const [busy, setBusy] = useState<string | null>(null)

  const load = useCallback(() => {
    Promise.all([appsCatalog({}), appsList({})])
      .then(([c, l]) => {
        setData(c)
        setConns(l.connections)
        setErr(null)
      })
      .catch((e) => setErr(errText(e)))
  }, [])
  useEffect(load, [load])

  const origin = window.location.origin
  const connect = async (app: CatalogApp, connectionId?: string, extra: { api_key?: string; client_id?: string; client_secret?: string } = {}) => {
    if ((app.auth === 'api_key' && !extra.api_key) || (app.needs_client && !connectionId && !extra.client_id)) {
      setKeyFor({ app, connectionId })
      return
    }
    setBusy(app.id)
    try {
      const out =
        connectionId && app.auth === 'oauth2' && !extra.client_id
          ? await appsSignin({ id: connectionId, redirect_base: origin })
          : await appsConnect({ app_id: app.id, connection_id: connectionId, redirect_base: origin, ...extra })
      setKeyFor(null)
      if (out.signin) {
        setSignin({ app, id: out.connection.id, signin: out.signin })
        window.open(out.signin.auth_url, '_blank', 'noopener')
      } else if (out.connection.status === 'ok') toast.show({ kind: 'done', text: `已連接 ${app.title}，所有 Bot 都能用` })
      else toast.show({ kind: 'error', text: out.connection.error || `${app.title} ${statusOf(out.connection.status).text}` })
      load()
    } catch (e) {
      if (keyFor) throw e
      toast.show({ kind: 'error', text: errText(e) })
    } finally {
      setBusy(null)
    }
  }

  const all = data?.apps ?? []
  const words = q.trim().toLowerCase()
  const match = (a: CatalogApp) => !words || `${a.title} ${a.description} ${a.site} ${a.id}`.toLowerCase().includes(words)
  const connected = all.filter((a) => a.connections.length > 0 && match(a))
  const custom = conns.filter((c) => c.custom && (!words || `${c.title} ${c.name}`.toLowerCase().includes(words)))
  const available = all.filter((a) => a.connections.length === 0 && match(a))
  const popular = showAll || words ? available : available.filter((a) => a.featured).slice(0, 15)
  const count = (data?.connected ?? 0)
  const nothing = data && connected.length === 0 && custom.length === 0 && (filter === 'connected' || available.length === 0)

  return (
    <main className="flex min-w-0 flex-1 flex-col overflow-y-auto">
      <div className="mx-auto flex w-full max-w-[1100px] flex-col gap-5 px-7 py-6 max-sm:px-4">
        <header className="flex items-start gap-3">
          <div className="min-w-0 flex-1">
            <h1 className="m-0 text-page font-normal">連接應用程式</h1>
            <p className="m-0 mt-0.5 text-sub text-ink-2">點一下、登入，Bot 就能讀寫裡面的資料。工具預設全開，所有 Bot 都能用。</p>
          </div>
          <IconButton icon={RefreshCw} label="重新整理" onClick={load} />
        </header>
        <ol aria-label="步驟" className="m-0 flex list-none flex-wrap items-center gap-2 p-0 text-ui-sm text-ink-2">
          <li className="flex items-center gap-2">
            <StepDot n={1} />
            點應用程式
          </li>
          <Icon icon={ArrowRight} size={16} />
          <li className="flex items-center gap-2">
            <StepDot n={2} />
            登入
          </li>
          <Icon icon={ArrowRight} size={16} />
          <li className="flex items-center gap-1.5 text-success">
            <Icon icon={CircleCheck} size={16} />
            完成，不用再設定
          </li>
        </ol>
        <div className="flex flex-wrap items-center gap-3">
          <TextInput
            label="搜尋應用程式"
            hideLabel
            icon={Search}
            placeholder="搜尋應用程式，例如 Notion、GitHub、Linear"
            value={q}
            onChange={(e) => setQ(e.target.value)}
            wrapperClassName="min-w-[240px] flex-1"
          />
          <Segmented
            label="篩選"
            value={filter}
            onChange={setFilter}
            options={[
              { value: 'all', label: '全部' },
              { value: 'connected', label: `已連接 ${count}` },
            ]}
          />
          <Button variant="outline" icon={Plug} onClick={() => navigate('/apps/mcp')}>
            自訂連接（MCP）
          </Button>
        </div>
        {err && (
          <p role="alert" className="m-0 text-ui-sm text-danger">
            讀不到應用程式：{err}
          </p>
        )}

        {(connected.length > 0 || custom.length > 0) && (
          <section aria-label="已連接">
            <h2 className="m-0 mb-2 text-ui-sm font-normal text-ink-2">已連接 · {connected.length + custom.length}</h2>
            <div className="grid grid-cols-2 gap-2.5 max-md:grid-cols-1">
              {connected.map((a) => {
                const mine = conns.filter((c) => c.app_id === a.id)
                const head = mine[0]
                const s = statusOf(a.state)
                const bad = a.state === 'needs_signin' || a.state === 'expired' || a.state === 'error'
                return (
                  <div key={a.id} className="flex items-center gap-3 rounded-[14px] bg-card px-4 py-3">
                    <AppIcon name={a.icon} />
                    <button type="button" onClick={() => setSheet(a)} className="flex min-w-0 flex-1 cursor-pointer flex-col border-0 bg-transparent p-0 text-left text-ink">
                      <span className="text-name">{a.title}</span>
                      <span className={cx('text-sub', toneText[s.tone])}>
                        {s.text}
                        {mine.length > 1 ? ` · ${mine.length} 個帳號` : head?.account ? ` · ${head.account}` : ''}
                      </span>
                    </button>
                    {head && <span className="text-meta text-ink-2 max-sm:hidden">{whoCanUse(head, bots)}</span>}
                    {bad && head && (
                      <Button size="sm" loading={busy === a.id} onClick={() => void connect(a, head.id)}>
                        {a.state === 'needs_signin' ? '登入' : '重新連接'}
                      </Button>
                    )}
                    <IconButton icon={ChevronRight} label={`${a.title} 詳細`} onClick={() => setSheet(a)} />
                  </div>
                )
              })}
              {custom.map((c) => {
                const s = statusOf(c.status)
                return (
                  <a
                    key={c.id}
                    href={`/apps/mcp?id=${c.id}`}
                    onClick={linkClick(`/apps/mcp?id=${c.id}`)}
                    className="flex items-center gap-3 rounded-[14px] bg-card px-4 py-3 text-ink no-underline"
                  >
                    <AppIcon name={c.icon} />
                    <span className="flex min-w-0 flex-1 flex-col">
                      <span className="truncate font-mono text-ui">{c.title}</span>
                      <span className={cx('text-sub', toneText[s.tone])}>
                        {s.text} · 自訂連接
                      </span>
                    </span>
                    <span className="text-meta text-ink-2 max-sm:hidden">{whoCanUse(c, bots)}</span>
                    <Icon icon={ChevronRight} size={18} />
                  </a>
                )
              })}
            </div>
          </section>
        )}

        {filter === 'all' && popular.length > 0 && (
          <section aria-label={words ? '搜尋結果' : '常用的應用程式'}>
            <div className="mb-2 flex items-center justify-between">
              <h2 className="m-0 text-ui-sm font-normal text-ink-2">{words ? `搜尋結果 · ${available.length}` : '常用的應用程式'}</h2>
              {!words && !showAll && (
                <button type="button" onClick={() => setShowAll(true)} className="cursor-pointer border-0 bg-transparent p-0 text-ui-sm text-accent-ink">
                  看全部 {available.length} 個
                </button>
              )}
            </div>
            <div className="grid grid-cols-3 gap-2.5 max-lg:grid-cols-2 max-sm:grid-cols-1">
              {popular.map((a) => (
                <div key={a.id} className="flex items-center gap-3 rounded-[14px] border border-solid border-line px-3 py-3">
                  <AppIcon name={a.icon} size={40} />
                  <span className="flex min-w-0 flex-1 flex-col">
                    <span className="truncate text-ui">{a.title}</span>
                    <span className="truncate text-meta text-ink-2">{a.description}</span>
                  </span>
                  <Button size="sm" aria-label={`連接 ${a.title}`} loading={busy === a.id} icon={a.auth === 'api_key' ? KeyRound : undefined} onClick={() => void connect(a)}>
                    連接
                  </Button>
                </div>
              ))}
            </div>
          </section>
        )}

        {nothing && (
          <div className="flex flex-col items-center gap-2 rounded-[14px] bg-inset px-6 py-10 text-center">
            <span className="text-ink-3">
              <Icon icon={Sparkles} size={28} />
            </span>
            <p className="m-0 text-ui">{words ? `找不到「${q.trim()}」` : '還沒有連接任何應用程式'}</p>
            <p className="m-0 text-sub text-ink-2">清單外的服務，只要有 MCP 網址或啟動指令，就能用自訂連接接進來。</p>
            <Button variant="outline" icon={Plug} onClick={() => navigate('/apps/mcp')}>
              自訂連接（MCP）
            </Button>
          </div>
        )}

        <section aria-label="進階設定" className="rounded-[14px] border border-solid border-line">
          <button
            type="button"
            aria-expanded={advanced}
            onClick={() => setAdvanced(!advanced)}
            className="flex w-full cursor-pointer items-center gap-2 border-0 bg-transparent px-4 py-3.5 text-left text-ui text-ink"
          >
            <span className={cx('transition-transform', advanced && 'rotate-90')}>
              <Icon icon={ChevronRight} size={18} />
            </span>
            進階設定
            <span className="ml-auto text-sub text-ink-2 max-sm:hidden">新連接的工具全開 · 所有 Bot 可用 · 寄出、刪除前先問你</span>
          </button>
          {advanced && (
            <div className="flex flex-col gap-2 px-4 pb-4 text-sub text-ink-2">
              <p className="m-0">新連接的應用程式工具全開、所有 Bot 都能用；要只給某些 Bot 或關掉工具，點已連接的應用程式調整。</p>
              <p className="m-0">寄出、刪除、付款這類動作照每個 Bot 的權限，「先問我」的 Bot 一定會先問你。</p>
              <p className="m-0">
                登入權杖加密存在這台主機，Bot 用的時候由 XBot 代為轉送，不會進 Bot 的電腦。Bot 瀏覽器裡的網站登入在{' '}
                <a href="/apps/logins" onClick={linkClick('/apps/logins')}>
                  網站登入
                </a>
                。
              </p>
            </div>
          )}
        </section>
      </div>

      <AppSheet
        app={sheet}
        ids={sheet ? conns.filter((c) => c.app_id === sheet.id).map((c) => c.id) : []}
        onClose={() => setSheet(null)}
        onChanged={load}
        onReconnect={(id) => sheet && void connect(sheet, id)}
        onAddAccount={() => sheet && void connect(sheet)}
        onDisconnect={async (ids) => {
          try {
            for (const id of ids) await appsDisconnect({ id })
            toast.show({ kind: 'done', text: ids.length > 1 ? `已移除 ${sheet?.title}` : '已中斷這個帳號' })
            if (sheet && ids.length >= conns.filter((c) => c.app_id === sheet.id).length) setSheet(null)
            load()
          } catch (e) {
            toast.show({ kind: 'error', text: errText(e) })
          }
        }}
      />
      <SigninDialog
        title={signin?.app.title ?? ''}
        icon={signin?.app.icon}
        signin={signin?.signin ?? null}
        bots={bots}
        onBotSignin={async (botId) => {
          if (!signin) return
          const out = await appsSignin({ id: signin.id, redirect_base: origin, signin_on_bot: botId })
          if (out.signin) setSignin({ ...signin, signin: out.signin })
          toast.show({ kind: 'info', text: `登入頁已開在${bots.find((b) => b.id === botId)?.name ?? 'Bot'}的電腦上，到「待我處理」接手登入` })
        }}
        onClose={() => {
          setSignin(null)
          load()
        }}
        onDone={() => {
          toast.show({ kind: 'done', text: `已連接 ${signin?.app.title}，所有 Bot 都能用` })
          setSignin(null)
          load()
        }}
      />
      <KeyDialog
        open={!!keyFor}
        title={keyFor?.app.title ?? ''}
        kind={keyFor?.app.auth === 'api_key' ? 'api_key' : 'client'}
        hint={keyFor?.app.token_hint ?? keyFor?.app.client_hint}
        link={keyFor?.app.token_url}
        onClose={() => setKeyFor(null)}
        onSubmit={async (a, b) => {
          if (!keyFor) return
          if (keyFor.app.auth === 'api_key') await connect(keyFor.app, keyFor.connectionId, { api_key: a })
          else await connect(keyFor.app, keyFor.connectionId, { client_id: a, client_secret: b || undefined })
        }}
      />
    </main>
  )
}

function StepDot({ n }: { n: number }) {
  return (
    <span aria-hidden="true" className="inline-flex size-6 items-center justify-center rounded-full bg-control text-meta text-ink">
      {n}
    </span>
  )
}
