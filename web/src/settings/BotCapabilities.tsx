import { useCallback, useEffect, useState } from 'react'
import { BookOpen, ChevronDown, ChevronUp, Info, MoreHorizontal, Pencil, Plus, Sparkles, Trash2, TriangleAlert, Undo2 } from 'lucide-react'
import {
  ApiError,
  botsUpdate,
  memoryHistory,
  memoryRead,
  memoryRestore,
  memoryWrite,
  skillsCreate,
  skillsDelete,
  skillsDisable,
  skillsEnable,
  skillsInstallToBot,
  skillsList,
  skillsUpdate,
  type BotsGetOutput,
  type MemoryReadOutput,
  type SkillsListOutput,
} from '../api/generated'
import { upsertBot } from '../data/bots'
import { linkClick, navigate } from '../router'
import { Button, IconButton } from '../ui/Button'
import { SegmentedControl, Switch } from '../ui/Controls'
import { ConfirmDialog } from '../ui/Dialog'
import { TextInput, Textarea } from '../ui/Field'
import { Icon } from '../ui/Icon'
import { MenuButton, type MenuItem } from '../ui/Menu'
import { StatusChip, type Tone } from '../ui/Pill'
import { useToast } from '../ui/toast-context'
import { cx } from '../ui/cx'
import { Block, Group, Note } from './parts'
import { ENGINE_LABEL } from './botModes'
import { errText, isPending } from './format'
import { actorName, changeKind, entryOrigin, originLink, shortDate, type ChangeKind, type MemChange } from './memoryHistory'

// Bot 設定的人設（SOUL.md）、技能（design/G05）、記憶（design/G06）分頁（C31；SK-*、MM-*）。

type Bot = BotsGetOutput
export type Skill = SkillsListOutput['skills'][number]
type Place = Skill['place']

/** 技能的來源標籤（G05：自訂技能／從對話學到／由分享匯入／XBot 內建）。 */
const SOURCE_LABEL: Record<string, { label: string; tone: Tone }> = {
  custom: { label: '自訂技能', tone: 'neutral' },
  learned: { label: '從對話學到', tone: 'neutral' },
  proposed: { label: 'Bot 提議', tone: 'neutral' },
  imported: { label: '匯入', tone: 'neutral' },
  xbot: { label: 'XBot 內建', tone: 'neutral' },
  hermes: { label: 'Hermes 內建', tone: 'neutral' },
}

// ── 人設 ──────────────────────────────────────────────

export function SoulTab({ bot, onSaved }: { bot: Bot; onSaved: (b: Bot) => void }) {
  const toast = useToast()
  const [soul, setSoul] = useState(bot.soul)
  const [desc, setDesc] = useState(bot.description)
  const [busy, setBusy] = useState(false)
  const dirty = soul !== bot.soul || desc !== bot.description
  const save = async () => {
    setBusy(true)
    try {
      const b = await botsUpdate({ id: bot.id, soul, description: desc })
      upsertBot(b)
      onSaved(b)
      toast.show({
        kind: 'done',
        text: `已更新${bot.name}的人設，從下一個對話開始生效`,
      })
    } catch (e) {
      toast.show({ kind: 'error', text: errText(e) })
    } finally {
      setBusy(false)
    }
  }
  return (
    <Block title="人設" sub={`${bot.name}的個性與常駐指示，會寫進它電腦裡的 SOUL.md。`}>
      <Group>
        <div className="flex flex-col gap-3 px-4 py-3">
          <TextInput label="一兩句話說明它負責什麼" value={desc} maxLength={1000} onChange={(e) => setDesc(e.target.value)} placeholder="例：每天整理重點、協調隊友" />
          <Textarea
            label="常駐指示"
            rows={8}
            value={soul}
            maxLength={4000}
            onChange={(e) => setSoul(e.target.value)}
            placeholder="例：說話簡短、先講結論。花錢或對外發送前一定先問我。"
            hint={`${soul.length} / 4,000 字 · 改了從下一個對話開始生效`}
          />
          <div className="flex justify-end gap-2">
            <Button
              disabled={!dirty || busy}
              onClick={() => {
                setSoul(bot.soul)
                setDesc(bot.description)
              }}
            >
              取消
            </Button>
            <Button variant="primary" disabled={!dirty} loading={busy} onClick={save}>
              儲存人設
            </Button>
          </div>
        </div>
      </Group>
    </Block>
  )
}

