import { useEffect, useRef, useState, type DragEvent } from 'react'
import { CircleCheck, Download, Info, Lock, ShieldCheck, TriangleAlert, Upload } from 'lucide-react'
import { botsExport, botsImport, type BotsExportOutput, type BotsImportOutput } from '../../api/generated'
import { look, upsertBot, type Bot } from '../../data/bots'
import { createStore, useStore } from '../../data/store'
import { navigate } from '../../router'
import { Avatar } from '../../ui/Avatar'
import { Button } from '../../ui/Button'
import { Checkbox } from '../../ui/Controls'
import { Dialog } from '../../ui/Dialog'
import { TextInput } from '../../ui/Field'
import { Icon } from '../../ui/Icon'
import { StatusChip } from '../../ui/Pill'
import { cx } from '../../ui/cx'
import { useToast } from '../../ui/toast-context'
import { errText } from '../format'

// 分享 Bot（G13）與匯入 Bot（G14）：.xbot 檔（xbot.bots.export／xbot.bots.import，BT-8）。
// 一律不帶登入資料、密碼、API 金鑰、對話紀錄、電腦裡的檔案與記憶；匯入的 Bot 一律從「先問我」開始（SEC-1）。

/** .xbot 檔大小上限（同 internal/limits BotFileBytes）。 */
const FILE_MAX = 800_000
const APPROVAL_LABEL: Record<string, string> = { ask: '先問我', edits: '一般的事自己做', auto: '放手做', full: '完全放手' }

function download(name: string, text: string) {
  const url = URL.createObjectURL(new Blob([text], { type: 'application/json;charset=utf-8' }))
  const a = document.createElement('a')
  a.href = url
  a.download = name
  a.click()
  setTimeout(() => URL.revokeObjectURL(url), 1000)
}

const kb = (n: number) => (n >= 1_000_000 ? `${(n / 1_000_000).toFixed(1)} MB` : `${Math.max(1, Math.round(n / 1000))} KB`)

// ── 分享（G13）────────────────────────────────────────

export function ShareBotDialog({ bot, open, onClose }: { bot: Bot; open: boolean; onClose: () => void }) {
  const toast = useToast()
  const [soul, setSoul] = useState(true)
  const [skills, setSkills] = useState(true)
  const [apps, setApps] = useState(true)
  const [first, setFirst] = useState<BotsExportOutput | null>(null)
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    botsExport({ id: bot.id })
      .then(setFirst)
      .catch(() => setFirst(null))
  }, [bot.id])

  const save = async () => {
    setBusy(true)
    try {
      const out = soul && skills && apps && first ? first : await botsExport({ id: bot.id, no_soul: !soul || undefined, no_skills: !skills || undefined, no_apps: !apps || undefined })
      download(out.filename, out.content)
      toast.show({ kind: 'done', text: `已下載 ${out.filename}` })
      onClose()
    } catch (e) {
      toast.show({ kind: 'error', text: errText(e) })
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog
      open={open}
      onClose={onClose}
      busy={busy}
      width={600}
      title={`分享${bot.name}`}
      description={`對方會拿到一份自己的${bot.name}，不會影響你的。`}
      actions={
        <>
          <Button onClick={onClose} disabled={busy}>
            取消
          </Button>
          <Button variant="primary" icon={Download} loading={busy} onClick={() => void save()}>
            下載 .xbot 檔
          </Button>
        </>
      }
    >
      <div className="mt-1 flex items-center gap-3 rounded-group bg-card px-4 py-3">
        <Avatar {...look(bot)} dot="none" size={44} blink={false} />
        <span className="min-w-0 flex-1">
          <span className="block text-ui">
            {bot.name} <span className="text-[13px] text-ink-2">{bot.title}</span>
          </span>
          <span className="mt-1 flex flex-wrap gap-1.5">
            <StatusChip size="sm">{first ? `${first.skills} 個技能` : '技能…'}</StatusChip>
            <StatusChip size="sm">{first ? `${first.apps} 個應用程式` : '應用程式…'}</StatusChip>
            <StatusChip size="sm">權限：{APPROVAL_LABEL[bot.approval_mode] ?? bot.approval_mode}</StatusChip>
          </span>
        </span>
      </div>
      <h3 className="m-0 mt-2 text-[15px]">要包含什麼</h3>
      <div className="flex flex-col overflow-hidden rounded-group bg-card" role="group" aria-label="要包含什麼">
        <Row>
          <Checkbox checked={soul} onChange={setSoul}>
            人設與回覆風格
            <span className="block text-[13px] text-ink-2">它做什麼、說話方式（名字、職稱、頭像一定會帶）</span>
          </Checkbox>
        </Row>
        <Row>
          <Checkbox checked={skills} onChange={setSkills}>
            技能{first ? `（${first.skills} 個）` : ''}
            <span className="block text-[13px] text-ink-2">只帶步驟文字；對方匯入後先停用，看過再打開</span>
          </Checkbox>
        </Row>
        <Row>
          <Checkbox checked={false} onChange={() => {}} disabled>
            記憶
            <span className="block text-[13px] text-warning-ink">可能有你的私人資料，不帶</span>
          </Checkbox>
        </Row>
        <Row>
          <Checkbox checked={apps} onChange={setApps}>
            要連的應用程式{first ? `（${first.apps} 個）` : ''}
            <span className="block text-[13px] text-ink-2">只帶名稱；對方要自己登入</span>
          </Checkbox>
        </Row>
      </div>
      {first?.note && (
        <p className="m-0 flex items-start gap-2 text-meta text-ink-2">
          <Icon icon={Info} size={14} className="mt-0.5 flex-none" />
          {first.note}
        </p>
      )}
      <p className="m-0 flex items-start gap-2 text-meta text-ink-2">
        <Icon icon={Lock} size={14} className="mt-0.5 flex-none" />
        登入資料、密碼、API 金鑰、對話紀錄和電腦裡的檔案一律不會分享。對方匯入後權限一律從「先問我」開始。
      </p>
      <p className="m-0 text-meta text-ink-3">分享連結與組織的 Bot 庫之後的版本開放；現在先用檔案。</p>
    </Dialog>
  )
}

