import type { ReactNode } from 'react'
import { Lexer, Tokenizer, type Token, type Tokens } from 'marked'
import { decodeEntities } from './safe'

// Markdown 的解析與純文字工具（不含畫面）。

// 粗體、斜體的備援規則（2026-10-20：畫面上一直露出 **）。CommonMark 的規則是給英文訂的：** 旁邊是中文標點（「」（）：）
// 又貼著中文字時不算粗體，例：**結論：**已完成、這是**「重點」**喔；模型也常寫成 ** 檔名**。標準規則認不出來時才用這裡的：
// 同一行裡成對的 ** 或 *，裡面至少一個字；兩邊都空一格的（2 ** 3 ** 4）不算。
const STRONG = /^\*\*(?!\*)([ \t]*)([^\n]*?[^\s*])([ \t]*)\*\*/
const EM = /^\*(?![*\s])([^\n*]*?[^\s*])\*(?!\*)/
// 標題少了空格（##標題）：兩個以上的 # 才算，單個 # 是 hashtag（#貓咪）
const BARE_HEADING = /^ {0,3}(#{2,6})(?=[^\s#])([^\n]*)(?:\n+|$)/

class CjkTokenizer extends Tokenizer {
  override emStrong(src: string, maskedSrc: string, prevChar = ''): Tokens.Em | Tokens.Strong | undefined {
    const std = super.emStrong(src, maskedSrc, prevChar)
    if (std) return std
    const strong = STRONG.exec(src)
    if (strong && !(strong[1] && strong[3])) {
      const text = strong[2]
      return { type: 'strong', raw: strong[0], text, tokens: this.lexer.inlineTokens(text) }
    }
    const em = EM.exec(src)
    if (em) return { type: 'em', raw: em[0], text: em[1], tokens: this.lexer.inlineTokens(em[1]) }
    return undefined
  }

  override heading(src: string): Tokens.Heading | undefined {
    const std = super.heading(src)
    if (std) return std
    const m = BARE_HEADING.exec(src)
    if (!m) return undefined
    const text = m[2].replace(/\s+#+\s*$/, '').trim()
    return { type: 'heading', raw: m[0], depth: m[1].length, text, tokens: this.lexer.inline(text) }
  }
}

export function lex(src: string): Token[] {
  return new Lexer({ gfm: true, breaks: true, tokenizer: new CjkTokenizer() }).lex(src)
}

/** 把行內語法單位轉成純文字（給 CSV、搜尋、複製用）。 */
export function plainText(tokens: Token[] | undefined): string {
  return (tokens ?? [])
    .map((t) => {
      const any = t as Tokens.Generic
      if (any.tokens) return plainText(any.tokens)
      if (t.type === 'br') return '\n'
      if (t.type === 'codespan') return (t as Tokens.Codespan).text
      return decodeEntities((any.text as string | undefined) ?? '')
    })
    .join('')
}

/** 這段文字有沒有用到「大」的排版（表格、程式碼、標題…）：有的話回覆泡泡放寬。 */
export function isRich(src: string): boolean {
  return /(^|\n)\s*(#{1,6}\s|```|~~~|\|.*\|\s*\n\s*\|?\s*:?-{2,}|\$\$|!\[)/.test(src)
}

export type TableCell = { node: ReactNode; text: string }
export type TableData = {
  header: TableCell[]
  rows: TableCell[][]
  align: Array<'left' | 'right' | 'center' | null>
}

/** 表格超過這麼多列先收起（design/C07：「表格超過 5 列先收起」）。 */
export const TABLE_PREVIEW_ROWS = 5

function csvField(s: string): string {
  return /[",\n\r]/.test(s) ? `"${s.replace(/"/g, '""')}"` : s
}

/** RFC 4180 CSV；開頭加 BOM，Excel 才認得 UTF-8 中文。 */
export function toCSV(t: TableData): string {
  const lines = [t.header.map((c) => csvField(c.text)), ...t.rows.map((r) => r.map((c) => csvField(c.text)))]
  return '\ufeff' + lines.map((l) => l.join(',')).join('\r\n') + '\r\n'
}

