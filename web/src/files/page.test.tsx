import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { FileTiles } from '../chat/Attachments'
import { fakeApi } from '../test-api'
import { isPage, PAGE_CSP, PAGE_SANDBOX, pageDoc } from './page'

afterEach(() => vi.unstubAllGlobals())

it('網頁：.html、.htm 才算', () => {
  expect(isPage('/workspace/儀表板.html')).toBe(true)
  expect(isPage('report.HTM')).toBe(true)
  expect(isPage('page.html.md')).toBe(false)
  expect(isPage('style.css')).toBe(false)
})

it('CSP 放在網頁最前面（任何程式之前），只准連 https，不准連本機的 XBot', () => {
  const withDoctype = pageDoc('<!DOCTYPE html><html><head><script>x()</script></head><body></body></html>')
  expect(withDoctype.startsWith('<!DOCTYPE html><meta http-equiv="Content-Security-Policy"')).toBe(true)
  const scriptFirst = pageDoc('<script>fetch("http://127.0.0.1:8780/api/v1/bots.list")</script><p>hi</p>')
  expect(scriptFirst.indexOf('Content-Security-Policy')).toBeLessThan(scriptFirst.indexOf('<script>'))
  expect(PAGE_CSP).toContain('connect-src https:')
  expect(PAGE_CSP).not.toContain("'self'")
  expect(PAGE_CSP).toContain("form-action 'none'")
})

it('沙盒不和 XBot 同源、不能換掉整個頁面', () => {
  expect(PAGE_SANDBOX).toContain('allow-scripts')
  expect(PAGE_SANDBOX).not.toContain('allow-same-origin')
  expect(PAGE_SANDBOX).not.toContain('allow-top-navigation')
})

it('Bot 回覆裡的網頁在對話裡直接顯示（沙盒＋CSP）；太大的請下載；你上傳的 HTML 還是檔案卡', async () => {
  const api = fakeApi({
    'files.read': (input) =>
      input.path === '/workspace/儀表板.html'
        ? { name: '儀表板.html', path: input.path, kind: 'code', size: 40, modified: 1, text: '<h1>營收</h1><canvas id="c"></canvas>', truncated: false, download_url: '/d' }
        : { name: '大.html', path: input.path, kind: 'code', size: 2 << 20, modified: 1, text: '<h1>', truncated: true, download_url: '/d' },
  })
  render(<FileTiles botId="b1" paths={['/workspace/儀表板.html', '/workspace/大.html']} />)
  const frame = await screen.findByTitle('儀表板.html')
  expect(frame.tagName).toBe('IFRAME')
  expect(frame.getAttribute('sandbox')).toBe(PAGE_SANDBOX)
  expect(frame.getAttribute('srcdoc')).toContain('Content-Security-Policy')
  expect(frame.getAttribute('srcdoc')).toContain('<h1>營收</h1>')
  expect(await screen.findByText('網頁超過 1 MB，對話裡放不下，請下載後開啟。')).toBeInTheDocument()
  await waitFor(() => expect(api.calls.filter((c) => c.path === 'files.read').every((c) => c.input.max_bytes === 1 << 20)).toBe(true))
  render(<FileTiles botId="b1" paths={['/workspace/uploads/上傳.html']} mine />)
  expect(screen.queryByTitle('上傳.html')).toBeNull()
})

it('觸控螢幕：對話裡的網頁只是預覽（手指滑過去捲的是對話），點一下放大', async () => {
  vi.stubGlobal('matchMedia', (q: string) => ({ matches: q === '(pointer: coarse)', addEventListener() {}, removeEventListener() {} }))
  fakeApi({ 'files.read': (input) => ({ name: '儀表板.html', path: input.path, kind: 'code', size: 40, modified: 1, text: '<h1>營收</h1>', truncated: false, download_url: '/d' }) })
  render(<FileTiles botId="b1" paths={['/workspace/儀表板.html']} />)
  const frame = await screen.findByTitle('儀表板.html')
  expect(frame).toHaveClass('pointer-events-none')
  fireEvent.click(screen.getByTestId('page-preview'))
  expect(await screen.findAllByTitle('儀表板.html')).toHaveLength(2)
})
