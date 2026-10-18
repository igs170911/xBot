import { useEffect, useState, type ReactNode } from 'react'
import {
  ArrowLeft,
  ArrowRight,
  Box,
  Check,
  CircleAlert,
  CircleCheck,
  CircleDashed,
  CircleX,
  Copy,
  KeyRound,
  LoaderCircle,
  Lock,
  Minus,
  Plus,
  RefreshCw,
  TriangleAlert,
} from 'lucide-react'
import { hostsAdd, hostsCheck, hostsInstallScript, hostsSetLimits, type HostsAddOutput, type HostsInstallScriptOutput } from '../api/generated'
import { useCopy } from '../chat/useCopy'
import { linkClick, navigate } from '../router'
import { Avatar } from '../ui/Avatar'
import { Button } from '../ui/Button'
import { Switch } from '../ui/Controls'
import { TextInput } from '../ui/Field'
import { Icon } from '../ui/Icon'
import { StatusChip } from '../ui/Pill'
import { useToast } from '../ui/toast-context'
import { cx } from '../ui/cx'
import { HostIcon, HostStatusChip } from './ComputersSection'
import { useRemoveHost } from './removeHost'
import { errText, fieldError } from './format'
import { Block, Group, Row, SectionHeader } from './parts'
import { STATUS_LABEL, ago, gb, hostSub, imageProgress, loadHosts, useHosts, type HostsState } from './hostsData'
import { look } from '../data/bots'

// 新增主機（J11，PRD §16 CL-2）：①在主機上貼一行安裝指令 ②XBot 自動檢查 ③取名、設同時開機上限 → 完成。
// 和畫板不同：指令不需要主機連回 XBot（沒有一次性代碼），所以第 1 步要填主機的位址；「從家裡出去」（CL-7）在電腦面板的「在哪裡跑」逐 Bot 打開。

type Report = HostsAddOutput['report']
type CheckItem = Report['items'][number]

const STEPS: Array<[number, string, string]> = [
  [1, '貼一行指令', '在主機上執行'],
  [2, '自動檢查', 'SSH、Docker、映像'],
  [3, '取名與設定', '然後完成'],
]

function Stepper({ step, done, go }: { step: number; done: number; go: (n: number) => void }) {
  return (
    <ol className="m-0 grid list-none grid-cols-3 gap-2 p-0 max-sm:grid-cols-1" aria-label="步驟">
      {STEPS.map(([n, t, sub]) => {
        const cur = step === n
        const ok = done >= n
        const can = n <= done + 1
        return (
          <li key={n}>
            <button
              type="button"
              disabled={!can}
              aria-current={cur ? 'step' : undefined}
              onClick={() => go(n)}
              className={cx(
                'flex w-full items-center gap-2.5 rounded-group border px-3 py-2.5 text-left disabled:opacity-50',
                cur ? 'border-hover bg-card text-ink' : 'border-card bg-transparent text-ink-2',
              )}
            >
              <span className={cx('flex size-6 flex-none items-center justify-center rounded-full text-meta', ok ? 'bg-accent-strong text-white' : 'bg-control text-ink')}>
                {ok ? <Icon icon={Check} size={14} /> : n}
              </span>
              <span className="flex min-w-0 flex-col">
                <span className={cx('text-ui-sm', (cur || ok) && 'text-ink')}>{t}</span>
                <span className="text-meta text-ink-3">{sub}</span>
              </span>
            </button>
          </li>
        )
      })}
    </ol>
  )
}

function Card({ title, sub, children }: { title: string; sub?: ReactNode; children: ReactNode }) {
  return (
    <section aria-label={title} className="flex flex-col gap-4 rounded-group bg-card p-5 max-sm:p-4">
      <div>
        <h3 className="m-0 text-[16px] leading-[1.4]">{title}</h3>
        {sub && <p className="m-0 mt-1 text-sub text-ink-2">{sub}</p>}
      </div>
      {children}
    </section>
  )
}

/** 指令很長（壓縮過的腳本）：畫面上中間遮起來，複製的是完整的一行。 */
function maskCommand(cmd: string): ReactNode {
  const m = cmd.match(/^echo '([^']+)'(.*)$/)
  if (!m) return cmd
  const b = m[1]
  return (
    <>
      echo &apos;<span className="text-warning-ink">{b.slice(0, 10)}••••••••••••{b.slice(-6)}</span>&apos;{m[2]}
    </>
  )
}

