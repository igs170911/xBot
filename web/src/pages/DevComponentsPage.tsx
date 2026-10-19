import { useState } from 'react'
import {
  Bell,
  ChevronRight,
  Crown,
  Ellipsis,
  Hand,
  KeyRound,
  Monitor,
  Pencil,
  Phone,
  Pin,
  Plus,
  Search,
  Send,
  Trash,
  Users,
} from 'lucide-react'
import { Avatar, Mascot } from '../ui/Avatar'
import { COLORS, COLOR_IDS, COLOR_LABELS, SHAPE_IDS, SHAPE_LABELS, type AvatarState } from '../ui/avatar-data'
import { Button, IconButton } from '../ui/Button'
import { Panel, OptionList, SettingsGroup, SettingsLink, SettingsRow } from '../ui/Card'
import { Bubble, Composer, TypingDots } from '../ui/Chat'
import { Checkbox, SegmentedControl, Slider, Switch } from '../ui/Controls'
import { ConfirmDialog, Dialog } from '../ui/Dialog'
import { TextInput, Textarea } from '../ui/Field'
import { Icon } from '../ui/Icon'
import { MenuButton, MenuList, Select, type MenuItem } from '../ui/Menu'
import { ChipButton, EventPill, JudgeTag, Kbd, SimplifiedTag, StatusChip, ToolPill } from '../ui/Pill'
import { ToastView } from '../ui/Toast'
import { useToast } from '../ui/toast-context'
import { BOTS } from '../shell/sample'

// /dev/components：列出所有元件與頭像矩陣，給 Playwright 截圖和 N01–N03 畫板對照。

const MENU: MenuItem[] = [
  { label: '釘選', icon: Pin },
  { label: '設為幕僚長', icon: Crown },
  { label: '標為未讀', icon: Bell },
  { label: '編輯設定', icon: Pencil, separator: true },
  { label: '分享登入給其他 Bot', icon: KeyRound },
  { label: '教它一件事', icon: Hand },
  { label: '刪除（需要你核定）', icon: Trash, separator: true, danger: true },
]

const MATRIX_ROWS: { id: AvatarState; zh: string; warn?: 'warning' | 'danger' }[] = [
  { id: 'idle', zh: '待命 · 圓點' },
  { id: 'working', zh: '工作 · 看旁邊' },
  { id: 'notifying', zh: '等你 · 橘點', warn: 'warning' },
  { id: 'alerting', zh: '出錯 · 睜大', warn: 'danger' },
  { id: 'happy', zh: '完成 · 開心' },
  { id: 'drowsy', zh: '無聊 · 半閉' },
  { id: 'sleeping', zh: '休息 · 閉眼' },
]

const EYES: { st: AvatarState; zh: string; ids: string }[] = [
  { st: 'idle', zh: '圓點', ids: 'idle · listening · notifying' },
  { st: 'working', zh: '看旁邊', ids: 'working · searching · thinking · curious · confused' },
  { st: 'happy', zh: '開心 ^', ids: 'happy · excited · celebrate · laughing · proud · playful · shy' },
  { st: 'surprised', zh: '睜大', ids: 'surprised · scared · alerting' },
  { st: 'drowsy', zh: '半閉', ids: 'drowsy · bored · waking · suspicious · sad · angry' },
  { st: 'sleeping', zh: '閉眼', ids: 'sleeping' },
]

const SURFACES = [
  ['頁面', 'bg-app', '#070707'],
  ['側欄／面板', 'bg-panel', '#111111'],
  ['內嵌', 'bg-inset', '#191919'],
  ['卡片', 'bg-card', '#262626'],
  ['控制項', 'bg-control', '#2F2F2F'],
  ['懸停', 'bg-hover', '#3D3D3D'],
  ['細線', 'bg-line', '#333333'],
  ['你的泡泡', 'bg-bubble-user', '#5A5A5A'],
]
const SEMANTIC = [
  ['文字', 'text-ink', '#FCFCFC'],
  ['次要文字', 'text-ink-2', '#FCFCFC99'],
  ['淡文字', 'text-ink-3', '#FCFCFC90'],
  ['強調', 'text-accent', '#1084FE'],
  ['強調文字', 'text-accent-ink', '#7FC0FF'],
  ['完成', 'text-success', '#38D591'],
  ['危險', 'text-danger', '#FF5667'],
  ['等你', 'text-warning-ink', '#FF9800 / #FFB547'],
]

