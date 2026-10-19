import { useEffect, useMemo, useRef, useState } from 'react'
import {
  CircleAlert,
  CircleCheck,
  ClipboardPaste,
  Ellipsis,
  FlaskConical,
  KeyRound,
  LaptopMinimal,
  Plus,
  RefreshCw,
  SquareTerminal,
  Trash2,
  Zap,
} from 'lucide-react'
import {
  enginesStatus,
  modelsAddProvider,
  modelsProbe,
  modelsRemoveProvider,
  modelsSetDefault,
  modelsSetFallback,
  modelsUpdateProvider,
  type EnginesStatusOutput,
  type ModelsProbeOutput,
} from '../api/generated'
import { linkClick } from '../router'
import { Button } from '../ui/Button'
import { ConfirmDialog, Dialog } from '../ui/Dialog'
import { TextInput } from '../ui/Field'
import { Icon } from '../ui/Icon'
import { MenuButton, Select } from '../ui/Menu'
import { StatusChip } from '../ui/Pill'
import { IconButton } from '../ui/Button'
import { useToast } from '../ui/toast-context'
import { cx } from '../ui/cx'
import { BotsEngineList } from './BotSettings'
import { FastJudge } from './FastJudge'
import { ModelChooser } from './ModelChooser'
import { OAuthDialog } from './OAuthLogin'
import { Block, Group, SectionHeader } from './parts'
import { errText, fieldError, isPending } from './format'
import { PROBE_STATUS, TRANSPORT_LABEL, guessPreset, maskKey, providerName, useModelData, type Preset, type Provider } from './modelData'

// AI 模型（J02／J03／J10）：方案（訂閱、API 金鑰、各個 Bot 用哪個）、快判、進階（備援）。

const TABS = [
  { to: '/settings/models', label: '方案' },
  { to: '/settings/models/judge', label: '快速判斷' },
  { to: '/settings/models/routing', label: '進階' },
]

function Tabs({ path }: { path: string }) {
  return (
    <nav aria-label="AI 模型分頁" className="flex w-fit max-w-full gap-0.5 overflow-x-auto rounded-field bg-inset p-[3px]">
      {TABS.map((t) => (
        <a
          key={t.to}
          href={t.to}
          onClick={linkClick(t.to)}
          aria-current={path === t.to ? 'page' : undefined}
          className={cx('flex h-8 items-center whitespace-nowrap rounded-seg px-3.5 text-ui-sm max-sm:h-10', path === t.to ? 'bg-control text-ink' : 'text-ink-2 hover:text-ink')}
        >
          {t.label}
        </a>
      ))}
    </nav>
  )
}

export function ModelsSection({ path }: { path: string }) {
  const data = useModelData()
  // 舊的「各個 Bot」分頁併進「方案」
  const tab = TABS.find((t) => t.to === path) ? path : '/settings/models'
  return (
    <>
      <SectionHeader title="AI 模型" />
      <Tabs path={tab} />
      {data.error && (
        <p role="alert" className="m-0 text-ui-sm text-danger">
          讀不到模型設定：{data.error}
        </p>
      )}
      {tab === '/settings/models' && <MainModels {...data} />}
      {tab === '/settings/models/routing' && <Routing {...data} />}
      {tab === '/settings/models/judge' && <FastJudge providers={data.models?.providers ?? []} defaultModel={data.models?.default} />}
    </>
  )
}

type Data = ReturnType<typeof useModelData>

// ── 方案 ─────────────────────────────────────────

