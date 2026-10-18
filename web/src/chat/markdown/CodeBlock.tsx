import { useEffect, useState, type ReactNode } from 'react'
import { Check, Copy } from 'lucide-react'
import { Icon } from '../../ui/Icon'
import { cx } from '../../ui/cx'
import { useCopy } from '../useCopy'
import { loadHighlighter, normalizeLang, type HNode, type HRoot } from './highlight'

function classOf(n: HNode): string | undefined {
  if (n.type !== 'element') return undefined
  const c = (n as { properties?: { className?: string[] | string } }).properties?.className
  return Array.isArray(c) ? c.join(' ') : c
}

function renderTree(nodes: HNode[] | undefined, key = 'h'): ReactNode[] {
  return (nodes ?? []).map((n, i) => {
    if (n.type === 'text') return (n as { value: string }).value
    if (n.type === 'element') {
      return (
        <span key={`${key}${i}`} className={classOf(n)}>
          {renderTree(n.children, `${key}${i}-`)}
        </span>
      )
    }
    return null
  })
}

// 程式碼區塊（design/C07）：#191919 圓角 12；上方語言標籤與複製（按了變「已複製」）；
// 寬的程式碼在框內橫向捲動。高亮用的語法檔第一次用到時才載入。
export function CodeBlock({ code, lang }: { code: string; lang?: string }) {
  const [copied, copy] = useCopy()
  const name = normalizeLang(lang ?? '')
  const [tree, setTree] = useState<{ code: string; root: HRoot } | null>(null)

  useEffect(() => {
    if (!name || code.length > 100_000) return
    let live = true
    loadHighlighter()
      .then((h) => {
        if (live && h.registered(name)) setTree({ code, root: h.highlight(name, code) })
      })
      .catch(() => {})
    return () => {
      live = false
    }
  }, [code, name])

  const label = (lang ?? '').trim().split(/\s/)[0] || '程式碼'
  return (
    <div className="flex min-w-0 flex-col overflow-hidden rounded-menu bg-inset" data-testid="md-code">
      <div className="flex h-9 flex-none items-center justify-between border-b border-line pl-3 pr-1.5">
        <span className="font-mono text-[12px] text-ink-3">{label}</span>
        <button
          type="button"
          onClick={() => void copy(code)}
          className={cx('relative flex h-7 items-center gap-1.5 rounded-seg bg-transparent px-2.5 text-[12.5px] touch-target hover:bg-control', copied ? 'text-success' : 'text-ink-2')}
        >
          <Icon icon={copied ? Check : Copy} size={14} />
          {copied ? '已複製' : '複製'}
        </button>
      </div>
      <pre className="m-0 overflow-x-auto px-3.5 py-3 font-mono text-[12.5px] leading-[1.7] text-[#e6e6e6]">
        <code className="hl">{tree && tree.code === code ? renderTree(tree.root.children) : code}</code>
      </pre>
    </div>
  )
}
