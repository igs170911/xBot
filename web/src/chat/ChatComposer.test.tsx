import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { expect, it, vi } from 'vitest'
import { ChatComposer } from './ChatComposer'
import { expandSkill, slashQuery } from './SkillMenu'
import { fakeApi } from '../test-api'

const box = () => screen.getByRole('textbox')

it('空白時送出鈕停用；有字才能送，送出成功後清空', async () => {
  const onSend = vi.fn(async () => true)
  render(<ChatComposer name="小秘" mode="idle" onSend={onSend} onStop={() => {}} />)
  expect(box()).toHaveAttribute('placeholder', '交代小秘…')
  expect(screen.getByRole('button', { name: '送出' })).toBeDisabled()
  fireEvent.change(box(), { target: { value: '  幫我比價  ' } })
  expect(screen.getByRole('button', { name: '送出' })).toBeEnabled()
  fireEvent.click(screen.getByRole('button', { name: '送出' }))
  await waitFor(() => expect(box()).toHaveValue(''))
  expect(onSend).toHaveBeenCalledWith('幫我比價')
})

it('Enter 送出；Shift+Enter 換行；輸入法選字中的 Enter 不送', async () => {
  const onSend = vi.fn(async () => true)
  render(<ChatComposer name="小秘" mode="idle" onSend={onSend} onStop={() => {}} />)
  fireEvent.change(box(), { target: { value: '你好' } })
  fireEvent.keyDown(box(), { key: 'Enter', shiftKey: true })
  fireEvent.keyDown(box(), { key: 'Enter', isComposing: true })
  fireEvent.keyDown(box(), { key: 'Enter', keyCode: 229 })
  expect(onSend).not.toHaveBeenCalled()
  fireEvent.keyDown(box(), { key: 'Enter' })
  await waitFor(() => expect(onSend).toHaveBeenCalledWith('你好'))
})

it('送不出去時保留文字', async () => {
  const onSend = vi.fn(async () => false)
  render(<ChatComposer name="小秘" mode="idle" onSend={onSend} onStop={() => {}} />)
  fireEvent.change(box(), { target: { value: '你好' } })
  fireEvent.keyDown(box(), { key: 'Enter' })
  await waitFor(() => expect(onSend).toHaveBeenCalled())
  expect(box()).toHaveValue('你好')
})

it('工作中：右側是停止鈕，提示補充說明；按下呼叫 onStop', () => {
  const onStop = vi.fn()
  render(<ChatComposer name="小秘" mode="working" onSend={async () => true} onStop={onStop} />)
  expect(box()).toHaveAttribute('placeholder', '補充說明給小秘…')
  expect(screen.queryByRole('button', { name: '送出' })).toBeNull()
  expect(screen.getByText(/小秘工作中/)).toBeInTheDocument()
  fireEvent.click(screen.getByRole('button', { name: '停止小秘' }))
  expect(onStop).toHaveBeenCalled()
})

it('正在停止：停止鈕變成停用的轉圈', () => {
  render(<ChatComposer name="小秘" mode="stopping" onSend={async () => true} onStop={() => {}} />)
  expect(screen.getByRole('button', { name: '正在停止' })).toBeDisabled()
  expect(box()).toHaveAttribute('placeholder', '正在停止小秘…')
})

// ── 附件（C05「8 · 附件」）────────────────────────────

const pick = (files: File[]) => fireEvent.change(screen.getByTestId('attach-input'), { target: { files } })

it('沒給 onUpload 時沒有附檔按鈕', () => {
  render(<ChatComposer name="小秘" mode="idle" onSend={async () => true} onStop={() => {}} />)
  expect(screen.queryByRole('button', { name: '附加檔案' })).toBeNull()
})