function Row({ children }: { children: React.ReactNode }) {
  return <div className="border-line px-4 py-3 not-first:border-t">{children}</div>
}

// ── 匯入（G14）────────────────────────────────────────

export const importBotDialog = createStore(false)
export const openImportBot = () => importBotDialog.set(true)

const STATUS_ICON = { ok: { icon: CircleCheck, cls: 'text-success' }, action: { icon: TriangleAlert, cls: 'text-warning-ink' }, info: { icon: ShieldCheck, cls: 'text-success' } } as const

export function ImportBotDialog() {
  const open = useStore(importBotDialog)
  const close = () => importBotDialog.set(false)
  return open ? <ImportBotForm onClose={close} /> : null
}

function ImportBotForm({ onClose }: { onClose: () => void }) {
  const toast = useToast()
  const input = useRef<HTMLInputElement>(null)
  const [file, setFile] = useState<{ name: string; text: string } | null>(null)
  const [preview, setPreview] = useState<BotsImportOutput | null>(null)
  const [name, setName] = useState('')
  const [err, setErr] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [over, setOver] = useState(false)

  const read = async (f: File | undefined) => {
    if (!f) return
    setErr(null)
    if (f.size > FILE_MAX) {
      setErr('檔案太大，不像是 .xbot 檔')
      return
    }
    setFile({ name: f.name, text: await f.text() })
  }

  const next = async () => {
    if (!file) return
    setBusy(true)
    setErr(null)
    try {
      const p = await botsImport({ content: file.text, filename: file.name, preview: true })
      setPreview(p)
      setName(p.bot.name)
    } catch (e) {
      setErr(errText(e))
    } finally {
      setBusy(false)
    }
  }

  const run = async () => {
    if (!file) return
    setBusy(true)
    try {
      const out = await botsImport({ content: file.text, filename: file.name, name: name.trim() || undefined })
      upsertBot(out.bot as Bot)
      const extra = out.skills_added.length ? `；${out.skills_added.length} 個技能放進技能庫，看過再打開` : ''
      toast.show({ kind: 'done', text: `已匯入${out.bot.name}${extra}` })
      onClose()
      navigate(`/bots/${out.bot.id}`)
    } catch (e) {
      setErr(errText(e))
      setBusy(false)
    }
  }

  const drop = (e: DragEvent) => {
    e.preventDefault()
    setOver(false)
    void read(e.dataTransfer.files[0])
  }

  if (!preview) {
    return (
      <Dialog
        open
        onClose={onClose}
        busy={busy}
        width={600}
        title="匯入 Bot"
        actions={
          <>
            <Button onClick={onClose} disabled={busy}>
              取消
            </Button>
            <Button variant="primary" disabled={!file} loading={busy} onClick={() => void next()}>
              下一步
            </Button>
          </>
        }
      >
        <div
          onDragOver={(e) => {
            e.preventDefault()
            setOver(true)
          }}
          onDragLeave={() => setOver(false)}
          onDrop={drop}
          className={cx('mt-1 flex flex-col items-center gap-1.5 rounded-group border border-dashed px-4 py-6 text-center', over ? 'border-accent bg-accent-soft/30' : 'border-line')}
        >
          <Icon icon={Upload} size={22} />
          <span className="text-ui">{file ? file.name : '把 .xbot 檔拖到這裡'}</span>
          <span className="text-meta text-ink-2">
            {file ? `${kb(file.text.length)} · ` : '或 '}
            <button type="button" onClick={() => input.current?.click()} className="bg-transparent p-0 text-meta text-accent-ink hover:underline">
              {file ? '換一個檔案' : '選擇檔案'}
            </button>
          </span>
          <input
            ref={input}
            type="file"
            accept=".xbot,.json,application/json"
            aria-label="選擇 .xbot 檔"
            className="sr-only-text"
            onChange={(e) => void read(e.target.files?.[0])}
          />
        </div>
        {err && (
          <p role="alert" className="m-0 text-meta text-danger">
            {err}
          </p>
        )}
        <p className="m-0 text-meta text-ink-3">匯入的 Bot 一律從「先問我」開始；技能先放進技能庫並停用，你看過再打開。</p>
      </Dialog>
    )
  }

  const b = preview.bot
  const date = preview.exported_at ? new Date(preview.exported_at).toLocaleDateString('zh-TW', { month: 'long', day: 'numeric' }) : ''
  return (
    <Dialog
      open
      onClose={onClose}
      busy={busy}
      width={600}
      title="匯入 Bot"
      actions={
        <>
          <span className="mr-auto self-center text-meta text-ink-2">{preview.can_create ? `還可以建立 ${preview.remaining} 個 Bot` : preview.reason}</span>
          <Button onClick={() => setPreview(null)} disabled={busy}>
            返回
          </Button>
          <Button variant="primary" loading={busy} disabled={!preview.can_create} onClick={() => void run()}>
            匯入
          </Button>
        </>
      }
    >
      <div className="mt-1 flex items-center gap-3">
        <Avatar {...look(b)} dot="none" size={40} blink={false} />
        <span className="min-w-0 flex-1">
          <span className="block text-ui">
            {preview.original} <span className="text-[13px] text-ink-2">{b.title}</span>
          </span>
          <span className="block text-[13px] text-ink-2">
            {date && `${date}分享 · `}
            {kb(preview.bytes)}
          </span>
        </span>
      </div>
      <ul aria-label="會帶入的內容" className="m-0 flex list-none flex-col overflow-hidden rounded-group bg-card p-0">
        {preview.items.map((it) => {
          const s = STATUS_ICON[it.status as keyof typeof STATUS_ICON] ?? STATUS_ICON.info
          return (
            <li key={it.kind + it.title} data-status={it.status} className="flex items-start gap-3 border-line px-4 py-3 not-first:border-t">
              <span className={cx('pt-0.5', s.cls)}>
                <Icon icon={s.icon} size={18} />
              </span>
              <span className="min-w-0 flex-1 text-ui">
                {it.title}
                {it.detail && <span className={cx('ml-2 text-[13px]', it.status === 'action' ? 'text-warning-ink' : 'text-ink-2')}>{it.detail}</span>}
              </span>
            </li>
          )
        })}
      </ul>
      <TextInput label="名字" value={name} maxLength={100} onChange={(e) => setName(e.target.value)} hint={preview.renamed ? `你已經有一個「${preview.original}」，先改叫「${b.name}」，可以改。` : undefined} />
      {err && (
        <p role="alert" className="m-0 text-meta text-danger">
          {err}
        </p>
      )}
    </Dialog>
  )
}
