import { memo, useContext, useMemo, type ReactNode } from 'react'
import { boxPath, FileLinkContext, fileRefProps } from './fileLink'
import type { Token, Tokens } from 'marked'
import { CircleAlert, Info, Lightbulb, MessageSquareWarning, Square, SquareCheck, TriangleAlert, type LucideIcon } from 'lucide-react'
import { Icon } from '../../ui/Icon'
import { cx } from '../../ui/cx'
import { CodeBlock } from './CodeBlock'
import { Diagram, FlowSteps } from './Diagram'
import { decodeEntities, safeHref, safeImageSrc } from './safe'
import { flowSteps, lex, plainText, type TableCell } from './parse'
import { Table } from './Table'

// 回覆排版（CH-6，design/C07）：GFM（標題、清單、待辦、表格、程式碼、引言、刪除線）＋提示框（> [!NOTE]）。
// 只用 marked 的 lexer 切出語法單位，再用 React 元素組出來：不經過 innerHTML，
// 回覆裡的 HTML 標籤一律當成文字顯示（Bot 的回覆是資料，SEC-8）。

// ── 行內 ─────────────────────────────────────────────

/** 行內程式碼；是 Bot 電腦裡的檔案路徑時變成連結（點了打開檔案）。 */
function Codespan({ text }: { text: string }) {
  const link = useContext(FileLinkContext)
  const href = link?.(decodeEntities(text))
  const cls = 'break-words rounded-[6px] bg-inset px-1.5 py-0.5 font-mono text-[13px]'
  if (!href) return <code className={cls}>{text}</code>
  return (
    <span {...fileRefProps(href)} className="cursor-pointer text-accent-ink underline decoration-accent-ink/40 underline-offset-2">
      <code className={cls}>{text}</code>
    </span>
  )
}

/** 連結；指到 Bot 電腦裡的檔案時（`[報告](file:///workspace/…)` 等）在旁邊打開那個檔案，不另開分頁。 */
function MdLink({ l, children }: { l: Tokens.Link; children: ReactNode }) {
  const link = useContext(FileLinkContext)
  const p = boxPath(l.href)
  const file = p ? link?.(p) : undefined
  if (file) {
    return (
      <span {...fileRefProps(file)} className="cursor-pointer text-accent-ink underline decoration-accent-ink/50 underline-offset-2 hover:decoration-accent-ink">
        {children}
      </span>
    )
  }
  const href = safeHref(l.href)
  if (!href) return <span>{children}</span>
  const external = !href.startsWith('#')
  return (
    <a href={href} draggable={false} title={l.title ?? undefined} {...(external ? { target: '_blank', rel: 'noopener noreferrer nofollow' } : {})} className="text-accent-ink underline decoration-accent-ink/50 underline-offset-2 hover:decoration-accent-ink">
      {children}
    </a>
  )
}

function inline(tokens: Token[] | undefined, key: string): ReactNode[] {
  return (tokens ?? []).map((t, i) => inlineOne(t, `${key}.${i}`))
}

function inlineOne(t: Token, k: string): ReactNode {
  switch (t.type) {
    case 'text':
    case 'escape': {
      const tt = t as Tokens.Text
      if (tt.tokens?.length) return inline(tt.tokens, k)
      return decodeEntities(tt.text)
    }
    case 'strong':
      return (
        <strong key={k} className="font-normal text-ink [text-shadow:0_0_0.4px_currentColor]">
          {inline((t as Tokens.Strong).tokens, k)}
        </strong>
      )
    case 'em':
      return (
        <em key={k} className="italic">
          {inline((t as Tokens.Em).tokens, k)}
        </em>
      )
    case 'del':
      return (
        <del key={k} className="text-ink-3">
          {inline((t as Tokens.Del).tokens, k)}
        </del>
      )
    case 'codespan':
      return <Codespan key={k} text={(t as Tokens.Codespan).text} />
    case 'br':
      return <br key={k} />
    case 'link': {
      const l = t as Tokens.Link
      return (
        <MdLink key={k} l={l}>
          {inline(l.tokens, k)}
        </MdLink>
      )
    }
    case 'image':
      return <InlineImage key={k} t={t as Tokens.Image} />
    case 'html': {
      // 標籤當文字顯示；只有 <br> 換行
      const text = (t as Tokens.HTML).text
      return /^<br\s*\/?>$/i.test(text.trim()) ? <br key={k} /> : text
    }
    default: {
      const any = t as Tokens.Generic
      if (any.tokens) return inline(any.tokens, k)
      return typeof any.text === 'string' ? decodeEntities(any.text) : null
    }
  }
}

function InlineImage({ t }: { t: Tokens.Image }) {
  const src = safeImageSrc(t.href)
  if (!src) return <span className="text-ink-3">［圖片：{t.text || '無法顯示'}］</span>
  return <img src={src} alt={t.text} title={t.title ?? undefined} loading="lazy" referrerPolicy="no-referrer" className="inline-block max-h-80 max-w-full rounded-field align-middle" />
}