// ── 技能 ──────────────────────────────────────────────

const PLACES: { value: Place; label: string; hint: (n: string) => string }[] = [
  {
    value: 'list',
    label: '技能清單',
    hint: () => '每次對話都會帶上名稱與用途，需要時才讀完整步驟。',
  },
  {
    value: 'library',
    label: '技能庫',
    hint: () => '平常不佔用，需要時才載入。',
  },
  { value: 'off', label: '已停用', hint: (n) => `${n}不會使用。` },
]

type Draft = {
  id?: string
  name: string
  description: string
  instructions: string
}

export function SkillsTab({ bot }: { bot: Bot }) {
  const toast = useToast()
  const [data, setData] = useState<SkillsListOutput | null>(null)
  const [lib, setLib] = useState<Skill[]>([])
  const [err, setErr] = useState<string | null>(null)
  const [place, setPlace] = useState<Place>('list')
  const [draft, setDraft] = useState<Draft | null>(null)
  const [busy, setBusy] = useState(false)
  const [removing, setRemoving] = useState<Skill | null>(null)

  const load = useCallback(() => {
    skillsList({ bot_id: bot.id })
      .then((d) => {
        setData(d)
        setErr(null)
      })
      .catch((e) => setErr(errText(e)))
    skillsList({})
      .then((d) => setLib(d.skills))
      .catch(() => setLib([]))
  }, [bot.id])
  useEffect(load, [load])

  const run = async (p: Promise<unknown>, done: string) => {
    try {
      await p
      toast.show({ kind: 'done', text: done })
      load()
      return true
    } catch (e) {
      toast.show({ kind: isPending(e) ? 'info' : 'error', text: errText(e) })
      return false
    }
  }

  const save = async () => {
    if (!draft) return
    setBusy(true)
    const p = draft.id
      ? skillsUpdate({
          bot_id: bot.id,
          id: draft.id,
          name: draft.name,
          description: draft.description,
          instructions: draft.instructions,
        })
      : skillsCreate({
          bot_id: bot.id,
          name: draft.name,
          description: draft.description,
          instructions: draft.instructions,
          place: place === 'off' ? 'list' : place,
        })
    if (await run(p, draft.id ? `已儲存「${draft.name}」，從下一次開始生效` : `${bot.name}學會了「${draft.name}」`)) setDraft(null)
    setBusy(false)
  }

  if (err) {
    return (
      <p role="alert" className="m-0 text-ui-sm text-danger">
        讀不到技能：{err}
      </p>
    )
  }
  if (!data) return <p className="m-0 text-ui-sm text-ink-2">讀取中…</p>

  const shown = data.skills.filter((s) => s.place === place)
  const installed = new Set(data.skills.map((s) => s.name))
  const installable = lib.filter((s) => !installed.has(s.name))
  const hint = PLACES.find((p) => p.value === place)!.hint(bot.name)

  return (
    <div className="flex gap-5 max-lg:flex-col">
      <div className="flex min-w-0 flex-1 flex-col gap-3">
        <div className="flex items-center gap-3 max-sm:flex-col max-sm:items-stretch">
          <SegmentedControl
            label="技能位置"
            value={place}
            onChange={(v) => {
              setPlace(v)
              setDraft(null)
            }}
            options={PLACES.map((p) => ({
              value: p.value,
              label: `${p.label} ${data.counts[p.value] ?? 0}`,
            }))}
          />
          <span className="min-w-0 flex-1 text-[13px] text-ink-2">{hint}</span>
          <Button icon={Plus} onClick={() => setDraft({ name: '', description: '', instructions: '' })}>
            新增技能
          </Button>
        </div>
        {data.note && (
          <Note icon={<Icon icon={BookOpen} size={18} />}>
            <span>{data.note}</span>
          </Note>
        )}
        <Group label="技能">
          {draft && !draft.id && <SkillEditor draft={draft} onChange={setDraft} onCancel={() => setDraft(null)} onSave={save} busy={busy} isNew />}
          {shown.length === 0 && !draft && (
            <div className="flex flex-col items-center gap-2 px-6 py-10 text-center">
              <span className="text-ink-3">
                <Icon icon={Sparkles} size={28} />
              </span>
              <p className="m-0 text-ui">{place === 'list' ? '還沒有技能' : place === 'library' ? '技能庫是空的' : '沒有停用的技能'}</p>
              {place === 'list' && (
                <>
                  <p className="m-0 text-sub text-ink-2">教{bot.name}一件事：寫下什麼時候用、怎麼做。</p>
                  <Button variant="primary" icon={Plus} onClick={() => setDraft({ name: '', description: '', instructions: '' })}>
                    教它一件事
                  </Button>
                </>
              )}
            </div>
          )}
          {shown.map((s) =>
            draft?.id === s.id ? (
              <SkillEditor key={s.id} draft={draft} lastUsed={s.last_used_at} onChange={setDraft} onCancel={() => setDraft(null)} onSave={save} busy={busy} />
            ) : (
              <SkillRow
                key={s.id}
                skill={s}
                onEdit={() =>
                  setDraft({
                    id: s.id,
                    name: s.name,
                    description: s.description,
                    instructions: s.instructions ?? '',
                  })
                }
                menu={[
                  ...(s.place !== 'list'
                    ? [
                        {
                          label: '放到技能清單',
                          onSelect: () =>
                            void run(
                              skillsEnable({
                                bot_id: bot.id,
                                id: s.id,
                                place: 'list',
                              }),
                              `「${s.name}」放到技能清單`,
                            ),
                        },
                      ]
                    : []),
                  ...(s.place !== 'library'
                    ? [
                        {
                          label: '放到技能庫',
                          onSelect: () =>
                            void run(
                              skillsEnable({
                                bot_id: bot.id,
                                id: s.id,
                                place: 'library',
                              }),
                              `「${s.name}」放到技能庫`,
                            ),
                        },
                      ]
                    : []),
                  ...(s.place !== 'off'
                    ? [
                        {
                          label: '停用',
                          onSelect: () => void run(skillsDisable({ bot_id: bot.id, id: s.id }), `已停用「${s.name}」`),
                        },
                      ]
                    : []),
                  {
                    label: '刪除',
                    icon: Trash2,
                    danger: true,
                    separator: true,
                    onSelect: () => setRemoving(s),
                  },
                ]}
              />
            ),
          )}
        </Group>
      </div>

      <aside className="flex w-[320px] flex-none flex-col gap-3 max-lg:w-full">
        <Group>
          <div className="flex flex-col gap-2 px-4 py-3.5">
            <span className="flex items-center gap-2 text-ui">
              <Icon icon={BookOpen} size={18} />
              從工作區技能庫加入
            </span>
            <span className="text-[13px] text-ink-2">所有 Bot 共用的技能；加入後各改各的。</span>
            {installable.length === 0 ? (
              <span className="text-[13px] text-ink-3">沒有可以加入的技能。</span>
            ) : (
              installable.slice(0, 6).map((s) => (
                <div key={s.id} className="flex items-center gap-2 border-t border-line pt-2">
                  <span className="min-w-0 flex-1">
                    <span className="block truncate text-ui-sm">{s.name}</span>
                    {!s.enabled && <span className="block text-[12px] text-warning-ink">技能庫裡是停用的，加入後也是停用</span>}
                  </span>
                  <Button size="sm" onClick={() => void run(skillsInstallToBot({ id: s.id, bot_id: bot.id }), `已把「${s.name}」加給${bot.name}`)}>
                    加入
                  </Button>
                </div>
              ))
            )}
            <a href="/settings/advanced/lab" onClick={linkClick('/settings/advanced/lab')} className="text-[13px]">
              管理技能庫
            </a>
          </div>
        </Group>
      </aside>

      <ConfirmDialog
        open={!!removing}
        danger
        title={`刪除「${removing?.name ?? ''}」？`}
        description="連同附帶的檔案一起刪掉，無法復原。只是先不用的話，可以停用。"
        confirmText="刪除"
        onCancel={() => setRemoving(null)}
        onConfirm={async () => {
          const s = removing
          setRemoving(null)
          if (s) await run(skillsDelete({ bot_id: bot.id, id: s.id }), `已刪除「${s.name}」`)
        }}
      />
    </div>
  )
}

