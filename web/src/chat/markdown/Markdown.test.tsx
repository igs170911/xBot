import { act, fireEvent, render, screen, within } from '@testing-library/react'
import type { Tokens } from 'marked'
import { describe, expect, it } from 'vitest'
import { Markdown } from './Markdown'
import { isRich, lex, plainText, toCSV } from './parse'
import { decodeEntities, safeHref, safeImageSrc } from './safe'

describe('safe', () => {
  it('只放行 http(s)、mailto 與錨點', () => {
    expect(safeHref('https://a.tw/x')).toBe('https://a.tw/x')
    expect(safeHref('mailto:a@b.tw')).toBe('mailto:a@b.tw')
    expect(safeHref('#src1')).toBe('#src1')
    expect(safeHref('javascript:alert(1)')).toBeNull()
    expect(safeHref('java\nscript:alert(1)')).toBeNull()
    expect(safeHref(' JaVaScRiPt:alert(1)')).toBeNull()
    expect(safeHref('data:text/html,<b>x')).toBeNull()
    expect(safeHref('vbscript:x')).toBeNull()
    expect(safeHref('/workspace/a.txt')).toBeNull()
    expect(safeHref('//evil.com')).toBeNull()
  })
  it('圖片只放行 http(s) 與點陣 data:', () => {
    expect(safeImageSrc('https://a.tw/p.png')).toBe('https://a.tw/p.png')
    expect(safeImageSrc('data:image/png;base64,AAAA')).toBe('data:image/png;base64,AAAA')
    expect(safeImageSrc('data:image/svg+xml;base64,AAAA')).toBeNull()
    expect(safeImageSrc('javascript:x')).toBeNull()
  })
  it('還原 HTML 字元參照', () => {
    expect(decodeEntities('a &amp; b &lt;c&gt; &#39;d&#x27; &unknown;')).toBe("a & b <c> 'd' &unknown;")
  })
})

