import { useEffect, useState, type ReactNode } from 'react'
import { ChevronRight } from 'lucide-react'
import { modelsGet, usageCaps, type ModelsGetOutput, type UsageCapsOutput } from '../api/generated'
import { linkClick } from '../router'
import { Icon } from '../ui/Icon'
import { Select } from '../ui/Menu'
import { Switch } from '../ui/Controls'
import { InstallRow } from '../mobile/PhoneSettings'
import { usePushSettings } from '../mobile/usePush'
import { connectionKind, useInstall } from '../mobile/pwa'
import { Meter } from './charts'
import { usd } from './format'
import { setTheme, useTheme, type ThemeChoice } from './theme'

// 手機的設定總覽（L06）：5 區分組，常改的（主題、主要模型、本月花費）直接看得到，其他點進去。

function Label({ children }: { children: ReactNode }) {
  return <h2 className="m-0 px-1 pt-1 text-meta text-ink-2">{children}</h2>
}

function Card({ children }: { children: ReactNode }) {
  return <div className="flex flex-col overflow-hidden rounded-group bg-card">{children}</div>
}

const ROW = 'flex min-h-12 items-center gap-2 border-line px-3.5 not-first:border-t'

function LinkRow({ to, title, note }: { to: string; title: string; note?: string }) {
  return (
    <a href={to} onClick={linkClick(to)} className={ROW + ' text-ink'}>
      <span className="flex-1 text-ui">{title}</span>
      {note && <span className="max-w-[60%] truncate text-[13px] text-ink-2">{note}</span>}
      <span className="text-ink-3">
        <Icon icon={ChevronRight} size={18} />
      </span>
    </a>
  )
}

export function SettingsOverview() {
  const theme = useTheme()
  const [models, setModels] = useState<ModelsGetOutput | null>(null)
  const [caps, setCaps] = useState<UsageCapsOutput | null>(null)
  useEffect(() => {
    modelsGet({})
      .then(setModels)
      .catch(() => setModels(null))
    usageCaps({})
      .then(setCaps)
      .catch(() => setCaps(null))
  }, [])
  const g = caps?.global
  const push = usePushSettings()
  const install = useInstall()
  return (
    <div className="flex flex-col gap-2.5">
      <a href="/settings/advanced/people" onClick={linkClick('/settings/advanced/people')} className="flex items-center gap-3 rounded-group bg-card px-3.5 py-3 text-ink">
        <span aria-hidden="true" className="flex size-10 items-center justify-center rounded-full bg-control text-ui text-ink-2">
          你
        </span>
        <span className="flex min-w-0 flex-1 flex-col">
          <span className="text-ui">你</span>
          <span className="truncate text-meta text-ink-2">擁有者 · {window.location.host}</span>
        </span>
        <span className="text-ink-3">
          <Icon icon={ChevronRight} size={18} />
        </span>
      </a>
      <Label>一般</Label>
      <Card>
        <div className={ROW + ' py-1.5'}>
          <span className="flex-1 text-ui">主題</span>
          <Select<ThemeChoice>
            label="主題"
            hideLabel
            look="compact"
            value={theme}
            onChange={setTheme}
            options={[
              { value: 'dark', label: '深色' },
              { value: 'light', label: '淺色' },
              { value: 'system', label: '跟隨系統' },
            ]}
            className="w-[140px]"
          />
        </div>
        <LinkRow to="/settings/general" title="語言" note="繁體中文" />
        <div className={ROW}>
          <span className="flex min-w-0 flex-1 flex-col">
            <span className="text-ui">等你的事推播到這支手機</span>
            {!push.supported && <span className="text-meta text-ink-3">{install.ios ? '先「加入主畫面」，從主畫面打開才能收推播' : '這個瀏覽器不支援推播'}</span>}
          </span>
          <Switch label="等你的事推播到這支手機" checked={push.on} disabled={!push.supported || push.busy} onChange={push.setOn} />
        </div>
        <div className={ROW}>
          <span className="flex-1 text-ui">工作完成也通知</span>
          <Switch label="工作完成也通知" checked={push.on && push.notifyDone} disabled={!push.on || push.busy} onChange={push.setNotifyDone} />
        </div>
        {push.on && (
          <button type="button" onClick={() => void push.test()} className={ROW + ' text-left text-ui text-accent-ink'}>
            送一則測試推播
          </button>
        )}
      </Card>
      <Label>AI 模型</Label>
      <Card>
        <LinkRow to="/settings/models" title="主要模型" note={models?.default?.model ?? '還沒設定'} />
        <LinkRow to="/settings/models/bots" title="各個 Bot 的引擎與權限" />
      </Card>
      <Label>電腦與裝置</Label>
      <Card>
        <LinkRow to="/settings/computers" title="電腦與主機" note="閒置 30 分鐘休眠" />
        <a href="/settings/computers" onClick={linkClick('/settings/computers')} className={ROW + ' text-ink'}>
          <span className="flex-1 text-ui">這支手機</span>
          <span className="flex items-center gap-1.5 text-[13px] text-ink-2">
            <span className="size-2 rounded-full bg-success" />
            已連線 · {connectionKind()}
          </span>
          <span className="text-ink-3">
            <Icon icon={ChevronRight} size={18} />
          </span>
        </a>
        <InstallRow className={ROW + ' py-2'} />
      </Card>
      <Label>用量與花費</Label>
      <Card>
        <a href="/settings/usage" onClick={linkClick('/settings/usage')} className="flex flex-col gap-2 px-3.5 py-3 text-ink">
          <span className="flex items-baseline gap-2">
            <span className="flex-1 text-ui">本月花費</span>
            <span className="text-ui">{usd(g?.spent_month_usd ?? 0)}</span>
            <span className="text-meta text-ink-2">{g?.monthly_usd ? `／上限 ${usd(g.monthly_usd)}` : '／不限'}</span>
          </span>
          {g?.monthly_usd ? <Meter ratio={g.spent_month_usd / g.monthly_usd} label="本月花費占上限" /> : null}
        </a>
      </Card>
      <Card>
        <LinkRow to="/settings/advanced" title="進階" note="備份、成員、外部代理" />
      </Card>
    </div>
  )
}