// ── 區塊 ─────────────────────────────────────────────

const ALERTS: Record<string, { title: string; icon: LucideIcon; tone: string; ink: string }> = {
  NOTE: { title: '提示', icon: Info, tone: 'bg-accent/13', ink: 'text-accent-ink' },
  TIP: { title: '小技巧', icon: Lightbulb, tone: 'bg-success/12', ink: 'text-success' },
  IMPORTANT: { title: '重要', icon: MessageSquareWarning, tone: 'bg-accent/13', ink: 'text-accent-ink' },
  WARNING: { title: '需要你決定', icon: TriangleAlert, tone: 'bg-warning/13', ink: 'text-warning-ink' },
  CAUTION: { title: '小心', icon: CircleAlert, tone: 'bg-danger/12', ink: 'text-danger' },
}

const MATH = /^\s*\$\$([\s\S]+?)\$\$\s*$/

function MathBlock({ tex }: { tex: string }) {
  // 數學式只做排版樣式（不載入 KaTeX）：原文顯示在置中的公式框裡
  return (
    <div
      role="math"
      aria-label={tex.trim()}
      className="overflow-x-auto whitespace-pre rounded-menu bg-inset px-2 py-3.5 text-center text-[19px] leading-[1.3]"
      style={{ fontFamily: "'STIX Two Math', 'Cambria Math', 'Times New Roman', serif" }}
      data-testid="md-math"
    >
      {tex.trim()}
    </div>
  )
}

function onlyImages(tokens: Token[] | undefined): Tokens.Image[] | null {
  const imgs: Tokens.Image[] = []
  for (const t of tokens ?? []) {
    if (t.type === 'image') imgs.push(t as Tokens.Image)
    else if (t.type === 'br' || (t.type === 'text' && !(t as Tokens.Text).text.trim())) continue
    else return null
  }
  return imgs.length ? imgs : null
}

function ImageGrid({ imgs }: { imgs: Tokens.Image[] }) {
  if (imgs.length === 1) {
    const src = safeImageSrc(imgs[0].href)
    if (!src) return <InlineImage t={imgs[0]} />
    return (
      <a href={src} target="_blank" rel="noopener noreferrer nofollow" className="block self-start">
        <img src={src} alt={imgs[0].text} loading="lazy" referrerPolicy="no-referrer" className="block max-h-96 max-w-full rounded-menu" />
      </a>
    )
  }
  return (
    <figure className="m-0 flex flex-col gap-1.5">
      <div className="grid grid-cols-2 gap-1.5 sm:grid-cols-3">
        {imgs.map((im, i) => {
          const src = safeImageSrc(im.href)
          return src ? (
            <a key={i} href={src} target="_blank" rel="noopener noreferrer nofollow" className="block overflow-hidden rounded-field bg-control">
              <img src={src} alt={im.text} loading="lazy" referrerPolicy="no-referrer" className="block h-[120px] w-full object-cover" />
            </a>
          ) : (
            <span key={i} className="flex h-[120px] items-center justify-center rounded-field bg-control p-2 text-center text-meta text-ink-3">
              {im.text || '無法顯示的圖片'}
            </span>
          )
        })}
      </div>
      <figcaption className="text-meta text-ink-3">{imgs.length} 張圖 · 點一下放大</figcaption>
    </figure>
  )
}

function ListBlock({ t, k }: { t: Tokens.List; k: string }) {
  const task = t.items.some((it) => it.task)
  const Tag = t.ordered ? 'ol' : 'ul'
  return (
    <Tag
      start={t.ordered && t.start !== '' && t.start !== 1 ? Number(t.start) : undefined}
      className={cx('m-0 flex flex-col gap-1', task ? 'list-none pl-0.5' : t.ordered ? 'list-decimal pl-6' : 'list-disc pl-5', 'marker:text-ink-3')}
    >
      {t.items.map((it, i) => (
        <li key={i} className={cx(it.task && 'flex items-start gap-2')}>
          {it.task && (
            <span role="checkbox" aria-checked={!!it.checked} aria-disabled="true" aria-label={it.checked ? '已完成' : '未完成'} className={cx('mt-[5px] flex', it.checked ? 'text-success' : 'text-ink-3')}>
              <Icon icon={it.checked ? SquareCheck : Square} size={16} />
            </span>
          )}
          <div className={cx('min-w-0 flex-1', it.loose ? 'flex flex-col gap-2' : '', it.task && it.checked && 'text-ink-2')}>{blocks(it.tokens.filter((x) => x.type !== 'checkbox'), `${k}.${i}`, true)}</div>
        </li>
      ))}
    </Tag>
  )
}

function tableData(t: Tokens.Table, k: string) {
  const cell = (c: Tokens.TableCell, i: number): TableCell => ({ node: inline(c.tokens, `${k}.${i}`), text: plainText(c.tokens) })
  return { header: t.header.map(cell), rows: t.rows.map((r) => r.map(cell)), align: t.align }
}