function MainModels({ models, presets, reload }: Data) {
  const toast = useToast()
  const providers = models?.providers ?? []
  const host = useHostLogins()
  const [adding, setAdding] = useState(false)
  const [oauth, setOauth] = useState(false)
  const setDefault = async (v: { provider: string; model: string } | null) => {
    if (!v) return
    try {
      await modelsSetDefault({ provider: v.provider, model: v.model })
      await reload()
      toast.show({ kind: 'done', text: `預設模型改成 ${v.model}` })
    } catch (e) {
      toast.show({ kind: 'error', text: errText(e) })
    }
  }
  // 有金鑰但還沒探測的供應商：背景探測一次，模型才有清單可以選
  const probed = useRef(new Set<string>())
  useEffect(() => {
    const todo = providers.filter((p) => !p.probed_at && !p.models?.length && !probed.current.has(p.id))
    if (!todo.length) return
    todo.forEach((p) => probed.current.add(p.id))
    void Promise.allSettled(todo.map((p) => modelsProbe({ provider: p.id }))).then(() => reload())
  }, [providers, reload])
  const loggedIn = host.list.filter((h) => h.logged_in)
  const notIn = host.list.filter((h) => !h.logged_in)
  const nothing = models && providers.length === 0 && !host.busy && loggedIn.length === 0
  return (
    <>
      <Block
        title="你的方案"
        sub="訂閱：用你登入的帳號。API 金鑰：按用量付費。"
        aside={
          <Button variant="ghost" size="sm" icon={RefreshCw} loading={host.busy} onClick={host.reload}>
            重新偵測
          </Button>
        }
      >
        {nothing && <p className="m-0 rounded-group bg-card px-4 py-3 text-ui-sm text-ink-2">還沒有方案。登入一個訂閱 CLI，或加一把 API 金鑰。</p>}
        {(loggedIn.length > 0 || providers.length > 0) && (
          <Group label="你的方案">
            {loggedIn.map((h) => (
              <div key={h.engine} className="flex min-h-14 items-center gap-3 border-line px-4 py-2.5 not-first:border-t">
                <span className="flex size-9 flex-none items-center justify-center rounded-field bg-control text-ink-2">
                  <Icon icon={SquareTerminal} size={18} />
                </span>
                <span className="min-w-0 flex-1">
                  <span className="block text-[15px]">{h.plan || h.label}</span>
                  <span className="block truncate text-[13px] text-ink-2">訂閱 · {h.label}</span>
                </span>
                <StatusChip tone="success" size="sm">
                  已登入
                </StatusChip>
              </div>
            ))}
            {providers.map((p) => (
              <ProviderRow key={p.id} p={p} presets={presets} isDefault={models?.default?.provider === p.id} isFallback={models?.fallback?.provider === p.id} onChanged={reload} />
            ))}
          </Group>
        )}
        {notIn.length > 0 && (
          <p className="m-0 text-meta text-ink-3" title={notIn.map((h) => `${h.label}：${h.login}`).join('\n')}>
            還沒登入：{notIn.map((h) => h.label).join('、')}（在這台電腦執行 {notIn.map((h) => <code key={h.engine} className="mx-0.5 font-mono">{h.login.split('（')[0]}</code>)}，再按重新偵測）
          </p>
        )}
        <div className="flex flex-wrap gap-2">
          <Button icon={Plus} onClick={() => setAdding((a) => !a)} aria-expanded={adding}>
            加 API 金鑰
          </Button>
          <Button icon={FlaskConical} onClick={() => setOauth(true)}>
            {providers.some((p) => p.auth === 'oauth_codex') ? '重新登入 ChatGPT' : '用 ChatGPT 帳號登入'}
          </Button>
        </div>
        {adding && <AddProvider presets={presets} providers={providers} hasDefault={!!models?.default} onAdded={reload} />}
      </Block>
      {providers.length > 0 && (
        <Block title="API 金鑰的預設模型" sub="用 API 金鑰、又沒指定模型的 Bot 用這個。">
          <Group>
            <div className="px-4 py-3">
              <ModelChooser label="預設模型" value={models?.default} providers={providers} presets={presets} onCommit={setDefault} />
            </div>
          </Group>
        </Block>
      )}
      <BotsEngineList />
      {oauth && <OAuthDialog onClose={() => setOauth(false)} onDone={reload} />}
    </>
  )
}

/** 這台電腦上已登入的訂閱 CLI（engines.status 的 host）。 */
function useHostLogins() {
  const [list, setList] = useState<EnginesStatusOutput['host']>([])
  const [busy, setBusy] = useState(true)
  const fetchStatus = () =>
    enginesStatus({})
      .then((st) => setList(st.host))
      .catch(() => setList([]))
      .finally(() => setBusy(false))
  useEffect(() => {
    void fetchStatus()
  }, [])
  return {
    list,
    busy,
    reload: () => {
      setBusy(true)
      void fetchStatus()
    },
  }
}

function probeLine(r: ModelsProbeOutput) {
  const c = r.check
  const ok = c?.status === 'ok'
  return (
    <div role="status" className={cx('flex flex-col gap-1 rounded-field px-3 py-2 text-[13px]', ok ? 'bg-success-soft' : 'bg-danger-soft')}>
      <span className="flex items-center gap-2">
        <Icon icon={ok ? CircleCheck : CircleAlert} size={16} color={ok ? 'var(--color-success)' : 'var(--color-danger)'} />
        <span className="text-ink">
          {c ? `${c.model}：${PROBE_STATUS[c.status] ?? c.status}` : '沒有測模型'}
          {ok && c?.transport ? ` · ${TRANSPORT_LABEL[c.transport] ?? c.transport}` : ''}
          {` · 列出 ${r.models.length} 個模型`}
        </span>
      </span>
      {c && !ok && c.message && <span className="pl-6 text-ink-2">{c.message}</span>}
      {r.list_error && <span className="pl-6 text-ink-2">列模型失敗：{r.list_error}</span>}
    </div>
  )
}

