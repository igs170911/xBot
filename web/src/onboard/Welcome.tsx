import { useState, type FormEvent } from 'react'
import { ArrowUp, FileDown, LoaderCircle, Sparkles } from 'lucide-react'
import { botsCreate, botsSuggest, type BotsSuggestOutput } from '../api/generated'
import { botsStore, upsertBot } from '../data/bots'
import { useStore } from '../data/store'
import { navigate } from '../router'
import { Avatar, Mascot } from '../ui/Avatar'
import { COLOR_IDS, SHAPE_IDS, type AvatarColor, type AvatarShape } from '../ui/avatar-data'
import { Button, IconButton } from '../ui/Button'
import { Icon } from '../ui/Icon'
import { StatusChip } from '../ui/Pill'
import { useToast } from '../ui/toast-context'
import { errText } from '../settings/format'
import { openImportBot } from '../settings/bot/import-store'

// 還沒有 Bot（A09、BT-1、§21「建立 Bot：1 個欄位＋確認」）：一句話說要它做什麼 → 系統建議名字、職稱、它會做什麼 → 建立；
// 下面 10 個範本點一下就建立。權限預設「先問我」。

const BOTS_MAX = 50

type Template = { name: string; title: string; desc: string; shape: AvatarShape; color: AvatarColor }

export const TEMPLATES: Template[] = [
  { name: '小秘', title: '幕僚長', desc: '每日重點與協調', shape: 'cursor', color: 'blue' },
  { name: '阿哲', title: '選品研究員', desc: '電商選品與市場調查', shape: 'blob', color: 'orange' },
  { name: '小編', title: '社群小編', desc: '貼文企劃與文案', shape: 'circle', color: 'pink' },
  { name: '阿凱', title: '業務開發', desc: '潛在客戶與開發信', shape: 'squircle', color: 'green' },
  { name: '小獵', title: '人才搜尋', desc: '職缺與候選人研究', shape: 'capsule', color: 'teal' },
  { name: '帳房', title: '費用管理', desc: '收據整理與對帳', shape: 'hexagon', color: 'yellow' },
  { name: '廣廣', title: '廣告成效分析', desc: '投放監控與預算建議', shape: 'drop', color: 'coral' },
  { name: '數據喵', title: '數據分析師', desc: '找出數字變化的原因', shape: 'diamond', color: 'purple' },
  { name: '抓蟲', title: 'Bug 重現', desc: '把回報變成重現步驟', shape: 'shield', color: 'red' },
  { name: '信差', title: '收件匣管家', desc: '整理信件與回覆草稿', shape: 'star', color: 'cyan' },
]

const soulOf = (name: string, title: string, desc: string) =>
  `你是${name}，${title}。負責：${desc}。說話簡短、先講結論，用台灣用語。不確定的事先問；付款、對外發送、刪除這類事做之前一定先問。`

const pick = <T,>(xs: readonly T[], seed: number): T => xs[Math.abs(seed) % xs.length]

