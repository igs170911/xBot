import { useEffect, useRef, useState } from 'react'
import { Check, MessageSquareText, Pause, Play, RotateCw, Save, Sparkles, Trash2, X } from 'lucide-react'
import { teachingDiscard, teachingDraft, teachingDrop, teachingNote, teachingPause, teachingSave, teachingStop } from '../api/generated'
import { navigate } from '../router'
import { Button, IconButton } from '../ui/Button'
import { Checkbox } from '../ui/Controls'
import { ConfirmDialog } from '../ui/Dialog'
import { fieldClass, Textarea, TextInput } from '../ui/Field'
import { Icon } from '../ui/Icon'
import { StatusChip } from '../ui/Pill'
import { useToast } from '../ui/toast-context'
import { cx } from '../ui/cx'
import { isDraft, isRecording, loadShot, setLesson, SOURCE_LABEL, type Lesson, type Step } from './teachingData'

function errText(err: unknown): string {
  return err instanceof Error ? err.message : String(err)
}

/** 示範截圖的縮圖：紅框標出點擊的位置（E04）。讀不到（已刪掉）時是灰底。 */
export function ShotThumb({ botId, lessonId, name, mark, className }: { botId: string; lessonId: string; name?: string; mark?: number[]; className?: string }) {
  const [src, setSrc] = useState<string | null>(null)
  useEffect(() => {
    let live = true
    if (name) void loadShot(botId, lessonId, name).then((s) => live && setSrc(s))
    return () => {
      live = false
    }
  }, [botId, lessonId, name])
  return (
    <span className={cx('relative block flex-none overflow-hidden rounded-[6px] bg-control', className)} data-testid="shot-thumb">
      {src && <img src={src} alt="" className="block size-full object-cover object-left-top" />}
      {src && mark && mark.length === 4 && (
        <span
          aria-hidden="true"
          className="absolute rounded-[2px] border-[1.5px] border-danger"
          style={{ left: `${mark[0] * 100}%`, top: `${mark[1] * 100}%`, width: `${Math.max(mark[2] * 100, 6)}%`, height: `${Math.max(mark[3] * 100, 8)}%` }}
        />
      )}
    </span>
  )
}

function StepRow({ step, n, botId, lessonId, last, recording, onDrop }: { step: Step; n: number; botId: string; lessonId: string; last: boolean; recording: boolean; onDrop?: () => void }) {
  const note = step.source === 'user'
  return (
    <li className={cx('flex items-center gap-3 rounded-[10px] px-1.5 py-1.5', last && recording && 'bg-inset')} data-testid="teach-step">
      {note ? (
        <span className="flex h-[45px] w-[72px] flex-none max-sm:h-[38px] max-sm:w-[60px] items-center justify-center rounded-[6px] bg-inset text-ink-2">
          <Icon icon={MessageSquareText} size={18} />
        </span>
      ) : (
        <ShotThumb botId={botId} lessonId={lessonId} name={step.shot} mark={step.mark} className="h-[45px] w-[72px] max-sm:h-[38px] max-sm:w-[60px]" />
      )}
      <span className="w-5 flex-none text-center text-sub text-ink-2">{n}</span>
      <span className={cx('flex-none rounded-[6px] px-2 py-0.5 text-[12px] max-sm:hidden', note ? 'bg-accent-soft text-accent-ink' : 'bg-control text-ink-2')}>
        {SOURCE_LABEL[step.source] ?? step.source}
      </span>
      <span className="min-w-0 flex-1 text-ui-sm leading-snug">{note ? `「${step.value ?? step.text}」` : step.text}</span>
      {last && recording && (
        <span role="img" aria-label="記錄中" className="flex flex-none items-center gap-1 text-meta text-danger">
          <span className="size-1.5 rounded-full bg-danger" />
          <span aria-hidden="true" className="max-sm:hidden">記錄中</span>
        </span>
      )}
      {onDrop && <IconButton icon={Trash2} label={`刪掉第 ${n} 步`} size="sm" onClick={onDrop} />}
    </li>
  )
}

