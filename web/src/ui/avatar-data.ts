// Bot 頭像的資料與眼睛邏輯，逐字移植自 design/Avatar.dc.html（畫板是權威來源，改這裡要先改畫板）。
// 平面、只有眼睛：一個實色剪影＋兩顆 #141416 眼睛，沒有嘴巴、漸層、外框。

export const SHAPES = {
  cursor: { d: 'M5.1 10 C3.8 16.69 3.91 23.36 5.41 30 C5.46 33.72 7.23 34.81 10.06 32.88 C10.88 32.02 11.84 31.37 12.94 30.91 C14.22 30.04 15.49 30.31 16.3 31.64 C17.09 33.87 18.3 35.85 19.92 37.58 C21.21 39.7 23.39 40.38 25.66 39.38 C26.27 39.24 26.83 38.99 27.34 38.62 C29.61 37.62 30.2 35.71 28.89 33.6 C28.22 31.73 27.19 30.07 25.82 28.63 C25 27.31 25.45 26.41 26.99 26.26 C29.12 25.59 31.31 25.38 33.53 25.63 C37.54 25.24 37.9 23.71 34.46 21.6 C27.52 15.02 19.54 10.13 10.54 6.9 C7.1 4.79 5.04 5.97 5.1 10 Z', x: 17.5, y: 20, g: 5.5 },
  blob: { d: 'M13 14 C 14 8.5, 20 6.5, 24 9.5 C 27 6, 34 7, 36 12 C 40 13, 42 18, 40.5 22.5 C 42 28, 39 33.5, 33.5 35 C 28 38.5, 16 38.5, 10.5 34.5 C 5.5 31, 4.5 24, 7 19 C 8 16, 10 14.5, 13 14 Z', x: 23.5, y: 22.5, g: 5.5 },
  circle: { d: 'M22 5 C 32.5 5, 39.5 12, 39.5 22 C 39.5 32, 32.5 39, 22 39 C 11.5 39, 4.5 32, 4.5 22 C 4.5 12, 11.5 5, 22 5 Z', x: 22, y: 20.5, g: 5.5 },
  squircle: { d: 'M14 6.5 L 30 6.5 C 35.5 6.5, 38.5 9.5, 38.5 15 L 38.5 29 C 38.5 34.5, 35.5 37.5, 30 37.5 L 14 37.5 C 8.5 37.5, 5.5 34.5, 5.5 29 L 5.5 15 C 5.5 9.5, 8.5 6.5, 14 6.5 Z', x: 22, y: 20.5, g: 5.5 },
  capsule: { d: 'M16 9 C 18.5 8.3, 26 8.1, 28.5 8.6 C 35.5 9.6, 40 15, 40 22 C 40 29.5, 35 35, 28 35.4 C 24 35.8, 19.5 35.8, 15.5 35.3 C 8.5 34.6, 4 29, 4 22 C 4 15, 9 9.8, 16 9 Z', x: 22, y: 21, g: 5.5 },
  drop: { d: 'M22 4.5 C 24.5 4.5, 28 9, 32 14 C 36 18.5, 38.5 23, 38.5 27 C 38.5 35, 31.5 40, 22 40 C 12.5 40, 5.5 35, 5.5 27 C 5.5 23, 8 18.5, 12 14 C 16 9, 19.5 4.5, 22 4.5 Z', x: 22, y: 26, g: 5.5 },
  shield: { d: 'M22 5 C 26 5, 33 6.5, 36.5 8.5 C 38.5 9.6, 39 11, 39 14 C 39 22.5, 36 30.5, 29 36 C 26.5 38, 24 39.5, 22 39.5 C 20 39.5, 17.5 38, 15 36 C 8 30.5, 5 22.5, 5 14 C 5 11, 5.5 9.6, 7.5 8.5 C 11 6.5, 18 5, 22 5 Z', x: 22, y: 19.5, g: 5.5 },
  hexagon: { d: 'M38.75 26.76 C37.66 29.41 36.25 31.86 34.5 34.12 C32.8 37.08 29.66 38.89 26.25 38.89 C23.42 39.27 20.58 39.27 17.75 38.89 C14.34 38.89 11.21 37.08 9.5 34.12 C7.75 31.86 6.34 29.41 5.25 26.76 C3.55 23.81 3.54 20.19 5.25 17.24 C6.34 14.59 7.75 12.14 9.5 9.88 C11.2 6.92 14.34 5.11 17.75 5.11 C20.58 4.73 23.42 4.73 26.25 5.11 C29.66 5.11 32.8 6.92 34.5 9.88 C36.25 12.14 37.66 14.59 38.75 17.24 C40.45 20.19 40.45 23.81 38.75 26.76 Z', x: 22, y: 21, g: 5.5 },
  diamond: { d: 'M28.36 8.86 C31.16 10.58 33.42 12.84 35.14 15.64 C39.08 19.58 39.08 24.42 35.14 28.36 C33.42 31.16 31.16 33.42 28.36 35.14 C24.42 39.08 19.58 39.08 15.64 35.14 C12.84 33.42 10.58 31.16 8.86 28.36 C4.92 24.42 4.92 19.58 8.86 15.64 C10.58 12.84 12.84 10.58 15.64 8.86 C19.58 4.92 24.42 4.92 28.36 8.86 Z', x: 22, y: 21.5, g: 5.5 },
  star: { d: 'M25.18 7.49 C25.89 8.5 26.61 9.52 27.33 10.53 C28.58 12.3 30.62 13.78 32.69 14.43 C33.87 14.8 35.06 15.16 36.24 15.53 C39.5 16.54 40.25 18.84 38.21 21.57 C37.47 22.57 36.72 23.56 35.98 24.56 C34.68 26.3 33.91 28.69 33.93 30.86 C33.95 32.1 33.96 33.34 33.98 34.59 C34.02 38 32.07 39.41 28.84 38.32 C27.67 37.92 26.49 37.52 25.31 37.12 C23.26 36.43 20.74 36.43 18.69 37.12 C17.51 37.52 16.33 37.92 15.16 38.32 C11.93 39.41 9.98 38 10.02 34.59 C10.04 33.34 10.05 32.1 10.07 30.86 C10.09 28.69 9.32 26.3 8.02 24.56 C7.28 23.56 6.53 22.57 5.79 21.57 C3.75 18.84 4.5 16.54 7.76 15.53 C8.94 15.16 10.13 14.8 11.31 14.43 C13.38 13.78 15.42 12.3 16.67 10.53 C17.39 9.52 18.11 8.5 18.82 7.49 C20.79 4.71 23.21 4.71 25.18 7.49 Z', x: 22, y: 23.5, g: 5 },
} as const