function Header({ code, title, note }: { code: string; title: string; note: string }) {
  return (
    <header className="flex flex-wrap items-end gap-x-6 gap-y-2">
      <div className="flex-1">
        <p className="m-0 mb-1 text-[13px] text-ink-3">XBot 設計系統 · {code}</p>
        <h1 className="m-0 text-[28px] leading-[1.2]">{title}</h1>
      </div>
      <p className="m-0 max-w-[720px] text-sub text-ink-2 md:text-right">{note}</p>
    </header>
  )
}

function TokensBoard() {
  return (
    <section aria-labelledby="n01" className="flex flex-col gap-5" data-testid="board-n01">
      <span id="n01" className="sr-only-text">
        色彩與字型
      </span>
      <Header code="N01" title="Midnight 色彩、字型與尺寸" note="深色是預設；黑白為底，Bot 是顏色；警示色只給等你的事。粉圓只有一種字重，階層靠字級和顏色。" />
      <Panel title="Midnight · 表面" aside="由深到淺，一層疊一層">
        <div className="grid grid-cols-4 gap-3 lg:grid-cols-8">
          {SURFACES.map(([name, cls, hex]) => (
            <div key={name} className="flex flex-col gap-[3px]">
              <span className={`h-11 rounded-field border border-line ${cls}`} />
              <span className="text-sub">{name}</span>
              <span className="font-mono text-tab text-ink-2">{hex}</span>
            </div>
          ))}
        </div>
        <div className="h-px bg-divider" />
        <div className="grid grid-cols-4 gap-3 lg:grid-cols-8">
          {SEMANTIC.map(([name, cls, hex]) => (
            <div key={name} className="flex flex-col gap-[3px]">
              <span className="flex h-11 items-center justify-center gap-2 rounded-field border border-line bg-inset">
                <span className={`text-[15px] ${cls}`}>Aa</span>
              </span>
              <span className="text-sub">{name}</span>
              <span className="font-mono text-tab text-ink-2">{hex}</span>
            </div>
          ))}
        </div>
      </Panel>
      <Panel title="字級 · 粉圓 Huninn 400">
        {[
          ['30', 'text-display', '早安，5 件等你'],
          ['22', 'text-page', '待我處理'],
          ['17', 'text-title', '帳房想付款'],
          ['15.5', 'text-name', '小秘 · 幕僚長'],
          ['15', 'text-body', '收到，我先請阿哲抓價格。'],
          ['13.5', 'text-sub text-ink-2', '工作中…彙整三家競品定價'],
          ['12.5', 'text-meta text-ink-3', '今天 下午 2:58 · 第 2/6 棒'],
          ['11.5', 'text-tab text-ink-2', '待我處理　對話　辦公室　行事曆'],
          ['mono', 'font-mono text-meta text-ink-2', '/workspace · ⌘K'],
        ].map(([size, cls, sample]) => (
          <div key={size} className="flex items-baseline gap-3 border-t border-divider pt-1">
            <span className="w-11 flex-none font-mono text-tab text-ink-3">{size}</span>
            <span className={`min-w-0 flex-1 truncate leading-[1.3] ${cls}`}>{sample}</span>
          </div>
        ))}
      </Panel>
    </section>
  )
}

