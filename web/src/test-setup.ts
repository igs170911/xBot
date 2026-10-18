import '@testing-library/jest-dom/vitest'
import { cleanup, configure } from '@testing-library/react'
import { afterEach } from 'vitest'

// vitest 沒開 globals，Testing Library 不會自己清畫面。
afterEach(() => cleanup())

// findBy／waitFor 是「等到出現為止」，不是固定等：預設 1 秒在機器很忙時不夠
// （第一次掛上整個 App、跑完 fetch 再畫出來，負載下會超過 1 秒），放寬到 5 秒。順利時一樣馬上往下走。
configure({ asyncUtilTimeout: 5_000 })
