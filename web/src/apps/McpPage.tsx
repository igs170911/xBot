import { useCallback, useEffect, useState } from 'react'
import { ArrowLeft, Check, CircleCheck, Globe, Info, Plug, Plus, Terminal } from 'lucide-react'
import { appsAddMcpServer, appsGrantToBot, appsList, appsRevokeFromBot, appsTest, appsUpdate, type AppsAddMcpServerInput } from '../api/generated'
import { botsStore } from '../data/bots'
import { useStore } from '../data/store'
import { navigate, useSearch } from '../router'
import { Button } from '../ui/Button'
import { Switch } from '../ui/Controls'
import { TextInput, Textarea } from '../ui/Field'
import { Icon } from '../ui/Icon'
import { Select } from '../ui/Menu'
import { cx } from '../ui/cx'
import { useToast } from '../ui/toast-context'
import { errText } from '../settings/format'
import { SigninDialog, type Signin } from './SigninDialog'
import { Segmented } from './shared'
import { statusOf, toneText, whoCanUse, when, type Conn } from './appsData'

type Draft = {
  id?: string
  name: string
  enabled: boolean
  who: string // all 或 bot id
  transport: 'http' | 'stdio'
  url: string
  command: string
  args: string
  auth: 'none' | 'api_key' | 'oauth2'
  token: string
  hint: string
  headers: string
  headersTouched: boolean
  env: string
  envTouched: boolean
}

const empty: Draft = {
  name: '', enabled: true, who: 'all', transport: 'http', url: '', command: '', args: '', auth: 'none', token: '', hint: '',
  headers: '', headersTouched: false, env: '', envTouched: false,
}

function toDraft(c: Conn): Draft {
  return {
    id: c.id, name: c.name, enabled: c.enabled, who: c.all_bots ? 'all' : (c.bot_ids[0] ?? 'all'), transport: c.transport,
    url: c.url, command: c.command, args: c.args.join(' '), auth: c.auth, token: '', hint: c.secret_hint,
    headers: c.header_names.map((h) => `${h}: `).join('\n'), headersTouched: false,
    env: c.env_names.map((k) => `${k}=`).join('\n'), envTouched: false,
  }
}

function parseLines(text: string, sep: string): Record<string, string> {
  const out: Record<string, string> = {}
  for (const line of text.split('\n')) {
    const i = line.indexOf(sep)
    if (i <= 0) continue
    out[line.slice(0, i).trim()] = line.slice(i + 1).trim()
  }
  return out
}

function splitArgs(s: string): string[] {
  return (s.match(/"[^"]*"|'[^']*'|\S+/g) ?? []).map((a) => a.replace(/^["']|["']$/g, ''))
}

