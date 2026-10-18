import { useEffect, useId, useRef, useState, type PointerEvent } from 'react'
import { Move, Upload, Wand2, ZoomIn, ZoomOut } from 'lucide-react'
import type { BotsGetOutput, BotsUpdateInput } from '../../api/generated'
import { linkClick } from '../../router'
import { SHAPES, SHAPE_IDS, SHAPE_LABELS, resolveShape, type AvatarShape } from '../../ui/avatar-data'
import { Button, IconButton } from '../../ui/Button'
import { Dialog } from '../../ui/Dialog'
import { Icon } from '../../ui/Icon'
import { cx } from '../../ui/cx'

// 換成圖片（G04、BT-2）：上傳照片、選裁切形狀（同吉祥物 10 種）、縮放 100%–200%、拖曳；
// 瀏覽器裁成 256×256、照形狀切好，存成頭像（不疊眼睛）。AI 產生要先設定圖片模型。

type Bot = BotsGetOutput

/** 上傳原圖上限（OpenMausBot：PNG、JPEG、GIF、WebP，最大 10 MB；同 internal/limits AvatarUploadBytes）。 */
export const UPLOAD_MAX = 10 << 20
/** 存起來的圖片上限（同 internal/limits AvatarImageBytes）。 */
const IMAGE_MAX = 400_000
const OUT = 256
const VIEW = 300
const TYPES = ['image/png', 'image/jpeg', 'image/gif', 'image/webp']

type Pic = { url: string; name: string; size: number; w: number; h: number; img: HTMLImageElement }

function clampOffset(x: number, y: number, w: number, h: number) {
  return { x: Math.min(0, Math.max(VIEW - w, x)), y: Math.min(0, Math.max(VIEW - h, y)) }
}

/** 依目前的縮放與位置，把照片裁成 256×256、照形狀切好的 data URL。 */
export function cropToDataURL(img: CanvasImageSource, w: number, h: number, zoom: number, off: { x: number; y: number }, shape: AvatarShape): string {
  const c = document.createElement('canvas')
  c.width = OUT
  c.height = OUT
  const ctx = c.getContext('2d')
  if (!ctx) throw new Error('瀏覽器不能裁切圖片')
  const k = OUT / 44
  const path = new Path2D()
  path.addPath(new Path2D(SHAPES[shape].d), new DOMMatrix([k, 0, 0, k, 0, 0]))
  ctx.clip(path)
  const base = VIEW / Math.min(w, h)
  const s = base * zoom * (OUT / VIEW)
  ctx.drawImage(img, off.x * (OUT / VIEW), off.y * (OUT / VIEW), w * s, h * s)
  for (const [type, q] of [
    ['image/webp', 0.9],
    ['image/webp', 0.75],
    ['image/png', undefined],
  ] as const) {
    const url = c.toDataURL(type, q)
    if (url.startsWith('data:' + type) && url.length <= IMAGE_MAX) return url
  }
  throw new Error('裁好的圖片太大')
}