function MascotBoard() {
  return (
    <section className="flex flex-col gap-5" data-testid="board-n02">
      <Header code="N02" title="Bot 頭像：Grok 平面吉祥物 · 10 種形狀" note="一個平面實色剪影＋兩顆眼睛，沒有嘴巴。10 種形狀 × 10 色分出是誰；眼睛和灰字說它在做什麼。" />
      <div className="flex flex-col gap-5 xl:flex-row">
        <Panel title="品牌吉祥物" className="flex-none xl:w-[340px]">
          <p className="m-0 -mt-2 text-[13px] text-ink-2">綠色游標，不帶小點；新 Bot 預設也是它。</p>
          <div className="grid grid-cols-4 gap-2">
            {(
              [
                ['lazy', '待命（預設）'],
                ['happy', '開心'],
                ['thinking', '思考'],
                ['sleep', '睡著'],
              ] as const
            ).map(([pose, label]) => (
              <div key={pose} className="flex flex-col items-center gap-1 rounded-menu bg-inset px-1 pb-2 pt-2.5">
                <Mascot size={48} pose={pose} />
                <span className="whitespace-nowrap text-meta">{label}</span>
              </div>
            ))}
          </div>
          <div className="flex flex-col gap-0.5">
            <h3 className="m-0 mb-1 text-ui">眼睛 6 種（狀態都對到這幾種）</h3>
            {EYES.map((e) => (
              <div key={e.st} className="flex min-h-[31px] items-center gap-2.5 border-t border-divider">
                <Avatar color="green" shape="circle" state={e.st} dot="none" size={26} />
                <span className="w-[50px] flex-none text-[13px]">{e.zh}</span>
                <span className="min-w-0 flex-1 font-mono text-[10.5px] leading-[1.35] text-ink-3">{e.ids}</span>
              </div>
            ))}
          </div>
        </Panel>

        <Panel title="10 種形狀 × 眼睛 × 顏色" aside="任何形狀配任何顏色；只有等你、出錯有小點" className="min-w-0 flex-1">
          <div tabIndex={0} role="region" aria-label="元件表" className="overflow-x-auto">
            <table className="w-full min-w-[640px] border-collapse" aria-label="頭像形狀、眼睛與顏色" data-testid="avatar-matrix">
              <thead>
                <tr>
                  <th className="w-24">
                    <span className="sr-only-text">項目</span>
                  </th>
                  {SHAPE_IDS.map((id) => (
                    <th key={id} scope="col" className="pb-1.5 font-normal leading-[1.3]">
                      <span className={`block whitespace-nowrap text-[13px] ${id === 'cursor' ? 'text-accent-ink' : ''}`}>
                        {SHAPE_LABELS[id]}
                        {id === 'cursor' && ' · 預設'}
                      </span>
                      <span className={`block font-mono text-[11px] ${id === 'cursor' ? 'text-accent-ink' : 'text-ink-3'}`}>{id}</span>
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {MATRIX_ROWS.map((r, i) => (
                  <tr key={r.id}>
                    <th scope="row" className="border-t border-divider text-left font-normal leading-[1.3]">
                      <span className={`block text-sub ${r.warn === 'warning' ? 'text-warning-ink' : r.warn === 'danger' ? 'text-danger' : ''}`}>{r.zh}</span>
                      <span className="block font-mono text-[11px] text-ink-3">{r.id}</span>
                    </th>
                    {SHAPE_IDS.map((shape, j) => (
                      <td key={shape} className="h-[47px] border-t border-divider">
                        <span className="flex justify-center">
                          <Avatar shape={shape} color={COLOR_IDS[(i + j) % 10]} state={r.id} dot="auto" size={38} />
                        </span>
                      </td>
                    ))}
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          <div className="flex flex-wrap items-center gap-x-3.5 gap-y-1 border-t border-divider pt-2 text-meta text-ink-2">
            <span>10 色</span>
            {COLOR_IDS.map((c) => (
              <span key={c} className="inline-flex items-center gap-[5px]">
                <span className="size-[11px] rounded-full" style={{ background: COLORS[c] }} />
                {COLOR_LABELS[c]}
              </span>
            ))}
          </div>
        </Panel>
      </div>

      <Panel title="示範角色（全站一致）" aside="每個 Bot 一組形狀＋顏色，到哪裡都一樣">
        <div className="grid grid-cols-2 gap-3 md:grid-cols-4 xl:grid-cols-8">
          {Object.values(BOTS).map((m) => (
            <div key={m.id} className="flex min-w-0 items-center gap-2.5 rounded-group bg-inset p-3">
              <Avatar shape={m.shape} color={m.color} state={m.state} dot={m.dot} ring="#191919" size={40} />
              <span className="flex min-w-0 flex-col leading-[1.35]">
                <span className="whitespace-nowrap text-body">{m.name}</span>
                <span className="truncate text-[12px] text-ink-3">{m.role}</span>
                <span className={`whitespace-nowrap text-meta ${m.dot === 'waiting' ? 'text-warning-ink' : 'text-ink-2'}`}>{m.status}</span>
              </span>
            </div>
          ))}
        </div>
      </Panel>

      <div className="grid gap-5 lg:grid-cols-[1fr_1.15fr_1fr]">
        <Panel title="尺寸">
          <div className="flex items-end justify-between gap-1.5">
            {(
              [
                [16, '行內', 'working', 'blue', 'cursor'],
                [24, '群組疊放', 'working', 'orange', 'blob'],
                [32, '側欄列', 'working', 'cyan', 'star'],
                [40, '卡片', 'notifying', 'yellow', 'hexagon'],
                [48, '列表', 'working', 'pink', 'circle'],
                [96, '外觀預覽', 'happy', 'blue', 'cursor'],
              ] as const
            ).map(([n, use, st, c, b]) => (
              <div key={n} className="flex flex-col items-center gap-2">
                <span className="flex h-24 items-end">
                  <Avatar shape={b} color={c} state={st} size={n} />
                </span>
                <span className="font-mono text-[12px]">{n} px</span>
                <span className="whitespace-nowrap text-[12px] text-ink-3">{use}</span>
              </div>
            ))}
          </div>
        </Panel>
        <Panel title="改用圖片" aside="形狀就是裁切框">
          <div className="grid grid-cols-5 items-center gap-2">
            <div className="flex flex-col items-center gap-1.5">
              <Avatar color="green" shape="cursor" state="happy" dot="none" size={62} />
              <span className="text-[13px]">吉祥物</span>
            </div>
            {(
              [
                ['圓', 'circle', 'none'],
                ['圓角', 'squircle', 'none'],
                ['六角', 'hexagon', 'waiting'],
                ['星星', 'star', 'none'],
              ] as const
            ).map(([zh, b, dot]) => (
              <div key={b} className="flex flex-col items-center gap-1.5">
                <Avatar shape={b} img="sample" dot={dot} size={62} />
                <span className="text-[13px]">{zh}</span>
              </div>
            ))}
          </div>
          <p className="m-0 text-meta text-ink-2">用圖片時不疊眼睛，狀態只看灰字和小點。</p>
        </Panel>
        <Panel title="規則">
          {(
            [
              ['平面、只有眼睛、沒有嘴巴、跟 Grok Bot 一樣', 'green', 'cursor', 'idle'],
              ['10 形狀 × 10 色分人：一個 Bot 一組，全站一致', 'purple', 'diamond', 'happy'],
              ['眼睛與灰字分狀態（看旁邊＝工作中…）', 'blue', 'cursor', 'working'],
              ['只有等你（橘）、出錯（紅）有小點', 'pink', 'circle', 'notifying'],
              ['形狀也是上傳圖片的裁切框', 'teal', 'drop', 'curious'],
            ] as const
          ).map(([zh, color, shape, st]) => (
            <div key={zh} className="flex min-h-10 items-center gap-3">
              <Avatar color={color} shape={shape} state={st} size={30} />
              <span className="flex-1 text-ui-sm">{zh}</span>
            </div>
          ))}
        </Panel>
      </div>
    </section>
  )
}

function ComponentsBoard() {
  const toast = useToast()
  const [sw, setSw] = useState({ a: true, b: false })
  const [ck, setCk] = useState({ a: true, b: false })
  const [asks, setAsks] = useState({ pay: true, send: true, del: false })
  const [seg, setSeg] = useState<'local' | 'cloud' | 'off'>('local')
  const [budget, setBudget] = useState(8000)
  const [opt, setOpt] = useState('A')
  const [model, setModel] = useState('sonnet')
  const [theme, setTheme] = useState('dark')
  const [notify, setNotify] = useState(true)
  const [expanded, setExpanded] = useState(false)
  const [dialog, setDialog] = useState(false)
  const [confirm, setConfirm] = useState(false)
  const [busy, setBusy] = useState(false)

  return (
    <section className="flex flex-col gap-5" data-testid="board-n03">
      <Header code="N03" title="元件（Grok 風格 · Midnight）" note="全部是自畫控制項，不用瀏覽器原生外觀。圖示一律 Lucide，線寬 1.75。可點的東西：桌面 ≥ 36，手機 ≥ 44。" />
      <div className="grid items-start gap-5 lg:grid-cols-3">
        <div className="flex min-w-0 flex-col gap-5">
          <Panel title="按鈕（膠囊）">
            <div className="flex flex-wrap items-center gap-2">
              <Button variant="primary">允許這次</Button>
              <Button>一律允許</Button>
              <Button variant="outline" icon={Plus}>
                對話串
              </Button>
              <Button variant="danger">停止</Button>
            </div>
            <div className="flex flex-wrap items-center gap-2">
              <Button variant="primary" size="sm">
                小 · 主要
              </Button>
              <Button size="sm">小 · 次要</Button>
              <IconButton icon={Phone} label="通話" />
              <IconButton icon={Monitor} label="電腦" pressed />
              <IconButton icon={Ellipsis} label="更多" />
              <Button variant="danger-solid">刪除</Button>
            </div>
            <div className="flex flex-wrap items-center gap-2">
              <Button variant="ghost">復原</Button>
              <Button variant="primary" loading>
                處理中…
              </Button>
              <Button disabled>停用</Button>
            </div>
            <p className="m-0 text-meta text-ink-3">主要 · 次要 · 外框 · 文字 · 危險（#2F2F2F 底紅字）· 小 30 · 圖示 36。實心紅只在確認對話框。</p>
          </Panel>

          <Panel title="輸入框與下拉選單">
            <TextInput label="預設" placeholder="輸入你的回答" />
            <TextInput label="聚焦" defaultValue="晨光選物" data-testid="focus-demo" />
            <TextInput label="出錯" defaultValue="xbot" error="名稱至少 3 個中文字" />
            <TextInput label="搜尋" hideLabel icon={Search} placeholder="搜尋" />
            <Textarea label="多行" placeholder="交代它要做的事…" rows={2} />
            <Select
              label="下拉選單 · 模型"
              value={model}
              onChange={setModel}
              options={[
                { value: 'sonnet', label: 'Claude Sonnet 5.5' },
                { value: 'opus', label: 'Claude Opus 5.5' },
                { value: 'gpt', label: 'GPT-6' },
              ]}
            />
          </Panel>

          <Panel title="開關 · 勾選 · 分段 · 滑桿">
            <div className="flex items-center gap-[22px]">
              <Switch checked={sw.a} onChange={(v) => setSw({ ...sw, a: v })} label="開關 A">
                {sw.a ? '開' : '關'}
              </Switch>
              <Switch checked={sw.b} onChange={(v) => setSw({ ...sw, b: v })} label="開關 B">
                {sw.b ? '開' : '關'}
              </Switch>
            </div>
            <div className="flex items-center gap-[22px]">
              <Checkbox checked={ck.a} onChange={(v) => setCk({ ...ck, a: v })}>
                已勾選
              </Checkbox>
              <Checkbox checked={ck.b} onChange={(v) => setCk({ ...ck, b: v })}>
                未勾選
              </Checkbox>
            </div>
            <SegmentedControl
              label="在哪裡跑"
              value={seg}
              onChange={setSeg}
              options={[
                { value: 'local', label: '這台主機' },
                { value: 'cloud', label: 'cloud-01（雲端）', weight: 1.5 },
                { value: 'off', label: '關閉' },
              ]}
            />
            <Slider label="每月花費上限" value={budget} min={0} max={20000} step={500} onChange={setBudget} format={(v) => `NT$${v.toLocaleString('en-US')}`} />
            <div className="flex flex-col gap-1.5">
              <span className="text-meta text-ink-3">勾選清單 · 這些事先問我</span>
              <div className="rounded-menu bg-inset">
                {(
                  [
                    ['pay', '花錢、付款', '預設'],
                    ['send', '對外發送訊息', '預設'],
                    ['del', '刪除檔案', ''],
                  ] as const
                ).map(([id, label, note], i) => (
                  <Checkbox
                    key={id}
                    checked={asks[id]}
                    onChange={(v) => setAsks({ ...asks, [id]: v })}
                    note={note}
                    className={`h-10 px-3 max-sm:h-11 ${i ? 'border-t border-line' : ''}`}
                  >
                    {label}
                  </Checkbox>
                ))}
              </div>
            </div>
            <button
              type="button"
              aria-expanded={expanded}
              onClick={() => setExpanded(!expanded)}
              className="flex h-10 items-center gap-2 border-t border-divider bg-transparent px-1 text-left text-ui-sm text-ink"
            >
              <span className={`text-ink-2 transition-transform ${expanded ? 'rotate-90' : ''}`}>
                <Icon icon={ChevronRight} size={18} />
              </span>
              <span className="flex-1">進階設定</span>
              <span className="text-meta text-ink-3">展開／收合 · 一般人不用動</span>
            </button>
          </Panel>
        </div>

        <div className="flex min-w-0 flex-col gap-5">
          <Panel title="A/B/C 選項卡">
            <div className="flex flex-col rounded-card bg-card px-[18px] pb-3.5 pt-4">
              <div className="text-[16px]">要不要也比較日本賣場？</div>
              <div className="mb-2 text-[13px] text-ink-2">選接近的，之後都能改。</div>
              <OptionList
                value={opt}
                onPick={setOpt}
                options={[
                  { key: 'A', label: '要，一起比較' },
                  { key: 'B', label: '不用，台灣就好' },
                  { key: 'C', label: '只比日本前三名' },
                ]}
              />
              <label className="mt-2.5 block">
                <span className="sr-only-text">或輸入你的回答</span>
                <input
                  type="text"
                  placeholder="或輸入你的回答"
                  className="h-11 w-full rounded-field bg-inset px-3 text-ui text-ink outline-none focus:shadow-[0_0_0_2px_var(--color-focus)]"
                />
              </label>
            </div>
          </Panel>

          <Panel title="工具膠囊 · 事件膠囊 · 工作列">
            <div className="flex flex-wrap gap-1.5">
              <ToolPill name="browser_open" />
              <ToolPill name="read_page" />
              <ToolPill name="sheet_write" status="running" />
            </div>
            <div className="flex flex-wrap gap-1.5">
              <EventPill done>來自阿哲的回覆 · 第 1/6 棒</EventPill>
              <EventPill icon={Send}>已傳請求給數據喵 · 第 2/6 棒</EventPill>
              <EventPill icon={Users}>群組第 2/4 輪</EventPill>
            </div>
            <ChipButton expanded={false} className="self-start">
              工作了 2 分鐘 · 6 個步驟
            </ChipButton>
          </Panel>

          <Panel title="泡泡與 Composer">
            <div className="flex flex-col gap-2">
              <p className="m-0 text-center text-meta text-ink-3">今天 下午 3:02</p>
              <Bubble from="me">幫我比較蝦皮三家競品的定價</Bubble>
              <Bubble from="bot">收到，我先請阿哲抓價格。</Bubble>
              <Bubble from="bot" name="阿哲 · 群組裡 Bot 訊息帶頭像和名字" bot={{ shape: 'blob', color: 'orange', state: 'happy' }}>
                三家是 NT$990、NT$1,080、NT$1,150。
              </Bubble>
              <TypingDots />
              <div className="mt-2">
                <Composer placeholder="交代小秘…" />
              </div>
            </div>
          </Panel>
        </div>

        <div className="flex min-w-0 flex-col gap-5">
          <Panel title="選單">
            <MenuList label="小秘" items={MENU} highlight={1} />
            <MenuButton
              label="小秘"
              items={MENU}
              trigger={(p) => (
                <Button size="sm" {...p}>
                  打開選單
                </Button>
              )}
            />
          </Panel>

          <Panel title="Toast">
            <ToastView kind="done" text="已允許這次 · 帳房繼續付款" action={{ label: '復原', onClick: () => {} }} />
            <ToastView kind="error" text="連線逾時，2 分鐘後自動重試" action={{ label: '現在重試', onClick: () => {} }} />
            <div className="flex flex-wrap gap-2">
              <Button size="sm" onClick={() => toast.show({ kind: 'done', text: '小秘完成了：競品定價表', bot: { shape: 'cursor', color: 'blue', state: 'happy' }, action: { label: '查看', onClick: () => {} } })}>
                顯示提示
              </Button>
              <Button size="sm" onClick={() => toast.show({ kind: 'error', text: '連線逾時，訊息還沒送出', action: { label: '重試', onClick: () => {} } })}>
                顯示錯誤
              </Button>
            </div>
          </Panel>

          <Panel title="對話框">
            <div className="flex flex-wrap gap-2">
              <Button size="sm" onClick={() => setDialog(true)}>
                打開對話框
              </Button>
              <Button size="sm" variant="danger" onClick={() => setConfirm(true)}>
                刪除 Bot…
              </Button>
            </div>
            <Dialog
              open={dialog}
              onClose={() => setDialog(false)}
              title="幫 Bot 取名字"
              description="名字會出現在側欄、群組和通知裡。"
              actions={
                <>
                  <Button onClick={() => setDialog(false)}>取消</Button>
                  <Button variant="primary" onClick={() => setDialog(false)}>
                    儲存
                  </Button>
                </>
              }
            >
              <TextInput label="名字" defaultValue="小秘" data-autofocus />
            </Dialog>
            <ConfirmDialog
              open={confirm}
              danger
              busy={busy}
              title="刪除「舊客服」？"
              description="對話和記憶會一起刪掉，不能復原。想保留資料可以改成封存。"
              confirmText="刪除"
              onCancel={() => setConfirm(false)}
              onConfirm={() => {
                setBusy(true)
                setTimeout(() => {
                  setBusy(false)
                  setConfirm(false)
                }, 1200)
              }}
            />
          </Panel>

          <Panel title="設定列群組">
            <SettingsGroup label="外觀">
              <SettingsRow title="主題">
                <Select
                  label="主題"
                  hideLabel
                  look="compact"
                  value={theme}
                  onChange={setTheme}
                  options={[
                    { value: 'dark', label: '深色' },
                    { value: 'light', label: '淺色' },
                  ]}
                />
              </SettingsRow>
              <SettingsRow title="等你的事要通知" sub="核定、提問、接手電腦">
                <Switch checked={notify} onChange={setNotify} label="等你的事要通知" />
              </SettingsRow>
              <SettingsLink title="進階" note="一般人不用動" />
            </SettingsGroup>
          </Panel>

          <Panel title="標註：簡化 · 快速判斷 · 狀態">
            <div className="flex flex-wrap items-center gap-2">
              <SimplifiedTag>簡化：6 步 → 2 步</SimplifiedTag>
              <span className="text-meta text-ink-3">放在被簡化畫板的右上角</span>
            </div>
            <div>
              <JudgeTag>已完成 · 92%</JudgeTag>
            </div>
            <div className="flex flex-wrap items-center gap-1.5">
              <StatusChip tone="accent">工作中</StatusChip>
              <StatusChip tone="warning">等你回覆</StatusChip>
              <StatusChip>排隊中</StatusChip>
              <span className="text-meta text-ink-3">待命 · 上午 10:42</span>
              <span className="ml-auto inline-flex gap-1">
                <Kbd>⌘K</Kbd>
                <Kbd>⌘⇧F</Kbd>
              </span>
            </div>
          </Panel>
        </div>
      </div>
    </section>
  )
}

export function DevComponentsPage() {
  return (
    <main className="flex flex-col gap-10 px-12 py-8 max-md:px-4 max-md:py-5">
      <ComponentsBoard />
      <MascotBoard />
      <TokensBoard />
    </main>
  )
}
