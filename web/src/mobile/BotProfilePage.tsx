import { useEffect, useState } from 'react'
import { ChevronRight, Crown, Monitor, MessageCircle, Pencil, Phone } from 'lucide-react'
import { ApiError, botsUpdate, hostsList, type BotsUpdateInput, type HostsListOutput } from '../api/generated'
import { botsStore, upsertBot, type Bot } from '../data/bots'
import { useStore } from '../data/store'
import { linkClick, navigate } from '../router'
import { SkillsTab } from '../settings/BotCapabilities'
import { MODES } from '../settings/botModes'
import { Avatar } from '../ui/Avatar'
import { COLORS, COLOR_IDS, COLOR_LABELS, SHAPE_IDS, SHAPE_LABELS, type AvatarColor, type AvatarShape } from '../ui/avatar-data'
import { Button } from '../ui/Button'
import { SegmentedControl } from '../ui/Controls'
import { Dialog } from '../ui/Dialog'
import { Icon } from '../ui/Icon'
import { useToast } from '../ui/toast-context'
import { cx } from '../ui/cx'
import { PhoneBar } from './Phone'

// L10 Bot 資料（/bots/:id/profile）：大頭像、名字、職稱與狀態；對話／電腦／通話；4 個分頁，變更自動儲存。
// L11 換頭像：點頭像打開的底部 sheet（形狀 10 種、顏色 10 種）。

type Tab = 'basic' | 'skills' | 'perm' | 'more'
const TABS: Array<{ value: Tab; label: string }> = [
  { value: 'basic', label: '基本' },
  { value: 'skills', label: '能力' },
  { value: 'perm', label: '權限與花費' },
  { value: 'more', label: '進階' },
]

function errText(err: unknown): string {
  return err instanceof ApiError || err instanceof Error ? err.message : String(err)
}

function useSave(bot: Bot | undefined) {
  const toast = useToast()
  return async (patch: Omit<BotsUpdateInput, 'id'>) => {
    if (!bot) return
    try {
      const b = await botsUpdate({ id: bot.id, ...patch })
      upsertBot({ ...bot, ...b })
    } catch (e) {
      // 提高權限要你在「待我處理」核准（pending_approval）：照樣用白話告訴你
      toast.show({ kind: e instanceof ApiError && e.code === 'pending_approval' ? 'done' : 'error', text: e instanceof ApiError && e.code === 'pending_approval' ? '已送到「待我處理」，核准後生效' : errText(e) })
    }
  }
}

/** L11 換頭像 sheet：上方預覽立即更新；「完成」才套用，「取消」不變。 */
export function AvatarSheet({ bot, open, onClose }: { bot: Bot; open: boolean; onClose: () => void }) {
  const [shape, setShape] = useState<AvatarShape>(bot.shape)
  const [color, setColor] = useState<AvatarColor>(bot.color)
  const [busy, setBusy] = useState(false)
  const save = useSave(bot)
  const done = async () => {
    setBusy(true)
    await save({ shape, color })
    setBusy(false)
    onClose()
  }
  return (
    <Dialog
      open={open}
      onClose={onClose}
      title="頭像"
      width={520}
      actions={
        <>
          <Button onClick={onClose}>取消</Button>
          <Button variant="primary" loading={busy} onClick={() => void done()}>
            完成
          </Button>
        </>
      }
    >
      <div className="flex items-center gap-4 py-1">
        <Avatar shape={shape} color={color} state="happy" dot="none" size={84} ring="#111111" label={`${SHAPE_LABELS[shape]}、${COLOR_LABELS[color]}`} />
        <p className="m-0 flex-1 text-sub text-ink-2">「{SHAPE_LABELS[shape]}」· 平面，只有眼睛、沒有嘴巴；眼睛跟著狀態變。</p>
      </div>
      <div className="flex items-baseline justify-between pt-1">
        <span className="text-sub text-ink-2">形狀</span>
        <span className="text-meta text-ink-3">游標＝品牌與預設</span>
      </div>
      <div role="radiogroup" aria-label="形狀" className="grid grid-cols-5 gap-1.5">
        {SHAPE_IDS.map((s) => (
          <button
            key={s}
            type="button"
            role="radio"
            aria-checked={s === shape}
            onClick={() => setShape(s)}
            className={cx('flex min-h-[54px] flex-col items-center justify-center gap-0.5 rounded-field border text-[11.5px]', s === shape ? 'border-focus bg-control text-ink' : 'border-transparent bg-inset text-ink-2')}
          >
            <Avatar shape={s} color={color} state="idle" dot="none" size={26} blink={false} />
            {SHAPE_LABELS[s]}
          </button>
        ))}
      </div>
      <span className="pt-1 text-sub text-ink-2">顏色</span>
      <div role="radiogroup" aria-label="顏色" className="grid grid-cols-5 gap-1.5">
        {COLOR_IDS.map((c) => (
          <button key={c} type="button" role="radio" aria-checked={c === color} aria-label={COLOR_LABELS[c]} onClick={() => setColor(c)} className="flex h-11 items-center justify-center">
            <span className={cx('size-8 rounded-full', c === color && 'ring-2 ring-ink ring-offset-2 ring-offset-panel')} style={{ background: COLORS[c] }} />
          </button>
        ))}
      </div>
      <p className="m-0 rounded-field bg-inset px-3 py-2 text-meta text-ink-3">上傳圖片、用 AI 產生頭像之後開放。</p>
    </Dialog>
  )
}