it('選檔：先上傳到 uploads，送出時把路徑附在訊息後面；只有附件也能送', async () => {
  const onSend = vi.fn(async () => true)
  const onUpload = vi.fn(async (f: File) => ({ name: f.name, path: `/workspace/uploads/${f.name}`, size: f.size }))
  render(<ChatComposer name="小秘" mode="idle" onSend={onSend} onStop={() => {}} onUpload={onUpload} />)
  expect(screen.getByRole('button', { name: '附加檔案' })).toBeEnabled()
  pick([new File(['a,b'], '10月訂單.csv', { type: 'text/csv' }), new File(['%PDF'], '報價單.pdf', { type: 'application/pdf' })])
  expect(await screen.findByText('10月訂單.csv')).toBeInTheDocument()
  await waitFor(() => expect(screen.getByRole('button', { name: '送出' })).toBeEnabled())
  expect(onUpload).toHaveBeenCalledTimes(2)
  expect(screen.getByText(/2 個 ·/)).toBeInTheDocument()

  fireEvent.click(screen.getByRole('button', { name: '移除 報價單.pdf' }))
  fireEvent.change(box(), { target: { value: '幫我整理' } })
  fireEvent.click(screen.getByRole('button', { name: '送出' }))
  await waitFor(() => expect(onSend).toHaveBeenCalledWith('幫我整理\n\n附件：/workspace/uploads/10月訂單.csv'))
  await waitFor(() => expect(screen.queryByTestId('attachments')).toBeNull())

  pick([new File(['x'], 'a.txt')])
  await waitFor(() => expect(screen.getByRole('button', { name: '送出' })).toBeEnabled())
  fireEvent.click(screen.getByRole('button', { name: '送出' }))
  await waitFor(() => expect(onSend).toHaveBeenLastCalledWith('附件：/workspace/uploads/a.txt'))
})

it('上傳中不能送出；上傳失敗顯示原因、不附在訊息裡', async () => {
  type Up = { name: string; path: string; size: number }
  let finish: (v: Up) => void = () => {}
  const onUpload = vi.fn((f: File) => (f.name === 'bad.bin' ? Promise.reject(new Error('檔案太大')) : new Promise<Up>((r) => (finish = r))))
  const onSend = vi.fn(async () => true)
  render(<ChatComposer name="小秘" mode="idle" onSend={onSend} onStop={() => {}} onUpload={onUpload} />)
  pick([new File(['x'], 'slow.txt'), new File(['x'], 'bad.bin')])
  fireEvent.change(box(), { target: { value: '看一下' } })
  expect(await screen.findByText('上傳中…')).toBeInTheDocument()
  expect(screen.getByRole('button', { name: '送出' })).toBeDisabled()
  expect(await screen.findByText(/沒有傳上去：檔案太大/)).toBeInTheDocument()
  finish({ name: 'slow.txt', path: '/workspace/uploads/slow.txt', size: 1 })
  await waitFor(() => expect(screen.getByRole('button', { name: '送出' })).toBeEnabled())
  fireEvent.click(screen.getByRole('button', { name: '送出' }))
  await waitFor(() => expect(onSend).toHaveBeenCalledWith('看一下\n\n附件：/workspace/uploads/slow.txt'))
})

it('貼上截圖：補上檔名並上傳；貼文字照常', async () => {
  const onUpload = vi.fn(async (f: File) => ({ name: f.name, path: `/workspace/uploads/${f.name}`, size: f.size }))
  render(<ChatComposer name="小秘" mode="idle" onSend={async () => true} onStop={() => {}} onUpload={onUpload} />)
  fireEvent.paste(box(), { clipboardData: { files: [new File(['png'], 'image.png', { type: 'image/png' })] } })
  await waitFor(() => expect(onUpload).toHaveBeenCalledTimes(1))
  expect(onUpload.mock.calls[0][0].name).toMatch(/^貼上的圖片-\d{8}-\d{6}\.png$/)
  fireEvent.paste(box(), { clipboardData: { files: [] } })
  expect(onUpload).toHaveBeenCalledTimes(1)
})