function SkillRow({ skill, onEdit, menu }: { skill: Skill; onEdit: () => void; menu: MenuItem[] }) {
  const src = SOURCE_LABEL[skill.source] ?? SOURCE_LABEL.custom
  return (
    <div className={cx('flex min-h-14 items-center gap-3 border-line px-4 py-2.5 not-first:border-t')}>
      <span className="flex size-8 flex-none items-center justify-center rounded-field bg-control text-ink-2">
        <Icon icon={Sparkles} size={18} />
      </span>
      <span className="min-w-0 flex-1">
        <span className="flex flex-wrap items-center gap-2">
          <span className="text-ui">{skill.name}</span>
          <StatusChip size="sm" tone={src.tone}>
            {src.label}
          </StatusChip>
          {skill.place === 'library' && (
            <StatusChip size="sm" tone="accent">
              技能庫
            </StatusChip>
          )}
        </span>
        <span className="block truncate text-[13px] text-ink-2">{skill.description || '（沒有寫什麼時候使用）'}</span>
      </span>
      <IconButton icon={Pencil} label={`修改「${skill.name}」`} onClick={onEdit} />
      <MenuButton
        label={`「${skill.name}」的更多動作`}
        align="end"
        items={menu}
        trigger={(p) => <IconButton icon={MoreHorizontal} label={`「${skill.name}」的更多動作`} {...p} />}
      />
    </div>
  )
}

