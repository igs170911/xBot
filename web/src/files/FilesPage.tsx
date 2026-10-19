import { useEffect, useRef, useState } from 'react'
import { ChevronRight, FolderOpen, LayoutGrid, List, Power, Search, Upload } from 'lucide-react'
import { filesDownloadUrl, filesList, filesUploadForm, type FilesListOutput } from '../api/generated'
import { botsStore, type Bot, look } from '../data/bots'
import { useStore } from '../data/store'
import { linkClick, navigate } from '../router'
import { errText } from '../settings/format'
import { Avatar } from '../ui/Avatar'
import { Button, IconButton } from '../ui/Button'
import { TextInput } from '../ui/Field'
import { Icon } from '../ui/Icon'
import { MenuButton } from '../ui/Menu'
import { Select } from '../ui/Menu'
import { cx } from '../ui/cx'
import { useToast } from '../ui/toast-context'
import { filesHref, GROUPS, groupLabel, kindMeta, matches, sizeLabel, whenLabel, type FileEntry, type FileGroup } from './data'
import { FileIcon } from './FilePreview'

// 全部檔案（M01）：每個 Bot 做出的成果、你上傳的、示範教學、從隊友來的，依 Bot 分組；縮圖看得出內容。
// 點一個檔案到那個 Bot 的檔案頁預覽。

type Filter = 'all' | FileGroup
type Result = { out?: FilesListOutput; error?: string }

/** 縮圖：圖片用真的縮圖；其他依種類畫出試算表格線、PDF 文字行、Markdown 文字、程式碼…（M01）。 */
function Thumb({ e, botId }: { e: FileEntry; botId: string }) {
  const [broken, setBroken] = useState(false)
  const box = 'relative flex h-[92px] items-center justify-center overflow-hidden rounded-[10px] bg-inset'
  if (e.kind === 'image' && !broken && /\.(png|jpe?g|gif)$/i.test(e.name)) {
    return (
      <div className={box}>
        <img src={filesDownloadUrl(botId, e.path, { thumb: true })} alt="" loading="lazy" onError={() => setBroken(true)} className="h-full w-full object-cover" />
      </div>
    )
  }
  if (e.kind === 'csv' || e.kind === 'sheet') {
    return (
      <div className={cx(box, 'bg-[#f4f6f4] p-1.5')} aria-hidden="true">
        <div className="grid h-full w-full grid-cols-4 grid-rows-5 gap-px bg-[#d5dcd6]">
          {Array.from({ length: 20 }, (_, i) => (
            <span key={i} className={cx('flex items-center px-1', i < 4 ? 'bg-[#dcefe2]' : 'bg-white')}>
              <span className="h-[3px] w-full rounded bg-[#9aa49c]" />
            </span>
          ))}
        </div>
      </div>
    )
  }
  if (e.kind === 'pdf' || e.kind === 'office') {
    return (
      <div className={cx(box, 'items-end pt-2')} aria-hidden="true">
        <div className="flex h-[84px] w-[64%] flex-col gap-1.5 bg-white px-2.5 pt-2.5">
          <span className="h-1 w-3/5 rounded bg-[#2a2a2a]" />
          {[90, 100, 80, 95, 70, 85].map((w, i) => (
            <span key={i} className="h-[3px] rounded bg-[#c9c9c9]" style={{ width: `${w}%` }} />
          ))}
        </div>
      </div>
    )
  }
  if (e.kind === 'markdown' || e.kind === 'text' || e.kind === 'code') {
    return (
      <div className={cx(box, 'flex-col items-start justify-start gap-2 px-3 pt-3')} aria-hidden="true">
        <span className="h-1 w-2/5 rounded" style={{ background: kindMeta(e.kind).color }} />
        {[92, 78, 88, 60].map((w, i) => (
          <span key={i} className="h-[3px] rounded bg-hover" style={{ width: `${w}%` }} />
        ))}
      </div>
    )
  }
  return (
    <div className={box}>
      <FileIcon kind={e.kind} size={44} />
    </div>
  )
}

function Card({ e, bot }: { e: FileEntry; bot: Bot }) {
  const to = filesHref(bot.id, e.path)
  const meta = [e.group && e.group !== 'result' ? groupLabel(e.group) : kindMeta(e.kind).label, sizeLabel(e.size), whenLabel(e.modified)].filter(Boolean).join(' · ')
  return (
    <a href={to} onClick={linkClick(to)} className="flex min-w-0 flex-col gap-1.5 rounded-group bg-panel p-1.5 pb-2 text-ink hover:bg-card" title={e.path} data-testid="file-card">
      <Thumb e={e} botId={bot.id} />
      <span className="truncate px-1 text-ui-sm">{e.name}</span>
      <span className="-mt-1 truncate px-1 text-meta text-ink-3">{meta}</span>
    </a>
  )
}

