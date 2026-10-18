import { useCallback, useEffect, useRef, useState } from 'react'
import { MoreHorizontal, Search, Sparkles, Trash2, TriangleAlert, Upload } from 'lucide-react'
import { skillsDelete, skillsDisable, skillsEnable, skillsImport, skillsInstallToBot, skillsList, type SkillsListOutput } from '../api/generated'
import { botsStore } from '../data/bots'
import { useStore } from '../data/store'
import { Avatar } from '../ui/Avatar'
import { Button, IconButton } from '../ui/Button'
import { Switch } from '../ui/Controls'
import { ConfirmDialog, Dialog } from '../ui/Dialog'
import { TextInput } from '../ui/Field'
import { Icon } from '../ui/Icon'
import { MenuButton } from '../ui/Menu'
import { StatusChip } from '../ui/Pill'
import { useToast } from '../ui/toast-context'
import { Block, Group, SectionHeader } from './parts'
import { errText } from './format'

// 設定 › 進階 › 技能庫（design/J09，C31 只做技能庫：搜尋、匯入（預設停用）、檢視內容、開關、裝到 Bot、刪除）。

type Skill = SkillsListOutput['skills'][number]

function toBase64(buf: ArrayBuffer): string {
  let s = ''
  const bytes = new Uint8Array(buf)
  for (let i = 0; i < bytes.length; i += 0x8000) s += String.fromCharCode(...bytes.subarray(i, i + 0x8000))
  return btoa(s)
}

