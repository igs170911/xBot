import { expect, it } from 'vitest'
import { filesFrom, hasFiles, withAttachments } from './attachments'

it('送出的文字：你打的字＋每個附件一行路徑', () => {
  expect(withAttachments(' 你好 ', [])).toBe('你好')
  expect(withAttachments('看這兩個', ['/workspace/uploads/a.png', '/workspace/uploads/b.pdf'])).toBe('看這兩個\n\n附件：/workspace/uploads/a.png\n附件：/workspace/uploads/b.pdf')
  expect(withAttachments('  ', ['/workspace/uploads/a.png'])).toBe('附件：/workspace/uploads/a.png')
})

it('貼上的圖片沒有檔名時補一個（含時間），有檔名的保留', () => {
  const now = new Date(2026, 9, 19, 9, 5, 7).getTime()
  const out = filesFrom([new File(['x'], 'image.png', { type: 'image/png' }), new File(['y'], '', { type: 'image/jpeg' }), new File(['z'], '報價單.pdf')], now)
  expect(out.map((f) => f.name)).toEqual(['貼上的圖片-20261019-090507.png', '貼上的圖片-20261019-090507-2.jpg', '報價單.pdf'])
  expect(filesFrom(null)).toEqual([])
})

it('只有拖檔案進來才算（拖文字不算）', () => {
  expect(hasFiles({ types: ['Files'] } as unknown as DataTransfer)).toBe(true)
  expect(hasFiles({ types: ['text/plain'] } as unknown as DataTransfer)).toBe(false)
  expect(hasFiles(null)).toBe(false)
})
