import { ChevronRight, Download, Maximize2 } from 'lucide-react'
import { useEffect, useRef, useState } from 'react'
import { filesDownloadUrl, filesRead } from '../api/generated'
import { filesHref, kindFromName } from '../files/data'
import { FileIcon } from '../files/FilePreview'
import { isPage, PAGE_MAX_BYTES } from '../files/page'
import { PageFrame } from '../files/PageFrame'
import { errText } from '../settings/format'
import { IconButton } from '../ui/Button'
import { Dialog } from '../ui/Dialog'
import { Icon } from '../ui/Icon'
import { cx } from '../ui/cx'
import { baseName, fileRefProps, isBoxFile, PATH_RE, touchScreen } from './markdown/fileLink'

// 對話裡的檔案，照一般聊天軟體的樣子（2026-10-21 使用者：「不要給我那些路徑，我又看不懂」）：
// 圖片直接顯示，其他檔案是一張有圖示與檔名的小卡，點了預覽、右邊可以下載。
// 你上傳的附件（訊息後面的「附件：/workspace/uploads/…」，Bot 靠它知道去哪裡讀）和 Bot 回覆裡提到的檔案都用這個。

/** 一則訊息最多顯示幾個檔案（太多時後面的就不顯示卡片，路徑還是點得到）。 */
export const TILES_MAX = 6

const ATTACH_LINE = /^附件：(\/workspace\/\S.*)$/

/** 把你上傳附件時加在訊息後面的「附件：路徑」行拿掉，回傳要顯示的文字與附件路徑。 */
export function splitAttachments(text: string): { text: string; paths: string[] } {
  const paths: string[] = []
  const keep: string[] = []
  for (const line of text.split('\n')) {
    const m = line.trim().match(ATTACH_LINE)
    if (m && isBoxFile(m[1])) paths.push(m[1].trim())
    else keep.push(line)
  }
  return { text: keep.join('\n').trim(), paths }
}

/** 回覆裡提到的 Bot 電腦檔案（有副檔名的檔案，不含資料夾），照出現順序、不重複。 */
export function filesIn(text: string): string[] {
  const out: string[] = []
  for (const m of text.matchAll(new RegExp(PATH_RE.source, 'g'))) {
    let p = m[0].replace(/[.,;:!?]+$/, '')
    try {
      p = decodeURI(p)
    } catch {
      // 照原樣
    }
    if (isBoxFile(p) && /\.[^/.]+$/.test(p) && !out.includes(p)) out.push(p)
  }
  return out
}

function ImageTile({ botId, path }: { botId: string; path: string }) {
  const [broken, setBroken] = useState(false)
  const name = baseName(path)
  if (broken) return <FileRow botId={botId} path={path} />
  return (
    <span {...fileRefProps(filesHref(botId, path))} aria-label={`看 ${name}`} className="block cursor-pointer overflow-hidden rounded-bubble bg-control">
      <img src={filesDownloadUrl(botId, path, { thumb: true })} alt={name} loading="lazy" onError={() => setBroken(true)} className="block max-h-72 max-w-full object-contain" />
    </span>
  )
}

function FileRow({ botId, path }: { botId: string; path: string }) {
  const name = baseName(path)
  return (
    <div className="flex w-[min(100%,340px)] items-center gap-1 rounded-[14px] bg-control pr-1">
      <div {...fileRefProps(filesHref(botId, path))} aria-label={`預覽 ${name}`} className="flex min-w-0 flex-1 cursor-pointer items-center gap-3 rounded-[14px] py-2 pl-2.5 text-ink hover:bg-hover/50">
        <FileIcon kind={kindFromName(name)} size={34} />
        <span className="flex min-w-0 flex-1 flex-col">
          <span className="truncate text-ui">{name}</span>
          <span className="text-meta text-ink-3">點一下打開</span>
        </span>
        <span className="text-ink-3">
          <Icon icon={ChevronRight} size={16} />
        </span>
      </div>
      <a href={filesDownloadUrl(botId, path)} download={name} aria-label={`下載 ${name}`} title="下載" className="flex size-9 flex-none items-center justify-center rounded-[10px] text-ink-2 hover:bg-hover hover:text-ink">
        <Icon icon={Download} size={18} />
      </a>
    </div>
  )
}

