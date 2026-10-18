import { act, fireEvent, render, screen } from '@testing-library/react'
import { useState } from 'react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { Plus } from 'lucide-react'
import { Button, IconButton } from './Button'
import { Checkbox, SegmentedControl, Switch } from './Controls'
import { ConfirmDialog, Dialog } from './Dialog'
import { TextInput } from './Field'
import { MenuButton, Select } from './Menu'
import { ToastProvider } from './Toast'
import { useToast } from './toast-context'

afterEach(() => vi.useRealTimers())

describe('按鈕', () => {
  it('是真的 button，預設 type=button；處理中會停用', () => {
    const onClick = vi.fn()
    const { rerender } = render(<Button onClick={onClick}>允許這次</Button>)
    const b = screen.getByRole('button', { name: '允許這次' })
    expect(b).toHaveAttribute('type', 'button')
    fireEvent.click(b)
    expect(onClick).toHaveBeenCalledOnce()
    rerender(<Button loading>允許這次</Button>)
    expect(screen.getByRole('button', { name: '允許這次' })).toBeDisabled()
  })

  it('圖示按鈕用 label 當名字，圖示線寬 1.75', () => {
    const { container } = render(<IconButton icon={Plus} label="新增" />)
    expect(screen.getByRole('button', { name: '新增' })).toBeInTheDocument()
    expect(container.querySelector('svg')!.getAttribute('stroke-width')).toBe('1.75')
    expect(container.querySelector('svg')!.getAttribute('aria-hidden')).toBe('true')
  })
})

describe('輸入框', () => {
  it('標籤綁定輸入框，錯誤訊息掛在 aria-describedby', () => {
    render(<TextInput label="名字" error="太短了" />)
    const input = screen.getByLabelText('名字')
    expect(input).toHaveAttribute('aria-invalid', 'true')
    expect(input).toHaveAccessibleDescription('太短了')
  })
})

describe('開關、勾選、分段', () => {
  function Demo() {
    const [on, setOn] = useState(false)
    const [ck, setCk] = useState(false)
    const [v, setV] = useState<'a' | 'b' | 'c'>('a')
    return (
      <>
        <Switch checked={on} onChange={setOn} label="通知" />
        <Checkbox checked={ck} onChange={setCk}>
          付款
        </Checkbox>
        <SegmentedControl
          label="在哪裡跑"
          value={v}
          onChange={setV}
          options={[
            { value: 'a', label: '這台主機' },
            { value: 'b', label: '雲端' },
            { value: 'c', label: '關閉' },
          ]}
        />
      </>
    )
  }

  it('開關是 role=switch，點了切換 aria-checked', () => {
    render(<Demo />)
    const sw = screen.getByRole('switch', { name: '通知' })
    expect(sw).toHaveAttribute('aria-checked', 'false')
    fireEvent.click(sw)
    expect(sw).toHaveAttribute('aria-checked', 'true')
  })

  it('勾選框底下是真的 checkbox', () => {
    render(<Demo />)
    const cb = screen.getByRole('checkbox', { name: '付款' })
    fireEvent.click(cb)
    expect(cb).toBeChecked()
  })

  it('分段控制用方向鍵切換，只有選中的能 Tab 到', () => {
    render(<Demo />)
    const a = screen.getByRole('radio', { name: '這台主機' })
    expect(a).toHaveAttribute('aria-checked', 'true')
    expect(screen.getByRole('radio', { name: '雲端' })).toHaveAttribute('tabindex', '-1')
    a.focus()
    fireEvent.keyDown(a, { key: 'ArrowRight' })
    const b = screen.getByRole('radio', { name: '雲端' })
    expect(b).toHaveAttribute('aria-checked', 'true')
    expect(b).toHaveFocus()
    fireEvent.keyDown(b, { key: 'ArrowLeft' })
    fireEvent.keyDown(screen.getByRole('radio', { name: '這台主機' }), { key: 'ArrowLeft' })
    expect(screen.getByRole('radio', { name: '關閉' })).toHaveAttribute('aria-checked', 'true')
  })
})