function ProviderRow({ p, presets, isDefault, isFallback, onChanged }: { p: Provider; presets: Preset[]; isDefault: boolean; isFallback: boolean; onChanged: () => Promise<void> }) {
  const toast = useToast()
  const [probing, setProbing] = useState(false)
  const [result, setResult] = useState<ModelsProbeOutput | null>(null)
  const [probeErr, setProbeErr] = useState<string | null>(null)
  const [removing, setRemoving] = useState(false)
  const [busy, setBusy] = useState(false)
  const [keyOpen, setKeyOpen] = useState(false)
  const name = providerName(p, presets)
  const oauth = p.auth === 'oauth_codex'
  const local = presets.find((x) => x.id === (p.preset || p.id))?.local

  const probe = async () => {
    setProbing(true)
    setProbeErr(null)
    try {
      const r = await modelsProbe({ provider: p.id })
      setResult(r)
      await onChanged()
    } catch (e) {
      setResult(null)
      setProbeErr(errText(e))
    } finally {
      setProbing(false)
    }
  }
  const remove = async () => {
    setBusy(true)
    try {
      const r = await modelsRemoveProvider({ id: p.id })
      setRemoving(false)
      await onChanged()
      toast.show({ kind: 'done', text: r.cleared_default ? `已移除 ${name}，預設模型也清掉了` : r.affected_bots.length ? `已移除 ${name}；${r.affected_bots.length} 個 Bot 改用預設模型` : `已移除 ${name}` })
    } catch (e) {
      setRemoving(false)
      toast.show({ kind: isPending(e) ? 'info' : 'error', text: errText(e) })
    } finally {
      setBusy(false)
    }
  }

  const sub = [
    oauth ? 'ChatGPT 訂閱登入' : local ? '本機模型' : p.api_key_set ? '金鑰已設定' : '沒有金鑰',
    p.models?.length ? `${p.models.length} 個模型` : null,
    p.probed_at ? '已探測' : '還沒探測',
  ]
    .filter(Boolean)
    .join(' · ')

  return (
    <div className="flex flex-col gap-2 border-line px-4 py-3 not-first:border-t">
      <div className="flex items-center gap-3">
        <span className="flex size-9 flex-none items-center justify-center rounded-field bg-control text-ink-2">
          <Icon icon={oauth ? FlaskConical : local ? LaptopMinimal : KeyRound} size={18} />
        </span>
        <span className="min-w-0 flex-1">
          <span className="flex flex-wrap items-center gap-2">
            <span className="text-[15px]">{name}</span>
            {isDefault && <StatusChip tone="accent" size="sm">預設</StatusChip>}
            {isFallback && <StatusChip size="sm">備援</StatusChip>}
            {oauth && <StatusChip size="sm">訂閱登入</StatusChip>}
          </span>
          <span className="block truncate text-[13px] text-ink-2">{sub}</span>
        </span>
        <Button size="sm" icon={Zap} loading={probing} onClick={probe} aria-label={`探測 ${name}`}>
          探測
        </Button>
        <MenuButton
          label={`${name} 的更多動作`}
          align="end"
          items={[
            ...(oauth || local ? [] : [{ label: '換金鑰', icon: KeyRound, onSelect: () => setKeyOpen(true) }]),
            { label: '移除', icon: Trash2, danger: true, separator: !(oauth || local), onSelect: () => setRemoving(true) },
          ]}
          trigger={(t) => <IconButton {...t} icon={Ellipsis} label={`${name} 的更多動作`} size="sm" />}
        />
      </div>
      {result && probeLine(result)}
      {probeErr && (
        <p role="alert" className="m-0 text-[13px] text-danger">
          {probeErr}
        </p>
      )}
      <ConfirmDialog
        open={removing}
        danger
        busy={busy}
        title={`移除 ${name}？`}
        description={
          isDefault
            ? '金鑰會一起刪掉，無法復原。預設模型也會清掉；用 API 金鑰的 Bot 要重新選模型（用訂閱的 Bot 不受影響）。'
            : '金鑰會一起刪掉，無法復原。指定了這個供應商的 Bot 之後會改用預設模型。'
        }
        confirmText="移除"
        onConfirm={remove}
        onCancel={() => setRemoving(false)}
      />
      <ReplaceKeyDialog open={keyOpen} provider={p} name={name} onClose={() => setKeyOpen(false)} onSaved={onChanged} />
    </div>
  )
}

