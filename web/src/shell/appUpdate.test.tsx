import { afterEach, beforeEach, expect, test, vi } from 'vitest'
import { render, screen } from '@testing-library/react'

const health = vi.hoisted(() => ({ version: 'v1' }))
vi.mock('../health', () => ({ fetchHealth: async () => ({ ok: true, version: health.version }) }))

import { checkForUpdate, reloader, updateStore, UpdateBanner } from './appUpdate'

let reloads = 0
beforeEach(() => {
  reloads = 0
  reloader.reload = () => {
    reloads++
  }
  updateStore.set(false)
})
afterEach(() => {
  document.body.innerHTML = ''
})

// 服務更新後開著的網頁要換成新版（2026-10-19：手機上修好的東西一直沒變）
test('版本沒變不動；換版了、沒有打到一半的字就直接重新載入', async () => {
  health.version = 'v1'
  await checkForUpdate() // 記下開網頁時的版本
  await checkForUpdate()
  expect(reloads).toBe(0)
  health.version = 'v2'
  await checkForUpdate()
  expect(reloads).toBe(1)
})

test('有字打到一半：不自動載入，顯示「重新載入」', async () => {
  health.version = 'v3' // 開網頁時是 v1（上一個測試記下的）
  const box = document.createElement('textarea')
  box.value = '還沒送出的話'
  document.body.appendChild(box)
  Object.defineProperty(box, 'offsetParent', { get: () => document.body })
  await checkForUpdate()
  expect(reloads).toBe(0)
  render(<UpdateBanner />)
  expect(screen.getByRole('button', { name: '重新載入' })).toBeInTheDocument()
})
