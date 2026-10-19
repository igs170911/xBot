import { useEffect, useState } from 'react'
import { Sparkles } from 'lucide-react'
import { skillsList, type SkillsListOutput } from '../api/generated'
import { Icon } from '../ui/Icon'
import { cx } from '../ui/cx'

// 「/」技能選單：輸入框開頭打「/」叫出這個 Bot 的技能（技能清單＋技能庫），點一列或按 Enter 選。
// 選了在輸入框留下「/名稱 」，送出時改寫成「使用技能「名稱」：…」，任何引擎都看得懂（引擎自己的 / 指令不經過這裡）。

export type Skill = SkillsListOutput['skills'][number]

/** 輸入框是不是正在打「/…」（開頭、還沒有空白）：回傳 / 後面的字，不是就回 null。 */
export function slashQuery(text: string): string | null {
  const m = text.match(/^\/([^\s/]*)$/)
  return m ? m[1] : null
}

// 內建的指令（列在技能前面）：/plan 先規劃（使用者決定 2026-10-19：大頭要先規劃方向、拆解給我確認，再交代下去）。
const PLAN_NAMES = ['plan', '規劃', '計畫']

/** /plan 的內容：先拆解成計畫給使用者確認；確認前不動手、不交辦隊友、不建 Bot。 */
export function planPrompt(task: string): string {
  return (
    '【先規劃，先不要動手】\n' +
    '請先把下面這件事拆成步驟計畫給我確認：目標、怎麼做、要交給哪些隊友做什麼、順序、需要我決定的事、預計產出。\n' +
    '在我回覆「開始」或確認之前：不要動手執行、不要交辦或傳話給隊友、不要建 Bot 或團隊。\n' +
    '我調整計畫時照我的意見改，再給我看一次。\n\n' +
    '要規劃的事：' +
    (task.trim() || '（請先問我要規劃什麼）')
  )
}

export const BUILTIN_PLAN: Skill = {
  id: '__plan__',
  name: 'plan',
  description: '先規劃：拆成步驟給你確認，確認前不動手、不交辦隊友',
  place: 'list',
  enabled: true,
  source: 'xbot',
  created_at: 0,
  updated_at: 0,
}

/** 送出前：「/plan …」改寫成先規劃；開頭是「/技能名稱」就改寫成請 Bot 使用那個技能；都不是就原樣送出。 */
export function expandSkill(text: string, skills: Skill[]): string {
  const m = text.match(/^\/(\S+)(?:\s+([\s\S]*))?$/)
  if (!m) return text
  if (PLAN_NAMES.includes(m[1].toLowerCase())) return planPrompt(m[2] ?? '')
  const s = skills.find((x) => x.name === m[1] || x.id === m[1])
  if (!s) return text
  const rest = (m[2] ?? '').trim()
  return `使用技能「${s.name}」${rest ? `：${rest}` : '。'}`
}

/** 這個 Bot 能用的技能（啟用的技能清單在前、技能庫在後）；讀不到就是空的。 */
export function useBotSkills(botId?: string): Skill[] {
  const [skills, setSkills] = useState<Skill[]>([])
  useEffect(() => {
    if (!botId) return
    let live = true
    skillsList({ bot_id: botId })
      .then((o) => {
        if (!live) return
        const usable = o.skills.filter((s) => s.place !== 'off' && s.enabled !== false)
        setSkills([...usable.filter((s) => s.place === 'list'), ...usable.filter((s) => s.place === 'library')])
      })
      .catch(() => live && setSkills([]))
    return () => {
      live = false
    }
  }, [botId])
  return botId ? skills : []
}

export function matchSkills(skills: Skill[], query: string): Skill[] {
  const q = query.toLowerCase()
  return [BUILTIN_PLAN, ...skills].filter((s) => s.name.toLowerCase().includes(q) || s.description.toLowerCase().includes(q)).slice(0, 30)
}

export function SkillMenu({ skills, query, active, onPick }: { skills: Skill[]; query: string; active: number; onPick: (s: Skill) => void }) {
  const list = matchSkills(skills, query)
  return (
    <div role="listbox" aria-label="技能" className="absolute inset-x-0 bottom-full z-20 mb-2 flex max-h-[min(380px,50dvh)] flex-col overflow-y-auto rounded-menu border border-divider bg-panel p-1.5 shadow-pop">
      <span className="px-2.5 pb-1 pt-1 text-meta text-ink-3">指令與技能</span>
      {list.length === 0 && <span className="px-2.5 py-2 text-sub text-ink-3">沒有符合的指令或技能</span>}
      {list.map((s, i) => (
        <button
          key={s.id}
          type="button"
          role="option"
          aria-selected={i === active}
          onMouseDown={(e) => e.preventDefault()}
          onClick={() => onPick(s)}
          className={cx('flex min-h-11 w-full items-center gap-3 rounded-seg px-2.5 text-left hover:bg-hover', i === active && 'bg-hover')}
        >
          <span className="flex size-7 flex-none items-center justify-center rounded-seg bg-control text-ink-2">
            <Icon icon={Sparkles} size={15} />
          </span>
          <span className="flex min-w-0 flex-1 flex-col">
            <span className="truncate text-ui text-ink">/{s.name}</span>
            {s.description && <span className="truncate text-meta text-ink-3">{s.description}</span>}
          </span>
          {s.id === BUILTIN_PLAN.id ? <span className="flex-none text-meta text-accent-ink">內建</span> : s.place === 'library' && <span className="flex-none text-meta text-ink-3">技能庫</span>}
        </button>
      ))}
    </div>
  )
}