function Basic({ bot }: { bot: Bot }) {
  const save = useSave(bot)
  const [editing, setEditing] = useState(false)
  const [text, setText] = useState(bot.description)
  const [hosts, setHosts] = useState<HostsListOutput['hosts'] | null>(null)
  useEffect(() => {
    hostsList({})
      .then((o) => setHosts(o.hosts))
      .catch(() => setHosts([]))
  }, [])
  const local = hosts?.find((h) => h.kind === 'local')
  const hostOptions = (hosts ?? []).slice(0, 3).map((h) => ({ value: h.id, label: h.kind === 'local' ? '這台主機' : h.name }))
  return (
    <div className="flex flex-col overflow-hidden rounded-card bg-card">
      <div className="flex flex-col gap-1 border-b border-line px-4 py-3">
        <span className="text-sub text-ink-2">它做什麼</span>
        {editing ? (
          <div className="flex flex-col gap-2">
            <textarea value={text} onChange={(e) => setText(e.target.value)} rows={3} maxLength={200} className="w-full resize-none rounded-field bg-control px-3 py-2 text-ui text-ink outline-none focus-visible:outline-2 focus-visible:outline-focus" />
            <div className="flex justify-end gap-2">
              <Button onClick={() => setEditing(false)}>取消</Button>
              <Button
                variant="primary"
                onClick={() => {
                  void save({ description: text.trim() })
                  setEditing(false)
                }}
              >
                好
              </Button>
            </div>
          </div>
        ) : (
          <button type="button" onClick={() => setEditing(true)} className="flex min-h-11 items-center gap-2 text-left text-ui text-ink">
            <span className="flex-1">{bot.description || '還沒寫，點一下補一句'}</span>
            <Icon icon={ChevronRight} size={18} className="text-ink-3" />
          </button>
        )}
      </div>
      <div className="flex flex-col gap-2 border-b border-line px-4 py-3">
        <span className="text-sub text-ink-2">思考強度</span>
        <SegmentedControl
          label="思考強度"
          value={(bot.effort || 'medium') as 'low' | 'medium' | 'high'}
          onChange={(v) => void save({ effort: v })}
          options={[
            { value: 'low', label: '快' },
            { value: 'medium', label: '標準' },
            { value: 'high', label: '仔細' },
          ]}
        />
      </div>
      <div className="flex flex-col gap-2 px-4 py-3">
        <span className="flex justify-between text-sub text-ink-2">
          放在哪台主機
          {local && <span className="text-ink-3">{local.resources.hostname || local.name}</span>}
        </span>
        {hostOptions.length > 1 ? (
          <SegmentedControl label="放在哪台主機" value={bot.host_id || local?.id || hostOptions[0].value} onChange={(v) => void save({ host_id: v })} options={hostOptions} />
        ) : (
          <span className="text-ui">{hostOptions[0]?.label ?? '自動擺放'}</span>
        )}
      </div>
    </div>
  )
}

function Perm({ bot }: { bot: Bot }) {
  const save = useSave(bot)
  return (
    <div className="flex flex-col gap-3">
      <div role="radiogroup" aria-label="權限" className="flex flex-col overflow-hidden rounded-card bg-card">
        {MODES.map((m) => (
          <button
            key={m.value}
            type="button"
            role="radio"
            aria-checked={bot.approval_mode === m.value}
            onClick={() => bot.approval_mode !== m.value && void save({ approval_mode: m.value })}
            className="flex min-h-12 items-center gap-3 border-line px-4 py-2.5 text-left not-first:border-t"
          >
            <span className={cx('flex size-5 flex-none items-center justify-center rounded-full border-2', bot.approval_mode === m.value ? 'border-accent' : 'border-check-off')}>
              {bot.approval_mode === m.value && <span className="size-2.5 rounded-full bg-accent" />}
            </span>
            <span className="flex flex-col">
              <span className="text-ui text-ink">{m.label}</span>
              <span className="text-meta text-ink-2">{m.desc}</span>
            </span>
          </button>
        ))}
      </div>
      <p className="m-0 px-1 text-meta text-ink-3">放寬權限要你在「待我處理」核准。付款、簽約、刪除永遠先問你。</p>
      <a href="/settings/usage" onClick={linkClick('/settings/usage')} className="flex min-h-12 items-center gap-2 rounded-card bg-card px-4 text-ui text-ink">
        <span className="flex-1">花費上限與本月用量</span>
        <Icon icon={ChevronRight} size={18} className="text-ink-3" />
      </a>
    </div>
  )
}

