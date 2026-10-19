import { useEffect, useState } from 'react'
import { RotateCcw } from 'lucide-react'
import { limitsGet, limitsSet, type LimitsGetOutput, type LimitsSetInput } from '../api/generated'
import { Button } from '../ui/Button'
import { TextInput } from '../ui/Field'
import { useToast } from '../ui/toast-context'
import { errText } from './format'
import { Block, Group, Row, SectionHeader } from './parts'

// 可以調的限制（設定 → 進階 → 限制，xbot.limits.get／set）：會擋住工作的那幾個數字。改了馬上生效。
// 花費上限在「用量與花費」，快判次數在「AI 模型 → 快判 → 進階」。

type Key = keyof LimitsGetOutput['values']

const ROWS: { key: Key; title: string; sub: string; unit: string }[] = [
  { key: 'max_hops', title: 'Bot 之間同一條往來', sub: '兩個 Bot 互相交辦、回覆，超過就停下來回報你（防止無限互傳）', unit: '次' },
  { key: 'room_rounds', title: '群組裡 Bot 互相接力', sub: '你說一次話，Bot 之間最多接幾輪', unit: '輪' },
  { key: 'auto_continue_max', title: '自動請 Bot 繼續', sub: 'Bot 只說要做、沒做完時，最多自動請它繼續幾次', unit: '次' },
  { key: 'bot_creates_per_day', title: 'Bot 每天建立 Bot', sub: '一般 Bot 每 24 小時最多建幾個隊友', unit: '個' },
  { key: 'manager_bot_creates_per_day', title: '有團隊管理權限的 Bot 每天建立 Bot', sub: '建團隊也算在內', unit: '個' },
  { key: 'bots_max', title: '全部 Bot 上限', sub: 'Bot 建 Bot、建團隊、匯入時檢查', unit: '個' },
  { key: 'agenda_open_max', title: '每個 Bot 同時在等你的提案與討論', sub: '超過時 Bot 要先等你處理', unit: '件' },
]

export function LimitsPage() {
  const toast = useToast()
  const [data, setData] = useState<LimitsGetOutput | null>(null)
  const [draft, setDraft] = useState<Record<string, string>>({})
  const [error, setError] = useState<string | null>(null)

  const show = (d: LimitsGetOutput) => {
    setData(d)
    setDraft(Object.fromEntries(Object.entries(d.values).map(([k, v]) => [k, String(v)])))
  }
  useEffect(() => {
    limitsGet({})
      .then(show)
      .catch((e: unknown) => setError(errText(e)))
  }, [])

  const save = async (patch: LimitsSetInput, done: string) => {
    try {
      show(await limitsSet(patch))
      toast.show({ kind: 'done', text: done })
    } catch (e) {
      toast.show({ kind: 'error', text: errText(e) })
      if (data) show(data)
    }
  }

  if (error) {
    return (
      <p role="alert" className="m-0 text-ui-sm text-danger">
        讀不到限制：{error}
      </p>
    )
  }
  if (!data) return <p className="m-0 text-ui-sm text-ink-2">讀取中…</p>

  return (
    <>
      <SectionHeader
        title="限制"
        sub="會擋住 Bot 工作的幾個數字。改了馬上生效。"
        aside={
          <Button size="sm" variant="ghost" icon={RotateCcw} onClick={() => void save({ reset: true }, '已回到預設')}>
            回到預設
          </Button>
        }
      />
      <Block title="Bot 之間">
        <Group label="限制">
          {ROWS.map((r) => {
            const [min, max] = data.ranges[r.key] ?? [0, 0]
            const val = draft[r.key] ?? ''
            const n = Number(val)
            const valid = val !== '' && Number.isInteger(n) && n >= min && n <= max
            const commit = () => {
              if (!valid) {
                setDraft((d) => ({ ...d, [r.key]: String(data.values[r.key]) }))
                if (val !== String(data.values[r.key])) toast.show({ kind: 'error', text: `${r.title}要在 ${min} 到 ${max} 之間` })
                return
              }
              if (n !== data.values[r.key]) void save({ [r.key]: n }, `${r.title}改成 ${n} ${r.unit}`)
            }
            return (
              <Row key={r.key} title={r.title} sub={`${r.sub}。預設 ${data.defaults[r.key]} ${r.unit}`} wrap>
                <span className="flex items-center gap-2">
                  <TextInput
                    label={r.title}
                    hideLabel
                    inputMode="numeric"
                    wrapperClassName="w-24"
                    value={val}
                    onChange={(e) => setDraft((d) => ({ ...d, [r.key]: e.target.value.replace(/[^0-9]/g, '') }))}
                    onBlur={commit}
                    onKeyDown={(e) => {
                      if (e.key !== 'Enter' || e.nativeEvent.isComposing) return
                      e.preventDefault()
                      ;(e.target as HTMLInputElement).blur()
                    }}
                  />
                  <span className="w-6 text-meta text-ink-2">{r.unit}</span>
                </span>
              </Row>
            )
          })}
        </Group>
      </Block>
      <p className="m-0 text-meta text-ink-3">每月花費上限在「用量與花費」；快速判斷每天幾次在「AI 模型 → 快速判斷 → 進階」。刪除 Bot、花錢、對外發送一定要你核准，這裡改不到。</p>
    </>
  )
}