function ReplaceKeyDialog({ open, provider, name, onClose, onSaved }: { open: boolean; provider: Provider; name: string; onClose: () => void; onSaved: () => Promise<void> }) {
  const [key, setKey] = useState('')
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<unknown>(null)
  const save = async () => {
    setBusy(true)
    setErr(null)
    try {
      await modelsUpdateProvider({ id: provider.id, api_key: key.trim() })
      setKey('')
      await onSaved()
      onClose()
    } catch (e) {
      setErr(e)
    } finally {
      setBusy(false)
    }
  }
  return (
    <Dialog
      open={open}
      onClose={onClose}
      busy={busy}
      title={`換 ${name} 的金鑰`}
      description="新的金鑰會蓋掉舊的。金鑰只存在這台主機，不會進 Bot 的電腦。"
      actions={
        <>
          <Button onClick={onClose} disabled={busy}>
            取消
          </Button>
          <Button variant="primary" loading={busy} disabled={!key.trim()} onClick={save}>
            儲存
          </Button>
        </>
      }
    >
      <TextInput label="新的金鑰" type="password" autoComplete="off" value={key} onChange={(e) => setKey(e.target.value)} error={fieldError(err, 'api_key') ?? (err ? errText(err) : undefined)} />
    </Dialog>
  )
}

// ── 貼上金鑰新增供應商（J02）─────────────────────────────