export function WelcomePage() {
  const toast = useToast()
  const bots = useStore(botsStore).bots
  const [text, setText] = useState('')
  const [asked, setAsked] = useState('')
  const [hint, setHint] = useState<BotsSuggestOutput | null>(null)
  const [thinking, setThinking] = useState(false)
  const [busy, setBusy] = useState<string | null>(null)
  const [seed, setSeed] = useState(1)
  const left = Math.max(0, BOTS_MAX - (bots?.length ?? 0))

  const suggest = async (e?: FormEvent) => {
    e?.preventDefault()
    const t = text.trim()
    if (!t) return
    setThinking(true)
    try {
      const s = await botsSuggest({ text: t })
      setHint(s)
      setAsked(t)
      setSeed((x) => x + 3)
    } catch (err) {
      toast.show({ kind: 'error', text: errText(err) })
    } finally {
      setThinking(false)
    }
  }

  const create = async (key: string, input: Parameters<typeof botsCreate>[0]) => {
    setBusy(key)
    try {
      const bot = await botsCreate(input)
      upsertBot(bot)
      navigate(`/bots/${bot.id}`)
    } catch (err) {
      toast.show({ kind: 'error', text: errText(err) })
      setBusy(null)
    }
  }

  const shape = pick(SHAPE_IDS, seed)
  const color = pick(COLOR_IDS, seed * 7 + 1)

  return (
    <main className="flex flex-1 flex-col items-center gap-6 overflow-y-auto px-8 pb-10 pt-12 max-sm:px-4 max-sm:pt-8" aria-label="建立第一個 Bot">
      <div className="flex flex-col items-center gap-2 text-center">
        <Mascot pose="happy" size={64} />
        <h1 className="m-0 text-[28px] leading-[1.35]">建立第一個 Bot</h1>
        <p className="m-0 text-ui text-ink-2">一句話說你要它做什麼，名字、職稱、技能我們幫你填好。</p>
      </div>

      <div className="flex w-full max-w-[720px] flex-col gap-4">
        <form onSubmit={(e) => void suggest(e)} className="flex h-14 items-center gap-2 rounded-full bg-control pl-6 pr-2">
          <label htmlFor="welcome-ask" className="sr-only-text">
            一句話說你要它做什麼
          </label>
          <input
            id="welcome-ask"
            value={text}
            onChange={(e) => setText(e.target.value)}
            maxLength={1000}
            placeholder="例：每天幫我找蝦皮上熱賣的居家小物，整理成表格"
            className="min-w-0 flex-1 bg-transparent text-body text-ink outline-none placeholder:text-ink-3"
          />
          <IconButton icon={thinking ? LoaderCircle : ArrowUp} label="產生建議" variant="send" type="submit" disabled={!text.trim() || thinking} />
        </form>

        {thinking && !hint && <div className="h-48 animate-pulse rounded-card bg-card" aria-label="想一下…" />}
        {hint && (
          <section aria-label="建議的 Bot" className="flex flex-col gap-3 rounded-card bg-card px-5 pb-4 pt-5">
            <div className="flex items-start gap-3">
              <Avatar shape={shape} color={color} state="happy" dot="none" size={44} />
              <div className="min-w-0 flex-1">
                <span className="block text-[17px]">{hint.name}</span>
                <span className="block text-sub text-ink-2">{hint.title}</span>
              </div>
              <StatusChip size="sm" icon={Sparkles}>
                系統建議 · 都可以改
              </StatusChip>
            </div>
            <dl className="m-0 grid grid-cols-[72px_1fr] gap-x-3 gap-y-2 text-ui-sm">
              <dt className="text-ink-2">它會做</dt>
              <dd className="m-0">{hint.description || asked}</dd>
            </dl>
            <div className="flex flex-wrap items-center gap-2 border-t border-line pt-3">
              <span className="min-w-0 flex-1 text-meta text-ink-2">
                權限預設「先問我」。還可以建立 {left} 個 Bot。
              </span>
              <Button disabled={thinking || !!busy} onClick={() => void suggest()}>
                換一個建議
              </Button>
              <Button
                variant="primary"
                loading={busy === 'hint'}
                disabled={left === 0 || (!!busy && busy !== 'hint')}
                onClick={() =>
                  void create('hint', { name: hint.name, title: hint.title || undefined, description: hint.description || asked, soul: hint.soul || undefined, shape, color })
                }
              >
                建立{hint.name}
              </Button>
            </div>
          </section>
        )}
      </div>

      <section aria-label="或從範本開始" className="flex w-full max-w-[1040px] flex-col gap-3">
        <div className="flex flex-wrap items-baseline gap-2">
          <h2 className="m-0 text-body">或從範本開始</h2>
          <span className="text-meta text-ink-2">點一下就建立，之後都能改</span>
          <span className="flex-1" />
          <Button size="sm" variant="ghost" icon={FileDown} onClick={openImportBot}>
            匯入 Bot
          </Button>
        </div>
        <ul className="m-0 grid list-none grid-cols-[repeat(auto-fill,minmax(190px,1fr))] gap-2.5 p-0">
          {TEMPLATES.map((t) => (
            <li key={t.title}>
              <button
                type="button"
                disabled={left === 0 || !!busy}
                onClick={() => void create(t.title, { name: t.name, title: t.title, description: t.desc, soul: soulOf(t.name, t.title, t.desc), shape: t.shape, color: t.color })}
                className="flex h-16 w-full items-center gap-3 rounded-group border border-line bg-transparent px-3.5 text-left text-ink hover:bg-card disabled:opacity-50"
                aria-label={`用範本建立：${t.title}`}
              >
                <Avatar shape={t.shape} color={t.color} dot="none" size={32} blink={false} />
                <span className="min-w-0 flex-1">
                  <span className="block text-ui">{t.title}</span>
                  <span className="block truncate text-[13px] text-ink-2">{t.desc}</span>
                </span>
                {busy === t.title && <Icon icon={LoaderCircle} size={16} className="animate-spin" />}
              </button>
            </li>
          ))}
        </ul>
      </section>
    </main>
  )
}