it('一則最多 10 個附件', async () => {
  const onUpload = vi.fn(async (f: File) => ({ name: f.name, path: `/workspace/uploads/${f.name}`, size: 1 }))
  render(<ChatComposer name="小秘" mode="idle" onSend={async () => true} onStop={() => {}} onUpload={onUpload} />)
  pick(Array.from({ length: 12 }, (_, i) => new File(['x'], `f${i}.txt`)))
  expect(await screen.findByText(/最多附 10 個檔案，多的沒有加進來/)).toBeInTheDocument()
  expect(onUpload).toHaveBeenCalledTimes(10)
  expect(screen.getByRole('button', { name: '附加檔案' })).toBeDisabled()
})

it('打「/」列出這個 Bot 的技能；選了送出時改寫成「使用技能」', async () => {
  const api = fakeApi({
    'skills.list': () => ({
      skills: [
        { id: 'price', name: '比價', description: '比三家價格', place: 'list', enabled: true, source: 'custom', created_at: 1, updated_at: 1 },
        { id: 'news', name: '新聞摘要', description: '整理今天新聞', place: 'library', enabled: true, source: 'custom', created_at: 1, updated_at: 1 },
        { id: 'old', name: '舊的', description: '', place: 'off', enabled: false, source: 'custom', created_at: 1, updated_at: 1 },
      ],
      counts: {},
      computer: true,
    }),
  })
  const onSend = vi.fn(async () => true)
  render(<ChatComposer name="小秘" mode="idle" onSend={onSend} onStop={() => {}} botId="b1" />)
  await waitFor(() => expect(api.calls.some((c) => c.path === 'skills.list')).toBe(true))
  fireEvent.change(box(), { target: { value: '/' } })
  const menu = await screen.findByRole('listbox', { name: '技能' })
  // 內建的 /plan 在最前面，再來是技能清單與技能庫（停用的不列）
  expect(within(menu).getAllByRole('option').map((o) => o.textContent?.split(' ')[0])).toHaveLength(3)
  expect(within(menu).getAllByRole('option')[0]).toHaveTextContent('/plan')
  expect(within(menu).getByText('技能庫')).toBeInTheDocument()
  fireEvent.change(box(), { target: { value: '/新聞' } })
  fireEvent.keyDown(box(), { key: 'Enter' })
  expect(box()).toHaveValue('/新聞摘要 ')
  expect(onSend).not.toHaveBeenCalled()
  fireEvent.change(box(), { target: { value: '/新聞摘要 只要科技類' } })
  fireEvent.keyDown(box(), { key: 'Enter' })
  await waitFor(() => expect(onSend).toHaveBeenCalledWith('使用技能「新聞摘要」：只要科技類'))
})

it('「/」開頭但不是技能名稱：原樣送出', () => {
  expect(expandSkill('/tmp 裡有什麼', [])).toBe('/tmp 裡有什麼')
  expect(slashQuery('/ab')).toBe('ab')
  expect(slashQuery('看 /ab')).toBeNull()
  expect(slashQuery('/ab c')).toBeNull()
})

it('/plan：先規劃，送出的是「先拆成計畫給我確認、確認前不動手不交辦」', async () => {
  const onSend = vi.fn(async (t: string) => t.length > 0)
  render(<ChatComposer name="小秘" mode="idle" onSend={onSend} onStop={() => {}} />)
  fireEvent.change(box(), { target: { value: '/plan 幫我經營 Threads 帳號' } })
  fireEvent.keyDown(box(), { key: 'Enter' })
  await waitFor(() => expect(onSend).toHaveBeenCalled())
  const sent = onSend.mock.calls[0][0]
  expect(sent).toContain('【先規劃，先不要動手】')
  expect(sent).toContain('不要交辦或傳話給隊友')
  expect(sent).toContain('要規劃的事：幫我經營 Threads 帳號')
  expect(expandSkill('/計畫 做網站', [])).toContain('要規劃的事：做網站')
})