/** 示範中（design/E04）：電腦畫面上方的教學列：標題「教學中 · 第 N 步」、暫停、完成；即時步驟清單；補充說明。 */
export function TeachBar({ lesson, botName, className }: { lesson: Lesson; botName: string; className?: string }) {
  const toast = useToast()
  const [note, setNote] = useState('')
  const [busy, setBusy] = useState<'pause' | 'note' | 'stop' | 'discard' | null>(null)
  const [confirm, setConfirm] = useState(false)
  const list = useRef<HTMLOListElement>(null)
  const paused = lesson.state === 'paused'
  const steps = lesson.steps
  const ref = { bot_id: lesson.bot_id, lesson_id: lesson.id }
  const act = async (kind: typeof busy, fn: () => Promise<Lesson | unknown>) => {
    setBusy(kind)
    try {
      const out = await fn()
      if (out && typeof out === 'object' && 'state' in out) setLesson(lesson.bot_id, out as Lesson)
      return true
    } catch (err) {
      toast.show({ kind: 'error', text: errText(err) })
      return false
    } finally {
      setBusy(null)
    }
  }
  // 新的一步進來就捲到最下面。
  useEffect(() => {
    const el = list.current
    if (el) el.scrollTop = el.scrollHeight
  }, [steps.length])

  const addNote = async () => {
    const text = note.trim()
    if (!text) return
    if (await act('note', () => teachingNote({ ...ref, text }))) setNote('')
  }
  return (
    <section
      aria-label="教學中"
      data-testid="teach-bar"
      className={cx('flex flex-col gap-2.5 rounded-group border bg-panel p-3.5', paused ? 'border-line' : 'border-danger/70', className)}
    >
      <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
        <span className="flex min-w-0 flex-1 items-center gap-2">
          <span className={cx('size-2 flex-none rounded-full', paused ? 'bg-check-off' : 'animate-pulse bg-danger')} />
          <strong className="flex-none whitespace-nowrap font-normal text-ui">
            {paused ? '已暫停' : '教學中'} · 第 {steps.length} 步
          </strong>
          {lesson.goal && <span className="min-w-0 truncate text-sub text-ink-2" title={lesson.goal}>目的：{lesson.goal}</span>}
        </span>
        <Button size="sm" icon={paused ? Play : Pause} loading={busy === 'pause'} onClick={() => void act('pause', () => teachingPause({ ...ref, paused: !paused }))}>
          {paused ? '繼續' : '暫停'}
        </Button>
        <Button size="sm" variant="primary" icon={Check} loading={busy === 'stop'} onClick={() => void act('stop', () => teachingStop(ref))}>
          {busy === 'stop' ? '整理技能中…' : '完成，請它學起來'}
        </Button>
        <IconButton icon={X} label="取消這次示範" size="sm" onClick={() => setConfirm(true)} />
      </div>
      {steps.length === 0 ? (
        <p className="m-0 rounded-[10px] bg-inset px-3 py-3 text-sub text-ink-2">在下面的畫面上照平常的方式操作，每一步都會出現在這裡。</p>
      ) : (
        <ol ref={list} className="m-0 flex max-h-[300px] list-none flex-col gap-0.5 overflow-y-auto overscroll-y-contain p-0" aria-label="示範的步驟">
          {steps.map((s, i) => (
            <StepRow
              key={s.id}
              step={s}
              n={i + 1}
              botId={lesson.bot_id}
              lessonId={lesson.id}
              last={i === steps.length - 1}
              recording={!paused}
              onDrop={() => void act(null, () => teachingDrop({ ...ref, step_id: s.id }))}
            />
          ))}
        </ol>
      )}
      <form
        className="flex gap-2"
        onSubmit={(e) => {
          e.preventDefault()
          void addNote()
        }}
      >
        <input
          aria-label="補充說明"
          value={note}
          maxLength={500}
          onChange={(e) => setNote(e.target.value)}
          placeholder="補充說明，例如：每次都先清空購物車"
          className={cx(fieldClass, 'h-10 flex-1 rounded-full px-4')}
        />
        <Button type="submit" loading={busy === 'note'} disabled={!note.trim()} className="rounded-full">
          加入
        </Button>
      </form>
      <p className="m-0 text-meta text-ink-3">密碼欄位只會記成 ••••。示範時{botName}暫停，不會跟你搶滑鼠。</p>
      <ConfirmDialog
        open={confirm}
        title="取消這次示範？"
        description="記下的步驟和截圖會刪掉，電腦交還給它。"
        confirmText="取消示範"
        cancelText="繼續示範"
        danger
        busy={busy === 'discard'}
        onCancel={() => setConfirm(false)}
        onConfirm={() =>
          void act('discard', () => teachingDiscard(ref)).then((ok) => {
            if (ok) {
              setLesson(lesson.bot_id, null)
              setConfirm(false)
            }
          })
        }
      />
    </section>
  )
}

