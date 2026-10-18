import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { Avatar, Mascot } from './Avatar'
import { COLORS, EYE, SHAPES, SHAPE_IDS, COLOR_IDS, eyePaths, eyeVariant, resolveColor, resolveDot, resolveShape } from './avatar-data'

const svgOf = (c: HTMLElement) => c.querySelector('svg')!
const parts = (c: HTMLElement, p: string) => [...c.querySelectorAll(`[data-part="${p}"]`)]

describe('頭像資料（照 design/Avatar.dc.html）', () => {
  it('10 種形狀、10 色、25 個狀態對到 6 種眼睛', () => {
    expect(SHAPE_IDS).toEqual(['cursor', 'blob', 'circle', 'squircle', 'capsule', 'drop', 'shield', 'hexagon', 'diamond', 'star'])
    expect(COLOR_IDS).toHaveLength(10)
    expect(COLORS).toMatchObject({ red: '#e5484d', orange: '#f28a30', blue: '#3f8cff', green: '#46a758', purple: '#8e4ec6' })
    expect(Object.keys(EYE)).toHaveLength(25)
    expect(new Set(Object.values(EYE))).toEqual(new Set(['dot', 'aside', 'happy', 'wide', 'half', 'closed']))
  })

  it('眼睛路徑和畫板算出來的一樣', () => {
    // 圓點眼、一般尺寸（r 2.6，g 5.5）
    expect(eyePaths('dot', 5.5, false)).toEqual({
      e0: 'M-8.1 0 a2.6 2.6 0 1 0 5.2 0 a2.6 2.6 0 1 0 -5.2 0 Z',
      e1: 'M2.9 0 a2.6 2.6 0 1 0 5.2 0 a2.6 2.6 0 1 0 -5.2 0 Z',
      line: false,
    })
    // 小尺寸眼睛放大（r 3.1）
    expect(eyePaths('dot', 5.5, true).e0).toBe('M-8.6 0 a3.1 3.1 0 1 0 6.2 0 a3.1 3.1 0 1 0 -6.2 0 Z')
    expect(eyePaths('aside', 5.5, false).e0).toBe('M-6.4 -0.7 a2.6 2.6 0 1 0 5.2 0 a2.6 2.6 0 1 0 -5.2 0 Z')
    expect(eyePaths('happy', 5.5, false)).toEqual({ e0: 'M-8.2 1.1 Q-5.5 -2.7 -2.8 1.1', e1: 'M2.8 1.1 Q5.5 -2.7 8.2 1.1', line: true })
    expect(eyePaths('closed', 5, false).e1).toBe('M2.3 0.3 Q5 2.4 7.7 0.3')
    expect(eyePaths('half', 5.5, false).e0).toBe('M-8.6 -0.4 A3.1 3.1 0 0 0 -2.4 -0.4 Z')
    expect(eyePaths('wide', 5.5, false).e0).toBe('M-8.9 0 a3.4 3.4 0 1 0 6.8 0 a3.4 3.4 0 1 0 -6.8 0 Z')
  })

  it('舊資料的形狀、顏色、狀態會轉成新的', () => {
    expect(resolveShape('round')).toBe('cursor')
    expect(resolveShape('nope')).toBe('cursor')
    expect(resolveColor('#1084fe')).toBe('blue')
    expect(resolveColor('#123456')).toBe('green')
    expect(eyeVariant('done')).toBe('happy')
    expect(eyeVariant('failed')).toBe('wide')
  })

  it('只有等你（橘）與出錯（紅）會自動有小點', () => {
    expect(resolveDot('auto', 'notifying')).toBe('waiting')
    expect(resolveDot('auto', 'alerting')).toBe('error')
    expect(resolveDot('auto', 'working')).toBe('none')
    expect(resolveDot('none', 'notifying')).toBe('none')
  })
})

