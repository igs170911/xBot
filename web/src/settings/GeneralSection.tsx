import { Check } from 'lucide-react'
import { navigate } from '../router'
import { Button } from '../ui/Button'
import { Icon } from '../ui/Icon'
import { Select } from '../ui/Menu'
import { cx } from '../ui/cx'
import { Block, Group, Row, SectionHeader } from './parts'
import { setTheme, useTheme, type ThemeChoice } from './theme'

// 一般（J01）：主題（Midnight／Daylight／跟隨系統）、字型（粉圓，不能換）、語言。
// 通知開關目前沒有對應的操作，先不放（PROGRESS 待確認）。

type Swatch = { bg: string; side: string; line: string; you: string; bot: string; comp: string }
const D: Swatch = { bg: '#070707', side: '#111111', line: '#333333', you: '#5A5A5A', bot: '#262626', comp: '#2F2F2F' }
const L: Swatch = { bg: '#FFFFFF', side: '#F3F3F4', line: '#D9D9DC', you: '#D4D4D8', bot: '#EDEDEF', comp: '#E6E6E9' }

const THEMES: Array<{ k: ThemeChoice; name: string; sub: string; halves: Swatch[] }> = [
  { k: 'dark', name: 'Midnight', sub: '深色 · 預設', halves: [D] },
  { k: 'light', name: 'Daylight', sub: '淺色', halves: [L] },
  { k: 'system', name: '跟隨系統', sub: '跟著電腦或手機的深淺色', halves: [D, L] },
]

function Preview({ halves }: { halves: Swatch[] }) {
  return (
    <span aria-hidden="true" className="flex h-[76px] overflow-hidden rounded-field border border-line">
      {halves.map((h, i) => (
        <span key={i} className="flex flex-1" style={{ background: h.bg }}>
          <span className="flex w-[28%] flex-col gap-[5px] px-1.5 py-2" style={{ background: h.side }}>
            <span className="h-1.5 rounded-[3px]" style={{ background: h.line }} />
            <span className="h-1.5 w-[70%] rounded-[3px]" style={{ background: h.line }} />
            <span className="h-1.5 w-[85%] rounded-[3px]" style={{ background: h.line }} />
          </span>
          <span className="flex flex-1 flex-col gap-[5px] p-2">
            <span className="h-3 w-[46%] self-end rounded-[6px]" style={{ background: h.you }} />
            <span className="h-5 w-[70%] rounded-[8px]" style={{ background: h.bot }} />
            <span className="mt-auto h-3 rounded-full" style={{ background: h.comp }} />
          </span>
        </span>
      ))}
    </span>
  )
}

export function ThemePicker() {
  const theme = useTheme()
  return (
    <div role="radiogroup" aria-label="主題" className="grid grid-cols-3 gap-3 max-sm:grid-cols-1">
      {THEMES.map((t) => {
        const on = theme === t.k
        return (
          <button
            key={t.k}
            type="button"
            role="radio"
            aria-checked={on}
            onClick={() => setTheme(t.k)}
            className={cx('flex flex-col gap-2 rounded-group border-2 bg-inset px-1.5 pb-2.5 pt-1.5 text-left text-ink', on ? 'border-accent' : 'border-card')}
          >
            <Preview halves={t.halves} />
            <span className="flex items-center gap-2 px-1">
              <span className="flex flex-1 flex-col">
                <span className="text-ui-sm">{t.name}</span>
                <span className="text-meta text-ink-2">{t.sub}</span>
              </span>
              {on && (
                <span className="flex size-5 items-center justify-center rounded-full bg-accent">
                  <Icon icon={Check} size={13} color="#FFFFFF" />
                </span>
              )}
            </span>
          </button>
        )
      })}
    </div>
  )
}

export function GeneralSection() {
  return (
    <>
      <SectionHeader title="一般" sub="外觀與語言。改了馬上生效，不用按儲存。" />
      <Block title="外觀">
        <Group>
          <div className="flex flex-col gap-2.5 px-4 pb-4 pt-3">
            <span>
              <span className="block text-ui">主題</span>
              <span className="block text-[13px] text-ink-2">預設 Midnight 深色；只存在這個瀏覽器。</span>
            </span>
            <ThemePicker />
          </div>
          <Row title="字型" sub="全部畫面都用粉圓">
            <span className="whitespace-nowrap text-ui-sm text-ink-2">粉圓</span>
          </Row>
        </Group>
      </Block>
      <Block title="語言">
        <Group>
          <Row title="介面語言" sub="目前只有繁體中文。用注音、倉頡選字時按 Enter 不會送出訊息。" wrap>
            <Select label="介面語言" hideLabel value="zh-TW" options={[{ value: 'zh-TW', label: '繁體中文（台灣）' }]} onChange={() => {}} className="w-[220px] max-sm:w-full" />
          </Row>
        </Group>
      </Block>
      <Block title="帶入舊設定">
        <Group>
          <Row title="從 LazyBoy 或 OpenMausBot 帶進來" sub="Bot、技能、記憶、定時任務、團隊與群組；只讀舊資料，不會改動它。" wrap>
            <Button onClick={() => navigate('/import')}>看看要帶哪些</Button>
          </Row>
        </Group>
      </Block>
    </>
  )
}
