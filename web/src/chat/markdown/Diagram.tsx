import { useEffect, useState } from 'react'
import { ArrowRight, Check, Code, Copy, Workflow } from 'lucide-react'
import { resolveTheme, useTheme } from '../../settings/theme'
import { Icon } from '../../ui/Icon'
import { cx } from '../../ui/cx'
import { useCopy } from '../useCopy'
import { CodeBlock } from './CodeBlock'

// 流程圖（design/C07：「流程圖用分開的方塊與箭頭，可切換原始碼」）。
// ```mermaid 區塊畫成圖：mermaid 很大，第一次遇到流程圖時才載入；securityLevel strict（不能點、標籤會清掉 HTML），
// 標籤用 SVG 文字（htmlLabels: false），不放 HTML。畫不出來（語法錯、串流還沒寫完）就顯示原始碼。

type Mermaid = typeof import('mermaid').default
let loading: Promise<Mermaid> | null = null
const loadMermaid = () => (loading ??= import('mermaid').then((m) => m.default))
let seq = 0

/** 串流中每來一個字就重畫太浪費：停一下才畫。 */
const SETTLE_MS = 200

export function Diagram({ code }: { code: string }) {
  const theme = resolveTheme(useTheme())
  const [drawn, setDrawn] = useState<{ code: string; theme: string; svg: string } | null>(null)
  const [bad, setBad] = useState<string | null>(null)
  const [source, setSource] = useState(false)
  const [copied, copy] = useCopy()

  useEffect(() => {
    let live = true
    const timer = setTimeout(() => {
      loadMermaid()
        .then(async (m) => {
          m.initialize({
            startOnLoad: false,
            securityLevel: 'strict',
            theme: theme === 'dark' ? 'dark' : 'default',
            fontFamily: getComputedStyle(document.body).fontFamily,
            flowchart: { htmlLabels: false },
          })
          if (!(await m.parse(code, { suppressErrors: true }))) throw new Error('流程圖語法有誤')
          const { svg } = await m.render(`xbot-diagram-${++seq}`, code)
          if (live) setDrawn({ code, theme, svg })
        })
        .catch(() => {
          if (live) setBad(code)
        })
    }, SETTLE_MS)
    return () => {
      live = false
      clearTimeout(timer)
    }
  }, [code, theme])

  const ready = drawn && drawn.code === code && drawn.theme === theme
  if (bad === code && !ready) return <CodeBlock code={code} lang="mermaid" />
  return (
    <div className="flex min-w-0 flex-col overflow-hidden rounded-menu bg-inset" data-testid="md-diagram">
      <div className="flex h-9 flex-none items-center gap-1 border-b border-line pl-3 pr-1.5">
        <Icon icon={Workflow} size={14} className="text-ink-3" />
        <span className="flex-1 text-[12px] text-ink-3">流程圖</span>
        <button
          type="button"
          aria-pressed={source}
          onClick={() => setSource((s) => !s)}
          className="relative flex h-7 items-center gap-1.5 rounded-seg bg-transparent px-2.5 text-[12.5px] text-ink-2 touch-target hover:bg-control"
        >
          <Icon icon={source ? Workflow : Code} size={14} />
          {source ? '看圖' : '原始碼'}
        </button>
        <button
          type="button"
          onClick={() => void copy(code)}
          className={cx('relative flex h-7 items-center gap-1.5 rounded-seg bg-transparent px-2.5 text-[12.5px] touch-target hover:bg-control', copied ? 'text-success' : 'text-ink-2')}
        >
          <Icon icon={copied ? Check : Copy} size={14} />
          {copied ? '已複製' : '複製'}
        </button>
      </div>
      {source || !ready ? (
        <pre tabIndex={0} className="m-0 overflow-x-auto px-3.5 py-3 font-mono text-[12.5px] leading-[1.7]" aria-busy={!ready || undefined}>
          <code>{code}</code>
        </pre>
      ) : (
        // mermaid（strict）產生的 SVG：標籤已清掉 HTML、不能點，直接放進來才用得到畫面的字型
        <div role="img" aria-label="流程圖" className="overflow-x-auto px-3 py-3 [&_svg]:mx-auto [&_svg]:h-auto [&_svg]:max-w-full" dangerouslySetInnerHTML={{ __html: drawn.svg }} />
      )}
    </div>
  )
}

/** 一行只有「A → B → C」的步驟（parse.ts flowSteps）：畫成方塊加箭頭。 */
export function FlowSteps({ steps }: { steps: string[] }) {
  return (
    <ol aria-label="流程" data-testid="md-flow" className="m-0 flex list-none flex-wrap items-center gap-x-1.5 gap-y-2 p-0">
      {steps.map((s, i) => (
        <li key={i} className="flex items-center gap-1.5">
          {i > 0 && <Icon icon={ArrowRight} size={16} className="flex-none text-ink-3" />}
          <span className="rounded-seg border border-line bg-black/15 px-2.5 py-1 text-[14px] leading-[1.45]">{s}</span>
        </li>
      ))}
    </ol>
  )
}
