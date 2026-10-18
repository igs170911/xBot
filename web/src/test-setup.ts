import '@testing-library/jest-dom/vitest'
import { cleanup } from '@testing-library/react'
import { afterEach } from 'vitest'

// vitest 沒開 globals，Testing Library 不會自己清畫面。
afterEach(() => cleanup())