export function SkillsLabPage() {
  const toast = useToast()
  const bots = (useStore(botsStore).bots ?? []).filter((b) => !b.archived)
  const [skills, setSkills] = useState<Skill[] | null>(null)
  const [err, setErr] = useState<string | null>(null)
  const [q, setQ] = useState('')
  const [viewing, setViewing] = useState<Skill | null>(null)
  const [removing, setRemoving] = useState<Skill | null>(null)
  const file = useRef<HTMLInputElement>(null)

  const load = useCallback(() => {
    skillsList({})
      .then((d) => {
        setSkills(d.skills)
        setErr(null)
      })
      .catch((e) => setErr(errText(e)))
  }, [])
  useEffect(load, [load])

  const run = async (p: Promise<unknown>, done: string) => {
    try {
      await p
      toast.show({ kind: 'done', text: done })
      load()
    } catch (e) {
      toast.show({ kind: 'error', text: errText(e) })
    }
  }

  const onFile = async (f: File | undefined) => {
    if (!f) return
    try {
      const s = await skillsImport({
        filename: f.name,
        content_base64: toBase64(await f.arrayBuffer()),
      })
      toast.show({
        kind: 'done',
        text: `已匯入「${s.name}」，先看過內容再打開`,
      })
      load()
      setViewing(s)
    } catch (e) {
      toast.show({ kind: 'error', text: `沒有匯入：${errText(e)}` })
    } finally {
      if (file.current) file.current.value = ''
    }
  }

  const words = q.trim().toLowerCase()
  const shown = (skills ?? []).filter((s) => !words || (s.name + ' ' + s.description).toLowerCase().includes(words))

  return (
    <>
      <SectionHeader title="技能庫與實驗" sub="技能是所有 Bot 共用的做事方法；加到 Bot 身上後，它需要時才會載入。" />
      <Block
        title={`技能庫 · ${skills?.length ?? 0}`}
        aside={
          <div className="flex items-center gap-2">
            <TextInput
              label="搜尋技能"
              hideLabel
              icon={Search}
              placeholder="搜尋技能"
              value={q}
              onChange={(e) => setQ(e.target.value)}
              wrapperClassName="w-[240px] max-sm:w-[150px]"
            />
            <Button icon={Upload} onClick={() => file.current?.click()}>
              匯入技能
            </Button>
            <input ref={file} type="file" accept=".md,.zip,text/markdown,application/zip" hidden aria-label="選擇技能檔案" onChange={(e) => void onFile(e.target.files?.[0])} />
          </div>
        }
      >
        {err && (
          <p role="alert" className="m-0 text-ui-sm text-danger">
            讀不到技能庫：{err}
          </p>
        )}
        {skills && (
          <Group label="技能庫">
            {skills.length === 0 && (
              <div className="flex flex-col items-center gap-2 px-6 py-10 text-center">
                <span className="text-ink-3">
                  <Icon icon={Sparkles} size={28} />
                </span>
                <p className="m-0 text-ui">技能庫還是空的</p>
                <p className="m-0 text-sub text-ink-2">匯入一個 SKILL.md 或 .zip，或在 Bot 設定的「技能」新增。</p>
              </div>
            )}
            {skills.length > 0 && shown.length === 0 && <p className="m-0 px-4 py-6 text-center text-ui-sm text-ink-2">找不到「{q}」相關的技能。</p>}
            {shown.map((s) => (
              <div key={s.id} className="border-line not-first:border-t">
                <div className="flex min-h-[62px] items-center gap-3 px-4 py-2.5">
                  <span className="flex size-9 flex-none items-center justify-center rounded-field bg-control text-ink-2">
                    <Icon icon={Sparkles} size={18} />
                  </span>
                  <span className="min-w-0 flex-1">
                    <span className="flex flex-wrap items-center gap-2">
                      <span className="text-ui">/{s.name}</span>
                      {s.source === 'imported' && !s.enabled && (
                        <StatusChip size="sm" tone="warning">
                          剛匯入 · 已停用
                        </StatusChip>
                      )}
                    </span>
                    <span className="block truncate text-[13px] text-ink-2">{s.description}</span>
                  </span>
                  <MenuButton
                    label={`加「${s.name}」給 Bot`}
                    align="end"
                    items={
                      bots.length
                        ? bots.map((b) => ({
                            label: `加給${b.name}`,
                            onSelect: () => void run(skillsInstallToBot({ id: s.id, bot_id: b.id }), `已把「${s.name}」加給${b.name}`),
                          }))
                        : [{ label: '還沒有 Bot', disabled: true }]
                    }
                    trigger={(p) => (
                      <button type="button" {...p} aria-label={`加「${s.name}」給 Bot`} className="flex items-center gap-1 bg-transparent p-0 text-[13px] text-ink-2 hover:text-ink">
                        {bots.length ? bots.slice(0, 3).map((b) => <Avatar key={b.id} shape={b.shape} color={b.color} size={20} blink={false} />) : null}
                        <span>指派</span>
                      </button>
                    )}
                  />
                  <Switch
                    checked={s.enabled}
                    label={s.enabled ? `停用「${s.name}」` : `打開「${s.name}」`}
                    onChange={(on) => void run(on ? skillsEnable({ id: s.id }) : skillsDisable({ id: s.id }), on ? `已打開「${s.name}」` : `已停用「${s.name}」`)}
                  />
                  <MenuButton
                    label={`「${s.name}」的更多動作`}
                    align="end"
                    items={[
                      { label: '檢視內容', onSelect: () => setViewing(s) },
                      {
                        label: '刪除',
                        icon: Trash2,
                        danger: true,
                        separator: true,
                        onSelect: () => setRemoving(s),
                      },
                    ]}
                    trigger={(p) => <IconButton icon={MoreHorizontal} label={`「${s.name}」的更多動作`} {...p} />}
                  />
                </div>
                {s.source === 'imported' && !s.enabled && (
                  <div className="flex items-center gap-3 bg-warning-soft px-4 py-2.5 text-[13px] text-warning-ink max-sm:flex-wrap">
                    <Icon icon={TriangleAlert} size={18} />
                    <span className="min-w-0 flex-1">
                      /{s.name} 是從外面匯入的
                      {s.review && s.review.commands.length > 0 && (
                        <>
                          ，會執行 <code className="font-mono">{s.review.commands[0].slice(0, 40)}</code>
                          {s.review.commands.length > 1 ? ` 等 ${s.review.commands.length} 個指令` : ''}
                        </>
                      )}
                      {s.review && s.review.scripts.length > 0 && `，附帶 ${s.review.scripts.length} 個程式`}
                      。看過步驟再打開。
                    </span>
                    <Button size="sm" onClick={() => setViewing(s)}>
                      檢視內容
                    </Button>
                  </div>
                )}
              </div>
            ))}
          </Group>
        )}
      </Block>

      <Dialog
        open={!!viewing}
        onClose={() => setViewing(null)}
        width={640}
        title={viewing ? `/${viewing.name}` : ''}
        description={viewing?.description}
        actions={
          <>
            <Button onClick={() => setViewing(null)}>關閉</Button>
            {viewing && !viewing.enabled && (
              <Button
                variant="primary"
                onClick={() => {
                  const s = viewing
                  setViewing(null)
                  void run(skillsEnable({ id: s.id }), `已打開「${s.name}」`)
                }}
              >
                看過了，打開
              </Button>
            )}
          </>
        }
      >
        {viewing?.review && (viewing.review.commands.length > 0 || viewing.review.scripts.length > 0) && (
          <div className="mb-3 flex flex-col gap-1 rounded-field bg-warning-soft px-3 py-2.5 text-[13px] text-warning-ink">
            {viewing.review.commands.length > 0 && <span>會執行的指令：</span>}
            {viewing.review.commands.map((c, i) => (
              <code key={i} className="font-mono break-all">
                {c}
              </code>
            ))}
            {viewing.review.scripts.length > 0 && <span>附帶的程式：{viewing.review.scripts.join('、')}</span>}
          </div>
        )}
        <pre tabIndex={0} className="m-0 max-h-[50vh] overflow-auto rounded-field bg-inset p-3 font-mono text-[13px] whitespace-pre-wrap">{viewing?.instructions}</pre>
        {viewing?.files && viewing.files.length > 0 && <p className="m-0 mt-2 text-[13px] text-ink-2">附帶的檔案：{viewing.files.join('、')}</p>}
      </Dialog>

      <ConfirmDialog
        open={!!removing}
        danger
        title={`刪除「${removing?.name ?? ''}」？`}
        description="從技能庫刪掉，無法復原。已經加到 Bot 身上的不受影響。"
        confirmText="刪除"
        onCancel={() => setRemoving(null)}
        onConfirm={async () => {
          const s = removing
          setRemoving(null)
          if (s) await run(skillsDelete({ id: s.id }), `已刪除「${s.name}」`)
        }}
      />
    </>
  )
}