function SkillEditor({
  draft,
  onChange,
  onCancel,
  onSave,
  busy,
  isNew,
  lastUsed,
}: {
  draft: Draft
  onChange: (d: Draft) => void
  onCancel: () => void
  onSave: () => void
  busy: boolean
  isNew?: boolean
  lastUsed?: string
}) {
  const ok = draft.name.trim() !== '' && draft.instructions.trim() !== ''
  return (
    <div className="flex flex-col gap-3 border-line bg-inset/40 px-4 py-3.5 not-first:border-t">
      <span className="text-ui">{isNew ? '新增技能' : draft.name}</span>
      <div className="flex gap-3 max-sm:flex-col">
        <TextInput
          label="技能名稱"
          wrapperClassName="sm:w-[200px]"
          value={draft.name}
          maxLength={64}
          onChange={(e) => onChange({ ...draft, name: e.target.value })}
          autoFocus={isNew}
        />
        <TextInput
          label="什麼時候使用"
          wrapperClassName="flex-1"
          value={draft.description}
          maxLength={1024}
          onChange={(e) => onChange({ ...draft, description: e.target.value })}
          placeholder="例：每天早上，或我問「今天有什麼事」的時候"
        />
      </div>
      <Textarea label="怎麼做" rows={5} value={draft.instructions} onChange={(e) => onChange({ ...draft, instructions: e.target.value })} placeholder={'1. …\n2. …'} />
      <div className="flex items-center gap-2 max-sm:flex-col max-sm:items-stretch">
        <span className="min-w-0 flex-1 text-[13px] text-ink-3">
          {lastUsed ? `上次用：${new Date(lastUsed).toLocaleString('zh-TW', { dateStyle: 'short', timeStyle: 'short' })} · ` : ''}
          改了從下一次開始生效
        </span>
        <Button variant="ghost" onClick={onCancel} disabled={busy}>
          取消
        </Button>
        <Button variant="primary" onClick={onSave} disabled={!ok} loading={busy}>
          儲存技能
        </Button>
      </div>
    </div>
  )
}

// ── 記憶 ──────────────────────────────────────────────

type Target = 'user' | 'memory'

const TARGETS: { value: Target; title: string; sub: (n: string) => string }[] = [
  {
    value: 'user',
    title: '關於你',
    sub: (n) => `${n}記得的你：偏好、身分、習慣`,
  },
  {
    value: 'memory',
    title: 'Bot 記住的事',
    sub: () => '工作上學到的事：環境、做法、專案位置',
  },
]

