import { useEffect, useState, type ReactNode } from 'react'
import { ArrowRight, ArrowUp, Check, Hand, X } from 'lucide-react'
import { Avatar } from '../ui/Avatar'
import { Button, IconButton } from '../ui/Button'
import { Card, OptionList } from '../ui/Card'
import { Icon } from '../ui/Icon'
import { SimplifiedTag, StatusChip, type Tone } from '../ui/Pill'
import { BOTS } from '../shell/sample'
import { fetchHealth, type Health } from '../health'

// 待我處理（design/Main.dc.html）的版型示範。資料是寫死的示範內容；真正的清單與核准在 C12 接上。

function CardHead({ bot, state, dot, chip, tone, sub, aside }: { bot: string; state?: string; dot?: 'waiting' | 'none' | 'queued'; chip: ReactNode; tone: Tone; sub: ReactNode; aside?: ReactNode }) {
  const b = BOTS[bot]
  return (
    <div className="flex items-center gap-3">
      <Avatar shape={b.shape} color={b.color} state={state ?? b.state} dot={dot ?? 'none'} ring="#262626" size={32} />
      <div className="flex min-w-0 flex-1 flex-col">
        <span className="flex items-center gap-2">
          <span className="text-body">{b.name}</span>
          <StatusChip size="sm" tone={tone}>
            {chip}
          </StatusChip>
        </span>
        <span className="text-[13px] text-ink-2">{sub}</span>
      </div>
      {aside}
    </div>
  )
}

type Decision = 'once' | 'always' | 'deny' | null

const DECISION = {
  once: { text: '已允許這次 · 帳房正在付款', chip: '已允許', tone: 'success' as Tone, ok: true },
  always: { text: '已一律允許「付款給晨光廣告」· 之後不再問', chip: '一律允許', tone: 'success' as Tone, ok: true },
  deny: { text: '已拒絕 · 已告訴帳房和小秘', chip: '已拒絕', tone: 'neutral' as Tone, ok: false },
}

const OPTIONS = [
  { key: 'A', label: '要，一起比較日本賣場' },
  { key: 'B', label: '不用，台灣三家就好' },
  { key: 'C', label: '只比日本前 3 名的商品' },
]

function Settled({ ok, text, undo, undoText = '復原' }: { ok: boolean; text: string; undo: () => void; undoText?: string }) {
  return (
    <div className={ok ? 'flex h-9 items-center gap-2 text-ui-sm text-success' : 'flex h-9 items-center gap-2 text-ui-sm text-ink-2'}>
      <Icon icon={ok ? Check : X} size={18} />
      <span className="flex-1">{text}</span>
      <Button size="sm" onClick={undo}>
        {undoText}
      </Button>
    </div>
  )
}

function ScreenThumb({ label, waiting, children }: { label: string; waiting?: boolean; children?: ReactNode }) {
  return (
    <span
      aria-hidden="true"
      className="flex h-[90px] items-center justify-center overflow-hidden rounded-field bg-[#F4F5F7] text-[11px] text-[#6B7280]"
      style={waiting ? { boxShadow: '0 0 0 1.5px #FF9800' } : undefined}
    >
      {children ?? label}
    </span>
  )
}