// I04：自訂連接（MCP）。清單＋右側編輯器（名稱、給誰用、連線方式、網址、驗證、標頭、工具、進階）。
export function McpPage() {
  const toast = useToast()
  const search = useSearch()
  const bots = (useStore(botsStore).bots ?? []).filter((b) => !b.archived)
  const [list, setList] = useState<Conn[] | null>(null)
  const [d, setD] = useState<Draft>(empty)
  const [busy, setBusy] = useState(false)
  const [test, setTest] = useState<{ ok: boolean; text: string } | null>(null)
  const [signin, setSignin] = useState<Signin | null>(null)
  const selected = search.get('id')

  const load = useCallback(() => {
    appsList({})
      .then((out) => setList(out.connections.filter((c) => c.custom)))
      .catch((e) => toast.show({ kind: 'error', text: errText(e) }))
  }, [toast])
  useEffect(load, [load])
  // 換選取的項目（或清單第一次讀到）時換掉編輯中的內容。
  const want = `${selected ?? ''}|${list ? 1 : 0}`
  const [seen, setSeen] = useState('')
  if (seen !== want) {
    setSeen(want)
    const c = list?.find((x) => x.id === selected)
    setD(c ? toDraft(c) : empty)
    setTest(null)
  }

  const cur = list?.find((x) => x.id === d.id)
  const set = (p: Partial<Draft>) => setD((x) => ({ ...x, ...p }))

  const save = async () => {
    setBusy(true)
    try {
      const headers = d.headersTouched ? parseLines(d.headers, ':') : undefined
      const env = d.envTouched || !d.id ? parseLines(d.env, '=') : undefined
      if (!d.id) {
        const input: AppsAddMcpServerInput = {
          name: d.name, transport: d.transport, disabled: !d.enabled, redirect_base: window.location.origin,
          bot_ids: d.who === 'all' ? undefined : [d.who],
        }
        if (d.transport === 'http') Object.assign(input, { url: d.url, auth: d.auth, token: d.token || undefined, headers })
        else Object.assign(input, { command: d.command, args: splitArgs(d.args), env })
        const out = await appsAddMcpServer(input)
        if (out.signin) {
          setSignin(out.signin)
          window.open(out.signin.auth_url, '_blank', 'noopener')
        } else toast.show({ kind: 'done', text: `已加入 ${d.name}` })
        navigate(`/apps/mcp?id=${out.connection.id}`, { replace: true })
      } else {
        await appsUpdate({
          id: d.id, enabled: d.enabled, token: d.token || undefined, headers, env,
          ...(d.transport === 'http' ? { url: d.url } : { command: d.command, args: splitArgs(d.args) }),
        })
        if (cur) {
          const wasAll = cur.all_bots
          if (d.who === 'all' && !wasAll) await appsGrantToBot({ id: d.id, all_bots: true })
          else if (d.who !== 'all' && (wasAll || cur.bot_ids.join() !== d.who)) {
            await appsRevokeFromBot({ id: d.id, all: true })
            await appsGrantToBot({ id: d.id, bot_id: d.who })
          }
        }
        toast.show({ kind: 'done', text: '已儲存' })
      }
      load()
    } catch (e) {
      toast.show({ kind: 'error', text: errText(e) })
    } finally {
      setBusy(false)
    }
  }

  const runTest = async () => {
    if (!d.id) return
    const t0 = performance.now()
    try {
      const c = await appsTest({ id: d.id })
      const s = statusOf(c.status)
      setTest(c.status === 'ok' ? { ok: true, text: `連線成功 · ${((performance.now() - t0) / 1000).toFixed(1)} 秒` } : { ok: false, text: c.error || s.text })
      load()
    } catch (e) {
      setTest({ ok: false, text: errText(e) })
    }
  }

  const whoOptions = [{ value: 'all', label: '所有 Bot' }, ...bots.map((b) => ({ value: b.id, label: `只給${b.name}` }))]

  return (
    <main className="flex min-w-0 flex-1 flex-col overflow-y-auto">
      <div className="mx-auto flex w-full max-w-[1100px] flex-col gap-4 px-7 py-6 max-sm:px-4 max-sm:pt-2">
        <header className="flex items-start gap-3">
          <Button variant="ghost" icon={ArrowLeft} onClick={() => navigate('/apps')} aria-label="回到連接應用程式" className="max-md:hidden" />
          <div className="min-w-0 flex-1">
            <h1 className="max-md:sr-only-text m-0 text-page font-normal">自訂連接（MCP）</h1>
            <p className="m-0 mt-0.5 text-sub text-ink-2">不在清單上的服務，只要有 MCP 網址或啟動指令就能加進來。</p>
          </div>
        </header>
        <div className="flex gap-5 max-md:flex-col">
          <aside aria-label="已加入的自訂連接" className="flex w-[380px] flex-none flex-col gap-2.5 max-md:w-auto">
            <h2 className="m-0 text-ui-sm font-normal text-ink-2">已加入 · {list?.length ?? 0}</h2>
            {(list ?? []).map((c) => {
              const s = statusOf(c.status)
              const on = c.id === d.id
              return (
                <button
                  key={c.id}
                  type="button"
                  aria-current={on ? 'true' : undefined}
                  onClick={() => navigate(`/apps/mcp?id=${c.id}`)}
                  className={cx(
                    'flex cursor-pointer flex-col gap-1 rounded-[14px] border border-solid bg-card px-4 py-3 text-left text-ink',
                    on ? 'border-accent' : 'border-transparent',
                  )}
                >
                  <span className="flex items-center gap-2">
                    <span className="min-w-0 flex-1 truncate font-mono text-ui">{c.name}</span>
                    <span className="rounded-full bg-control px-2 py-0.5 text-meta text-ink-2">{c.all_bots ? '所有 Bot' : whoCanUse(c, bots)}</span>
                  </span>
                  <span className={cx('text-sub', toneText[s.tone])}>
                    {c.missing_env.length
                      ? `還缺 ${c.missing_env.join('、')}，補上就能啟用`
                      : `${s.text}${c.tools.length ? ` · ${c.tools.length} 個工具` : ''}${c.checked_at ? ` · ${when(c.checked_at)}測試` : ''}${c.transport === 'stdio' ? ' · 本機程式' : ''}`}
                  </span>
                </button>
              )
            })}
            <Button variant="outline" icon={Plus} onClick={() => navigate('/apps/mcp')}>
              新增自訂連接
            </Button>
            <p className="m-0 flex gap-2 text-meta text-ink-2">
              <Icon icon={Info} size={16} />
              不知道怎麼填？跟提供服務的人要「MCP 網址」或啟動指令，例如 npx -y @acme/mcp-server。只加你信任的服務：本機程式會在 Bot 的電腦上執行。
            </p>
          </aside>

          <section aria-label="編輯自訂連接" className="flex min-w-0 flex-1 flex-col gap-4 rounded-[18px] bg-inset p-5">
            <div className="flex items-center gap-3">
              <h2 className="m-0 flex-1 text-title font-normal">{d.id ? <>設定 <span className="font-mono">{d.name}</span></> : '新增自訂連接'}</h2>
              <Switch checked={d.enabled} onChange={(v) => set({ enabled: v })} label="啟用">
                啟用
              </Switch>
            </div>
            <div className="grid grid-cols-2 gap-3 max-sm:grid-cols-1">
              <TextInput label="名稱" value={d.name} disabled={!!d.id} onChange={(e) => set({ name: e.target.value })} placeholder="shop-inventory" className="font-mono" />
              <Select label="給誰用" value={d.who} options={whoOptions} onChange={(v) => set({ who: v })} />
            </div>
            <div className="flex flex-col gap-1.5">
              <span className="text-ui-sm text-ink-2">連線方式</span>
              {d.id ? (
                <span className="flex items-center gap-2 text-ui">
                  <Icon icon={d.transport === 'http' ? Globe : Terminal} size={18} />
                  {d.transport === 'http' ? '遠端網址（HTTP）' : '本機程式（指令）'}
                </span>
              ) : (
                <Segmented
                  label="連線方式"
                  value={d.transport}
                  onChange={(v) => set({ transport: v, auth: 'none' })}
                  className="w-full"
                  options={[
                    { value: 'http', label: '遠端網址（HTTP）' },
                    { value: 'stdio', label: '本機程式（指令）' },
                  ]}
                />
              )}
            </div>
            {d.transport === 'http' ? (
              <>
                <TextInput label="服務網址" value={d.url} onChange={(e) => set({ url: e.target.value })} placeholder="https://mcp.example.com/mcp" className="font-mono" />
                <div className="grid grid-cols-2 gap-3 max-sm:grid-cols-1">
                  <Select
                    label="驗證方式"
                    value={d.auth}
                    onChange={(v) => set({ auth: v })}
                    options={[
                      { value: 'none', label: '不用驗證' },
                      { value: 'api_key', label: 'Token（放在 Authorization）' },
                      { value: 'oauth2', label: 'OAuth 登入' },
                    ]}
                  />
                  {d.auth === 'api_key' && (
                    <TextInput
                      label="Token"
                      type="password"
                      autoComplete="off"
                      value={d.token}
                      onChange={(e) => set({ token: e.target.value })}
                      placeholder={d.hint ? `已存 · ${d.hint}` : '貼上 Token'}
                    />
                  )}
                </div>
                <Textarea
                  label="其他標頭（每行一個，選填）"
                  rows={2}
                  className="font-mono"
                  value={d.headers}
                  onChange={(e) => set({ headers: e.target.value, headersTouched: true })}
                  placeholder="X-Shop-Id: 88213"
                />
              </>
            ) : (
              <>
                <div className="grid grid-cols-[1fr_2fr] gap-3 max-sm:grid-cols-1">
                  <TextInput label="指令" value={d.command} onChange={(e) => set({ command: e.target.value })} placeholder="npx" className="font-mono" />
                  <TextInput label="參數" value={d.args} onChange={(e) => set({ args: e.target.value })} placeholder="-y @acme/mcp-server" className="font-mono" />
                </div>
                <Textarea
                  label="環境變數（每行 名稱=值；值空白表示還缺）"
                  rows={3}
                  className="font-mono"
                  value={d.env}
                  onChange={(e) => set({ env: e.target.value, envTouched: true })}
                  placeholder="REPORT_API_KEY=…"
                  hint="值加密存放，只寫進有授權的 Bot 的電腦（唯讀檔案）；已存的值不會顯示，空白表示保留原本的值。"
                />
              </>
            )}
            {cur && cur.tools.length > 0 && (
              <div className="flex flex-col gap-2">
                <span className="text-ui-sm text-ink-2">
                  這個服務提供的工具 · {cur.tools.length} 個，{cur.disabled_tools.length ? `關了 ${cur.disabled_tools.length} 個` : '全部開啟'}
                </span>
                <div className="flex flex-wrap gap-2">
                  {cur.tools.map((t) => (
                    <span key={t.name} title={t.description} className="inline-flex items-center gap-1.5 rounded-full border border-solid border-line px-2.5 py-1 font-mono text-meta">
                      <span className={cur.disabled_tools.includes(t.name) ? 'text-ink-3' : 'text-success'}>
                        <Icon icon={Check} size={14} />
                      </span>
                      {t.name}
                    </span>
                  ))}
                </div>
              </div>
            )}
            <p className="m-0 border-0 border-t border-solid border-divider pt-3 text-meta text-ink-2">
              進階：單次結果上限 25,000 字 · 等待 60 秒 · {d.transport === 'http' ? 'Streamable HTTP，由 XBot 轉送（Token 不進 Bot 的電腦）' : '在 Bot 的電腦裡執行'}
            </p>
            <div className="mt-auto flex flex-wrap items-center gap-3">
              <Button icon={Plug} disabled={!d.id || d.transport !== 'http'} onClick={() => void runTest()}>
                測試連線
              </Button>
              {test && (
                <span role="status" className={cx('flex items-center gap-1.5 text-ui-sm', test.ok ? 'text-success' : 'text-danger')}>
                  {test.ok && <Icon icon={CircleCheck} size={16} />}
                  {test.text}
                </span>
              )}
              <span className="ml-auto" />
              <Button onClick={() => navigate('/apps')}>取消</Button>
              <Button variant="primary" loading={busy} onClick={() => void save()}>
                儲存
              </Button>
            </div>
          </section>
        </div>
      </div>
      <SigninDialog
        title={d.name}
        icon="plug"
        signin={signin}
        onClose={() => {
          setSignin(null)
          load()
        }}
        onDone={() => {
          setSignin(null)
          toast.show({ kind: 'done', text: `已連接 ${d.name}` })
          load()
        }}
      />
    </main>
  )
}