/** 錄完（E04「整理技能中」之後）：技能草稿，看過、改過再存成技能；預設存好後請它試做一次（E05）。 */
export function DraftReview({ lesson, botName, className }: { lesson: Lesson; botName: string; className?: string }) {
  const toast = useToast()
  const draft = lesson.draft!
  const [name, setName] = useState(draft.name)
  const [desc, setDesc] = useState(draft.description)
  const [text, setText] = useState(draft.instructions)
  const [goal, setGoal] = useState(lesson.goal)
  const [trial, setTrial] = useState(true)
  const [busy, setBusy] = useState<'save' | 'discard' | 'redo' | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [confirm, setConfirm] = useState(false)
  // 草稿重新整理後（補上目的），欄位跟著換。
  const [seen, setSeen] = useState(draft)
  if (seen !== draft) {
    setSeen(draft)
    setName(draft.name)
    setDesc(draft.description)
    setText(draft.instructions)
  }
  const ref = { bot_id: lesson.bot_id, lesson_id: lesson.id }
  const save = async () => {
    setBusy('save')
    setError(null)
    try {
      const out = await teachingSave({ ...ref, name: name.trim(), description: desc.trim(), instructions: text, skip_trial: !trial })
      setLesson(lesson.bot_id, null)
      toast.show({
        kind: 'done',
        text: out.task_id ? `${botName}學會了「${out.skill.name}」，正在試做一次` : `${botName}學會了「${out.skill.name}」`,
        action: out.thread_id ? { label: '看試做', onClick: () => navigate(`/bots/${lesson.bot_id}/threads/${out.thread_id}`) } : undefined,
      })
      if (out.note) toast.show({ kind: 'error', text: out.note })
      if (out.thread_id) navigate(`/bots/${lesson.bot_id}/threads/${out.thread_id}`)
    } catch (err) {
      setError(errText(err))
    } finally {
      setBusy(null)
    }
  }
  const redo = async () => {
    setBusy('redo')
    try {
      setLesson(lesson.bot_id, (await teachingDraft({ ...ref, goal: goal.trim(), redo: true })) as Lesson)
    } catch (err) {
      setError(errText(err))
    } finally {
      setBusy(null)
    }
  }
  const shots = draft.shots.filter((s) => text.includes(s))
  return (
    <section aria-label="技能草稿" data-testid="teach-draft" className={cx('flex flex-col gap-3 rounded-group border border-line bg-panel p-4', className)}>
      <div className="flex items-center gap-2">
        <Icon icon={Sparkles} size={18} className="text-accent-ink" />
        <h3 className="m-0 min-w-0 flex-1 text-ui">整理好了：確認後存成{botName}的技能</h3>
        <StatusChip size="sm">{lesson.steps.length} 步</StatusChip>
        {draft.by === 'model' && <StatusChip size="sm" tone="accent">系統整理</StatusChip>}
      </div>
      <div className="flex items-end gap-2">
        <TextInput label="要教什麼（選填）" value={goal} maxLength={200} onChange={(e) => setGoal(e.target.value)} placeholder="例：重現結帳運費歸零" wrapperClassName="flex-1" />
        <Button icon={RotateCw} loading={busy === 'redo'} disabled={goal.trim() === lesson.goal.trim()} onClick={() => void redo()}>
          重新整理
        </Button>
      </div>
      <TextInput label="技能名稱" value={name} maxLength={64} onChange={(e) => setName(e.target.value)} />
      <TextInput label="什麼時候使用" value={desc} maxLength={1024} onChange={(e) => setDesc(e.target.value)} />
      <Textarea label="怎麼做（可以直接改）" value={text} rows={12} onChange={(e) => setText(e.target.value)} className="text-ui-sm" />
      {shots.length > 0 && (
        <div className="flex flex-col gap-1.5">
          <span className="text-meta text-ink-3">會一起存的截圖（{shots.length} 張；從內文刪掉的就不存）</span>
          <div className="flex gap-2 overflow-x-auto pb-1">
            {shots.map((s) => (
              <ShotThumb key={s} botId={lesson.bot_id} lessonId={lesson.id} name={s} className="h-[54px] w-[86px]" />
            ))}
          </div>
        </div>
      )}
      <Checkbox checked={trial} onChange={setTrial}>
        存好後請{botName}試做一次
      </Checkbox>
      {error && (
        <p className="m-0 text-sub text-danger" role="alert">
          {error}
        </p>
      )}
      <div className="flex flex-wrap justify-end gap-2">
        <Button variant="danger" icon={Trash2} onClick={() => setConfirm(true)} disabled={busy !== null}>
          捨棄
        </Button>
        <Button variant="primary" icon={Save} loading={busy === 'save'} disabled={!name.trim() || !text.trim()} onClick={() => void save()}>
          存成技能
        </Button>
      </div>
      <ConfirmDialog
        open={confirm}
        title="捨棄這份草稿？"
        description="示範的步驟和截圖會刪掉，不能復原。"
        confirmText="捨棄"
        danger
        busy={busy === 'discard'}
        onCancel={() => setConfirm(false)}
        onConfirm={() => {
          setBusy('discard')
          teachingDiscard(ref)
            .then(() => {
              setLesson(lesson.bot_id, null)
              setConfirm(false)
            })
            .catch((err: unknown) => setError(errText(err)))
            .finally(() => setBusy(null))
        }}
      />
    </section>
  )
}

/** 電腦面板與電腦頁共用：錄製中顯示教學列，錄完顯示草稿。 */
export function TeachSection({ lesson, botName, className }: { lesson: Lesson | null | undefined; botName: string; className?: string }) {
  if (!lesson) return null
  if (isRecording(lesson)) return <TeachBar lesson={lesson} botName={botName} className={className} />
  if (isDraft(lesson)) return <DraftReview key={lesson.id} lesson={lesson} botName={botName} className={className} />
  return null
}