function More({ bot }: { bot: Bot }) {
  const links = [
    { to: `/settings/bots/${bot.id}`, label: '引擎與模型' },
    { to: `/settings/bots/${bot.id}/soul`, label: '人設（常駐指示）' },
    { to: `/settings/bots/${bot.id}/memory`, label: '記憶' },
    { to: `/bots/${bot.id}/files`, label: '檔案' },
  ]
  return (
    <div className="flex flex-col overflow-hidden rounded-card bg-card">
      {links.map((l) => (
        <a key={l.to} href={l.to} onClick={linkClick(l.to)} className="flex min-h-12 items-center gap-2 border-line px-4 text-ui text-ink not-first:border-t">
          <span className="flex-1">{l.label}</span>
          <Icon icon={ChevronRight} size={18} className="text-ink-3" />
        </a>
      ))}
    </div>
  )
}

export function BotProfilePage({ botId }: { botId: string }) {
  const { bots, activity } = useStore(botsStore)
  const bot = bots?.find((b) => b.id === botId)
  const [tab, setTab] = useState<Tab>('basic')
  const [sheet, setSheet] = useState(false)
  if (!bot) return <main className="flex-1" aria-busy={!bots || undefined} />
  const st = activity[bot.id]?.state
  const waiting = st === 'waiting_input' || st === 'waiting_approval'
  const status = waiting ? '等你回覆' : st && st !== 'done' ? '工作中' : '待命'
  const quick = 'flex min-h-[64px] flex-1 flex-col items-center justify-center gap-1 rounded-card bg-card text-sub text-ink active:bg-control'
  return (
    <main className="flex min-h-dvh flex-1 flex-col bg-app pb-[max(16px,env(safe-area-inset-bottom))]" aria-label={`${bot.name}的資料`}>
      <PhoneBar title="Bot 資料" back={`/bots/${bot.id}`} />
      <div className="mx-auto flex w-full max-w-[560px] flex-col gap-3 px-4">
        <div className="flex flex-col items-center gap-1 pt-1 text-center">
          <button type="button" aria-label="換頭像" onClick={() => setSheet(true)} className="relative rounded-full">
            <Avatar shape={bot.shape} color={bot.color} state={waiting ? 'notifying' : st && st !== 'done' ? 'working' : 'idle'} dot={waiting ? 'waiting' : 'none'} size={96} ring="#070707" />
            <span className="absolute -bottom-1 -right-1 flex size-8 items-center justify-center rounded-full border-2 border-app bg-control text-ink">
              <Icon icon={Pencil} size={15} />
            </span>
          </button>
          <h1 className="m-0 mt-2 flex items-center gap-1.5 text-page">
            {bot.name}
            {bot.primary && <Icon icon={Crown} size={18} className="text-ink-3" label="主要 Bot" />}
          </h1>
          <span className="text-sub text-ink-2">{[bot.title, bot.description].filter(Boolean).join(' · ') || '還沒有職稱'}</span>
          <span className={cx('text-sub', waiting ? 'text-warning-ink' : 'text-ink-3')}>{status}</span>
        </div>
        <div className="flex gap-2">
          <button type="button" className={quick} onClick={() => navigate(`/bots/${bot.id}`)}>
            <Icon icon={MessageCircle} size={22} />
            對話
          </button>
          <button type="button" className={quick} onClick={() => navigate(`/bots/${bot.id}/computer`)}>
            <Icon icon={Monitor} size={22} />
            電腦
          </button>
          <button type="button" className={quick} onClick={() => navigate(`/bots/${bot.id}/call`)}>
            <Icon icon={Phone} size={22} />
            通話
          </button>
        </div>
        <SegmentedControl label="分頁" value={tab} onChange={setTab} options={TABS} className="w-full" />
        {tab === 'basic' && <Basic bot={bot} />}
        {tab === 'skills' && <SkillsTab bot={bot} />}
        {tab === 'perm' && <Perm bot={bot} />}
        {tab === 'more' && <More bot={bot} />}
        <p className="m-0 text-center text-meta text-ink-3">變更會自動儲存</p>
      </div>
      <AvatarSheet key={sheet ? 'open' : 'closed'} bot={bot} open={sheet} onClose={() => setSheet(false)} />
    </main>
  )
}
