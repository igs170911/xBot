import { useState } from 'react'
import { Monitor, ShieldCheck, X } from 'lucide-react'
import type { Bot } from '../data/bots'
import { Avatar } from '../ui/Avatar'
import { IconButton } from '../ui/Button'
import { Icon } from '../ui/Icon'
import { STARTERS } from './format'
import { look } from '../data/bots'

const APPROVAL_LABEL: Record<string, string> = { ask: '先問我', edits: '一般的事自己做', auto: '放手做', full: '完全放手' }

// 新 Bot 的空狀態（design/C03）：頭像、名字、職稱、預設值膠囊；打招呼與「可以這樣開始」卡片。
// 卡片的選項只是把話放進輸入框（Bot 真正的提問卡由 C12 的卡片處理）。
export function EmptyState({ bot, onPick, onFree }: { bot: Bot; onPick: (text: string) => void; onFree: () => void }) {
  const [hide, setHide] = useState(false)
  const sub = [bot.title, bot.description].filter(Boolean).join(' · ')
  const greet = `嗨，我是${bot.name}。${bot.description ? `我負責${bot.description.replace(/[。.]$/, '')}。` : ''}有什麼要我做的，直接交代就好。`
  return (
    <div className="flex flex-col gap-3.5" data-testid="chat-empty">
      <div className="flex flex-col items-center gap-1.5 pb-3 text-center">
        <Avatar {...look(bot)} state="happy" dot="none" size={72} />
        <h1 className="m-0 mt-1.5 text-[24px] leading-[1.35]">{bot.name}</h1>
        {sub && <span className="text-ui-sm text-ink-2">{sub}</span>}
        <span className="mt-1.5 flex flex-wrap justify-center gap-1.5 text-meta text-ink-2">
          <span className="inline-flex h-[26px] items-center gap-1.5 rounded-full border border-line px-2.5">
            <Icon icon={ShieldCheck} size={14} />
            {APPROVAL_LABEL[bot.approval_mode] ?? bot.approval_mode}
          </span>
          <span className="inline-flex h-[26px] items-center gap-1.5 rounded-full border border-line px-2.5">
            <Icon icon={Monitor} size={14} />
            這台主機 · 第一次交辦時開機
          </span>
        </span>
      </div>
      <div className="max-w-[85%] self-start rounded-bubble bg-bubble-bot px-4 py-2.5 text-[15px] leading-[1.6]">{greet}</div>
      {!hide && (
        <section aria-label="可以這樣開始" className="flex flex-col gap-3 rounded-[20px] bg-card px-5 pb-5 pt-[18px] max-sm:px-4">
          <div className="flex items-start gap-3">
            <div className="flex min-w-0 flex-1 flex-col">
              <span className="text-[16px]">想先請{bot.name}做什麼？</span>
              <span className="text-meta text-ink-2">選一個放進輸入框，改一改再送出。</span>
            </div>
            <IconButton icon={X} label="略過" size="sm" onClick={() => setHide(true)} />
          </div>
          <div className="flex flex-col overflow-hidden rounded-menu border border-line">
            {STARTERS.map((s, i) => (
              <button
                key={s}
                type="button"
                onClick={() => onPick(s)}
                className="flex min-h-12 items-center gap-3 border-t border-line bg-transparent px-3.5 py-2 text-left text-ui text-ink first:border-t-0 hover:bg-control"
              >
                <span className="flex size-[22px] flex-none items-center justify-center rounded-[6px] bg-control text-[12px] text-ink-2">{String.fromCharCode(65 + i)}</span>
                {s}
              </button>
            ))}
          </div>
          <button type="button" onClick={onFree} className="flex h-11 items-center rounded-menu bg-inset px-3.5 text-left text-ui text-ink-3 hover:text-ink-2">
            輸入你自己的交代…
          </button>
        </section>
      )}
    </div>
  )
}