function AddProvider({ presets, providers, hasDefault, onAdded }: { presets: Preset[]; providers: Provider[]; hasDefault: boolean; onAdded: () => Promise<void> }) {
  const toast = useToast()
  const [key, setKey] = useState('')
  const [picked, setPicked] = useState('')
  const [modelPick, setModelPick] = useState<{ preset: string; model: string } | null>(null)
  const [advanced, setAdvanced] = useState(false)
  const [baseUrl, setBaseUrl] = useState('')
  const [id, setId] = useState('')
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<unknown>(null)
  const [probe, setProbe] = useState<ModelsProbeOutput | null>(null)

  const guessed = useMemo(() => guessPreset(key, presets), [key, presets])
  const presetId = picked || guessed?.id || ''
  const preset = presets.find((p) => p.id === presetId)
  const custom = presetId === '__custom__'
  // 換供應商時自動選好建議的模型；使用者自己選過就用他選的
  const model = modelPick && modelPick.preset === presetId ? modelPick.model : (preset?.default_model ?? preset?.models[0] ?? '')
  const setModel = (m: string) => setModelPick({ preset: presetId, model: m })

  const options = [
    ...presets.map((p) => ({ value: p.id, label: p.label + (p.local ? '（本機）' : '') + (p.configured ? ' · 已設定' : '') })),
    { value: '__custom__', label: '其他（自己填網址）' },
  ]
  const needsKey = !preset?.local
  const canAdd = !!presetId && (!needsKey || key.trim()) && (!custom || (baseUrl.trim() && id.trim()))

  const paste = async () => {
    try {
      const t = await navigator.clipboard.readText()
      if (t) setKey(t.trim())
    } catch {
      toast.show({ kind: 'error', text: '瀏覽器不讓網頁讀剪貼簿，請直接貼到欄位裡。' })
    }
  }

  const add = async () => {
    setBusy(true)
    setErr(null)
    setProbe(null)
    try {
      const out = await modelsAddProvider({
        preset: custom ? undefined : presetId,
        id: custom ? id.trim() : id.trim() || undefined,
        base_url: custom || baseUrl.trim() ? baseUrl.trim() : undefined,
        api_key: key.trim() || undefined,
        default_model: !hasDefault && model ? model : undefined,
      })
      setKey('')
      setPicked('')
      await onAdded()
      toast.show({ kind: 'done', text: `已加入 ${out.provider.label || out.provider.id}，正在探測…` })
      try {
        setProbe(await modelsProbe({ provider: out.provider.id, model: model || undefined }))
        await onAdded()
      } catch (e) {
        toast.show({ kind: 'error', text: '探測失敗：' + errText(e) })
      }
    } catch (e) {
      setErr(e)
    } finally {
      setBusy(false)
    }
  }

  const general = err && !fieldError(err, 'api_key') && !fieldError(err, 'base_url') && !fieldError(err, 'id') ? errText(err) : null
  const dup = !custom && providers.some((p) => p.id === (id.trim() || presetId))

  return (
    <>
      <Group>
        <div className="flex flex-col gap-3 px-4 pb-4 pt-3">
          <span className="block text-[13px] text-ink-2">貼上金鑰，自動認出是哪一家。金鑰只存在這台主機，不會進 Bot 的電腦。</span>
          <div className="flex items-end gap-2">
            <TextInput
              label="API 金鑰"
              hideLabel
              icon={KeyRound}
              type="password"
              autoComplete="off"
              spellCheck={false}
              value={key}
              onChange={(e) => setKey(e.target.value)}
              placeholder={preset?.local ? '本機模型不用金鑰' : 'sk-…'}
              className="font-mono"
              wrapperClassName="flex-1"
              error={fieldError(err, 'api_key')}
            />
            <Button icon={ClipboardPaste} onClick={paste} className="h-10 max-sm:h-11">
              貼上
            </Button>
          </div>
          {key.trim() && (
            <p className="m-0 text-meta text-ink-3" aria-live="polite">
              {guessed ? (
                <>
                  <Icon icon={CircleCheck} size={14} color="var(--color-success)" className="mr-1 inline-block align-[-2px]" />
                  認出是 {guessed.label}（{maskKey(key)}）
                </>
              ) : (
                '認不出是哪一家，請在下面選供應商。'
              )}
            </p>
          )}
          <div className="grid grid-cols-2 gap-3 max-sm:grid-cols-1">
            <Select label="供應商" value={presetId} options={options} onChange={setPicked} placeholder="選供應商" />
            {preset && preset.models.length > 0 ? (
              <Select label={hasDefault ? '建議的模型（探測用）' : '預設模型'} value={model} options={preset.models.map((m) => ({ value: m, label: m }))} onChange={setModel} />
            ) : (
              <TextInput label={hasDefault ? '模型（探測用，選填）' : '預設模型'} value={model} onChange={(e) => setModel(e.target.value)} placeholder="例：gpt-4.1-mini" />
            )}
          </div>
          {preset?.signup_url && !key && (
            <p className="m-0 text-meta text-ink-3">
              還沒有金鑰？到{' '}
              <a href={preset.signup_url} target="_blank" rel="noreferrer">
                {preset.label} 申請
              </a>
            </p>
          )}
          <button type="button" aria-expanded={advanced || custom} onClick={() => setAdvanced((a) => !a)} className="self-start bg-transparent p-0 text-meta text-accent-ink">
            {advanced || custom ? '收起進階' : '進階：代號、API 網址'}
          </button>
          {(advanced || custom) && (
            <div className="grid grid-cols-2 gap-3 max-sm:grid-cols-1">
              <TextInput label="供應商代號" value={id} onChange={(e) => setId(e.target.value)} placeholder={custom ? '例：my-llm' : presetId || '和範本同名'} hint="小寫英文、數字、-" error={fieldError(err, 'id')} />
              <TextInput label="API 網址" inputMode="url" value={baseUrl} onChange={(e) => setBaseUrl(e.target.value)} placeholder={preset?.base_url ?? 'https://…/v1'} error={fieldError(err, 'base_url')} />
            </div>
          )}
          {dup && <p className="m-0 text-meta text-warning-ink">已經有同代號的供應商；加入會換掉它的設定。</p>}
          {general && (
            <p role="alert" className="m-0 text-meta text-danger">
              {general}
            </p>
          )}
          {probe && probeLine(probe)}
          <div className="flex justify-end">
            <Button variant="primary" icon={Plus} loading={busy} disabled={!canAdd} onClick={add}>
              加入並探測
            </Button>
          </div>
        </div>
      </Group>
    </>
  )
}

// ── 進階：備援（J03）─────────────────────────────────

function Routing({ models, presets, reload }: Data) {
  const toast = useToast()
  const providers = models?.providers ?? []
  const setFallback = async (v: { provider: string; model: string } | null) => {
    if (v && models?.default && v.provider === models.default.provider && v.model === models.default.model) {
      toast.show({ kind: 'error', text: '備援模型不能和預設模型一樣，請換一個。' })
      return
    }
    try {
      await modelsSetFallback(v ? { provider: v.provider, model: v.model } : {})
      await reload()
      toast.show({ kind: 'done', text: v ? `備援模型改成 ${v.model}` : '不用備援模型' })
    } catch (e) {
      toast.show({ kind: 'error', text: errText(e) })
    }
  }
  return (
    <>
      <Block title="備援模型" sub="主要模型限流、出錯時，改用這個重試一次。">
        <Group>
          <div className="flex flex-col gap-2 px-4 py-3">
            <ModelChooser label="備援模型" value={models?.fallback} providers={providers} presets={presets} onCommit={setFallback} noneLabel="不用備援" disabled={providers.length === 0} />
          </div>
        </Group>
      </Block>
    </>
  )
}