export function MemoryTab({ bot, onBot }: { bot: Bot; onBot: (b: Bot) => void }) {
  // 記憶是 Hermes 的 MEMORY.md／USER.md：其他引擎不讀也不寫，只說明
  if (bot.engine && bot.engine !== 'hermes') {
    const to = `/settings/bots/${bot.id}/advanced`
    return (
      <Note
        icon={<Icon icon={Info} size={18} />}
        action={
          <a href={to} onClick={linkClick(to)} className="text-ui-sm text-accent-ink">
            換引擎
          </a>
        }
      >
        {bot.name}用 {ENGINE_LABEL[bot.engine] ?? bot.engine} 引擎，不會讀這裡的記憶，也不會寫進來。換回 Hermes 才會用到。
      </Note>
    )
  }
  return <HermesMemory bot={bot} onBot={onBot} />
}

function HermesMemory({ bot, onBot }: { bot: Bot; onBot: (b: Bot) => void }) {
  const toast = useToast()
  const [mem, setMem] = useState<MemoryReadOutput | null>(null)
  const [hist, setHist] = useState<MemChange[]>([])
  const [err, setErr] = useState<string | null>(null)

  const load = useCallback(() => {
    memoryRead({ bot_id: bot.id })
      .then((m) => {
        setMem(m)
        setErr(null)
      })
      .catch((e) => setErr(errText(e)))
    memoryHistory({ bot_id: bot.id })
      .then((h) => setHist(h.changes))
      .catch(() => setHist([]))
  }, [bot.id])
  useEffect(load, [load])

  // 記憶在對話開始時載入：已經開著的對話串看不到這次的修改
  const saved = (done: string) =>
    toast.show({
      kind: 'done',
      text: `${done}。新的對話串才會用到`,
      action: { label: '開新對話串', onClick: () => navigate(`/bots/${bot.id}/threads/new`) },
    })

  const write = async (target: Target, entries: string[], done: string) => {
    if (!mem) return false
    try {
      const m = await memoryWrite({
        bot_id: bot.id,
        target,
        entries,
        previous: mem[target].entries,
      })
      setMem(m)
      saved(done)
      memoryHistory({ bot_id: bot.id })
        .then((h) => setHist(h.changes))
        .catch(() => {})
      return true
    } catch (e) {
      toast.show({ kind: 'error', text: errText(e) })
      if (e instanceof ApiError && e.code === 'conflict') load()
      return false
    }
  }

  const toggle = async (on: boolean) => {
    try {
      const b = await botsUpdate({ id: bot.id, memory_enabled: on })
      upsertBot(b)
      onBot(b)
      toast.show({
        kind: 'done',
        text: on ? `${bot.name}會使用記憶` : `已關掉${bot.name}的記憶`,
      })
    } catch (e) {
      toast.show({ kind: 'error', text: errText(e) })
    }
  }

  const undo = async (id: string) => {
    try {
      const m = await memoryRestore({ bot_id: bot.id, change_id: id })
      setMem(m)
      saved('已復原')
      load()
    } catch (e) {
      toast.show({ kind: 'error', text: errText(e) })
      load()
    }
  }

  if (err) {
    return (
      <p role="alert" className="m-0 text-ui-sm text-danger">
        讀不到記憶：{err}
      </p>
    )
  }
  if (!mem) return <p className="m-0 text-ui-sm text-ink-2">讀取中…</p>
  const on = bot.memory_enabled

  return (
    <div className="flex flex-col gap-3">
      <div className="flex items-center justify-end">
        <Switch checked={on} onChange={(v) => void toggle(v)} label={`讓${bot.name}使用記憶`}>
          讓{bot.name}使用記憶
        </Switch>
      </div>
      {!on && (
        <Note tone="warning" icon={<Icon icon={TriangleAlert} size={18} />}>
          記憶關著：下面的內容不會帶進對話，{bot.name}
          也不會再記新的事。下次開機生效。
        </Note>
      )}
      <div className="flex gap-5 max-lg:flex-col">
        <div className={cx('flex min-w-0 flex-1 flex-col gap-4')}>
          {TARGETS.map((t) => (
            <MemoryList
              key={t.value}
              title={t.title}
              sub={t.sub(bot.name)}
              list={mem[t.value]}
              origin={(e) => entryOrigin(e, t.value, hist)}
              bot={bot}
              onWrite={(entries, done) => write(t.value, entries, done)}
            />
          ))}
        </div>
        <aside className="flex w-[320px] flex-none flex-col gap-3 max-lg:w-full">
          <Group label="最近的變更">
            <div className="flex flex-col px-4 py-3.5">
              <span className="pb-2 text-ui">最近的變更</span>
              {hist.length === 0 && <span className="text-[13px] text-ink-3">還沒有變更。</span>}
              {hist.slice(0, 8).map((c) => (
                <Change key={c.id} c={c} bot={bot} onUndo={() => void undo(c.id)} />
              ))}
            </div>
          </Group>
          <p className="m-0 px-1 text-[12px] text-ink-3">寫入前會自動遮蔽密碼與金鑰。超過上限的部分不會帶進對話。</p>
        </aside>
      </div>
    </div>
  )
}

