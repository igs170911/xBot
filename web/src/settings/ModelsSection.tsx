import { useEffect, useMemo, useState } from 'react'
import {
  CircleAlert,
  CircleCheck,
  ClipboardPaste,
  Ellipsis,
  FlaskConical,
  Info,
  KeyRound,
  LaptopMinimal,
  Plus,
  RefreshCw,
  Server,
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
import { OAuthCard } from './OAuthLogin'
import { Block, Group, Note, Row, SectionHeader } from './parts'
import { errText, fieldError, isPending } from './format'
import { PROBE_STATUS, TRANSPORT_LABEL, guessPreset, maskKey, providerName, useModelData, type Preset, type Provider } from './modelData'

// AI 模型（J02／J03／J10）：主要模型、快判模型、進階（備援）、各個 Bot 的引擎。

const TABS = [
  { to: '/settings/models', label: '主要模型' },
  { to: '/settings/models/judge', label: '快判模型' },
  { to: '/settings/models/routing', label: '進階：備援' },
  { to: '/settings/models/bots', label: '各個 Bot' },
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
  const tab = TABS.find((t) => t.to === path) ? path : '/settings/models'
  return (
    <>
      <SectionHeader title="AI 模型" sub="Bot 優先用你已經登入的訂閱；沒有的話貼一把 API 金鑰就好。" />
      <Tabs path={tab} />
      {data.error && (
        <p role="alert" className="m-0 text-ui-sm text-danger">
          讀不到模型設定：{data.error}
        </p>
      )}
      {tab === '/settings/models' && <MainModels {...data} />}
      {tab === '/settings/models/routing' && <Routing {...data} />}
      {tab === '/settings/models/judge' && <FastJudge providers={data.models?.providers ?? []} />}
      {tab === '/settings/models/bots' && <BotsEngineList />}
    </>
  )
}

type Data = ReturnType<typeof useModelData>

// ── 主要模型 ─────────────────────────────────────────

function MainModels({ models, presets, reload }: Data) {
  const toast = useToast()
  const providers = models?.providers ?? []
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
  return (
    <>
      <HostEngines />
      <Block title="模型供應商" sub="金鑰只存在這台主機的 Model Proxy，不會進 Bot 的電腦，也不會再顯示。">
        {models && providers.length === 0 && <p className="m-0 rounded-group bg-card px-4 py-3 text-ui-sm text-ink-2">還沒有任何供應商。在下面貼一把 API 金鑰就能開始。</p>}
        {providers.length > 0 && (
          <Group label="已設定的供應商">
            {providers.map((p) => (
              <ProviderRow key={p.id} p={p} presets={presets} isDefault={models?.default?.provider === p.id} isFallback={models?.fallback?.provider === p.id} onChanged={reload} />
            ))}
          </Group>
        )}
      </Block>
      <Block title="預設模型" sub="沒有指定模型的 Bot 用這個。改了之後，新的對話串就會用新的模型。">
        <Group>
          <div className="px-4 py-3">
            <ModelChooser label="預設模型" value={models?.default} providers={providers} presets={presets} onCommit={setDefault} disabled={providers.length === 0} />
          </div>
        </Group>
      </Block>
      <AddProvider presets={presets} providers={providers} hasDefault={!!models?.default} onAdded={reload} />
      <OAuthCard providers={providers} onDone={reload} />
    </>
  )
}

/** 這台電腦上已登入的訂閱 CLI（engines.status 的 host）。 */
function HostEngines() {
  const [st, setSt] = useState<EnginesStatusOutput | null>(null)
  const [busy, setBusy] = useState(true)
  const fetchStatus = () =>
    enginesStatus({})
      .then(setSt)
      .catch(() => setSt({ bots: [], host: [] }))
      .finally(() => setBusy(false))
  const load = () => {
    setBusy(true)
    void fetchStatus()
  }
  useEffect(() => {
    void fetchStatus()
  }, [])
  const host = st?.host ?? []
  return (
    <Block
      title="這台電腦上的訂閱"
      sub="直接用你已登入的訂閱，不另外算 API 費用。要逐個 Bot 同意才會用。"
      aside={
        <Button variant="ghost" size="sm" icon={RefreshCw} loading={busy} onClick={load}>
          重新偵測
        </Button>
      }
    >
      <div className="grid grid-cols-2 gap-3 max-sm:grid-cols-1">
        {host.map((h) => (
          <div key={h.engine} className="flex min-h-16 items-center gap-3 rounded-group bg-card px-3.5 py-3">
            <span className="flex size-9 flex-none items-center justify-center rounded-field bg-control text-ink-2">
              <Icon icon={SquareTerminal} size={18} />
            </span>
            <span className="min-w-0 flex-1">
              <span className="block text-[15px]">{h.label}</span>
              <span className="block truncate text-[13px] text-ink-2">
                {h.logged_in ? `已登入${h.plan ? ` · ${h.plan}` : ''}` : <>沒有登入 · 在這台電腦執行 <code className="font-mono text-meta">{h.login}</code></>}
              </span>
            </span>
            {h.logged_in ? (
              <a href="/settings/models/bots" onClick={linkClick('/settings/models/bots')} className="flex h-[30px] flex-none items-center rounded-full bg-control px-3 text-sub text-ink hover:bg-hover">
                給 Bot 用
              </a>
            ) : (
              <StatusChip tone="warning">要登入</StatusChip>
            )}
          </div>
        ))}
        <div className="flex min-h-16 items-center gap-3 rounded-group border border-dashed border-line px-3.5 py-3">
          <span className="flex size-9 flex-none items-center justify-center rounded-full border border-line text-ink-2">
            <Icon icon={Server} size={18} />
          </span>
          <span className="text-[13px] text-ink-2">Hermes 是內建引擎，搭配下面的 API 金鑰或本機模型使用。</span>
        </div>
      </div>
    </Block>
  )
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
      toast.show({ kind: 'done', text: r.affected_bots.length ? `已移除 ${name}；${r.affected_bots.length} 個 Bot 改用預設模型` : `已移除 ${name}` })
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
            ? '這是預設模型用的供應商，要先換預設模型才能移除。'
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
    <Block title="用 API 金鑰（選用）">
      <Group>
        <div className="flex flex-col gap-3 px-4 pb-4 pt-3">
          <span>
            <span className="block text-ui">貼上 API 金鑰</span>
            <span className="block text-[13px] text-ink-2">自動認出供應商、選好建議的模型。金鑰只存在這台主機，不會進 Bot 的電腦。</span>
          </span>
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
    </Block>
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
      <Note icon={<Icon icon={Info} />}>這頁是「進階」，一般人不用動。沒設定的地方全部沿用主要模型。</Note>
      <Block title="備援模型" sub="主要模型限流（429）、出錯（5xx）或連不上，而且還沒開始回覆時，改用備援模型重試一次。已經開始送出的回覆不會重播。">
        <Group>
          <div className="flex flex-col gap-2 px-4 py-3">
            <ModelChooser label="備援模型" value={models?.fallback} providers={providers} presets={presets} onCommit={setFallback} noneLabel="不用備援" disabled={providers.length === 0} />
            <p className="m-0 text-meta text-ink-3">Bot 自己設定的備援優先（在「各個 Bot」裡改）。備援是按用量計費的 API 時，花費一樣算進花費上限。</p>
          </div>
        </Group>
      </Block>
      <Block title="之後會放在這裡">
        <Group>
          <Row title="網路搜尋服務" sub="Exa、Tavily、Brave 與金鑰">
            <StatusChip size="sm">還沒開放</StatusChip>
          </Row>
          <Row title="聊天與工作分流" sub="閒聊用便宜快速的模型，動手做事時換工作模型">
            <StatusChip size="sm">還沒開放</StatusChip>
          </Row>
        </Group>
      </Block>
    </>
  )
}

