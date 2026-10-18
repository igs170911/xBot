import type { ReactNode } from 'react'
import { Lexer, type Token, type Tokens } from 'marked'
import { decodeEntities } from './safe'

// Markdown 的解析與純文字工具（不含畫面）。

export function lex(src: string): Token[] {
  return new Lexer({ gfm: true, breaks: true }).lex(src)
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