export type AvatarShape = keyof typeof SHAPES
export const SHAPE_IDS = Object.keys(SHAPES) as AvatarShape[]
export const SHAPE_LABELS: Record<AvatarShape, string> = {
  cursor: '游標', blob: '果凍', circle: '圓', squircle: '圓角', capsule: '膠囊',
  drop: '水滴', shield: '盾牌', hexagon: '六角', diamond: '菱形', star: '星星',
}

// Grok Bot 的 5 個原色＋同飽和度的 5 色。
export const COLORS = {
  green: '#46a758', blue: '#3f8cff', red: '#e5484d', orange: '#f28a30', purple: '#8e4ec6',
  cyan: '#2ab0d4', pink: '#e5579a', yellow: '#f2c230', teal: '#22a68f', coral: '#f0694f',
} as const

export type AvatarColor = keyof typeof COLORS
export const COLOR_IDS = Object.keys(COLORS) as AvatarColor[]
export const COLOR_LABELS: Record<AvatarColor, string> = {
  green: '綠', blue: '藍', red: '紅', orange: '橘', purple: '紫',
  cyan: '青', pink: '粉紅', yellow: '黃', teal: '藍綠', coral: '珊瑚',
}

export type EyeVariant = 'dot' | 'closed' | 'happy' | 'wide' | 'aside' | 'half'
export const EYE_VARIANTS: EyeVariant[] = ['dot', 'aside', 'happy', 'wide', 'half', 'closed']

// 25 個狀態 → 6 種眼睛。
export const EYE = {
  idle: 'dot', listening: 'dot', notifying: 'dot', sleeping: 'closed',
  happy: 'happy', excited: 'happy', celebrate: 'happy', laughing: 'happy', proud: 'happy', playful: 'happy', shy: 'happy',
  surprised: 'wide', scared: 'wide', alerting: 'wide',
  working: 'aside', searching: 'aside', thinking: 'aside', curious: 'aside', confused: 'aside',
  drowsy: 'half', bored: 'half', waking: 'half', suspicious: 'half', sad: 'half', angry: 'half',
} as const satisfies Record<string, EyeVariant>

export type AvatarState = keyof typeof EYE
export type AvatarDot = 'auto' | 'none' | 'waiting' | 'error' | 'queued' | 'teammate'

// 舊資料（OpenMausBot 匯入）的對照表，照畫板保留。
const LEGACY_COLOR: Record<string, AvatarColor> = { '#1084FE': 'blue', '#FF6700': 'orange', '#FF309B': 'pink', '#00C972': 'green', '#FF9800': 'yellow', '#9159FE': 'purple', '#777777': 'cyan', '#FF263C': 'red', '#00BCA6': 'teal', '#936439': 'coral', '#F1F2F2': 'cyan', '#7C5CFF': 'purple', '#16181D': 'green', '#2B44E0': 'blue', '#0E7C66': 'teal', '#B4235A': 'pink', '#7C3AED': 'purple', '#A16207': 'yellow', '#475569': 'cyan' }
const LEGACY_SHAPE: Record<string, AvatarShape> = { round: 'cursor', organic: 'blob', cloud: 'circle', boxy: 'squircle', hexagon: 'hexagon', triangle: 'diamond', sun: 'star', nub: 'shield', capsule: 'capsule', droplet: 'drop' }
const LEGACY_MOOD: Record<string, AvatarState> = { idle: 'idle', ack: 'surprised', working: 'working', waiting: 'notifying', done: 'happy', failed: 'alerting', error: 'alerting', sleepy: 'sleeping', wink: 'playful', happy: 'happy', sad: 'sad', mad: 'angry', surprised: 'surprised', smug: 'proud', unsure: 'confused', scared: 'scared', love: 'shy', shy: 'shy', sick: 'bored', thinking: 'thinking', queued: 'idle', none: 'idle' }
const MOOD_DOT: Record<string, AvatarDot> = { waiting: 'waiting', failed: 'error', error: 'error', queued: 'queued', notifying: 'waiting', alerting: 'error' }
// 只有「等你」（橘）與「出錯」（紅）有小點；queued、teammate 不畫。
export const DOT_COLORS: Partial<Record<AvatarDot, string>> = { waiting: '#FF9800', error: '#FF5667' }

