import { describe, expect, it } from 'vitest'
import { splitBubbles } from './bubbles'

describe('splitBubbles：一則回覆拆成幾個泡泡', () => {
  it('空行分開的段落各自一個泡泡', () => {
    expect(splitBubbles('比價表做好了，在 /workspace/比價.xlsx。\n\nA 店最便宜。\n\n要照 A 店調價嗎？')).toEqual([
      '比價表做好了，在 /workspace/比價.xlsx。',
      'A 店最便宜。',
      '要照 A 店調價嗎？',
    ])
  })

  it('沒有空行就是一個泡泡（單純換行不拆）', () => {
    expect(splitBubbles('好了\n放在桌面')).toEqual(['好了\n放在桌面'])
    expect(splitBubbles('好')).toEqual(['好'])
  })

  it('程式碼、表格、清單裡的空行不拆', () => {
    const code = '```py\na = 1\n\nb = 2\n```'
    expect(splitBubbles(`程式改好了。\n\n${code}`)).toEqual(['程式改好了。', code])
    const list = '- 第一項\n\n- 第二項'
    expect(splitBubbles(list)).toEqual([list])
    const table = '| 店 | 價錢 |\n| --- | --- |\n| A | 120 |'
    expect(splitBubbles(`好了。\n\n${table}`)).toEqual(['好了。', table])
  })

  it('標題跟著下一段；冒號結尾的引言跟著後面的清單', () => {
    expect(splitBubbles('## 結果\n\nA 店最便宜。\n\n要調價嗎？')).toEqual(['## 結果\n\nA 店最便宜。', '要調價嗎？'])
    expect(splitBubbles('重點：\n\n- A 店最便宜\n- B 店缺貨\n\n要調價嗎？')).toEqual(['重點：\n\n- A 店最便宜\n- B 店缺貨', '要調價嗎？'])
  })

  it('串流中寫到一半也能拆（最後一段還在長）', () => {
    expect(splitBubbles('好了。\n\nA 店最')).toEqual(['好了。', 'A 店最'])
  })

  it('空字串原樣回傳', () => {
    expect(splitBubbles('')).toEqual([''])
  })
})
