import { useId } from 'react'
import { useReducedMotion } from './useReducedMotion'
import {
  COLORS,
  DOT_COLORS,
  EYE_INK,
  SAMPLE_IMAGE,
  SHAPES,
  eyePaths,
  resolveColor,
  resolveDot,
  resolveShape,
  resolveState,
  EYE,
  EYE_VARIANTS,
  SHAPE_IDS,
  type EyeVariant,
  type AvatarColor,
  type AvatarDot,
  type AvatarShape,
  type AvatarState,
} from './avatar-data'

export type AvatarProps = {
  /** 形狀（游標是品牌與預設）。也接受舊資料的形狀名稱。 */
  shape?: AvatarShape | (string & {})
  /** 10 色之一，或舊資料的 hex。 */
  color?: AvatarColor | (string & {})
  /** 25 個狀態之一，決定眼睛（6 種）與自動小點。 */
  state?: AvatarState | (string & {})
  /** 平常的眼睛（G03）：只在待命（state 沒給或 idle）時用，其他狀態照狀態換眼睛。 */
  eyes?: EyeVariant | (string & {})
  /** 小點：auto 依狀態；只有 waiting（橘）與 error（紅）會畫出來。 */
  dot?: AvatarDot
  /** 像素大小，預設 40。 */
  size?: number
  /** 圖片網址；有圖片時形狀當裁切框、不疊眼睛。'sample' 用示範圖。 */
  img?: string
  /** 小點外圈顏色，填頭像所在的底色。 */
  ring?: string
  /** 待命與工作中偶爾眨眼，預設開。 */
  blink?: boolean
  /** 有名字時當成圖片讀出來；沒有就隱藏（旁邊通常已有名字）。 */
  label?: string
  className?: string
}

// Bot 頭像：照 design/Avatar.dc.html 一比一移植。
export function Avatar({
  shape,
  color,
  state,
  eyes: restEyes,
  dot,
  size = 40,
  img,
  ring = '#111111',
  blink = true,
  label,
  className,
}: AvatarProps) {
  const uid = useId().replace(/[^a-zA-Z0-9_-]/g, '')
  const small = size < 28
  const fill = COLORS[resolveColor(color)]
  const bodyId = resolveShape(shape)
  const b = SHAPES[bodyId]
  const st = resolveState(state)
  const variant: EyeVariant = st === 'idle' && restEyes && (EYE_VARIANTS as string[]).includes(restEyes) ? (restEyes as EyeVariant) : EYE[st]
  const eyes = eyePaths(variant, b.g, small)
  const picture = img === 'sample' ? SAMPLE_IMAGE : img || ''
  const d = resolveDot(dot, state)
  const dotColor = DOT_COLORS[d]
  const reduced = useReducedMotion()
  const doBlink = !picture && (st === 'idle' || st === 'working') && blink && !reduced
  const k = SHAPE_IDS.indexOf(bodyId)
  const clipId = `av${uid}c`
  const eyeProps = {
    fill: eyes.line ? 'none' : EYE_INK,
    stroke: eyes.line ? EYE_INK : 'none',
    strokeWidth: small ? 2.9 : 2.3,
    strokeLinecap: 'round' as const,
    strokeLinejoin: 'round' as const,
  }

  return (
    <span
      className={className}
      style={{ position: 'relative', display: 'block', width: size, height: size, flex: 'none' }}
      role={label ? 'img' : undefined}
      aria-label={label}
      aria-hidden={label ? undefined : true}
      data-shape={bodyId}
      data-eyes={variant}
      data-dot={dotColor ? d : 'none'}
    >
      <svg viewBox="0 0 44 44" width={size} height={size} aria-hidden="true" style={{ display: 'block', overflow: 'visible' }}>
        {picture ? (
          <>
            <defs>
              <clipPath id={clipId}>
                <path d={b.d} />
              </clipPath>
            </defs>
            <g clipPath={`url(#${clipId})`}>
              <rect x="0" y="0" width="44" height="44" fill="#2F2F2F" />
              <image href={picture} x="0" y="0" width="44" height="44" preserveAspectRatio="xMidYMid slice" />
            </g>
          </>
        ) : (
          <>
            <path d={b.d} fill={fill} data-part="body" />
            <g transform={`translate(${b.x} ${b.y})`}>
              <g>
                {doBlink && (
                  <animateTransform
                    attributeName="transform"
                    type="scale"
                    values="1 1;1 1;1 0.12;1 1"
                    keyTimes="0;0.95;0.975;1"
                    dur={`${4.6 + (k % 5) * 0.45}s`}
                    repeatCount="indefinite"
                  />
                )}
                <path d={eyes.e0} data-part="eye" {...eyeProps} />
                <path d={eyes.e1} data-part="eye" {...eyeProps} />
              </g>
            </g>
          </>
        )}
        {dotColor && (
          <circle
            cx="37"
            cy="37"
            r={small ? 6 : 4.8}
            fill={dotColor}
            style={{ stroke: ring }}
            strokeWidth={small ? 2.6 : 2}
            data-part="dot"
          />
        )}
      </svg>
    </span>
  )
}

const POSE: Record<string, AvatarState> = { lazy: 'idle', sleep: 'sleeping', happy: 'happy', wave: 'excited' }

export type MascotPose = 'lazy' | 'sleep' | 'happy' | 'wave' | 'working' | 'thinking' | 'notifying' | 'alerting'

// 品牌吉祥物＝綠色游標，不帶小點（design/Mochi.dc.html）。
export function Mascot({ pose = 'lazy', size = 48 }: { pose?: MascotPose; size?: number }) {
  return (
    <span role="img" aria-label="XBot 吉祥物" style={{ display: 'block', width: size, height: size, flex: 'none' }}>
      <Avatar color="green" shape="cursor" state={POSE[pose] || pose} dot="none" size={size} />
    </span>
  )
}