export const EYE_INK = '#141416'

export function resolveColor(color: string | undefined): AvatarColor {
  let c = color || 'green'
  if (c[0] === '#') c = LEGACY_COLOR[c.toUpperCase()] || 'green'
  return c in COLORS ? (c as AvatarColor) : 'green'
}

export function resolveShape(shape: string | undefined): AvatarShape {
  if (shape && shape in SHAPES) return shape as AvatarShape
  return (shape && LEGACY_SHAPE[shape]) || 'cursor'
}

export function resolveState(state: string | undefined): AvatarState {
  const raw = state || 'idle'
  return raw in EYE ? (raw as AvatarState) : LEGACY_MOOD[raw] || 'idle'
}

export function eyeVariant(state: string | undefined): EyeVariant {
  return EYE[resolveState(state)]
}

export function resolveDot(dot: AvatarDot | undefined, state: string | undefined): AvatarDot {
  const d = dot || 'auto'
  if (d !== 'auto') return d
  const raw = state || 'idle'
  return MOOD_DOT[raw] || MOOD_DOT[resolveState(raw)] || 'none'
}

const n = (x: number) => Math.round(x * 100) / 100
const dotPath = (x: number, y: number, r: number) =>
  `M${n(x - r)} ${n(y)} a${r} ${r} 0 1 0 ${n(2 * r)} 0 a${r} ${r} 0 1 0 ${n(-2 * r)} 0 Z`

export type Eyes = { e0: string; e1: string; line: boolean }

// 兩顆眼睛的路徑（以眼睛中心為原點）。small＝小於 28 px，眼睛略放大。
export function eyePaths(variant: EyeVariant, gap: number, small: boolean): Eyes {
  const g = gap
  const r = small ? 3.1 : 2.6
  switch (variant) {
    case 'wide': {
      const R = small ? 3.7 : 3.4
      return { e0: dotPath(-g, 0, R), e1: dotPath(g, 0, R), line: false }
    }
    case 'aside':
      return { e0: dotPath(-g + 1.7, -0.7, r), e1: dotPath(g + 1.7, -0.7, r), line: false }
    case 'half': {
      const r2 = small ? 3.4 : 3.1
      const H = (x: number) => `M${n(x - r2)} -0.4 A${r2} ${r2} 0 0 0 ${n(x + r2)} -0.4 Z`
      return { e0: H(-g), e1: H(g), line: false }
    }
    case 'happy': {
      const A = (x: number) => `M${n(x - 2.7)} 1.1 Q${n(x)} -2.7 ${n(x + 2.7)} 1.1`
      return { e0: A(-g), e1: A(g), line: true }
    }
    case 'closed': {
      const C = (x: number) => `M${n(x - 2.7)} 0.3 Q${n(x)} 2.4 ${n(x + 2.7)} 0.3`
      return { e0: C(-g), e1: C(g), line: true }
    }
    default:
      return { e0: dotPath(-g, 0, r), e1: dotPath(g, 0, r), line: false }
  }
}

// 示範用的圖片（畫板的 img="sample"）。
export const SAMPLE_IMAGE =
  'data:image/svg+xml;utf8,' +
  encodeURIComponent(
    "<svg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 84 84'><rect width='84' height='84' fill='#F2B37A'/><rect width='84' height='46' fill='#F7CFA0'/><circle cx='66' cy='18' r='9' fill='#FFF1D6'/><path d='M0 52 L18 36 L32 46 L50 30 L84 54 L84 84 L0 84 Z' fill='#C9845A'/><path d='M18 84 C18 66 28 60 42 60 C56 60 66 66 66 84 Z' fill='#2E4A6B'/><rect x='37' y='50' width='10' height='12' rx='4' fill='#B97A55'/><ellipse cx='42' cy='41' rx='12' ry='14' fill='#E3A57E'/><path d='M29 39 C29 26 36 23 42 23 C50 23 56 28 55 40 C52 33 46 31 42 31 C36 31 32 34 29 39 Z' fill='#3A2A22'/></svg>",
  )