/** Bot 做的網頁、儀表板：在對話裡直接顯示（沙盒，見 files/page.ts），可以放大、點檔名看原始碼、下載。捲到才讀。 */
function PageTile({ botId, path }: { botId: string; path: string }) {
  const name = baseName(path)
  const [html, setHtml] = useState<string | null>(null)
  const [note, setNote] = useState('')
  const [big, setBig] = useState(false)
  const box = useRef<HTMLDivElement>(null)
  useEffect(() => {
    const el = box.current
    if (!el) return
    let live = true
    const load = () =>
      filesRead({ bot_id: botId, path, max_bytes: PAGE_MAX_BYTES })
        .then((d) => {
          if (!live) return
          if (d.truncated) setNote('網頁超過 1 MB，對話裡放不下，請下載後開啟。')
          else if (!d.text) setNote(d.note || '這個網頁是空的。')
          else setHtml(d.text)
        })
        .catch((e: unknown) => live && setNote(errText(e)))
    if (typeof IntersectionObserver === 'undefined') {
      void load()
      return () => {
        live = false
      }
    }
    const seen = new IntersectionObserver(
      (entries) => {
        if (entries.some((e) => e.isIntersecting)) {
          seen.disconnect()
          void load()
        }
      },
      { rootMargin: '200px' },
    )
    seen.observe(el)
    return () => {
      live = false
      seen.disconnect()
    }
  }, [botId, path])
  return (
    <div ref={box} data-testid="page-tile" className="flex w-[640px] max-w-full flex-col overflow-hidden rounded-[14px] bg-control">
      <div className="flex items-center gap-2 py-1.5 pl-2.5 pr-1">
        <FileIcon kind={kindFromName(name)} size={28} />
        <span {...fileRefProps(filesHref(botId, path))} title="看原始碼" className="min-w-0 flex-1 cursor-pointer truncate text-ui hover:underline">
          {name}
        </span>
        <IconButton icon={Maximize2} label={`放大 ${name}`} onClick={() => setBig(true)} disabled={!html} />
        <a href={filesDownloadUrl(botId, path)} download={name} aria-label={`下載 ${name}`} title="下載" className="flex size-9 flex-none items-center justify-center rounded-[10px] text-ink-2 hover:bg-hover hover:text-ink">
          <Icon icon={Download} size={18} />
        </a>
      </div>
      {html && touchScreen() ? (
        // 觸控螢幕：對話裡只看預覽，點一下放大再操作。iPhone 從網頁或連結上開始滑，對話會捲不動（見 webRefProps）
        <div data-testid="page-preview" onClick={() => setBig(true)} className="cursor-pointer">
          <PageFrame html={html} title={name} className="pointer-events-none h-[440px] max-md:h-[320px]" />
        </div>
      ) : html ? (
        <PageFrame html={html} title={name} className="h-[440px] max-md:h-[320px]" />
      ) : (
        <div className="flex h-24 items-center justify-center px-4 text-center text-ui-sm text-ink-3">{note || '網頁載入中…'}</div>
      )}
      <Dialog open={big} onClose={() => setBig(false)} title={name} width={1100}>
        {html && <PageFrame html={html} title={name} className="h-[75vh] rounded-group max-md:h-[70vh]" />}
      </Dialog>
    </div>
  )
}

/** 一則訊息的檔案：圖片直接顯示，網頁在對話裡打開，其他是檔案卡。mine＝你的訊息（靠右）。 */
export function FileTiles({ botId, paths, mine }: { botId: string; paths: string[]; mine?: boolean }) {
  const shown = paths.slice(0, TILES_MAX)
  if (!shown.length) return null
  return (
    <div data-testid="file-tiles" className={cx('flex max-w-[85%] flex-col gap-1.5', mine ? 'items-end self-end' : 'items-start self-start')}>
      {shown.map((p) =>
        kindFromName(p) === 'image' ? (
          <ImageTile key={p} botId={botId} path={p} />
        ) : isPage(p) && !mine ? (
          <PageTile key={p} botId={botId} path={p} />
        ) : (
          <FileRow key={p} botId={botId} path={p} />
        ),
      )}
    </div>
  )
}
