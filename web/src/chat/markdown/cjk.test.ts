import { describe, expect, it } from 'vitest'
import type { Token } from 'marked'
import { lex } from './parse'

// 一段的行內結構，寫成好比對的字串：strong(…)、em(…)，其他照原文。
const shape = (ts: Token[] | undefined): string =>
  (ts ?? [])
    .map((t) => {
      const any = t as Token & { tokens?: Token[] }
      if (t.type === 'strong' || t.type === 'em') return `${t.type}(${shape(any.tokens)})`
      if (any.tokens) return shape(any.tokens)
      return t.raw
    })
    .join('')
const para = (src: string) => shape(lex(src))

describe('中文的粗體、斜體（CommonMark 遇到中文標點會認不出來）', () => {
  it('** 旁邊是中文標點也算粗體', () => {
    expect(para('**結論：**已完成')).toBe('strong(結論：)已完成')
    expect(para('這是**「重點」**喔')).toBe('這是strong(「重點」)喔')
    expect(para('**A 店（最便宜）**比我們低')).toBe('strong(A 店（最便宜）)比我們低')
  })

  it('模型多打一個空格（** 檔名**、**粗體 **）也算', () => {
    expect(para('** threads-方案.md**（定稿）')).toBe('strong(threads-方案.md)（定稿）')
    expect(para('**粗體 **後面')).toBe('strong(粗體)後面')
  })

  it('本來就對的照舊；兩邊都有空格的乘號不算', () => {
    expect(para('先看**酷比內容隊**的報告')).toBe('先看strong(酷比內容隊)的報告')
    expect(para('2 ** 3 ** 4')).toBe('2 ** 3 ** 4')
    expect(para('2 ** 3 等於 8')).toBe('2 ** 3 等於 8')
    expect(para('`a ** b` 和 **粗**')).toBe('`a ** b` 和 strong(粗)')
  })

  it('斜體同樣處理', () => {
    expect(para('這是*「斜體」*喔')).toBe('這是em(「斜體」)喔')
  })

  it('##標題 少了空格也是標題；單個 # 是 hashtag 不動', () => {
    const [h] = lex('##結果\n內容')
    expect(h).toMatchObject({ type: 'heading', depth: 2, text: '結果' })
    expect(lex('#貓咪 #療癒')[0].type).toBe('paragraph')
  })

  it('程式碼區塊裡的 ** 和 ## 不動', () => {
    const [code] = lex('```\n**結論：**x\n##y\n```')
    expect(code).toMatchObject({ type: 'code', text: '**結論：**x\n##y' })
  })
})
