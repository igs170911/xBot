import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { expect, it, vi } from 'vitest'
import { ChatComposer } from './ChatComposer'

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