export function AvatarUploadDialog({
  open,
  bot,
  onClose,
  save,
}: {
  open: boolean
  bot: Bot
  onClose: () => void
  save: (patch: Omit<BotsUpdateInput, 'id'>, done?: string) => Promise<boolean>
}) {
  const file = useRef<HTMLInputElement>(null)
  const clip = useId().replace(/[^a-zA-Z0-9_-]/g, '')
  const [pic, setPic] = useState<Pic | null>(null)
  const [shape, setShape] = useState<AvatarShape>(resolveShape(bot.shape))
  const [zoom, setZoom] = useState(1.25)
  const [off, setOff] = useState({ x: 0, y: 0 })
  const [err, setErr] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const drag = useRef<{ x: number; y: number; ox: number; oy: number } | null>(null)

  useEffect(
    () => () => {
      if (pic) URL.revokeObjectURL(pic.url)
    },
    [pic],
  )

  const base = pic ? VIEW / Math.min(pic.w, pic.h) : 1
  const dw = pic ? pic.w * base * zoom : VIEW
  const dh = pic ? pic.h * base * zoom : VIEW

  const choose = (f: File | undefined) => {
    if (!f) return
    setErr(null)
    if (!TYPES.includes(f.type)) return setErr('只接受 PNG、JPEG、GIF、WebP')
    if (f.size > UPLOAD_MAX) return setErr('圖片超過 10 MB，請換小一點的')
    const url = URL.createObjectURL(f)
    const img = new Image()
    img.onload = () => {
      const w = img.naturalWidth
      const h = img.naturalHeight
      const b = VIEW / Math.min(w, h)
      setZoom(1.25)
      setOff(clampOffset((VIEW - w * b * 1.25) / 2, (VIEW - h * b * 1.25) / 2, w * b * 1.25, h * b * 1.25))
      setPic({ url, name: f.name, size: f.size, w, h, img })
    }
    img.onerror = () => {
      URL.revokeObjectURL(url)
      setErr('讀不到這張圖片')
    }
    img.src = url
  }

  const rezoom = (z: number) => {
    const nz = Math.min(2, Math.max(1, z))
    if (!pic) return setZoom(nz)
    // 以中心縮放
    const cx0 = VIEW / 2 - off.x
    const cy0 = VIEW / 2 - off.y
    const r = nz / zoom
    setZoom(nz)
    setOff(clampOffset(VIEW / 2 - cx0 * r, VIEW / 2 - cy0 * r, pic.w * base * nz, pic.h * base * nz))
  }

  const down = (e: PointerEvent) => {
    if (!pic) return
    ;(e.target as Element).setPointerCapture?.(e.pointerId)
    drag.current = { x: e.clientX, y: e.clientY, ox: off.x, oy: off.y }
  }
  const move = (e: PointerEvent) => {
    const d = drag.current
    if (!d) return
    setOff(clampOffset(d.ox + e.clientX - d.x, d.oy + e.clientY - d.y, dw, dh))
  }

  const use = async () => {
    if (!pic) return
    setBusy(true)
    try {
      const image = cropToDataURL(pic.img, pic.w, pic.h, zoom, off, shape)
      if (await save({ image, shape }, '已換成圖片')) onClose()
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  const shapeD = SHAPES[shape].d
  return (
    <Dialog open={open} onClose={onClose} busy={busy} width={900} title="換成圖片">
      <div className="mt-1 grid grid-cols-2 gap-6 max-md:grid-cols-1">
        <section aria-label="上傳圖片" className="flex flex-col gap-3">
          <div className="flex items-center gap-2 text-ui">
            <Icon icon={Upload} size={18} />
            <span className="flex-1">上傳圖片</span>
            {pic && <span className="truncate font-mono text-meta text-ink-2">{pic.name} · {(pic.size / 1048576).toFixed(1)} MB</span>}
          </div>
          {pic ? (
            <div
              data-testid="crop"
              className="relative mx-auto cursor-move touch-none overflow-hidden rounded-group bg-inset select-none"
              style={{ width: VIEW, height: VIEW }}
              onPointerDown={down}
              onPointerMove={move}
              onPointerUp={() => (drag.current = null)}
              onPointerCancel={() => (drag.current = null)}
            >
              <img src={pic.url} alt="" draggable={false} className="pointer-events-none absolute max-w-none" style={{ left: off.x, top: off.y, width: dw, height: dh }} />
              <svg viewBox="0 0 44 44" width={VIEW} height={VIEW} className="pointer-events-none absolute inset-0" aria-hidden="true">
                <path d={`M0 0H44V44H0Z ${shapeD}`} fillRule="evenodd" fill="rgba(0,0,0,0.6)" />
                <path d={shapeD} fill="none" stroke="rgba(255,255,255,0.85)" strokeWidth={0.35} />
                <defs>
                  <clipPath id={clip}>
                    <path d={shapeD} />
                  </clipPath>
                </defs>
                <g clipPath={`url(#${clip})`} stroke="rgba(255,255,255,0.45)" strokeWidth={0.15}>
                  <path d="M14.67 0V44M29.33 0V44M0 14.67H44M0 29.33H44" />
                </g>
              </svg>
              <span className="pointer-events-none absolute bottom-2 left-2 flex items-center gap-1 rounded-full bg-black/60 px-2 py-0.5 text-meta">
                <Icon icon={Move} size={12} />
                拖曳調整位置
              </span>
            </div>
          ) : (
            <button
              type="button"
              onClick={() => file.current?.click()}
              className="mx-auto flex flex-col items-center justify-center gap-2 rounded-group border border-dashed border-line bg-inset text-ui-sm text-ink-2 hover:text-ink"
              style={{ width: VIEW, height: VIEW }}
            >
              <Icon icon={Upload} size={24} />
              選一張照片
              <span className="text-meta text-ink-3">PNG、JPEG、GIF、WebP，最大 10 MB</span>
            </button>
          )}
          <input ref={file} type="file" accept={TYPES.join(',')} aria-label="選擇頭像圖片" className="sr-only-text" onChange={(e) => choose(e.target.files?.[0] ?? undefined)} />
          <div className="flex items-center gap-2">
            <IconButton icon={ZoomOut} label="縮小" size="sm" disabled={!pic || zoom <= 1} onClick={() => rezoom(zoom - 0.25)} />
            <input
              type="range"
              aria-label="縮放"
              min={100}
              max={200}
              step={5}
              value={Math.round(zoom * 100)}
              disabled={!pic}
              onChange={(e) => rezoom(Number(e.target.value) / 100)}
              className="xb-slider h-4 flex-1"
              style={{ ['--pct' as string]: `${(zoom - 1) * 100}%` }}
            />
            <IconButton icon={ZoomIn} label="放大" size="sm" disabled={!pic || zoom >= 2} onClick={() => rezoom(zoom + 0.25)} />
            <span className="w-12 text-right text-meta text-ink-2">{Math.round(zoom * 100)}%</span>
          </div>
          <div className="flex items-baseline justify-between text-meta">
            <span>裁切形狀</span>
            <span className="text-ink-2">和吉祥物一樣 10 種</span>
          </div>
          <div role="radiogroup" aria-label="裁切形狀" className="grid grid-cols-5 gap-1.5">
            {SHAPE_IDS.map((s) => (
              <button
                key={s}
                type="button"
                role="radio"
                aria-checked={s === shape}
                aria-label={SHAPE_LABELS[s]}
                onClick={() => setShape(s)}
                className={cx('flex h-[54px] flex-col items-center justify-center gap-0.5 rounded-field border-2 bg-inset text-meta text-ink-2', s === shape ? 'border-accent text-ink' : 'border-transparent hover:bg-control')}
              >
                <ShapeThumb shape={s} pic={pic} />
                <span aria-hidden="true">{SHAPE_LABELS[s]}</span>
              </button>
            ))}
          </div>
          {err && (
            <p role="alert" className="m-0 text-meta text-danger">
              {err}
            </p>
          )}
          <div className="flex justify-end gap-2">
            <Button variant="outline" onClick={() => file.current?.click()} disabled={busy}>
              {pic ? '換一張' : '選照片'}
            </Button>
            <Button variant="primary" disabled={!pic} loading={busy} onClick={() => void use()}>
              用這張
            </Button>
          </div>
        </section>
        <section aria-label="用 AI 產生" className="flex flex-col gap-3 border-l border-divider pl-6 max-md:border-l-0 max-md:border-t max-md:pl-0 max-md:pt-4">
          <div className="flex items-center gap-2 text-ui">
            <Icon icon={Wand2} size={18} />
            用 AI 產生
          </div>
          <p className="m-0 text-ui-sm text-ink-2">用一句話描述，產生 3 張候選讓你挑。要先在「AI 模型」設定圖片模型；金鑰只存在這台電腦，不會傳給 Bot。</p>
          <a href="/settings/models" onClick={linkClick('/settings/models', onClose)} className="w-fit text-ui-sm">
            先設定圖片模型
          </a>
          <p className="m-0 text-meta text-ink-3">圖片模型在之後的版本開放；現在先上傳照片。</p>
        </section>
      </div>
    </Dialog>
  )
}

/** 裁切形狀的小格：有照片時顯示照片裁成該形狀，沒有時是灰色剪影。 */
function ShapeThumb({ shape, pic }: { shape: AvatarShape; pic: Pic | null }) {
  const id = useId().replace(/[^a-zA-Z0-9_-]/g, '')
  return (
    <svg viewBox="0 0 44 44" width={24} height={24} aria-hidden="true">
      <defs>
        <clipPath id={id}>
          <path d={SHAPES[shape].d} />
        </clipPath>
      </defs>
      {pic ? (
        <g clipPath={`url(#${id})`}>
          <image href={pic.url} x="0" y="0" width="44" height="44" preserveAspectRatio="xMidYMid slice" />
        </g>
      ) : (
        <path d={SHAPES[shape].d} fill="#5a5a5a" />
      )}
    </svg>
  )
}