/** 在哪條對話串記的：「在「標題」中」，標題連到那條對話串（或群組）。不知道時不顯示。 */
function Where({ c, botId }: { c: MemChange; botId: string }) {
  const to = originLink(c, botId)
  if (!to) return null
  const link = (
    <a href={to} onClick={linkClick(to)} className="text-ink-2 underline underline-offset-2">
      {c.thread_title || '對話'}
    </a>
  )
  return c.thread_title ? <>在「{link}」中</> : <>在{link}中</>
}

/** 一條記憶的來歷，例：水哥哥 · 10/18 在「整理客戶名單」中記下。 */
function Origin({ c, bot }: { c: MemChange; bot: Bot }) {
  const verb = c.actor.startsWith('bot:') ? '記下' : changeKind(c) === '修改' ? '修改' : '新增'
  return (
    <span className="block text-[12px] text-ink-3">
      {actorName(c.actor, bot.name)} · {shortDate(c.created_at)} <Where c={c} botId={bot.id} />
      {verb}
    </span>
  )
}

const KIND_TONE: Record<ChangeKind, string> = { 新增: 'text-success', 刪除: 'text-danger', 修改: 'text-ink-2', 整理: 'text-ink-2' }

function Change({ c, bot, onUndo }: { c: MemChange; bot: Bot; onUndo: () => void }) {
  const kind = changeKind(c)
  const more = Math.max(c.added.length, c.removed.length) - 1
  const one = c.added[0] ?? c.removed[0] ?? ''
  return (
    <div className="flex flex-col gap-0.5 border-line py-2 not-first:border-t">
      <span className="flex items-center gap-2 text-[13px]">
        <span className={KIND_TONE[kind]}>{kind}</span>
        <span className="text-ink-3">
          {actorName(c.actor, bot.name)} ·{' '}
          {new Date(c.created_at).toLocaleString('zh-TW', {
            dateStyle: 'short',
            timeStyle: 'short',
          })}
        </span>
        <span className="flex-1" />
        {c.undoable && (
          <button type="button" onClick={onUndo} className="inline-flex items-center gap-1 bg-transparent p-0 text-[13px] text-accent-ink">
            <Icon icon={Undo2} size={14} />
            復原
          </button>
        )}
      </span>
      {kind === '修改' ? (
        <>
          <span className="truncate text-ui-sm text-ink-3 line-through" title={c.removed[0]}>
            {c.removed[0]}
          </span>
          <span className="truncate text-ui-sm" title={c.added[0]}>
            → {c.added[0]}
          </span>
        </>
      ) : kind === '整理' ? (
        <span className="text-ui-sm text-ink-3">內容沒變，只調整了順序或重複的條目</span>
      ) : (
        <span className="truncate text-ui-sm" title={one}>
          {one}
        </span>
      )}
      {more > 0 && <span className="text-[12px] text-ink-3">還有 {more} 條</span>}
      {originLink(c, bot.id) && (
        <span className="truncate text-[12px] text-ink-3">
          <Where c={c} botId={bot.id} />
          {c.actor.startsWith('bot:') ? '記下' : '修改'}
        </span>
      )}
    </div>
  )
}

