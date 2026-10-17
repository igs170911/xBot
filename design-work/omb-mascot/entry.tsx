import React from 'react'
import { createRoot } from 'react-dom/client'
import { CursorAvatar, POOLS } from './src/components/CursorAvatar'
import { MASCOT_BODIES } from './shared/mascot-bodies'
const MAUS_COLORS = { green: "#009957", blue: "#377FE6", red: "#D94B52", orange: "#E78531", purple: "#8057C8", cyan: "#0EA5C6", pink: "#D84F8B", yellow: "#D8A729", teal: "#01A492", coral: "#E5634E" }
const mix = (a, b, t) => { const n = parseInt(a.slice(1), 16), m = parseInt(b.slice(1), 16); const c = (k) => Math.round(((n >> k) & 255) * (1 - t) + ((m >> k) & 255) * t); return '#' + [16, 8, 0].map((k) => c(k).toString(16).padStart(2, '0')).join('') }
const grad = (c) => [mix(c, '#ffffff', 0.55), c, mix(c, '#000000', 0.42)]
;(window as any).__POOLS = POOLS
;(window as any).__BODIES = MASCOT_BODIES
const states = Object.keys(POOLS)
function App() {
  return <div>{states.map((st) => (
    <span key={st} data-state={st} style={{ display: 'inline-block' }}>
      <CursorAvatar state={st as any} size={120} motion={0} effects={false} glyphs={false} autoBlink={false} autoExpression={false} expression={(POOLS as any)[st][0]} gradient={grad(MAUS_COLORS.green)} />
    </span>))}</div>
}
createRoot(document.getElementById('root')!).render(<App />)
