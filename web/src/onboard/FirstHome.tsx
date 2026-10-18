import { ArrowRight, Compass, FolderInput, Smartphone, X } from 'lucide-react'
import type { Bot } from '../data/bots'
import { useStore } from '../data/store'
import { navigate } from '../router'
import { IconButton } from '../ui/Button'
import { Icon } from '../ui/Icon'
import { dismissCard, onboardStore, startTour } from './state'

// 首次設定完成後的首頁（A05、§21）：小秘打招呼，下面是「之後再做」卡片（帶入舊設定只在這台電腦找到 LazyBoy、
// OpenMausBot 還沒帶進來的舊資料時出現（xbot.onboard.status 的 legacy）；連手機、看導覽），輸入框上方三個建議句子
// （ON-3「第一個任務建議」）。

export const FIRST_TASKS = ['比較蝦皮三家競品的定價', '整理今天的信', '每天早上 9 點給我新聞重點']

type Card = { id: 'import' | 'phone' | 'tour'; icon: typeof Compass; title: string; body: string; action: string; go: () => void }

const IMPORT_CARD: Omit<Card, 'body'> = { id: 'import', icon: FolderInput, title: '帶入舊設定', action: '看看要帶哪些', go: () => navigate('/import') }

const CARDS: Card[] = [
  { id: 'phone', icon: Smartphone, title: '連手機', body: '掃一個 QR 就好。出門在外也能交代事情、按允許。', action: '連手機', go: () => navigate('/settings/computers') },
  { id: 'tour', icon: Compass, title: '看導覽', body: '1 分鐘認識三個地方：輸入框、電腦、待我處理。', action: '開始導覽', go: startTour },
]

/** 首次設定剛做完、只有一個 Bot、對話還是空的：顯示這個首頁。 */
export function useFirstHome(bots: Bot[] | null): boolean {
  const s = useStore(onboardStore).status
  return !!s && s !== 'off' && s.state.completed && (bots?.filter((b) => !b.archived).length ?? 0) === 1
}

export function FirstHome({ bot }: { bot: Bot }) {
  const s = useStore(onboardStore).status
  const dismissed = s && s !== 'off' ? s.state.dismissed : []
  // 已經在手機上打開：連手機卡不出現
  const phone = typeof window.matchMedia === 'function' && window.matchMedia('(max-width: 767px)').matches
  const legacy = s && s !== 'off' ? s.legacy : undefined
  const all = legacy?.found ? [{ ...IMPORT_CARD, body: legacy.summary ?? '' }, ...CARDS] : CARDS
  const cards = all.filter((c) => !dismissed.includes(c.id) && !(phone && c.id === 'phone'))
  return (
    <div className="flex flex-col gap-5" data-testid="first-home">
      <div className="max-w-[85%] self-start rounded-bubble bg-bubble-bot px-4 py-2.5 text-[15px] leading-[1.6]">
        嗨，我是{bot.name}，你的{bot.title || '幫手'}。
        <br />
        交代我一件事就能開始，像是比較競品定價、整理信件。
      </div>
      <section aria-label="之後再做" className="flex flex-col gap-2.5">
        <div className="flex items-baseline gap-2">
          <h2 className="m-0 text-body">之後再做</h2>
          <span className="text-meta text-ink-2">都可以跳過，隨時在設定找到</span>
        </div>
        {cards.length === 0 ? (
          <p className="m-0 text-sub text-ink-2">都關掉了。帶入舊設定在「設定 › 一般」，連手機在「設定 › 電腦與裝置」，導覽在對話的「更多」選單。</p>
        ) : (
          <div className="grid grid-cols-[repeat(auto-fill,minmax(240px,1fr))] gap-3">
            {cards.map((c) => (
              <div key={c.id} data-testid={`later-${c.id}`} className="flex flex-col gap-2 rounded-card bg-card px-4 pb-4 pt-3.5">
                <div className="flex items-center gap-2.5">
                  <span className="flex size-9 items-center justify-center rounded-field bg-control">
                    <Icon icon={c.icon} size={18} />
                  </span>
                  <span className="flex-1 text-[16px]">{c.title}</span>
                  <IconButton icon={X} label={`關掉「${c.title}」`} size="sm" onClick={() => dismissCard(c.id)} />
                </div>
                <p className="m-0 text-ui-sm text-ink-2">{c.body}</p>
                <button type="button" onClick={c.go} className="flex h-8 w-fit items-center gap-1.5 rounded-full bg-control px-3.5 text-sub text-ink hover:bg-hover">
                  {c.action}
                  <Icon icon={ArrowRight} size={14} />
                </button>
              </div>
            ))}
          </div>
        )}
      </section>
    </div>
  )
}

/** 輸入框上方的建議句子：點了放進輸入框。 */
export function FirstTasks({ onPick }: { onPick: (t: string) => void }) {
  return (
    <div aria-label="可以這樣開始" role="group" className="flex flex-wrap gap-2">
      {FIRST_TASKS.map((t) => (
        <button key={t} type="button" onClick={() => onPick(t)} className="h-8 rounded-full border border-line bg-transparent px-3.5 text-sub text-ink hover:bg-control">
          {t}
        </button>
      ))}
    </div>
  )
}