function MemoryList({
  title,
  sub,
  list,
  origin,
  bot,
  onWrite,
}: {
  title: string
  sub: string
  list: MemoryReadOutput['user']
  origin: (entry: string) => MemChange | undefined
  bot: Bot
  onWrite: (entries: string[], done: string) => Promise<boolean>
}) {
  const [editing, setEditing] = useState<number | null>(null)
  const [text, setText] = useState('')
  const [open, setOpen] = useState(true)
  const ratio = list.limit > 0 ? Math.min(1, list.used / list.limit) : 0
  const high = ratio > 0.8
  const commit = async () => {
    const v = text.trim()
    if (editing === null) return
    const next = [...list.entries]
    if (editing === -1) {
      if (!v) return setEditing(null)
      next.push(v)
    } else if (v) next[editing] = v
    else next.splice(editing, 1)
    if (await onWrite(next, editing === -1 ? '已新增一條' : v ? '已儲存' : '已刪除一條')) setEditing(null)
  }
  return (
    <Group label={title}>
      <div className="flex items-start gap-4 px-4 py-3.5">
        <span className="min-w-0 flex-1">
          <span className="block text-ui">{title}</span>
          <span className="block text-[13px] text-ink-2">{sub}</span>
        </span>
        <span className="flex w-[150px] flex-none flex-col items-end gap-1.5">
          <span className={cx('text-[13px]', high ? 'text-warning-ink' : 'text-ink-2')}>
            已用 {list.used.toLocaleString()} / {list.limit.toLocaleString()} 字
          </span>
          <span
            className="h-1 w-full overflow-hidden rounded-full bg-control"
            role="meter"
            aria-label={`${title}用量`}
            aria-valuenow={list.used}
            aria-valuemin={0}
            aria-valuemax={list.limit}
          >
            <span className={cx('block h-full rounded-full', high ? 'bg-warning' : 'bg-accent')} style={{ width: `${ratio * 100}%` }} />
          </span>
        </span>
        <IconButton icon={open ? ChevronUp : ChevronDown} size="sm" label={open ? `收起${title}` : `展開${title}`} onClick={() => setOpen(!open)} />
      </div>
      {high && open && <p className="m-0 border-t border-line px-4 py-2 text-[13px] text-warning-ink">快滿了：刪掉過時的，或把幾條合成一條。</p>}
      {open &&
        list.entries.map((e, i) => {
          if (editing === i) return <EntryEditor key={i} text={text} setText={setText} onCancel={() => setEditing(null)} onSave={commit} />
          const o = origin(e)
          return (
            <div key={i} className="flex min-h-12 items-center gap-2 border-t border-line px-4 py-2">
              <span className="min-w-0 flex-1">
                <span className="block text-ui-sm whitespace-pre-wrap">{e}</span>
                {o && <Origin c={o} bot={bot} />}
              </span>
              <IconButton
                icon={Pencil}
                label="修改這一條"
                onClick={() => {
                  setEditing(i)
                  setText(e)
                }}
              />
              <IconButton
                icon={Trash2}
                label="刪除這一條"
                onClick={() =>
                  void onWrite(
                    list.entries.filter((_, j) => j !== i),
                    '已刪除一條',
                  )
                }
              />
            </div>
          )
        })}
      {open &&
        (editing === -1 ? (
          <EntryEditor text={text} setText={setText} onCancel={() => setEditing(null)} onSave={commit} />
        ) : (
          <button
            type="button"
            onClick={() => {
              setEditing(-1)
              setText('')
            }}
            className="flex h-12 items-center gap-2 border-t border-line bg-transparent px-4 text-left text-ui-sm text-ink hover:bg-control/40"
          >
            <Icon icon={Plus} size={18} />
            新增一條
          </button>
        ))}
    </Group>
  )
}

function EntryEditor({ text, setText, onCancel, onSave }: { text: string; setText: (s: string) => void; onCancel: () => void; onSave: () => void }) {
  return (
    <div className="flex flex-col gap-2 border-t border-line bg-inset/40 px-4 py-3">
      <TextInput
        label="記憶內容"
        hideLabel
        value={text}
        maxLength={2000}
        autoFocus
        onChange={(e) => setText(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === 'Enter' && !e.nativeEvent.isComposing) onSave()
          if (e.key === 'Escape') onCancel()
        }}
      />
      <div className="flex items-center gap-2">
        <span className="min-w-0 flex-1 text-[13px] text-ink-3">改了從下一次對話開始生效</span>
        <Button variant="ghost" onClick={onCancel}>
          取消
        </Button>
        <Button variant="primary" onClick={onSave}>
          儲存
        </Button>
      </div>
    </div>
  )
}