function Fact({ icon, children }: { icon: typeof Box; children: ReactNode }) {
  return (
    <span className="flex gap-2.5 text-ui-sm text-ink-2">
      <span className="mt-0.5 flex-none text-ink-3">
        <Icon icon={icon} size={16} />
      </span>
      <span>{children}</span>
    </span>
  )
}

function CheckIcon({ it }: { it: CheckItem }) {
  switch (it.status) {
    case 'ok':
      return <Icon icon={CircleCheck} size={18} color="var(--color-success)" label="通過" />
    case 'warn':
      return <Icon icon={CircleAlert} size={18} color="var(--color-warning)" label="建議處理" />
    case 'fail':
      return <Icon icon={CircleX} size={18} color="var(--color-danger)" label="沒過" />
    case 'running':
      return <Icon icon={LoaderCircle} size={18} className="animate-spin text-accent-ink" label="進行中" />
  }
  return <Icon icon={CircleDashed} size={18} className="text-ink-3" label="等待中" />
}

function Checks({ items, progress }: { items: CheckItem[]; progress?: { percent: number; done: number; total: number } }) {
  return (
    <Group label="自動檢查">
      {items.map((it) => {
        const img = it.id === 'image' && it.status === 'running' && progress
        return (
          <div key={it.id} className="grid grid-cols-[22px_180px_minmax(0,1fr)] items-start gap-x-3 border-line px-4 py-3 not-first:border-t max-sm:grid-cols-[22px_minmax(0,1fr)]" data-check={it.id} data-status={it.status}>
            <span className="pt-px">
              <CheckIcon it={it} />
            </span>
            <span className="text-ui">{it.label}</span>
            <span className="min-w-0 text-ui-sm text-ink-2 max-sm:col-start-2">
              {img ? `${gb(progress.done)} / ${gb(progress.total)}` : it.status === 'pending' && it.id !== 'image' ? '前面的項目沒過，還沒檢查' : it.detail}
              {img && (
                <span className="mt-1.5 flex items-center gap-2">
                  <span className="h-1.5 flex-1 overflow-hidden rounded-full bg-line">
                    <span className="block h-full rounded-full bg-accent" style={{ width: `${progress.percent}%` }} />
                  </span>
                  <span className="text-meta text-ink">{progress.percent}%</span>
                </span>
              )}
              {it.fix && it.status !== 'ok' && <span className={cx('mt-1 block', it.status === 'fail' ? 'text-danger' : 'text-warning-ink')}>→ {it.fix}</span>}
            </span>
          </div>
        )
      })}
    </Group>
  )
}

function Counter({ value, onChange, min = 1, max = 64, label }: { value: number; onChange: (v: number) => void; min?: number; max?: number; label: string }) {
  return (
    <span className="flex items-center gap-2" role="group" aria-label={label}>
      <button type="button" aria-label="少一台" disabled={value <= min} onClick={() => onChange(value - 1)} className="flex size-9 items-center justify-center rounded-full bg-control text-ink disabled:opacity-40">
        <Icon icon={Minus} size={16} />
      </button>
      <span className="w-10 text-center text-[16px]" aria-live="polite">
        {value}
      </span>
      <button type="button" aria-label="多一台" disabled={value >= max} onClick={() => onChange(value + 1)} className="flex size-9 items-center justify-center rounded-full bg-control text-ink disabled:opacity-40">
        <Icon icon={Plus} size={16} />
      </button>
    </span>
  )
}

