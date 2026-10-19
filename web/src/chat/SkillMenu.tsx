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

/** 送出前：開頭是「/技能名稱」就改寫成請 Bot 使用那個技能；不是技能名稱就原樣送出。 */
export function expandSkill(text: string, skills: Skill[]): string {
  const m = text.match(/^\/(\S+)(?:\s+([\s\S]*))?$/)
  if (!m) return text
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
  return skills.filter((s) => s.name.toLowerCase().includes(q) || s.description.toLowerCase().includes(q)).slice(0, 30)
}

export function SkillMenu({ skills, query, active, onPick }: { skills: Skill[]; query: string; active: number; onPick: (s: Skill) => void }) {
  const list = matchSkills(skills, query)
  return (
    <div role="listbox" aria-label="技能" className="absolute inset-x-0 bottom-full z-20 mb-2 flex max-h-[min(380px,50dvh)] flex-col overflow-y-auto rounded-menu border border-divider bg-panel p-1.5 shadow-pop">
      <span className="px-2.5 pb-1 pt-1 text-meta text-ink-3">技能</span>
      {list.length === 0 && <span className="px-2.5 py-2 text-sub text-ink-3">{skills.length ? '沒有符合的技能' : '這個 Bot 還沒有技能'}</span>}
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
          {s.place === 'library' && <span className="flex-none text-meta text-ink-3">技能庫</span>}
        </button>
      ))}
    </div>
  )
}
