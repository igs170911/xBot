import { useCallback, useEffect, useRef, useState, type DragEvent, type ReactNode } from 'react'
import { ArrowLeft, AtSign, ChevronRight, Download, FolderPlus, Power, RefreshCw, Search, Send, Upload } from 'lucide-react'
import { filesList, filesUploadForm, type FilesListOutput } from '../api/generated'
import { botsStore, look } from '../data/bots'
import { useStore } from '../data/store'
import { navigate, useSearch } from '../router'
import { errText } from '../settings/format'
import { Avatar } from '../ui/Avatar'
import { Button, IconButton } from '../ui/Button'
import { TextInput } from '../ui/Field'
import { Icon } from '../ui/Icon'
import { cx } from '../ui/cx'
import { useLockBody } from '../ui/useLockBody'
import { useToast } from '../ui/toast-context'
import { FileDialogs } from './FileDialogs'
import { fileMenu, type FileDialog } from './menu'
import { crumbs, GROUPS, matches, sizeLabel, whenLabel, type FileEntry } from './data'
import { FileIcon, FilePreview } from './FilePreview'
import { mentionInChat } from './mention'
import { NewFolderDialog } from './NewFolderDialog'
import { useNarrow } from './useNarrow'
import { BackButton } from '../mobile/Phone'

// 一個 Bot 的檔案（E07 電腦面板「檔案」、手機 L19）：/bots/<id>/files?path=<檔案>&view=folder&folder=<資料夾>。
// 預設依「成果／你上傳的／示範教學／從隊友來的」分組；也可以照資料夾瀏覽。點檔案在右邊預覽（手機從底部拉出）。


type Listing = { key: string; out?: FilesListOutput; error?: string }

/** 列出檔案（all：全部檔案；否則一個資料夾）。 */
function useListing(botId: string, all: boolean, folder: string) {
  const [state, setState] = useState<Listing | null>(null)
  const [n, setN] = useState(0)
  const [wake, setWake] = useState(false)
  const key = `${botId}|${all}|${folder}|${n}`
  useEffect(() => {
    let live = true
    filesList({ bot_id: botId, ...(all ? { all: true } : { path: folder }), limit: 500, ...(wake ? { wake: true } : {}) })
      .then((out) => live && setState({ key, out }))
      .catch((e) => live && setState({ key, error: errText(e) }))
    return () => {
      live = false
    }
  }, [botId, all, folder, wake, key])
  const reload = useCallback(() => setN((x) => x + 1), [])
  return { state: state?.key === key ? state : null, reload, wake: () => setWake(true), waking: wake }
}

function Row({ e, on, onOpen, sub }: { e: FileEntry; on: boolean; onOpen: () => void; sub: ReactNode }) {
  return (
    <li>
      <button
        type="button"
        onClick={onOpen}
        aria-current={on ? 'true' : undefined}
        className={cx('flex min-h-[52px] w-full items-center gap-3 rounded-menu px-2 py-1.5 text-left text-ink', on ? 'bg-control' : 'hover:bg-control/60')}
      >
        <FileIcon kind={e.kind} />
        <span className="flex min-w-0 flex-1 flex-col">
          <span className="truncate text-ui" title={e.name}>
            {e.name}
          </span>
          <span className="truncate text-meta text-ink-3">{sub}</span>
        </span>
        {e.dir && (
          <span className="text-ink-3">
            <Icon icon={ChevronRight} size={16} />
          </span>
        )}
      </button>
    </li>
  )
}

function Section({ title, count, children }: { title: string; count: number; children: ReactNode }) {
  return (
    <section aria-label={title} className="flex flex-col gap-0.5">
      <h2 className="m-0 flex items-center justify-between px-2 pb-1 pt-3 text-[13px] font-normal text-ink-3">
        <span>{title}</span>
        <span>{count}</span>
      </h2>
      <ul className="m-0 flex list-none flex-col gap-0.5 p-0">{children}</ul>
    </section>
  )
}

/** 電腦沒開機、還沒有電腦、讀不到。 */
function ListState({ out, error, waking, onWake, botName }: { out?: FilesListOutput; error?: string; waking: boolean; onWake: () => void; botName: string }) {
  if (error)
    return (
      <p role="alert" className="m-0 px-2 py-6 text-ui-sm text-danger">
        {error}
      </p>
    )
  if (out?.state === 'none')
    return <p className="m-0 px-2 py-6 text-ui-sm text-ink-2">{botName}還沒有電腦。先跟它說句話，電腦開好後這裡就會有它做的檔案。</p>
  if (out?.state === 'off')
    return (
      <div className="flex flex-col items-start gap-3 px-2 py-6">
        <p className="m-0 text-ui-sm text-ink-2">{botName}的電腦沒有開機。要開機才看得到檔案清單（你也可以直接在對話裡請它傳檔案）。</p>
        <Button variant="secondary" icon={Power} onClick={onWake} disabled={waking}>
          {waking ? '開機中…' : '開機看檔案'}
        </Button>
      </div>
    )
  return <p className="m-0 px-2 py-6 text-ui-sm text-ink-3">讀取中…</p>
}

