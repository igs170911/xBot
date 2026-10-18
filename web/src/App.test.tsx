import { render, screen } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import App from './App'

afterEach(() => vi.unstubAllGlobals())

it('顯示服務版本', async () => {
  vi.stubGlobal('fetch', async () => new Response(JSON.stringify({ ok: true, version: 'v-test' })))
  render(<App />)
  expect(await screen.findByText('服務正常 · v-test')).toBeInTheDocument()
})