/** /settings/computers/add */
export function AddHostPage() {
  const toast = useToast()
  const hs = useHosts()
  const [copied, copy] = useCopy()
  const [step, setStep] = useState(1)
  const [done, setDone] = useState(0)
  const [tailscale, setTailscale] = useState(true)
  const [script, setScript] = useState<HostsInstallScriptOutput | null>(null)
  const [scriptErr, setScriptErr] = useState('')
  const [address, setAddress] = useState('')
  const [addrErr, setAddrErr] = useState('')
  const [checking, setChecking] = useState(false)
  const [res, setRes] = useState<HostsAddOutput | null>(null)
  const [name, setName] = useState('')
  const [nameErr, setNameErr] = useState('')
  const [limit, setLimit] = useState(3)
  const [saving, setSaving] = useState(false)
  const [finished, setFinished] = useState(false)

  useEffect(() => {
    let live = true
    hostsInstallScript({ no_tailscale: !tailscale })
      .then((s) => live && (setScript(s), setScriptErr('')))
      .catch((err) => live && setScriptErr(errText(err)))
    return () => {
      live = false
    }
  }, [tailscale])

  const host = res?.added ? hs.hosts?.find((h) => h.id === res.host.id) : undefined
  const progress = host ? imageProgress(host, hs) : undefined
  const loading = !!res?.loading && host?.status === 'updating'
  // 映像傳完（主機變在線）時，把檢查清單上的映像那一項改成已就緒。
  const items = (res?.report.items ?? []).map((it) =>
    it.id === 'image' && it.status === 'running' && res?.loading && host && host.status !== 'updating'
      ? { ...it, status: host.status === 'online' ? 'ok' : 'fail', detail: host.status === 'online' ? '已就緒' : host.status_error || '沒有傳完', fix: host.status === 'online' ? undefined : '按「重新檢查」再傳一次' }
      : it,
  )
  const passed = !!res?.report.ok && items.every((it) => it.status !== 'fail')

  const check = async () => {
    if (!address.trim()) {
      setAddrErr('先填主機的位址')
      return
    }
    setChecking(true)
    setAddrErr('')
    setStep(2)
    try {
      const r = await hostsAdd({ address: address.trim() })
      setRes(r)
      setDone(1)
      if (r.added) {
        setName((n) => n || r.host.name)
        setLimit(r.host.max_running || 3)
        void loadHosts()
      }
    } catch (err) {
      const f = fieldError(err, 'address')
      if (f) {
        setAddrErr(f)
        setStep(1)
      } else toast.show({ kind: 'error', text: errText(err) })
    } finally {
      setChecking(false)
    }
  }

  const finish = async () => {
    if (!res?.added) return
    setSaving(true)
    setNameErr('')
    try {
      await hostsSetLimits({ id: res.host.id, name: name.trim() || res.host.name, max_running: limit })
      setDone(3)
      setFinished(true)
      void loadHosts()
    } catch (err) {
      const f = fieldError(err, 'name')
      if (f) setNameErr(f)
      else toast.show({ kind: 'error', text: errText(err) })
    } finally {
      setSaving(false)
    }
  }

  const go = (n: number) => {
    if (n === 3 && !passed) return
    setStep(n)
  }

  if (finished && res) {
    const h = host
    return (
      <>
        <AddHeader />
        <section aria-label="完成" className="flex flex-col items-center gap-3 rounded-group bg-card px-6 py-10 text-center">
          <span className="flex size-12 items-center justify-center rounded-full bg-success-soft text-success">
            <Icon icon={CircleCheck} size={26} />
          </span>
          <h3 className="m-0 text-[18px]">{name.trim() || h?.name} 已加入</h3>
          <p className="m-0 max-w-[460px] text-sub text-ink-2">
            {loading
              ? `電腦映像還在傳（${progress?.percent ?? 0}%），傳完主機就會變成在線。`
              : `同時開機上限 ${limit} 台。新 Bot 可以放到這台；主機滿了新工作會排隊「等電腦空出來」。`}
          </p>
          <span className="mt-2 flex gap-2">
            <Button variant="primary" onClick={() => navigate('/settings/computers')}>
              看主機清單
            </Button>
          </span>
        </section>
      </>
    )
  }

  return (
    <>
      <AddHeader />
      <Stepper step={step} done={done} go={go} />
      {step === 1 && (
        <Card title="1 · 在要加入的主機上貼這一行" sub="用 SSH 或主機商的網頁終端機登入那台 Linux 主機（Debian、Ubuntu 都可以），貼上後按 Enter。">
          <div className="flex items-start gap-3 rounded-group border border-line bg-inset px-4 py-3 max-sm:flex-col">
            <code className="min-w-0 flex-1 break-all font-mono text-[13.5px] leading-[1.6] text-ink" data-testid="install-command">
              <span className="text-ink-3">$ </span>
              {script ? maskCommand(script.command) : scriptErr || '產生中…'}
            </code>
            <Button size="sm" icon={copied ? Check : Copy} disabled={!script} onClick={() => script && void copy(script.command)}>
              {copied ? '已複製' : '複製'}
            </Button>
          </div>
          <span className="flex flex-wrap items-center gap-x-5 gap-y-2 text-meta text-ink-2">
            <span className="flex items-center gap-1.5">
              <Icon icon={KeyRound} size={14} />
              只放 XBot 的公鑰（{script?.fingerprint.slice(0, 19) ?? '…'}…），沒有密碼或秘密
            </span>
            <Switch checked={tailscale} onChange={setTailscale} label="也安裝 Tailscale">
              也安裝 Tailscale
            </Switch>
          </span>
          <div className="grid grid-cols-2 gap-x-6 gap-y-3 border-t border-line pt-4 max-sm:grid-cols-1">
            <Fact icon={Box}>會安裝 Docker{tailscale ? ' 與 Tailscale' : ''}（已經有就略過）{tailscale ? '，讓這台加入你的 tailnet' : ''}。</Fact>
            <Fact icon={KeyRound}>
              建立專用的 <code>xbot</code> 帳號，只放 XBot 的 SSH 公鑰，不需要你的任何密碼。
            </Fact>
            <Fact icon={Lock}>不開任何公開埠：XBot 只用 SSH 連進來，Bot 的畫面埠只綁 127.0.0.1。</Fact>
            <Fact icon={TriangleAlert}>
              建議整台主機專給 XBot：<code>xbot</code> 在 docker 群組，等於有這台的最高權限。
            </Fact>
          </div>
          <div className="flex items-end gap-3 border-t border-line pt-4 max-sm:flex-col max-sm:items-stretch">
            <TextInput
              label="這台主機的位址"
              hint="指令跑完會印出來：Tailscale 名稱或 IP（例：cloud-01、100.101.7.23），不是 22 埠時加上 :埠號"
              placeholder="cloud-01"
              value={address}
              error={addrErr || undefined}
              onChange={(e) => setAddress(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter' && !e.nativeEvent.isComposing) void check()
              }}
              wrapperClassName="flex-1"
            />
            <Button variant="primary" icon={ArrowRight} onClick={() => void check()} loading={checking} className="mb-[22px] max-sm:mb-0">
              我貼好了，開始檢查
            </Button>
          </div>
        </Card>
      )}
      {step === 2 && (
        <Card
          title="2 · XBot 自動檢查"
          sub={checking ? `正在連 ${address}…不用做什麼，等它跑完。` : res && !res.report.ok ? '有項目沒過：照紅字修好後按「重新檢查」。' : '不用做什麼，等它跑完。'}
        >
          {checking && !res && (
            <p className="m-0 flex items-center gap-2 text-ui-sm text-ink-2">
              <Icon icon={LoaderCircle} size={16} className="animate-spin" /> 檢查 SSH、Docker、資源、架構、轉發…
            </p>
          )}
          {res && <Checks items={items} progress={loading ? progress : undefined} />}
          <p className="m-0 text-meta text-ink-3">每台主機都用同一個電腦映像，所以 Bot 可以在主機之間搬家。模型金鑰不會放到這台。</p>
          <div className="flex items-center gap-3 max-sm:flex-wrap">
            <Button icon={ArrowLeft} onClick={() => setStep(1)}>
              上一步
            </Button>
            <span className="flex-1 text-meta text-ink-2">{loading ? '映像還在傳也可以先取名，傳完會自動完成' : ''}</span>
            {res && !res.report.ok && (
              <Button icon={RefreshCw} onClick={() => void check()} loading={checking}>
                重新檢查
              </Button>
            )}
            <Button variant="primary" icon={ArrowRight} disabled={!passed || checking} onClick={() => setStep(3)}>
              下一步：取名
            </Button>
          </div>
        </Card>
      )}
      {step === 3 && res?.added && (
        <Card title="3 · 取名與設定" sub="這些之後都能在主機清單改。">
          <Group>
            <Row title="主機名稱" sub="給你自己認的名字，例：cloud-01、辦公室電腦" wrap>
              <TextInput label="主機名稱" hideLabel value={name} error={nameErr || undefined} maxLength={40} onChange={(e) => setName(e.target.value)} wrapperClassName="w-[240px] max-sm:w-full" />
            </Row>
            <Row title="同時開機上限" sub="每台 Bot 電腦用 2 CPU／3 GB；滿了新工作會排隊「等電腦空出來」。">
              <Counter label="同時開機上限" value={limit} onChange={setLimit} />
            </Row>
            <Row title="控制中心" sub="XBot 服務還是在這台電腦；Bot 的電腦放到新主機時，模型金鑰仍只留在控制中心。" />
          </Group>
          <div className="flex items-center gap-3">
            <Button icon={ArrowLeft} onClick={() => setStep(2)}>
              上一步
            </Button>
            <span className="flex-1" />
            <Button variant="primary" icon={Check} onClick={() => void finish()} loading={saving}>
              完成
            </Button>
          </div>
        </Card>
      )}
    </>
  )
}