export function InboxPage() {
  const [decision, setDecision] = useState<Decision>(null)
  const [answer, setAnswer] = useState<string | null>(null)
  const [health, setHealth] = useState<Health | null | undefined>(undefined)
  useEffect(() => {
    fetchHealth().then(setHealth)
  }, [])

  const left = 5 - (decision ? 1 : 0) - (answer ? 1 : 0)
  const d = decision ? DECISION[decision] : null
  const answered = OPTIONS.find((o) => o.key === answer)

  return (
    <div className="flex flex-1 flex-wrap">
      <main className="min-w-0 flex-[999_1_560px] px-8 pb-2.5 pt-4 max-sm:px-4">
        <div className="mx-auto flex max-w-[780px] flex-col gap-2">
          <header className="flex flex-wrap items-end justify-between gap-x-6 gap-y-3 pb-1.5">
            <div className="flex flex-col gap-0.5">
              <div className="flex flex-wrap items-center gap-3">
                <h1 className="m-0 text-[24px] leading-[1.35]">{left > 0 ? `早安，有 ${left} 件事等你` : '早安，事情都處理完了'}</h1>
                <SimplifiedTag>簡化：核定 3 步 → 1 步</SimplifiedTag>
              </div>
              <span className="text-sub text-ink-2">10 月 17 日（六）· 2 個 Bot 工作中 · 今天完成 4 件 · 其他事 Bot 會自己處理</span>
            </div>
            <a href="/usage" className="flex w-[200px] flex-col gap-1.5 text-ink">
              <span className="flex justify-between text-meta text-ink-2">
                <span>本月花費</span>
                <span>$38 / $100</span>
              </span>
              <span className="block h-1 rounded-full bg-line">
                <span className="block h-1 w-[38%] rounded-full bg-accent" />
              </span>
            </a>
          </header>

          <Card>
            <CardHead
              bot="zhangfang"
              state={d ? (d.ok ? 'working' : 'idle') : 'notifying'}
              dot={d ? 'none' : 'waiting'}
              chip={d ? d.chip : '等你允許'}
              tone={d ? d.tone : 'warning'}
              sub="小秘交辦的工作 · 第 2/6 棒 · 3 分鐘前"
              aside={<a href="/bots/zhangfang" className="text-sub">打開對話</a>}
            />
            <p className="m-0">付款 NT$1,280 給晨光廣告（10 月廣告費）。帳單和投放紀錄已比對過，金額一致。</p>
            {d ? (
              <Settled ok={d.ok} text={d.text} undo={() => setDecision(null)} />
            ) : (
              <div className="flex flex-wrap gap-2">
                <Button variant="primary" onClick={() => setDecision('once')}>
                  允許這次
                </Button>
                <Button onClick={() => setDecision('always')}>
                  一律允許
                </Button>
                <Button variant="outline" onClick={() => setDecision('deny')}>
                  拒絕
                </Button>
              </div>
            )}
          </Card>

          <Card layout="flex flex-wrap items-center gap-4 py-3 pl-[18px] pr-3">
            <div className="flex min-w-0 flex-[999_1_320px] flex-col gap-2.5">
              <CardHead bot="xiaobian" dot="waiting" chip="需要你接手電腦" tone="warning" sub="發佈新品貼文 · 已暫停 8 分鐘" />
              <p className="m-0">登入社群後台要簡訊驗證碼，碼傳到你的手機。請接手電腦輸入，好了按「完成，交還」。</p>
              <div className="flex flex-wrap gap-2">
                <Button variant="primary" icon={Hand}>
                  接手電腦
                </Button>
                <Button variant="danger">
                  停止這件工作
                </Button>
              </div>
            </div>
            <span className="max-w-[216px] flex-[1_1_190px]">
              <ScreenThumb label="小編的電腦畫面" waiting>
                <span className="flex w-[132px] flex-col items-center gap-1 rounded-[6px] bg-white p-2 text-[8px] text-[#111827] shadow-sm">
                  輸入簡訊驗證碼
                  <span className="flex gap-[3px]">
                    {[0, 1, 2, 3, 4, 5].map((i) => (
                      <span key={i} className="h-4 w-[13px] rounded-[3px] border" style={{ borderColor: i ? '#D1D5DB' : '#FF9800' }} />
                    ))}
                  </span>
                  <span className="h-3 w-full rounded-[3px] bg-accent" />
                </span>
              </ScreenThumb>
            </span>
          </Card>

          <Card>
            <CardHead
              bot="azhe"
              state={answered ? 'happy' : 'curious'}
              chip={answered ? '已回答' : '有問題問你'}
              tone={answered ? 'success' : 'warning'}
              sub={answered ? '工作中 · 你剛剛回答' : '工作中，邊比價邊等你 · 10 分鐘沒回答就過期（還有 7 分鐘）'}
            />
            <p className="m-0">競品比較要不要也放進日本賣場的價格？會多花約 10 分鐘。</p>
            {answered ? (
              <Settled ok text={`已回答「${answered.label}」· 阿哲繼續比價`} undo={() => setAnswer(null)} undoText="改答案" />
            ) : (
              <div className="overflow-hidden rounded-menu border border-line bg-[#2A2A2A] px-2 [&>div>button:first-child]:border-t-0">
                <OptionList options={OPTIONS} onPick={setAnswer} />
              </div>
            )}
          </Card>

          <Card>
            <CardHead bot="shujumiao" chip="想找你討論" tone="neutral" sub="排隊中，等週報資料 · 1 小時前" />
            <p className="m-0">經典帆布包上週退貨變多，留言多半說「比照片小」。要不要先在商品頁補一張尺寸對照圖？</p>
            <div className="flex items-center gap-2">
              <label className="relative flex min-w-0 flex-1">
                <span className="sr-only-text">回覆數據喵</span>
                <input
                  type="text"
                  placeholder="直接回覆，不用開會"
                  className="h-10 min-w-0 flex-1 rounded-field border border-line bg-inset pl-3.5 pr-12 text-ui text-ink outline-none focus:border-focus max-sm:h-11"
                />
                <span className="absolute right-1 top-1 max-sm:top-1.5">
                  <IconButton icon={ArrowUp} label="送出回覆" variant="send" size="sm" />
                </span>
              </label>
              <Button variant="outline">
                不用討論
              </Button>
            </div>
          </Card>

          <Card layout="flex flex-wrap items-center gap-x-3 gap-y-2.5 py-3 pl-[18px] pr-3.5">
            <Avatar shape="shield" color="red" state="idle" dot="none" ring="#262626" size={32} />
            <div className="flex min-w-0 flex-[999_1_300px] flex-col">
              <span className="flex items-center gap-2">
                <span className="text-body">抓蟲</span>
                <StatusChip size="sm" tone="warning">
                  預算用完，已暫停
                </StatusChip>
              </span>
              <span className="text-sub text-ink-2">「重現結帳失敗」這條工作鏈用完 $5 上限，停在第 4 步。</span>
            </div>
            <div className="flex gap-2">
              <Button>結束並回報</Button>
              <Button variant="primary">
                再給 $5
              </Button>
            </div>
          </Card>

          <Card layout="flex flex-wrap items-center gap-x-3 gap-y-2 py-2.5 pl-[18px] pr-3.5">
            <Avatar shape="cursor" color="blue" state="working" dot="none" ring="#262626" size={32} />
            <div className="flex min-w-0 flex-[999_1_300px] flex-col">
              <span className="text-ui">小秘提案：每週一上午 9:00 整理銷售週報，發到「每週一早會」</span>
              <span className="text-[13px] text-ink-2">不用你處理 · 沒意見就在 10 月 20 日照做</span>
            </div>
            <div className="flex gap-2">
              <Button size="sm" variant="outline">
                否決
              </Button>
              <Button size="sm">現在就同意</Button>
            </div>
          </Card>

          <p className="m-0 pt-2 text-center text-meta text-ink-3" data-testid="health">
            {health === undefined ? '連線中…' : health ? `服務正常 · ${health.version}` : '連不到服務'}
          </p>
        </div>
      </main>

      <aside aria-label="團隊現況" className="flex max-w-full flex-[1_1_340px] flex-col gap-6 border-l border-edge bg-panel px-5 py-[22px] max-[1179px]:border-l-0 max-[1179px]:border-t">
        <section className="flex flex-col gap-2.5">
          <div className="flex items-baseline justify-between">
            <h2 className="m-0 text-body">電腦</h2>
            <a href="/office" className="text-[13px]">
              打開辦公室
            </a>
          </div>
          <div className="grid grid-cols-2 gap-x-2.5 gap-y-3">
            {[
              { bot: 'xiaomi', state: 'working', note: '彙整價差表', thumb: '競品定價.xlsx' },
              { bot: 'azhe', state: 'searching', note: '比價第 3 家', thumb: '購物網站' },
              { bot: 'xiaobian', state: 'notifying', note: '等你接手', thumb: '輸入簡訊驗證碼', waiting: true },
              { bot: 'zhangfang', state: 'notifying', note: decision ? (decision === 'deny' ? '待命' : '付款中') : '等你允許', thumb: '帳單付款', waiting: !decision },
            ].map((c) => {
              const b = BOTS[c.bot]
              return (
                <a key={c.bot} href="/office" aria-label={`${b.name}的電腦：${c.thumb}`} className="flex flex-col gap-1.5 text-ink">
                  <ScreenThumb label={c.thumb} waiting={c.waiting} />
                  <span className="flex min-w-0 items-center gap-1.5 text-[13px]">
                    <Avatar shape={b.shape} color={b.color} state={c.state} dot={c.waiting ? 'waiting' : 'none'} size={18} />
                    {b.name}
                    <span className={c.waiting ? 'truncate text-warning-ink' : 'truncate text-ink-2'}>{c.note}</span>
                  </span>
                </a>
              )
            })}
          </div>
        </section>

        <section className="flex flex-col gap-2">
          <h2 className="m-0 text-body">Bot 之間</h2>
          {[
            { from: 'xiaomi', to: 'azhe', tag: '進行中 · 第 1/6 棒', tone: 'accent' as Tone, text: '請求：抓三家競品主力商品的價格' },
            { from: 'shujumiao', to: 'xiaomi', tag: '已回覆', tone: 'success' as Tone, text: '回覆：上週銷售摘要與 2 張圖（附檔）' },
            { from: 'xiaomi', to: 'zhangfang', tag: '等你允許', tone: 'warning' as Tone, text: '請求：結清 10 月廣告費' },
          ].map((h) => {
            const f = BOTS[h.from]
            const t = BOTS[h.to]
            return (
              <div key={h.text} className="flex flex-col gap-1 rounded-menu bg-inset px-3 py-2.5">
                <div className="flex items-center gap-1.5 text-sub">
                  <Avatar shape={f.shape} color={f.color} state="idle" dot="none" size={18} />
                  <span>{f.name}</span>
                  <Icon icon={ArrowRight} size={14} color="var(--color-ink-3)" />
                  <Avatar shape={t.shape} color={t.color} state="idle" dot="none" size={18} />
                  <span>{t.name}</span>
                  <StatusChip size="sm" tone={h.tone} className="ml-auto">
                    {h.tag}
                  </StatusChip>
                </div>
                <span className="text-[13px] text-ink-2">{h.text}</span>
              </div>
            )
          })}
        </section>

        <section className="flex flex-col gap-1.5">
          <h2 className="m-0 mb-0.5 text-body">今天完成</h2>
          {[
            ['10 月對帳單整理', '帳房'],
            ['新品貼文文案三版', '小編'],
            ['開發信草稿 3 封', '阿凱'],
            ['收件匣分類 24 封', '信差'],
          ].map(([t, who]) => (
            <div key={t} className="flex min-h-6 items-center gap-2 text-sub">
              <Icon icon={Check} size={16} strokeWidth={2} color="var(--color-success)" />
              <span className="flex-1">{t}</span>
              <span className="text-ink-3">{who}</span>
            </div>
          ))}
        </section>
      </aside>
    </div>
  )
}