describe('對話框', () => {
  function Demo() {
    const [open, setOpen] = useState(false)
    return (
      <>
        <button type="button" onClick={() => setOpen(true)}>
          打開
        </button>
        <Dialog
          open={open}
          onClose={() => setOpen(false)}
          title="取名字"
          description="名字會出現在側欄"
          actions={
            <>
              <Button onClick={() => setOpen(false)}>取消</Button>
              <Button variant="primary">儲存</Button>
            </>
          }
        >
          <TextInput label="名字" />
        </Dialog>
      </>
    )
  }

  it('開啟時焦點進來、Tab 在裡面循環、Esc 關閉並把焦點還回去', () => {
    render(<Demo />)
    const opener = screen.getByRole('button', { name: '打開' })
    opener.focus()
    fireEvent.click(opener)
    const dialog = screen.getByRole('dialog', { name: '取名字' })
    expect(dialog).toHaveAttribute('aria-modal', 'true')
    expect(dialog).toHaveAccessibleDescription('名字會出現在側欄')
    const input = screen.getByLabelText('名字')
    expect(input).toHaveFocus()

    const save = screen.getByRole('button', { name: '儲存' })
    save.focus()
    fireEvent.keyDown(save, { key: 'Tab' })
    expect(input).toHaveFocus()
    fireEvent.keyDown(input, { key: 'Tab', shiftKey: true })
    expect(save).toHaveFocus()

    fireEvent.keyDown(save, { key: 'Escape' })
    expect(screen.queryByRole('dialog')).toBeNull()
    expect(opener).toHaveFocus()
  })

  it('確認對話框：處理中時按鈕顯示「處理中…」、取消停用、Esc 不會關', () => {
    const onCancel = vi.fn()
    render(<ConfirmDialog open danger busy title="刪除？" description="不能復原" confirmText="刪除" onConfirm={() => {}} onCancel={onCancel} />)
    expect(screen.getByRole('alertdialog', { name: '刪除？' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '處理中…' })).toBeDisabled()
    expect(screen.getByRole('button', { name: '取消' })).toBeDisabled()
    fireEvent.keyDown(screen.getByRole('alertdialog'), { key: 'Escape' })
    expect(onCancel).not.toHaveBeenCalled()
  })
})

describe('選單與下拉', () => {
  it('選單按鈕：打開後焦點在第一項，方向鍵移動，Esc 關閉並回到按鈕', () => {
    const pin = vi.fn()
    render(
      <MenuButton
        label="小秘"
        items={[
          { label: '釘選', onSelect: pin },
          { label: '標為未讀' },
        ]}
        trigger={(p) => <Button {...p}>更多</Button>}
      />,
    )
    const btn = screen.getByRole('button', { name: '更多' })
    fireEvent.click(btn)
    expect(btn).toHaveAttribute('aria-expanded', 'true')
    const first = screen.getByRole('menuitem', { name: '釘選' })
    expect(first).toHaveFocus()
    fireEvent.keyDown(first, { key: 'ArrowDown' })
    expect(screen.getByRole('menuitem', { name: '標為未讀' })).toHaveFocus()
    fireEvent.keyDown(screen.getByRole('menuitem', { name: '標為未讀' }), { key: 'Escape' })
    expect(screen.queryByRole('menu')).toBeNull()
    expect(btn).toHaveFocus()
    fireEvent.click(btn)
    fireEvent.click(screen.getByRole('menuitem', { name: '釘選' }))
    expect(pin).toHaveBeenCalledOnce()
  })

  it('下拉選單：listbox 選一項', () => {
    function Demo() {
      const [v, setV] = useState('a')
      return (
        <Select
          label="模型"
          value={v}
          onChange={setV}
          options={[
            { value: 'a', label: 'Sonnet' },
            { value: 'b', label: 'Opus' },
          ]}
        />
      )
    }
    render(<Demo />)
    const btn = screen.getByRole('button', { name: '模型 Sonnet' })
    fireEvent.click(btn)
    expect(screen.getByRole('option', { name: 'Sonnet' })).toHaveAttribute('aria-selected', 'true')
    fireEvent.keyDown(screen.getByRole('option', { name: 'Sonnet' }), { key: 'ArrowDown' })
    fireEvent.keyDown(screen.getByRole('option', { name: 'Opus' }), { key: 'Enter' })
    expect(screen.queryByRole('listbox')).toBeNull()
    expect(screen.getByRole('button', { name: '模型 Opus' })).toHaveFocus()
  })
})

describe('Toast', () => {
  function Demo() {
    const t = useToast()
    return (
      <>
        <button type="button" onClick={() => t.show({ kind: 'done', text: '完成了' })}>
          一般
        </button>
        <button type="button" onClick={() => t.show({ kind: 'decide', text: '等你允許', action: { label: '允許這次', onClick: () => {} } })}>
          決定
        </button>
      </>
    )
  }

  it('一般提示 4 秒後消失；要你決定的不會自己消失；最多同時 3 則', () => {
    vi.useFakeTimers()
    render(
      <ToastProvider>
        <Demo />
      </ToastProvider>,
    )
    fireEvent.click(screen.getByRole('button', { name: '一般' }))
    fireEvent.click(screen.getByRole('button', { name: '決定' }))
    expect(screen.getByText('完成了')).toBeInTheDocument()
    act(() => vi.advanceTimersByTime(4100))
    expect(screen.queryByText('完成了')).toBeNull()
    expect(screen.getByText('等你允許')).toBeInTheDocument()

    for (let i = 0; i < 4; i++) fireEvent.click(screen.getByRole('button', { name: '一般' }))
    expect(screen.getAllByRole('status')).toHaveLength(3)
    fireEvent.click(screen.getAllByRole('button', { name: '關閉提示' })[0])
    expect(screen.queryByText('等你允許')).toBeNull()
    expect(screen.getAllByRole('status')).toHaveLength(3)
  })
})