describe('Markdown', () => {
  it('HTML 標籤當成文字，不會變成元素', () => {
    const { container } = render(<Markdown text={'hi <script>alert(1)</script> <img src=x onerror=alert(1)>\n\n<div onclick="x">block</div>'} />)
    expect(container.querySelector('script')).toBeNull()
    expect(container.querySelector('img')).toBeNull()
    expect(container.querySelector('div[onclick]')).toBeNull()
    expect(container.textContent).toContain('<script>')
    expect(container.textContent).toContain('<div onclick="x">block</div>')
  })

  it('危險連結不會變成可點的 <a>；安全連結另開分頁', () => {
    render(<Markdown text={'[壞](javascript:alert(1)) 和 [好](https://example.com)'} />)
    expect(screen.getByText('壞').closest('a')).toBeNull()
    const a = screen.getByRole('link', { name: '好' })
    expect(a).toHaveAttribute('href', 'https://example.com')
    expect(a).toHaveAttribute('target', '_blank')
    expect(a.getAttribute('rel')).toContain('noopener')
  })

  it('標題、清單、待辦、引言、行內程式碼、刪除線', () => {
    const { container } = render(<Markdown text={'# 報告\n## 小節\n\n- 一\n- **二**\n\n1. 甲\n2. 乙\n\n- [x] 做完\n- [ ] 還沒\n\n> 引一句\n\n用 `make check` ~~不要~~'} />)
    expect(screen.getByRole('heading', { name: '報告' }).tagName).toBe('H3')
    expect(screen.getByRole('heading', { name: '小節' }).tagName).toBe('H4')
    expect(container.querySelector('ul')).toHaveTextContent('一二')
    expect(container.querySelector('ol')).toHaveTextContent('甲乙')
    const boxes = screen.getAllByRole('checkbox')
    expect(boxes.map((b) => b.getAttribute('aria-checked'))).toEqual(['true', 'false'])
    expect(container.querySelector('blockquote')).toHaveTextContent('引一句')
    expect(container.querySelector('code')).toHaveTextContent('make check')
    expect(container.querySelector('del')).toHaveTextContent('不要')
  })

  it('提示框（> [!NOTE]、> [!WARNING]）', () => {
    render(<Markdown text={'> [!NOTE]\n> 價差 5% 以內先不動\n\n> [!WARNING]\n> 3 項調價等你核定'} />)
    const notes = screen.getAllByRole('note')
    expect(notes[0]).toHaveTextContent('提示價差 5% 以內先不動')
    expect(notes[1]).toHaveTextContent('需要你決定3 項調價等你核定')
  })

  it('程式碼區塊有語言標籤與複製', async () => {
    const writes: string[] = []
    Object.assign(navigator, { clipboard: { writeText: async (t: string) => void writes.push(t) } })
    render(<Markdown text={'```python\ndef f():\n    return 1\n```'} />)
    const block = screen.getByTestId('md-code')
    expect(block).toHaveTextContent('python')
    expect(block.querySelector('pre')).toHaveTextContent('def f():')
    await act(async () => fireEvent.click(within(block).getByRole('button', { name: '複製' })))
    expect(writes).toEqual(['def f():\n    return 1'])
    expect(within(block).getByRole('button', { name: '已複製' })).toBeInTheDocument()
  })

  it('小表格直接顯示；超過 5 列有工具列、先收起、可搜尋', () => {
    const rows = Array.from({ length: 8 }, (_, i) => `| 商品${i + 1} | ${100 + i} |`).join('\n')
    render(<Markdown text={'| 品名 | 價格 |\n|---|--:|\n| 杯 | 590 |\n\n| 商品 | 我們 |\n|---|--:|\n' + rows} />)
    const [small, big] = screen.getAllByTestId('md-table')
    expect(within(small).getAllByRole('row')).toHaveLength(2)
    expect(within(small).queryByRole('button', { name: '下載 CSV' })).toBeNull()
    expect(within(big).getByText('8 列 · 2 欄')).toBeInTheDocument()
    expect(within(big).getAllByRole('row')).toHaveLength(6) // 標題＋5 列
    fireEvent.click(within(big).getByRole('button', { name: '顯示全部' }))
    expect(within(big).getAllByRole('row')).toHaveLength(9)
    fireEvent.change(within(big).getByRole('searchbox', { name: '搜尋表格' }), { target: { value: '商品7' } })
    expect(within(big).getAllByRole('row')).toHaveLength(2)
    expect(within(big).getByText('找到 1 列')).toBeInTheDocument()
    expect(within(big).getByRole('cell', { name: '106' })).toHaveClass('text-right')
  })

  it('CSV：逗號與引號跳脫，含 BOM', () => {
    const cell = (text: string) => ({ node: text, text })
    expect(toCSV({ header: [cell('a'), cell('b')], rows: [[cell('1,2'), cell('say "hi"')]], align: [null, null] })).toBe('﻿a,b\r\n"1,2","say ""hi"""\r\n')
  })

  it('圖片：安全的才顯示；多張變成圖片格', () => {
    const { container } = render(<Markdown text={'![壞](javascript:x)\n\n![甲](https://a.tw/1.png) ![乙](https://a.tw/2.png)'} />)
    expect(container.textContent).toContain('［圖片：壞］')
    const imgs = container.querySelectorAll('img')
    expect(imgs).toHaveLength(2)
    expect(imgs[0]).toHaveAttribute('referrerpolicy', 'no-referrer')
    expect(screen.getByText('2 張圖 · 點一下放大')).toBeInTheDocument()
  })

  it('數學式區塊', () => {
    render(<Markdown text={'$$\nx = min(p_1, p_2) × 1.02\n$$'} />)
    expect(screen.getByRole('math')).toHaveTextContent('x = min(p_1, p_2) × 1.02')
  })

  it('單行換行保留（聊天習慣）', () => {
    const { container } = render(<Markdown text={'第一行\n第二行'} />)
    expect(container.querySelector('br')).not.toBeNull()
  })

  it('isRich／plainText', () => {
    expect(isRich('普通一句話')).toBe(false)
    expect(isRich('# 標題')).toBe(true)
    expect(isRich('看這段\n```js\nx\n```')).toBe(true)
    expect(isRich('| a | b |\n|---|---|\n| 1 | 2 |')).toBe(true)
    const p = lex('**粗** `碼` &amp; [連](https://a.tw)')[0] as Tokens.Paragraph
    expect(plainText(p.tokens)).toBe('粗 碼 & 連')
  })
})

describe('檔案連結', () => {
  it('Bot 寫的三種檔案連結都在旁邊打開那個檔案；其他不明網址還是不能點', async () => {
    const { FileLinkContext, botFileLinker } = await import('./fileLink')
    render(
      <FileLinkContext.Provider value={botFileLinker('b1')}>
        <Markdown text={'[報告](file:///workspace/reports/a.md)、[研究](sandbox:/workspace/reports/%E8%B2%93.md)、[表格](/workspace/bench/t.md)、[壞的](javascript:alert(1))'} />
      </FileLinkContext.Provider>,
    )
    expect(screen.getByRole('link', { name: '報告' })).toHaveAttribute('data-href', '/bots/b1/files?path=%2Fworkspace%2Freports%2Fa.md')
    expect(screen.getByRole('link', { name: '研究' })).toHaveAttribute('data-href', '/bots/b1/files?path=%2Fworkspace%2Freports%2F%E8%B2%93.md')
    // 不是 <a>：iPhone 從連結上開始滑會滑不動
    expect(screen.getByRole('link', { name: '報告' }).tagName).toBe('SPAN')
    expect(screen.getByRole('link', { name: '表格' })).not.toHaveAttribute('target')
    expect(screen.queryByRole('link', { name: '壞的' })).toBeNull()
  })
})
