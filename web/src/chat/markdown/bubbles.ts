import type { Token } from 'marked'
import { lex } from './parse'

// 一則 Bot 回覆拆成幾個對話泡泡（2026-10-20：像人傳訊息一樣一段一則，見 hermes 的回話風格）。
// 照 Markdown 的區塊切：空行分開的段落各自一個泡泡；表格、程式碼、清單、引言本來就是一整塊，不會被切開。
// 標題跟著下一段，「重點：」這種冒號結尾的句子跟著後面的清單／表格。

/** 冒號結尾：在介紹下一塊（清單、表格），不要拆開。 */
const INTRO = /[:：]\s*$/

export function splitBubbles(src: string): string[] {
  const text = src.trim()
  if (!text) return [src]
  let tokens: Token[]
  try {
    tokens = lex(text)
  } catch {
    return [src]
  }
  const out: string[] = []
  let cur = ''
  let hold = false // 目前這塊只有標題或冒號結尾的引言：遇到空行也先不切
  for (const t of tokens) {
    if (t.type === 'space') {
      if (cur.trim() && !hold) {
        out.push(cur.trim())
        cur = ''
      } else if (cur) {
        cur += t.raw
      }
      continue
    }
    if (t.type === 'def') {
      cur += t.raw // 參考式連結的定義跟著目前這塊
      continue
    }
    cur += t.raw
    hold = t.type === 'heading' || (t.type === 'paragraph' && INTRO.test(t.raw))
  }
  if (cur.trim()) out.push(cur.trim())
  return out.length ? out : [src]
}
