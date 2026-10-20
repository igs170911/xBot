import { useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { ChevronDown, ChevronUp, ImagePlus, MessageSquare, Palette, Pencil, Wand2, X } from 'lucide-react'
import { botsUpdate, type BotsGetOutput, type BotsUpdateInput } from '../../api/generated'
import { look } from '../../data/bots'
import { linkClick } from '../../router'
import { Avatar } from '../../ui/Avatar'
import type { AvatarShape } from '../../ui/avatar-data'
import { Button, IconButton } from '../../ui/Button'
import { SegmentedControl } from '../../ui/Controls'
import { TextInput, Textarea } from '../../ui/Field'
import { Icon } from '../../ui/Icon'
import { useFocusTrap } from '../../ui/useFocusTrap'
import { useToast } from '../../ui/toast-context'
import { errText } from '../format'
import { AvatarUploadDialog } from './AvatarUpload'
import { ColorPicker, EyePicker, ShapePicker } from './pickers'

// Bot 設定 · 基本（G02）：頭像、名字、它做什麼、人設；外觀 sheet（G03）、上傳裁切（G04）。全部自動儲存，沒有儲存按鈕。

type Bot = BotsGetOutput
const SOUL_MAX = 4000

/** 存一個或幾個欄位；成功回傳新的 Bot。 */
function useSave(bot: Bot, onSaved: (b: Bot) => void) {
  const toast = useToast()
  return async (patch: Omit<BotsUpdateInput, 'id'>, done?: string): Promise<boolean> => {
    try {
      const b = await botsUpdate({ id: bot.id, ...patch })
      onSaved(b)
      if (done) toast.show({ kind: 'done', text: done })
      return true
    } catch (e) {
      toast.show({ kind: 'error', text: errText(e) })
      return false
    }
  }
}

export function BasicTab({ bot, onSaved, openSoul }: { bot: Bot; onSaved: (b: Bot) => void; openSoul?: boolean }) {
  const toast = useToast()
  const save = useSave(bot, onSaved)
  const [name, setName] = useState(bot.name)
  const [desc, setDesc] = useState(bot.description)
  const [soul, setSoul] = useState(bot.soul)
  const [soulOpen, setSoulOpen] = useState(!!openSoul)
  const [sheet, setSheet] = useState(false)
  const [upload, setUpload] = useState(false)

  const commitName = async () => {
    const n = name.trim()
    if (!n) {
      setName(bot.name)
      toast.show({ kind: 'error', text: '名字不能空白' })
      return
    }
    if (n !== bot.name) await save({ name: n }, `已改名為${n}`)
  }
  const commitDesc = async () => {
    if (desc.trim() !== bot.description) await save({ description: desc.trim() }, '已更新它做什麼')
  }
  const commitSoul = async () => {
    if (soul !== bot.soul) await save({ soul }, `已更新${bot.name}的人設，從下一個對話開始生效`)
  }
  const enter = (fn: () => void) => (e: React.KeyboardEvent<HTMLInputElement>) => {
    if (e.key === 'Enter' && !e.nativeEvent.isComposing) {
      e.preventDefault()
      fn()
    }
  }

  return (
    <div className="grid grid-cols-[300px_minmax(0,1fr)] items-start gap-6 max-lg:grid-cols-1">
      <div className="flex flex-col gap-4">
        <section aria-label="頭像" className="flex flex-col gap-3 rounded-card bg-card p-4">
          <div className="flex items-center justify-between">
            <h3 className="m-0 text-[15px]">頭像</h3>
            <span className="text-meta text-ink-2">{bot.image ? '圖片不疊眼睛' : '眼睛會跟著狀態變'}</span>
          </div>
          <div className="relative flex h-[168px] items-center justify-center rounded-group bg-inset">
            <Avatar {...look(bot)} dot="none" size={120} label={`${bot.name}的頭像`} />
            <span className="absolute bottom-2.5 right-2.5">
              <IconButton icon={Pencil} label="編輯頭像" variant="filled" shape="round" size="sm" onClick={() => setSheet(true)} />
            </span>
          </div>
          <div className="grid grid-cols-2 gap-2">
            <Button icon={Palette} onClick={() => setSheet(true)}>
              換外觀
            </Button>
            <Button icon={ImagePlus} variant="outline" onClick={() => setUpload(true)}>
              上傳圖片
            </Button>
          </div>
        </section>
        <section className="flex flex-col gap-1.5 rounded-card bg-card p-4 text-[13px] text-ink-2">
          <span className="flex items-center gap-2 text-ui text-ink">
            <Icon icon={MessageSquare} size={16} />
            不想看設定頁？
          </span>
          直接在對話裡告訴{bot.name}你想怎麼改（例：「以後回答短一點」），改設定前它會先問你。
          <a href={`/bots/${bot.id}`} onClick={linkClick(`/bots/${bot.id}`)} className="mt-1 w-fit text-ui-sm">
            打開對話
          </a>
        </section>
      </div>

      <div className="flex min-w-0 flex-col gap-5">
        <TextInput label="名字" value={name} maxLength={100} onChange={(e) => setName(e.target.value)} onBlur={() => void commitName()} onKeyDown={enter(() => (document.activeElement as HTMLElement)?.blur())} />
        <TextInput
          label="它做什麼"
          hint="一句話，會顯示在側欄和隊友名單"
          value={desc}
          maxLength={1000}
          onChange={(e) => setDesc(e.target.value)}
          onBlur={() => void commitDesc()}
          onKeyDown={enter(() => (document.activeElement as HTMLElement)?.blur())}
          placeholder="例：每天整理重點，把工作交給對的隊友"
        />
        <section aria-label="人設" className="flex flex-col gap-2">
          <div className="flex items-center gap-2">
            <h3 className="m-0 text-[15px]">人設</h3>
            <span className="flex-1 text-meta text-ink-2">怎麼說話、負責什麼、界線</span>
            <Button size="sm" icon={soulOpen ? ChevronUp : ChevronDown} onClick={() => setSoulOpen((o) => !o)} aria-expanded={soulOpen}>
              {soulOpen ? '收起' : '展開'}
            </Button>
          </div>
          {soulOpen ? (
            <Textarea
              label="常駐指示"
              hideLabel
              rows={9}
              value={soul}
              maxLength={SOUL_MAX}
              onChange={(e) => setSoul(e.target.value)}
              onBlur={() => void commitSoul()}
              placeholder="例：說話簡短、先講結論。花錢或對外發送前一定先問我。"
              hint={`${soul.length.toLocaleString()} / 4,000 字 · 會寫進它電腦裡的 SOUL.md，離開欄位就儲存，從下一個對話開始生效`}
            />
          ) : (
            <button
              type="button"
              onClick={() => setSoulOpen(true)}
              className="min-h-[76px] rounded-field border border-line bg-inset px-3.5 py-2.5 text-left text-ui-sm text-ink"
            >
              {bot.soul ? <span className="line-clamp-3 whitespace-pre-line">{bot.soul}</span> : <span className="text-ink-3">還沒寫人設。按一下開始寫。</span>}
            </button>
          )}
        </section>
      </div>

      <AppearanceSheet open={sheet} bot={bot} onClose={() => setSheet(false)} onUpload={() => setUpload(true)} save={save} />
      {upload && <AvatarUploadDialog open bot={bot} onClose={() => setUpload(false)} save={save} />}
    </div>
  )
}

// ── 外觀（G03）────────────────────────────────────────

function AppearanceSheet({
  open,
  bot,
  onClose,
  onUpload,
  save,
}: {
  open: boolean
  bot: Bot
  onClose: () => void
  onUpload: () => void
  save: (patch: Omit<BotsUpdateInput, 'id'>, done?: string) => Promise<boolean>
}) {
  const ref = useRef<HTMLDivElement>(null)
  const onKeyDown = useFocusTrap(ref, open, onClose)
  if (!open) return null
  const v = { shape: bot.shape, color: bot.color, eyes: bot.eyes || 'dot', img: bot.image || undefined }
  const source = bot.image ? 'image' : 'mascot'
  return createPortal(
    <div className="fixed-screen z-50 flex justify-end bg-black/60 p-2 max-sm:p-0" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <div
        ref={ref}
        role="dialog"
        aria-modal="true"
        aria-label="外觀"
        tabIndex={-1}
        onKeyDown={onKeyDown}
        className="flex h-full w-[460px] max-w-full flex-col overflow-y-auto rounded-card border border-divider bg-panel shadow-dialog outline-none max-sm:rounded-none"
      >
        <header className="flex h-14 flex-none items-center gap-2 px-3">
          <span className="size-9" />
          <h2 className="m-0 flex-1 text-center text-title">外觀</h2>
          <IconButton icon={X} label="關閉" onClick={onClose} />
        </header>
        <div className="flex flex-col gap-4 px-4 pb-5">
          <div className="flex items-center justify-between">
            <span className="text-meta text-ink-2">點了立刻套用、自動儲存</span>
            <Button size="sm" variant="ghost" onClick={() => void save({ shape: 'cursor', eyes: 'dot', image: '' }, '已還原吉祥物')}>
              還原吉祥物
            </Button>
          </div>
          <div className="flex h-[150px] items-center justify-center rounded-group bg-inset">
            <Avatar {...look(bot)} dot="none" size={110} label="預覽" />
          </div>
          <div className="flex items-center gap-2">
            <SegmentedControl
              label="來源"
              className="flex-1"
              value={source}
              onChange={(s) => (s === 'mascot' ? void save({ image: '' }, '改用吉祥物') : onUpload())}
              options={[
                { value: 'mascot', label: '吉祥物' },
                { value: 'image', label: '圖片' },
              ]}
            />
            <Button icon={ImagePlus} onClick={onUpload}>
              上傳圖片
            </Button>
          </div>
          <div className="flex items-baseline justify-between text-meta">
            <span>形狀</span>
            <span className="text-ink-2">{source === 'image' ? '形狀＝圖片的裁切框' : '游標＝品牌與預設'}</span>
          </div>
          <ShapePicker value={v} onChange={(shape: AvatarShape) => void save({ shape })} />
          <div className="flex items-baseline justify-between text-meta">
            <span>眼睛</span>
            <span className="text-ink-2">只有眼睛，沒有嘴巴</span>
          </div>
          <EyePicker value={v} dim={source === 'image'} onChange={(eyes) => void save(source === 'image' ? { eyes, image: '' } : { eyes })} />
          <p className="-mt-2 m-0 text-meta text-ink-3">挑的是平常的樣子；對話中眼睛仍會跟著狀態變。{source === 'image' && '點眼睛會切回吉祥物。'}</p>
          <span className="text-meta">顏色</span>
          <ColorPicker value={bot.color} onChange={(color) => void save({ color })} />
          <section className="flex flex-col gap-2 border-t border-line pt-4">
            <span className="flex items-center gap-2 text-ui">
              <Icon icon={Wand2} size={16} />
              用 AI 產生
            </span>
            <p className="m-0 text-meta text-ink-2">用一句話描述，產生 3 張候選讓你挑；用你在「AI 模型」設定的圖片模型。</p>
            <a href="/settings/models" onClick={linkClick('/settings/models', onClose)} className="w-fit text-ui-sm">
              先設定圖片模型
            </a>
          </section>
        </div>
      </div>
    </div>,
    document.body,
  )
}