function AddHeader() {
  return (
    <SectionHeader
      title="新增主機"
      sub="把你自己的 VPS、公司伺服器或另一台電腦加進來，Bot 的電腦就能放在那裡。建議只經 Tailscale 連線，不開公開埠。"
      aside={
        <a href="/settings/computers" onClick={linkClick('/settings/computers')} className="flex flex-none items-center gap-1.5 whitespace-nowrap pt-1 text-ui-sm text-ink-2 hover:text-ink max-sm:hidden">
          <Icon icon={ArrowLeft} size={16} />
          主機清單
        </a>
      }
    />
  )
}

/** /settings/computers/hosts/:id */
export function HostDetailPage({ hostId }: { hostId: string }) {
  const toast = useToast()
  const hs = useHosts()
  const h = hs.hosts?.find((x) => x.id === hostId)
  const remove = useRemoveHost(() => navigate('/settings/computers', { replace: true }))
  const [checking, setChecking] = useState(false)
  const [report, setReport] = useState<Report | null>(null)
  const [draft, setDraft] = useState<string | null>(null)
  const [nameErr, setNameErr] = useState('')

  if (!hs.hosts) return <p className="m-0 text-ui-sm text-ink-2">讀取中…</p>
  if (!h) return <p className="m-0 rounded-group bg-card px-4 py-3 text-ui-sm text-ink-2">找不到這台主機（可能已經移除）。</p>

  const save = async (patch: { name?: string; max_running?: number }) => {
    setNameErr('')
    try {
      await hostsSetLimits({ id: h.id, ...patch })
      void loadHosts()
    } catch (err) {
      const f = fieldError(err, 'name')
      if (f) setNameErr(f)
      else toast.show({ kind: 'error', text: errText(err) })
    }
  }
  const recheck = async (updateImage: boolean) => {
    setChecking(true)
    try {
      const r = await hostsCheck({ id: h.id, update_image: updateImage })
      setReport(r.report)
      void loadHosts()
    } catch (err) {
      toast.show({ kind: 'error', text: errText(err) })
    } finally {
      setChecking(false)
    }
  }
  const p = imageProgress(h, hs as HostsState)
  const r = h.resources
  const needImage = report?.items.some((it) => it.id === 'image' && it.status === 'pending')
  return (
    <>
      <SectionHeader
        title={h.name}
        sub={hostSub(h, hs)}
        aside={
          <a href="/settings/computers" onClick={linkClick('/settings/computers')} className="flex flex-none items-center gap-1.5 whitespace-nowrap pt-1 text-ui-sm text-ink-2 hover:text-ink max-sm:hidden">
            <Icon icon={ArrowLeft} size={16} />
            主機清單
          </a>
        }
      />
      <Block title="狀態">
        <Group>
          <Row title="連線" sub={h.status_error || (h.kind === 'local' ? '本機 Docker' : `ssh://${h.user || 'xbot'}@${h.address}${h.port && h.port !== 22 ? ':' + h.port : ''}`)}>
            <span className="flex items-center gap-2">
              <HostIcon h={h} />
              <HostStatusChip h={h} />
            </span>
          </Row>
          {h.kind !== 'local' && (
            <Row
              title="最後在線"
              sub={h.health ? `已連線 ${h.health.connects} 次 · 往返 ${h.health.rtt_ms} ms · 轉發 ${h.health.reverse_forwards + h.health.local_forwards} 條` : undefined}
            >
              <span className="text-ui-sm text-ink-2">{h.status === 'online' ? '現在' : ago(h.last_seen_at)}</span>
            </Row>
          )}
          <Row title="資源" sub={r?.os || undefined}>
            <span className="text-right text-ui-sm text-ink-2">
              {r?.cpus ? `${r.cpus} 核 · ${gb(r.mem_bytes)}` : '—'}
              {r?.disk_free_bytes ? ` · 可用 ${gb(r.disk_free_bytes)}` : ''}
              <br />
              {[h.arch, r?.docker_version && `Docker ${r.docker_version}`].filter(Boolean).join(' · ')}
            </span>
          </Row>
          {p && (
            <Row title="電腦映像" sub={`${gb(p.done)} / ${gb(p.total)}`}>
              <span className="flex w-[160px] items-center gap-2">
                <span className="h-1.5 flex-1 overflow-hidden rounded-full bg-line">
                  <span className="block h-full rounded-full bg-accent" style={{ width: `${p.percent}%` }} />
                </span>
                <span className="text-meta">{p.percent}%</span>
              </span>
            </Row>
          )}
        </Group>
      </Block>
      <Block title="設定">
        <Group>
          <Row title="主機名稱" wrap>
            <TextInput
              label="主機名稱"
              hideLabel
              value={draft ?? h.name}
              maxLength={40}
              error={nameErr || undefined}
              onChange={(e) => setDraft(e.target.value)}
              onBlur={() => {
                const n = (draft ?? '').trim()
                if (n && n !== h.name) void save({ name: n }).then(() => setDraft(null))
                else setDraft(null)
              }}
              wrapperClassName="w-[240px] max-sm:w-full"
            />
          </Row>
          <Row title="同時開機上限" sub={h.max_running === 0 ? '不限台數' : `目前開機 ${h.running} 台；滿了新工作會排隊`}>
            <Counter label="同時開機上限" min={h.kind === 'local' ? 0 : 1} value={h.max_running} onChange={(v) => void save({ max_running: v })} />
          </Row>
        </Group>
      </Block>
      <Block
        title="上面的 Bot"
        aside={
          h.queued > 0 ? (
            <StatusChip tone="warning" size="sm">
              {h.queued} 件工作在排隊
            </StatusChip>
          ) : undefined
        }
      >
        <Group>
          {h.bots.length === 0 && <Row title="還沒有 Bot" sub="新 Bot 會依「新 Bot 放在」自動擺放，也可以在 Bot 設定指定主機。" />}
          {h.bots.map((b) => (
            <div key={b.id} className="flex items-center gap-3 border-line px-4 py-2.5 not-first:border-t">
              <Avatar {...look(b)} size={28} blink={false} />
              <span className="flex-1 text-ui">{b.name}</span>
              <span className="text-[13px] text-ink-2">{h.status === 'offline' ? '主機離線' : b.running ? '開機中' : b.state === 'none' ? '還沒開機' : '休眠'}</span>
            </div>
          ))}
        </Group>
      </Block>
      {report && (
        <Block title="檢查結果">
          <Checks items={report.items} />
        </Block>
      )}
      <div className="flex flex-wrap items-center gap-2">
        <Button icon={RefreshCw} onClick={() => void recheck(false)} loading={checking}>
          {h.status === 'offline' || h.status === 'failed' ? '重新連線並檢查' : '重新檢查'}
        </Button>
        {h.kind !== 'local' && needImage && (
          <Button onClick={() => void recheck(true)} disabled={checking}>
            更新電腦映像
          </Button>
        )}
        <span className="flex-1" />
        <Button variant="danger" disabled={h.control_center} onClick={() => remove.ask(h)}>
          移除這台主機
        </Button>
      </div>
      {h.control_center && <p className="m-0 text-meta text-ink-3">控制中心所在的主機不能移除；要換控制中心，請把 XBot 服務裝到另一台（make deploy）。</p>}
      <span className="sr-only-text">{STATUS_LABEL[h.status]}</span>
      {remove.dialog}
    </>
  )
}