describe('<Avatar>', () => {
  it('平面剪影＋兩顆 #141416 眼睛，沒有嘴巴、漸層', () => {
    const { container } = render(<Avatar shape="hexagon" color="yellow" state="idle" size={44} />)
    const body = parts(container, 'body')
    expect(body).toHaveLength(1)
    expect(body[0].getAttribute('d')).toBe(SHAPES.hexagon.d)
    expect(body[0].getAttribute('fill')).toBe('#f2c230')
    const eyes = parts(container, 'eye')
    expect(eyes).toHaveLength(2)
    eyes.forEach((e) => expect(e.getAttribute('fill')).toBe('#141416'))
    expect(container.querySelector('linearGradient, radialGradient')).toBeNull()
    expect(container.querySelectorAll('path')).toHaveLength(3)
    // 眼睛中心照形狀的 x / y
    expect(container.querySelector('g')!.getAttribute('transform')).toBe('translate(22 21)')
  })

  it('開心、閉眼是線條眼睛', () => {
    const { container } = render(<Avatar state="happy" />)
    const eye = parts(container, 'eye')[0]
    expect(eye.getAttribute('fill')).toBe('none')
    expect(eye.getAttribute('stroke')).toBe('#141416')
    expect(container.firstElementChild!.getAttribute('data-eyes')).toBe('happy')
  })

  it('等你有橘點、出錯有紅點，外圈填底色', () => {
    const { container, rerender } = render(<Avatar state="notifying" ring="#262626" />)
    let dot = parts(container, 'dot')[0]
    expect(dot.getAttribute('fill')).toBe('#FF9800')
    expect((dot as SVGElement).style.stroke).toBe('rgb(38, 38, 38)')
    rerender(<Avatar state="alerting" />)
    dot = parts(container, 'dot')[0]
    expect(dot.getAttribute('fill')).toBe('#FF5667')
    rerender(<Avatar state="idle" dot="queued" />)
    expect(parts(container, 'dot')).toHaveLength(0)
  })

  it('待命與工作中會眨眼，其他狀態不眨', () => {
    const { container, rerender } = render(<Avatar state="working" />)
    expect(container.querySelector('animateTransform')).not.toBeNull()
    rerender(<Avatar state="happy" />)
    expect(container.querySelector('animateTransform')).toBeNull()
    rerender(<Avatar state="idle" blink={false} />)
    expect(container.querySelector('animateTransform')).toBeNull()
  })

  it('改用圖片：形狀當裁切框，不疊眼睛', () => {
    const { container } = render(<Avatar shape="star" img="https://example.com/a.png" dot="waiting" />)
    expect(parts(container, 'eye')).toHaveLength(0)
    const clip = container.querySelector('clipPath')!
    expect(clip.querySelector('path')!.getAttribute('d')).toBe(SHAPES.star.d)
    const g = container.querySelector('g[clip-path]')!
    expect(g.getAttribute('clip-path')).toBe(`url(#${clip.id})`)
    expect(container.querySelector('image')!.getAttribute('href')).toBe('https://example.com/a.png')
    expect(parts(container, 'dot')).toHaveLength(1)
  })

  it('同一頁多個圖片頭像，裁切框 id 不重複', () => {
    const { container } = render(
      <>
        <Avatar img="sample" />
        <Avatar img="sample" />
      </>,
    )
    const ids = [...container.querySelectorAll('clipPath')].map((c) => c.id)
    expect(new Set(ids).size).toBe(2)
  })

  it('尺寸：16 px 也用放大的眼睛與小點', () => {
    const { container } = render(<Avatar size={16} state="notifying" />)
    expect(svgOf(container).getAttribute('width')).toBe('16')
    expect(parts(container, 'eye')[0].getAttribute('stroke-width')).toBe('2.9')
    expect(parts(container, 'dot')[0].getAttribute('r')).toBe('6')
  })

  it('預設隱藏給輔助技術；有 label 時是圖片', () => {
    render(<Avatar label="小秘" />)
    expect(screen.getByRole('img', { name: '小秘' })).toBeInTheDocument()
  })

  it('品牌吉祥物是綠色游標、沒有小點', () => {
    const { container } = render(<Mascot pose="sleep" />)
    expect(screen.getByRole('img', { name: 'XBot 吉祥物' })).toBeInTheDocument()
    expect(parts(container, 'body')[0].getAttribute('fill')).toBe('#46a758')
    expect(parts(container, 'body')[0].getAttribute('d')).toBe(SHAPES.cursor.d)
    expect(container.querySelector('[data-eyes]')!.getAttribute('data-eyes')).toBe('closed')
    expect(parts(container, 'dot')).toHaveLength(0)
  })
})