function Callout({ kind, body, k }: { kind: string; body: string; k: string }) {
  const a = ALERTS[kind]
  return (
    <div role="note" className={cx('flex gap-2.5 rounded-menu px-3.5 py-3 leading-[1.6]', a.tone)}>
      <span className={cx('flex pt-0.5', a.ink)}>
        <Icon icon={a.icon} size={18} />
      </span>
      <div className="flex min-w-0 flex-1 flex-col text-[14px]">
        <span className={a.ink}>{a.title}</span>
        <div className="flex flex-col gap-1.5 text-ink/85">{blocks(lex(body), k)}</div>
      </div>
    </div>
  )
}

function blocks(tokens: Token[], key: string, tight = false): ReactNode[] {
  const out: ReactNode[] = []
  tokens.forEach((t, i) => {
    const k = `${key}.${i}`
    switch (t.type) {
      case 'space':
      case 'def':
        return
      case 'heading': {
        const h = t as Tokens.Heading
        const cls = h.depth === 1 ? 'text-[20px]' : h.depth === 2 ? 'mt-1 text-[18px]' : 'mt-1 text-[16px]'
        const Tag = h.depth === 1 ? 'h3' : h.depth === 2 ? 'h4' : 'h5'
        out.push(
          <Tag key={k} className={cx('m-0 font-normal leading-[1.4]', cls)}>
            {inline(h.tokens, k)}
          </Tag>,
        )
        return
      }
      case 'paragraph': {
        const p = t as Tokens.Paragraph
        const m = p.text.match(MATH)
        if (m) {
          out.push(<MathBlock key={k} tex={m[1]} />)
          return
        }
        const imgs = onlyImages(p.tokens)
        if (imgs) {
          out.push(<ImageGrid key={k} imgs={imgs} />)
          return
        }
        const steps = flowSteps(plainText(p.tokens))
        if (steps) {
          out.push(<FlowSteps key={k} steps={steps} />)
          return
        }
        out.push(
          <p key={k} className="m-0">
            {inline(p.tokens, k)}
          </p>,
        )
        return
      }
      case 'text': {
        // 緊湊清單裡的文字
        const tt = t as Tokens.Text
        out.push(tight ? <span key={k}>{tt.tokens ? inline(tt.tokens, k) : decodeEntities(tt.text)}</span> : <p key={k} className="m-0">{tt.tokens ? inline(tt.tokens, k) : decodeEntities(tt.text)}</p>)
        return
      }
      case 'code': {
        const c = t as Tokens.Code
        const lang = (c.lang ?? '').trim().toLowerCase()
        if (lang === 'math' || lang === 'latex' || lang === 'tex' || lang === 'katex') out.push(<MathBlock key={k} tex={c.text} />)
        else if (lang === 'mermaid') out.push(<Diagram key={k} code={c.text} />)
        else out.push(<CodeBlock key={k} code={c.text} lang={c.lang} />)
        return
      }
      case 'blockquote': {
        const b = t as Tokens.Blockquote
        const alert = b.text.match(/^\s*\[!(NOTE|TIP|IMPORTANT|WARNING|CAUTION)\][ \t]*\n?/i)
        if (alert) {
          out.push(<Callout key={k} kind={alert[1].toUpperCase()} body={b.text.slice(alert[0].length)} k={k} />)
          return
        }
        out.push(
          <blockquote key={k} className="m-0 flex flex-col gap-2 border-l-[3px] border-hover py-0.5 pl-3.5 text-[14.5px] text-ink/80">
            {blocks(b.tokens, k)}
          </blockquote>,
        )
        return
      }
      case 'list':
        out.push(<ListBlock key={k} t={t as Tokens.List} k={k} />)
        return
      case 'table':
        out.push(<Table key={k} t={tableData(t as Tokens.Table, k)} />)
        return
      case 'hr':
        out.push(<hr key={k} className="m-0 my-0.5 border-0 border-t border-line" />)
        return
      case 'html':
        out.push(
          <p key={k} className="m-0 whitespace-pre-wrap">
            {(t as Tokens.HTML).text}
          </p>,
        )
        return
      default: {
        const any = t as Tokens.Generic
        if (typeof any.raw === 'string' && any.raw.trim())
          out.push(
            <p key={k} className="m-0 whitespace-pre-wrap">
              {any.raw}
            </p>,
          )
      }
    }
  })
  return out
}

/** Markdown 回覆。text 每次改變（串流中）都重新切，已經算過的不重算。 */
export const Markdown = memo(function Markdown({ text, className }: { text: string; className?: string }) {
  const nodes = useMemo(() => blocks(lex(text), 'b'), [text])
  return <div className={cx('flex min-w-0 flex-col gap-3 break-words', className)}>{nodes}</div>
})