function ListRow({ e, bot }: { e: FileEntry; bot: Bot }) {
  const to = filesHref(bot.id, e.path)
  return (
    <li>
      <a href={to} onClick={linkClick(to)} className="flex min-h-[52px] items-center gap-3 rounded-menu px-2 py-1.5 text-ink hover:bg-control/60" data-testid="file-card">
        <FileIcon kind={e.kind} />
        <span className="min-w-0 flex-1 truncate text-ui">{e.name}</span>
        <span className="flex-none text-meta text-ink-3 max-sm:hidden">{groupLabel(e.group)}</span>
        <span className="w-16 flex-none text-right text-meta text-ink-3">{sizeLabel(e.size)}</span>
        <span className="w-32 flex-none text-right text-meta text-ink-3 max-sm:hidden">{whenLabel(e.modified)}</span>
      </a>
    </li>
  )
}

export function FilesPage() {
  const bots = (useStore(botsStore).bots ?? []).filter((b) => !b.archived)
  const [results, setResults] = useState<Record<string, Result>>({})
  const [filter, setFilter] = useState<Filter>('all')
  const [who, setWho] = useState('all')
  const [q, setQ] = useState('')
  const [grid, setGrid] = useState(true)
  // 各個 Bot 的檔案各自讀：一個一個冒出來、讀完沒檔案的又收掉，整頁一直跳（2026-10-20 手機）。
  // 全部讀完（最多等 3 秒）再一次畫出來。
  const [settled, setSettled] = useState(false)
  useEffect(() => {
    const t = window.setTimeout(() => setSettled(true), 3000)
    return () => window.clearTimeout(t)
  }, [])
  const [uploadTo, setUploadTo] = useState('')
  const input = useRef<HTMLInputElement>(null)
  const toast = useToast()
  const ids = bots.map((b) => b.id).join(',')

  const load = (botId: string, wake = false) => {
    filesList({ bot_id: botId, all: true, ...(wake ? { wake: true } : {}) })
      .then((out) => setResults((r) => ({ ...r, [botId]: { out } })))
      .catch((e) => setResults((r) => ({ ...r, [botId]: { error: errText(e) } })))
  }
  useEffect(() => {
    for (const id of ids.split(',').filter(Boolean)) load(id)
  }, [ids])

  const all = bots.flatMap((b) => (results[b.id]?.out?.entries ?? []).filter((e) => e.important && matches(e, q)).map((e) => ({ e, b })))
  const count = (f: Filter) => all.filter((x) => (who === 'all' || x.b.id === who) && (f === 'all' || (x.e.group ?? 'result') === f)).length
  const shownBots = bots.filter((b) => who === 'all' || b.id === who)

  const upload = async (files: File[]) => {
    if (!uploadTo || !files.length) return
    try {
      const out = await filesUploadForm(uploadTo, files)
      toast.show({ kind: 'done', text: `已上傳 ${out.length} 個檔案給${bots.find((b) => b.id === uploadTo)?.name ?? 'Bot'}` })
      load(uploadTo)
    } catch (e) {
      toast.show({ kind: 'error', text: errText(e) })
    }
  }

  return (
    <main className="min-w-0 flex-1 px-6 pb-8 pt-5 max-sm:px-4">
      <div className="mx-auto flex max-w-[1180px] flex-col gap-4">
        <header className="flex flex-wrap items-start justify-between gap-3">
          <div className="flex flex-col gap-0.5">
            <h1 className="max-md:sr-only-text m-0 flex items-center gap-2.5 text-[22px] font-normal">
              <span className="text-accent-ink">
                <Icon icon={FolderOpen} size={24} />
              </span>
              全部檔案
            </h1>
            <p className="m-0 text-sub text-ink-2">Bot 做出來的成果、你上傳的附件、示範教學，照 Bot 分好。</p>
          </div>
          <input
            ref={input}
            type="file"
            multiple
            hidden
            aria-label="選擇要上傳的檔案"
            onChange={(e) => void upload([...(e.target.files ?? [])]).finally(() => (e.target.value = ''))}
          />
          <MenuButton
            label="上傳給哪個 Bot"
            align="end"
            items={bots.map((b) => ({
              label: `上傳給${b.name}`,
              onSelect: () => {
                setUploadTo(b.id)
                setTimeout(() => input.current?.click(), 0)
              },
            }))}
            trigger={(p) => (
              <Button {...p} variant="primary" icon={Upload} disabled={!bots.length}>
                上傳檔案
              </Button>
            )}
          />
        </header>

        <div className="flex flex-wrap items-center gap-2">
          <TextInput label="搜尋檔名" hideLabel icon={Search} placeholder="搜尋檔名" value={q} onChange={(e) => setQ(e.target.value)} wrapperClassName="w-[260px] max-sm:w-full" />
          {/* 手機上排成一列、左右滑：數字讀進來時寬度變了也不會換行讓下面整片往下跳 */}
          <div role="radiogroup" aria-label="種類" className="flex flex-wrap rounded-field bg-inset p-[3px] max-sm:w-full max-sm:flex-nowrap max-sm:overflow-x-auto max-sm:[scrollbar-width:none]">
            {([['all', '全部'] as const, ...GROUPS.map((g) => [g.key, g.label] as const)]).map(([k, label]) => (
              <button
                key={k}
                type="button"
                role="radio"
                aria-checked={filter === k}
                onClick={() => setFilter(k)}
                className={cx('h-8 flex-none whitespace-nowrap rounded-seg px-3 text-ui-sm max-sm:h-10', filter === k ? 'bg-control text-ink' : 'text-ink-2 hover:text-ink')}
              >
                {label} <span className="text-ink-3">{count(k)}</span>
              </button>
            ))}
          </div>
          <Select
            label="Bot"
            hideLabel
            look="compact"
            value={who}
            onChange={setWho}
            options={[{ value: 'all', label: '所有 Bot' }, ...bots.map((b) => ({ value: b.id, label: b.name }))]}
          />
          <span className="flex-1" />
          <div className="flex rounded-field bg-inset p-[3px]">
            <IconButton icon={LayoutGrid} label="格狀" aria-pressed={grid} onClick={() => setGrid(true)} className={cx(grid && 'bg-control')} />
            <IconButton icon={List} label="清單" aria-pressed={!grid} onClick={() => setGrid(false)} className={cx(!grid && 'bg-control')} />
          </div>
        </div>

        {bots.length === 0 && <p className="m-0 py-10 text-center text-ui text-ink-2">還沒有 Bot。建立一個 Bot，它做出的檔案會出現在這裡。</p>}

        {!settled && shownBots.some((b) => !results[b.id]) && <p className="m-0 py-6 text-center text-sub text-ink-3">讀取中…</p>}
        {(settled || shownBots.every((b) => results[b.id])) && shownBots.map((b) => {
          const r = results[b.id]
          const files = (r?.out?.entries ?? []).filter((e) => e.important && matches(e, q) && (filter === 'all' || (e.group ?? 'result') === filter))
          const state = r?.out?.state
          if (who === 'all' && state === 'none') return null
          if (who === 'all' && state === 'ready' && files.length === 0) return null
          const to = filesHref(b.id)
          return (
            <section key={b.id} aria-label={`${b.name}的檔案`} className="flex flex-col gap-2.5">
              <h2 className="m-0 flex items-center gap-2 text-[15.5px] font-normal">
                <Avatar {...look(b)} dot="none" size={24} ring="#070707" />
                <span>{b.name}</span>
                {state === 'ready' && <span className="text-sub text-ink-3">{files.length} 個</span>}
                <span className="flex-1" />
                <a href={to} onClick={linkClick(to)} className="flex items-center gap-0.5 text-sub text-accent-ink hover:underline">
                  打開檔案
                  <Icon icon={ChevronRight} size={14} />
                </a>
              </h2>
              {!r && <p className="m-0 text-sub text-ink-3">讀取中…</p>}
              {r?.error && <p className="m-0 text-sub text-danger">{r.error}</p>}
              {state === 'none' && <p className="m-0 text-sub text-ink-2">{b.name}還沒有電腦。</p>}
              {state === 'off' && (
                <div className="flex flex-wrap items-center gap-3 rounded-group bg-panel px-4 py-3">
                  <span className="flex-1 text-sub text-ink-2">{b.name}的電腦沒有開機，開機後才看得到檔案。</span>
                  <Button variant="secondary" size="sm" icon={Power} onClick={() => load(b.id, true)}>
                    開機看檔案
                  </Button>
                </div>
              )}
              {state === 'ready' && files.length === 0 && <p className="m-0 text-sub text-ink-3">沒有符合的檔案。</p>}
              {state === 'ready' &&
                files.length > 0 &&
                (grid ? (
                  <div className="grid grid-cols-[repeat(auto-fill,minmax(172px,1fr))] gap-3 max-sm:grid-cols-2">
                    {files.slice(0, 48).map((e) => (
                      <Card key={e.path} e={e} bot={b} />
                    ))}
                  </div>
                ) : (
                  <ul className="m-0 flex list-none flex-col gap-0.5 p-0">
                    {files.slice(0, 200).map((e) => (
                      <ListRow key={e.path} e={e} bot={b} />
                    ))}
                  </ul>
                ))}
              {state === 'ready' && files.length > 48 && grid && (
                <button type="button" onClick={() => navigate(to)} className="self-start text-sub text-accent-ink hover:underline">
                  看全部 {files.length} 個
                </button>
              )}
            </section>
          )
        })}
        {(settled || shownBots.every((b) => results[b.id])) && <p className="m-0 text-meta text-ink-3">成果不會自動刪除。這裡只列出成果；建置檔和快取到各 Bot 的檔案頁用資料夾瀏覽。</p>}
      </div>
    </main>
  )
}