export function BotFilesPage({ botId }: { botId: string }) {
  const bots = useStore(botsStore).bots
  const bot = bots?.find((b) => b.id === botId)
  const search = useSearch()
  const selected = search.get('path')
  const folderView = search.get('view') === 'folder'
  const folder = search.get('folder') || '/workspace'
  const [q, setQ] = useState('')
  const [showAll, setShowAll] = useState(false)
  const [newFolder, setNewFolder] = useState(false)
  const [drag, setDrag] = useState(false)
  const [uploading, setUploading] = useState(false)
  const input = useRef<HTMLInputElement>(null)
  const toast = useToast()
  const { state, reload, wake, waking } = useListing(botId, !folderView, folder)
  const name = bot?.name ?? 'Bot'
  const narrow = useNarrow()
  useLockBody(!!selected && narrow) // 手機的檔案預覽 sheet 開著：後面的清單不跟著捲

  const go = (patch: Record<string, string | null>, replace = false) => {
    const p = new URLSearchParams(window.location.search)
    for (const [k, v] of Object.entries(patch)) {
      if (v === null) p.delete(k)
      else p.set(k, v)
    }
    const s = p.toString()
    navigate(`/bots/${encodeURIComponent(botId)}/files${s ? '?' + s : ''}`, { replace })
  }
  const entries = (state?.out?.entries ?? []).filter((e) => matches(e, q))
  const selectedEntry = state?.out?.entries.find((e) => e.path === selected) ?? (selected ? ({ name: selected.slice(selected.lastIndexOf('/') + 1), path: selected, dir: false, size: 0, modified: 0, kind: 'other', important: true } as FileEntry) : null)
  const [dlg, setDlg] = useState<FileDialog>(null)
  const items = fileMenu(botId, selectedEntry, setDlg)

  const upload = async (files: File[]) => {
    if (!files.length) return
    setUploading(true)
    try {
      const out = await filesUploadForm(botId, files, folderView ? folder : '')
      toast.show({ kind: 'done', text: out.length === 1 ? `已上傳「${out[0].name}」` : `已上傳 ${out.length} 個檔案` })
      reload()
      if (out.length === 1) go({ path: out[0].path })
    } catch (e) {
      toast.show({ kind: 'error', text: errText(e) })
    } finally {
      setUploading(false)
    }
  }
  const onDrop = (e: DragEvent) => {
    e.preventDefault()
    setDrag(false)
    void upload([...e.dataTransfer.files])
  }

  const sub = (e: FileEntry) =>
    e.dir ? '資料夾' : [e.group === 'peer' && e.from ? e.from : null, sizeLabel(e.size), whenLabel(e.modified)].filter(Boolean).join(' · ')
  const open = (e: FileEntry) => (e.dir ? go({ view: 'folder', folder: e.path, path: null }) : go({ path: e.path }))

  let list: ReactNode
  if (!state || !state.out || state.out.state !== 'ready') {
    list = <ListState out={state?.out} error={state?.error} waking={waking} onWake={wake} botName={name} />
  } else if (folderView) {
    list = (
      <>
        <nav aria-label="目前的資料夾" className="flex flex-wrap items-center gap-0.5 px-1 pb-1 pt-2 text-sub">
          {crumbs(folder).map((c, i, all) => (
            <span key={c.path} className="flex items-center gap-0.5">
              {i > 0 && <Icon icon={ChevronRight} size={14} className="text-ink-3" />}
              {i === all.length - 1 ? (
                <span aria-current="page" className="px-1 text-ink">
                  {c.name}
                </span>
              ) : (
                <button type="button" onClick={() => go({ folder: c.path, path: null })} className="rounded-seg px-1 text-ink-2 hover:text-ink">
                  {c.name}
                </button>
              )}
            </span>
          ))}
        </nav>
        {entries.length === 0 ? (
          <p className="m-0 px-2 py-6 text-ui-sm text-ink-3">{q ? '沒有符合的檔案。' : '這個資料夾是空的。'}</p>
        ) : (
          <ul className="m-0 flex list-none flex-col gap-0.5 p-0" aria-label="資料夾內容">
            {entries.map((e) => (
              <Row key={e.path} e={e} on={e.path === selected} onOpen={() => open(e)} sub={sub(e)} />
            ))}
          </ul>
        )}
        {state.out.total > state.out.entries.length && <p className="m-0 px-2 py-2 text-sub text-ink-3">還有 {state.out.total - state.out.entries.length} 項沒有列出</p>}
      </>
    )
  } else {
    const important = entries.filter((e) => e.important)
    const other = entries.filter((e) => !e.important)
    const shown = showAll ? entries : important
    list = (
      <>
        {shown.length === 0 && <p className="m-0 px-2 py-6 text-ui-sm text-ink-3">{q ? '沒有符合的檔案。' : `${name}還沒有做出檔案。`}</p>}
        {GROUPS.map((g) => {
          const xs = shown.filter((e) => (e.group ?? 'result') === g.key)
          if (!xs.length) return null
          return (
            <Section key={g.key} title={g.label} count={xs.length}>
              {xs.map((e) => (
                <Row key={e.path} e={e} on={e.path === selected} onOpen={() => open(e)} sub={sub(e)} />
              ))}
            </Section>
          )
        })}
        {other.length > 0 && (
          <button type="button" onClick={() => setShowAll((v) => !v)} className="mx-2 mt-2 self-start text-sub text-accent-ink hover:underline">
            {showAll ? '只看成果' : `也顯示建置檔、快取等 ${other.length} 個檔案`}
          </button>
        )}
        {state.out.truncated && <p className="m-0 px-2 py-2 text-sub text-ink-3">檔案太多，只列出最新的一部分。用資料夾瀏覽可以看到全部。</p>}
      </>
    )
  }

  const count = state?.out?.state === 'ready' ? state.out.total : null
  return (
    <main className="flex min-h-0 min-w-0 flex-1 max-md:flex-col md:h-dvh" aria-label={`${name}的檔案`}>
      <section
        aria-label={`${name}的檔案`}
        className={cx('relative flex min-h-0 flex-col border-r border-divider md:w-[380px] md:flex-none', 'max-md:flex-1')}
        onDragOver={(e) => {
          e.preventDefault()
          setDrag(true)
        }}
        onDragLeave={(e) => {
          if (e.currentTarget === e.target) setDrag(false)
        }}
        onDrop={onDrop}
      >
        {narrow && (
          // 手機（L19）：返回、Bot 頭像、「小秘的檔案 · N 個檔案」
          <div className="flex min-h-[60px] flex-none items-center gap-2.5 px-3 pb-1 pt-[max(8px,env(safe-area-inset-top))]">
            <BackButton fallback={`/bots/${botId}`} />
            {bot && <Avatar shape={bot.shape} color={bot.color} dot="none" size={28} ring="#070707" />}
            <div className="flex min-w-0 flex-1 flex-col">
              <h1 className="m-0 truncate text-title font-normal">{name}的檔案</h1>
              <span className="text-meta text-ink-3">{count === null ? ' ' : `${count} ${folderView ? '項' : '個檔案'}`}</span>
            </div>
          </div>
        )}
        <header className="flex flex-none flex-col gap-2.5 px-3 pb-2 pt-3 max-md:pt-1">
          <div className="flex items-center gap-2.5 px-1 max-md:hidden">
            <Button variant="ghost" size="sm" icon={ArrowLeft} onClick={() => navigate(`/bots/${botId}`)}>
              回到對話
            </Button>
            {bot && <Avatar {...look(bot)} dot="none" size={30} ring="#070707" />}
            <div className="flex min-w-0 flex-1 flex-col">
              <h1 className="m-0 truncate text-title font-normal">{name}的檔案</h1>
              <span className="text-meta text-ink-3">{count === null ? ' ' : `${count} ${folderView ? '項' : '個檔案'}`}</span>
            </div>
            <IconButton icon={RefreshCw} label="重新整理" onClick={reload} />
          </div>
          <TextInput label="搜尋檔案" hideLabel icon={Search} placeholder="搜尋檔案" value={q} onChange={(e) => setQ(e.target.value)} />
          <div role="radiogroup" aria-label="瀏覽方式" className="grid grid-cols-2 gap-0.5 rounded-field bg-inset p-[3px]">
            {[
              [false, '依種類'],
              [true, '資料夾'],
            ].map(([v, label]) => (
              <button
                key={String(v)}
                type="button"
                role="radio"
                aria-checked={folderView === v}
                onClick={() => go({ view: v ? 'folder' : null, folder: null })}
                className={cx('h-8 rounded-seg text-ui-sm max-sm:h-10', folderView === v ? 'bg-control text-ink' : 'text-ink-2 hover:text-ink')}
              >
                {label as string}
              </button>
            ))}
          </div>
        </header>
        <div className="min-h-0 flex-1 overflow-auto px-2 pb-3" data-testid="file-list">
          {list}
        </div>
        <footer className="flex flex-none items-center gap-2 border-t border-divider px-3 py-2.5 pb-[max(10px,env(safe-area-inset-bottom))]">
          <input ref={input} type="file" multiple hidden onChange={(e) => void upload([...(e.target.files ?? [])]).finally(() => (e.target.value = ''))} aria-label="選擇要上傳的檔案" />
          <Button variant="outline" icon={Upload} className="flex-1" onClick={() => input.current?.click()} disabled={uploading}>
            {uploading ? '上傳中…' : `上傳檔案給${name}`}
          </Button>
          {folderView && <IconButton icon={FolderPlus} label="新資料夾" onClick={() => setNewFolder(true)} />}
        </footer>
        {drag && (
          <div className="pointer-events-none absolute inset-2 flex flex-col items-center justify-center gap-2 rounded-card border-2 border-dashed border-accent bg-app/85 text-ui">
            <Icon icon={Upload} size={28} className="text-accent-ink" />
            放開就上傳到{folderView ? folder : ' /workspace/uploads'}
          </div>
        )}
      </section>

      {/* 桌面：右邊預覽 */}
      {!narrow && (
        <div className="flex min-h-0 min-w-0 flex-1">
          {selected ? (
            <FilePreview key={selected} botId={botId} bot={bot} path={selected} actions={items} onClose={() => go({ path: null })} />
          ) : (
            <div className="flex flex-1 flex-col items-center justify-center gap-2 p-8 text-center text-ink-3">
              <Icon icon={Upload} size={28} />
              <p className="m-0 text-ui-sm">點左邊的檔案在這裡預覽。把檔案拖到左邊就能上傳給{name}。</p>
            </div>
          )}
        </div>
      )}

      {/* 手機：從底部拉出的預覽（L19） */}
      {selected && narrow && (
        <div className="fixed inset-0 z-40 flex flex-col justify-end">
          <div className="absolute inset-0 bg-black/60" onClick={() => go({ path: null })} aria-hidden="true" />
          <div role="dialog" aria-modal="true" aria-label={`預覽：${selectedEntry?.name ?? ''}`} className="relative flex max-h-[85dvh] flex-col rounded-t-[22px] border-t border-divider bg-panel pb-[env(safe-area-inset-bottom)] shadow-dialog">
            <span aria-hidden="true" className="mx-auto mt-2 h-1 w-10 flex-none rounded-full bg-control" />
            <FilePreview
              key={selected}
              botId={botId}
              bot={bot}
              path={selected}
              compact
              actions={items.filter((i) => i.label !== '傳給其他 Bot' && i.label !== '在對話中提到')}
              onClose={() => go({ path: null })}
              footer={
                selectedEntry && (
                  <div className="flex flex-none items-center gap-2 px-4 pb-3 pt-1">
                    <Button variant="primary" icon={AtSign} className="h-12 flex-1" onClick={() => mentionInChat(botId, selected)}>
                      在對話中提到
                    </Button>
                    <a
                      href={`/api/v1/files.download?${new URLSearchParams({ bot_id: botId, path: selected })}`}
                      download={selectedEntry.name}
                      aria-label="下載"
                      className="flex size-12 flex-none items-center justify-center rounded-full bg-control text-ink"
                    >
                      <Icon icon={Download} />
                    </a>
                    <ActionsButton items={items} />
                  </div>
                )
              }
            />
          </div>
        </div>
      )}
      <FileDialogs
        botId={botId}
        entry={selectedEntry}
        dlg={dlg}
        onClose={() => setDlg(null)}
        onChanged={(p) => {
          reload()
          go({ path: p })
        }}
      />
      {newFolder && (
        <NewFolderDialog
          botId={botId}
          parent={folder}
          onClose={() => setNewFolder(false)}
          onCreated={(p) => {
            setNewFolder(false)
            go({ folder: p, path: null })
          }}
        />
      )}
    </main>
  )
}

/** 手機 sheet 的「傳給其他 Bot」與更多動作。 */
function ActionsButton({ items }: { items: { label: string; onSelect?: () => void }[] }) {
  const send = items.find((i) => i.label === '傳給其他 Bot')
  return (
    <>
      {send && (
        <button type="button" aria-label="傳給其他 Bot" onClick={send.onSelect} className="flex size-12 flex-none items-center justify-center rounded-full bg-control text-ink">
          <Icon icon={Send} />
        </button>
      )}
    </>
  )
}
