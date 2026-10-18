import fs from 'node:fs'
import http from 'node:http'
import path from 'node:path'
import { expect, test, type Page, type Route } from '@playwright/test'
import { MockServer, settle, type Bot } from './mock'

// E32 語音：K02 設定聲音、C05 語音輸入、VC-1 自動朗讀（語音訊息）、K01／L18 通話（說話→辨識→交辦→念回覆→用說的核定→掛斷）。
// 不打真的供應商：瀏覽器層的假服務，或（最後一個測試）真的服務＋假的 OpenAI 相容語音供應商。
// 麥克風用 Chromium 的假裝置：一段像說話的聲音（1 秒）接 2.5 秒安靜，一直重複。
const shot = (name: string) => `e2e-results/screens/${name}.png`

// ── 假的聲音檔 ───────────────────────────────────────

function wav(segments: Array<{ ms: number; speech: boolean }>, rate = 16000): Buffer {
  const total = segments.reduce((n, s) => n + Math.round((s.ms / 1000) * rate), 0)
  const buf = Buffer.alloc(44 + total * 2)
  buf.write('RIFF', 0)
  buf.writeUInt32LE(36 + total * 2, 4)
  buf.write('WAVEfmt ', 8)
  buf.writeUInt32LE(16, 16)
  buf.writeUInt16LE(1, 20)
  buf.writeUInt16LE(1, 22)
  buf.writeUInt32LE(rate, 24)
  buf.writeUInt32LE(rate * 2, 28)
  buf.writeUInt16LE(2, 32)
  buf.writeUInt16LE(16, 34)
  buf.write('data', 36)
  buf.writeUInt32LE(total * 2, 40)
  let i = 0
  for (const s of segments) {
    const n = Math.round((s.ms / 1000) * rate)
    for (let k = 0; k < n; k++, i++) {
      let v = 0
      if (s.speech) {
        const t = k / rate
        const f0 = 170 + 30 * Math.sin(2 * Math.PI * 1.3 * t)
        for (let h = 1; h <= 6; h++) v += Math.sin(2 * Math.PI * f0 * h * t) / h
        v *= 0.35 * (0.35 + 0.65 * Math.abs(Math.sin(2 * Math.PI * 3 * t)))
      }
      buf.writeInt16LE(Math.max(-32767, Math.min(32767, Math.round(v * 32767))), 44 + i * 2)
    }
  }
  return buf
}

const dir = path.resolve('e2e-results')
fs.mkdirSync(dir, { recursive: true })
const micFile = path.join(dir, 'fake-mic.wav')
fs.writeFileSync(micFile, wav([{ ms: 1000, speech: true }, { ms: 2500, speech: false }]))
const replyWav = wav([{ ms: 500, speech: true }])

test.use({
  permissions: ['microphone'],
  launchOptions: { args: ['--use-fake-ui-for-media-stream', '--use-fake-device-for-media-stream', `--use-file-for-fake-audio-capture=${micFile}`, '--autoplay-policy=no-user-gesture-required'] },
})

test.beforeEach(async ({ page }, info) => {
  if (info.project.name === 'desktop') await page.setViewportSize({ width: 1440, height: 960 })
})

// ── 假服務的語音操作 ─────────────────────────────────

const VOICES = [
  { id: 'qing', name: '晴', desc: '溫和的女聲 · 推薦給幕僚長', upstream: 'coral' },
  { id: 'an', name: '安', desc: '沉穩的男聲', upstream: 'onyx' },
  { id: 'kui', name: '小葵', desc: '活潑的女聲', upstream: 'nova' },
  { id: 'jie', name: '阿杰', desc: '爽朗的男聲', upstream: 'ash' },
  { id: 'ya', name: '雅', desc: '像新聞主播，咬字清楚', upstream: 'sage' },
  { id: 'system', name: '系統內建', desc: '免費，聽起來比較機械' },
]

type VoiceMock = { bot: Record<string, unknown>; transcripts: string[]; uploads: Array<{ type: string; size: number }> }

async function voiceMocks(page: Page, api: MockServer, bot: Bot, opts: { autoRead?: boolean; transcripts?: string[] } = {}): Promise<VoiceMock> {
  const v: VoiceMock = {
    bot: { bot_id: bot.id, voice: 'qing', speed: 1, auto_read: !!opts.autoRead, voice_name: '晴', engine: 'proxy', auto_read_now: !!opts.autoRead },
    transcripts: opts.transcripts ?? ['幫我比較三家的價格'],
    uploads: [],
  }
  const settings = {
    settings: { tts: { provider: 'openai', model: 'gpt-4o-mini-tts' }, stt: { provider: 'openai', model: 'gpt-4o-mini-transcribe' }, hangup_minutes: 5 },
    voices: VOICES,
    engines: [
      { provider: '', label: '瀏覽器內建', usable: true, note: '' },
      { provider: 'openai', label: 'OpenAI', usable: true, note: '' },
    ],
    tts_ready: true,
    stt_ready: true,
    limits: { max_seconds: 120, upload_bytes: 10 << 20, speak_chars: 4096, speed_min: 0.5, speed_max: 2 },
    lang: 'zh-TW',
  }
  let n = 0
  Object.assign(api.override, {
    'voice.settings_get': () => settings,
    'voice.settings_set': (i: Record<string, unknown>) => Object.assign(settings.settings, i) && settings,
    'voice.bot_get': () => v.bot,
    'voice.bot_set': (i: Record<string, unknown>) => {
      Object.assign(v.bot, i)
      v.bot.voice_name = VOICES.find((x) => x.id === v.bot.voice)?.name
      return v.bot
    },
    'voice.thread_set': (i: Record<string, unknown>) => ({ ...v.bot, auto_read_now: i.auto_read === 'on' }),
    'voice.speak': (i: Record<string, unknown>) => {
      n++
      return { engine: 'proxy', audio_id: `a${n}`, url: `/api/v1/voice.audio?id=a${n}`, content_type: 'audio/wav', bytes: replyWav.length, text: String(i.text ?? '回覆'), voice: i.voice ?? 'qing', voice_name: '晴', speed: 1, lang: 'zh-TW', message_id: i.message_id }
    },
    'voice.call_end': (i: Record<string, unknown>) => ({ message_id: 'call1', text: `通話 ${i.duration_s} 秒` }),
  })
  // 內容不走 JSON 的兩個出口（後登記的路由優先）
  await page.route('**/api/v1/voice.audio**', (r: Route) => r.fulfill({ status: 200, contentType: 'audio/wav', body: replyWav }))
  await page.route('**/api/v1/voice.transcribe', (r: Route) => {
    v.uploads.push({ type: r.request().headers()['content-type'] ?? '', size: r.request().postDataBuffer()?.length ?? 0 })
    api.calls.push({ op: 'voice.transcribe', input: {} })
    const text = v.transcripts.shift() ?? ''
    return r.fulfill({ json: { text, provider: 'openai', model: 'gpt-4o-mini-transcribe', bytes: 1000 } })
  })
  return v
}

function seedBot(api: MockServer) {
  const mi = api.addBot({ name: '小秘', title: '幕僚長', description: '每日重點與協調', shape: 'cursor', color: 'blue' })
  Object.assign(mi, { primary: true })
  return mi
}

// ── K02 ─────────────────────────────────────────────

test('K02 設定 Bot 的聲音：選了就存、試聽、語速、自動朗讀、進階引擎', async ({ page }, info) => {
  const api = new MockServer(page)
  const mi = seedBot(api)
  await api.install()
  const v = await voiceMocks(page, api, mi)
  await page.goto(`/bots/${mi.id}/voice`)
  const main = page.getByRole('main', { name: '小秘的聲音' })
  const list = main.getByRole('radiogroup', { name: '聲音' })
  await expect(list.getByRole('radio', { name: /晴/ })).toHaveAttribute('aria-checked', 'true')
  await expect(main.getByTestId('voice-saved')).toContainText('已選「晴」· 自動儲存')

  // 試聽：用那個聲音念一段示範，播放中顯示「停止」
  await main.getByRole('button', { name: '試聽 安' }).click()
  await expect.poll(() => api.calls.find((c) => c.op === 'voice.speak')?.input).toMatchObject({ bot_id: mi.id, voice: 'an' })
  // 選另一個聲音：自動儲存
  await list.getByRole('radio', { name: /小葵/ }).click()
  await expect.poll(() => api.calls.find((c) => c.op === 'voice.bot_set')?.input).toEqual({ bot_id: mi.id, voice: 'kui' })
  await expect(main.getByTestId('voice-saved')).toContainText('已選「小葵」')
  await main.getByRole('switch', { name: '自動朗讀每則回覆' }).click()
  await expect.poll(() => api.calls.filter((c) => c.op === 'voice.bot_set').at(-1)?.input).toEqual({ bot_id: mi.id, auto_read: true })
  expect(v.bot.auto_read).toBe(true)

  await main.getByRole('button', { name: /進階/ }).click()
  await expect(main.getByTestId('voice-advanced')).toContainText('金鑰只存在這台主機')
  await settle(page)
  await page.screenshot({ path: shot(`${info.project.name}-K02-voice-setup`), fullPage: true })
  await main.getByRole('button', { name: '開始通話' }).click()
  await expect(page).toHaveURL(new RegExp(`/bots/${mi.id}/call$`))
})

// ── C05 語音輸入 ─────────────────────────────────────

test('C05 語音輸入：錄音、經 XBot 辨識、文字放進輸入框可以再改', async ({ page }, info) => {
  const api = new MockServer(page)
  const mi = seedBot(api)
  const th = api.addThread(mi.id, '比價')
  api.addMessage(th.id, { sender: 'owner', content: '早安' })
  await api.install()
  const v = await voiceMocks(page, api, mi, { transcripts: ['幫我比較三家的價格'] })
  await page.goto(`/bots/${mi.id}/threads/${th.id}`)
  const main = page.getByRole('main', { name: '和小秘的對話' })
  await main.getByRole('button', { name: '語音輸入' }).click()
  const bar = main.getByTestId('dictation')
  await expect(bar.getByRole('button', { name: '說完了' })).toBeVisible()
  await page.waitForTimeout(1500)
  await settle(page)
  await page.screenshot({ path: shot(`${info.project.name}-C05-dictation`) })
  await bar.getByRole('button', { name: '說完了' }).click()
  await expect(main.getByRole('textbox')).toHaveValue('幫我比較三家的價格')
  expect(v.uploads[0].type).toContain('multipart/form-data')
  expect(v.uploads[0].size).toBeGreaterThan(200)
  // 可以再改了才送
  await main.getByRole('textbox').press('End')
  await main.getByRole('textbox').pressSequentially('，週一前')
  await main.getByRole('textbox').press('Enter')
  expect(api.lastSend?.text).toBe('幫我比較三家的價格，週一前')
  // 取消：不送去辨識
  await main.getByRole('button', { name: '語音輸入' }).click()
  await main.getByRole('button', { name: '取消語音輸入' }).click()
  await expect(main.getByTestId('dictation')).toBeHidden()
  expect(v.uploads).toHaveLength(1)
})

// ── VC-1 自動朗讀與語音訊息 ───────────────────────────

test('VC-1 自動朗讀：看著寫完的回覆念出來，變成語音訊息卡', async ({ page }, info) => {
  const api = new MockServer(page)
  const mi = seedBot(api)
  const th = api.addThread(mi.id, '比價')
  const old = api.addMessage(th.id, { sender: 'bot:' + mi.id, content: '早安，今天有 3 件事要你看。', status: 'complete' })
  await api.install()
  await voiceMocks(page, api, mi, { autoRead: true })
  await page.goto(`/bots/${mi.id}/threads/${th.id}`)
  const main = page.getByRole('main', { name: '和小秘的對話' })
  await expect(main.getByTestId('msg-bot')).toHaveText(/早安/)
  // 舊的回覆不會自己念
  await page.waitForTimeout(300)
  expect(api.calls.filter((c) => c.op === 'voice.speak')).toHaveLength(0)

  const owner = api.addMessage(th.id, { sender: 'owner', content: '比價完了嗎？', task_id: 'k1' })
  const reply = api.addMessage(th.id, { sender: 'bot:' + mi.id, content: '', task_id: 'k1', streaming: true })
  const base = { bot_id: mi.id, thread_id: th.id, task_id: 'k1' }
  await api.emit(
    { ...base, kind: 'message.created', payload: api.storeMessage(th.id, owner) },
    { ...base, kind: 'message.created', payload: api.storeMessage(th.id, reply) },
    { ...base, kind: 'task.state', payload: { state: 'running' } },
  )
  const text = '比完了。三家都比我們便宜一些，有 5 項差最多，我把清單放進對話了。'
  await api.emit({ ...base, kind: 'chat.text', persist: false, payload: { message_id: reply.id, text } })
  await api.emit({ ...base, kind: 'task.state', payload: { state: 'done', verdict: 'answer' } }, { ...base, kind: 'chat.done', payload: { message_id: reply.id, status: 'complete', verdict: 'answer', text } })
  await expect.poll(() => api.calls.find((c) => c.op === 'voice.speak')?.input).toEqual({ bot_id: mi.id, message_id: reply.id, voice_note: true })
  // 服務把它標成語音訊息（meta.voice）並推 message.created
  const stored = api.storeMessage(th.id, { ...reply, content: text, streaming: false, status: 'complete' })
  const marked = { ...stored, meta: { ...stored.meta, voice: { engine: 'proxy', audio_id: 'a1', voice: 'qing', speed: 1, chars: 34, bytes: 1000 } } }
  await api.emit({ ...base, kind: 'message.created', payload: marked })
  const note = main.getByTestId('voice-note')
  await expect(note).toBeVisible()
  await expect(note).toContainText('晴')
  expect(api.calls.filter((c) => c.op === 'voice.speak')).toHaveLength(1)
  // 再播一次（不標記）
  await page.waitForTimeout(1200)
  await note.getByRole('button', { name: '播放語音訊息' }).click()
  await expect.poll(() => api.calls.filter((c) => c.op === 'voice.speak').at(-1)?.input).toEqual({ bot_id: mi.id, message_id: reply.id })
  void old
  await settle(page)
  await page.screenshot({ path: shot(`${info.project.name}-VC1-voice-note`) })
})

// ── K01／L18 通話 ───────────────────────────────────

test('K01／L18 通話：說話→辨識→交辦→念回覆；核定卡用說的允許；掛斷留通話紀錄', async ({ page }, info) => {
  test.setTimeout(90_000)
  const phone = info.project.name !== 'desktop'
  const api = new MockServer(page)
  const mi = seedBot(api)
  const th = api.addThread(mi.id, '競品定價')
  api.addMessage(th.id, { sender: 'owner', content: '早安' })
  api.override['chat.respond'] = (i: Record<string, unknown>) => ({ inbox_id: i.inbox_id, task_id: 'k-call', thread_id: th.id, status: 'resolved', resolution: i.choice })
  await api.install()
  await voiceMocks(page, api, mi, { transcripts: ['那就調降那 5 項，其他維持', '允許'] })
  await page.goto(`/bots/${mi.id}/call`)
  const main = page.getByRole('main', { name: '通話' })
  const status = main.getByTestId('call-status')
  await expect(status).toHaveText('正在聽…')

  // 你說話（假麥克風）→ 停一下 → 辨識 → 交給小秘
  await expect.poll(() => api.lastSend?.text, { timeout: 15_000 }).toBe('那就調降那 5 項，其他維持')
  const s = api.lastSend!
  expect(s.threadId).toBe(th.id)
  const base = { bot_id: mi.id, thread_id: th.id, task_id: s.taskId }
  const reply = api.addMessage(th.id, { sender: 'bot:' + mi.id, content: '', task_id: s.taskId, streaming: true })
  await api.emit({ ...base, kind: 'message.created', payload: api.storeMessage(th.id, reply) }, { ...base, kind: 'task.state', payload: { state: 'running' } })
  await api.emit({ ...base, kind: 'chat.text', persist: false, payload: { message_id: reply.id, text: '5 項新價格列好了，' } })
  await expect(main.getByTestId('call-caption')).toContainText('5 項新價格列好了')

  // 要你核定：卡片出現，念出來，再用說的「允許」
  await api.emit(
    { ...base, kind: 'task.state', payload: { state: 'waiting_approval' } },
    { ...base, kind: 'chat.needs_approval', payload: { inbox_id: 'ap1', request: { id: 'r1', kind: 'approval', method: 'approval', prompt: '調價', choices: ['once', 'deny'], params: { description: '調降 5 項商品售價' } } } },
  )
  const ask = page.getByTestId('call-ask')
  await expect(ask).toContainText('調降 5 項商品售價')
  if (phone) await expect(ask).toContainText('說「允許」或「拒絕」，也可以直接按')
  await expect.poll(() => api.calls.some((c) => c.op === 'voice.speak' && String(c.input.text).includes('需要你核定'))).toBe(true)
  await settle(page)
  await page.screenshot({ path: shot(`${info.project.name}-${phone ? 'L18' : 'K01'}-call-approval`) })
  await expect.poll(() => api.calls.find((c) => c.op === 'chat.respond')?.input, { timeout: 20_000 }).toEqual({ inbox_id: 'ap1', choice: 'once', via: 'voice' })
  await expect(page.getByTestId('call-decided')).toHaveText('已允許這次')

  // 做完：念回覆（標成語音訊息），再回到聽
  const text = '5 項新價格列好了，已經改好，清單放進對話了。'
  await api.emit(
    { ...base, kind: 'chat.answered', payload: { inbox_id: 'ap1', kind: 'approval', resolution: 'once', text: '' } },
    { ...base, kind: 'task.state', payload: { state: 'done', verdict: 'answer' } },
    { ...base, kind: 'chat.done', payload: { message_id: reply.id, status: 'complete', verdict: 'answer', text } },
  )
  await expect.poll(() => api.calls.find((c) => c.op === 'voice.speak' && c.input.message_id === reply.id)?.input).toEqual({ bot_id: mi.id, message_id: reply.id, voice_note: true })
  await expect(status).toHaveText(/正在聽…|正在說…/)
  if (!phone) await expect(page.getByTestId('call-transcript')).toContainText('那就調降那 5 項，其他維持')
  await expect(main.getByTestId('call-caption')).toBeVisible()
  await settle(page)
  await page.screenshot({ path: shot(`${info.project.name}-${phone ? 'L18' : 'K01'}-call`) })

  // 靜音、字幕
  await main.getByRole('button', { name: '靜音' }).click()
  await expect(status).toHaveText('已靜音')
  await main.getByRole('button', { name: '取消靜音' }).click()
  await main.getByRole('button', { name: '關閉字幕' }).click()
  await expect(main.getByText('字幕已關閉')).toBeVisible()

  // 掛斷：回到對話，留一列通話紀錄
  await main.getByRole('button', { name: phone ? '掛斷' : /結束通話/ }).click()
  await expect(page).toHaveURL(new RegExp(`/bots/${mi.id}/threads/${th.id}$`))
  const end = api.calls.find((c) => c.op === 'voice.call_end')?.input
  expect(end).toMatchObject({ bot_id: mi.id, thread_id: th.id, reason: 'hangup' })
  expect(end?.turns).toBeGreaterThanOrEqual(2)
})

// ── 高風險的核定：用說的只能拒絕，允許要在畫面上按 ──────────

test('通話中的高風險核定：危險指令與 confirm 級操作要在畫面上按，用說的只能拒絕', async ({ page }, info) => {
  test.setTimeout(90_000)
  const phone = info.project.name !== 'desktop'
  const api = new MockServer(page)
  const mi = seedBot(api)
  const th = api.addThread(mi.id, '清理')
  api.addMessage(th.id, { sender: 'owner', content: '幫我清一下舊檔案' })
  api.override['chat.respond'] = (i: Record<string, unknown>) => ({ inbox_id: i.inbox_id, task_id: 'k-rm', thread_id: th.id, status: 'resolved', resolution: i.choice })
  api.override['approvals.decide'] = (i: Record<string, unknown>) => ({ approval: { id: i.approval_id, status: i.decision === 'allow' ? 'approved' : 'denied' }, settled: true, executed: i.decision === 'allow', failed: false })
  await api.install()
  await voiceMocks(page, api, mi, { transcripts: ['允許', '拒絕'] })
  await page.goto(`/bots/${mi.id}/call`)
  const main = page.getByRole('main', { name: '通話' })
  const status = main.getByTestId('call-status')
  await expect(status).toHaveText('正在聽…')
  // 先靜音，等卡片出現再開口
  await main.getByRole('button', { name: '靜音' }).click()
  await expect(status).toHaveText('已靜音')
  const base = { bot_id: mi.id, thread_id: th.id, task_id: 'k-rm' }
  await api.emit(
    { ...base, kind: 'task.state', payload: { state: 'waiting_approval' } },
    { ...base, kind: 'chat.needs_approval', payload: { inbox_id: 'rm1', request: { id: 'r1', kind: 'approval', method: 'approval', prompt: '刪除', choices: ['once', 'deny'], params: { command: 'rm -rf /workspace/old', description: '刪掉舊資料夾' } } } },
  )
  const ask = page.getByTestId('call-ask')
  await expect(ask).toContainText('刪掉舊資料夾')
  await expect(page.getByTestId('call-tap-only')).toContainText('這件事要在畫面上按確認')
  await settle(page)
  await page.screenshot({ path: shot(`${info.project.name}-${phone ? 'L18' : 'K01'}-call-tap-only`) })
  // 說「允許」：不送出，提醒要按；說「拒絕」：可以（帶 via=voice）
  await main.getByRole('button', { name: '取消靜音' }).click()
  await expect.poll(() => api.calls.some((c) => c.op === 'voice.speak' && String(c.input.text).includes('這件事要在畫面上按確認，不能只用說的')), { timeout: 20_000 }).toBe(true)
  expect(api.calls.filter((c) => c.op === 'chat.respond')).toHaveLength(0)
  await expect.poll(() => api.calls.find((c) => c.op === 'chat.respond')?.input, { timeout: 20_000 }).toEqual({ inbox_id: 'rm1', choice: 'deny', via: 'voice' })
  await expect(page.getByTestId('call-decided')).toHaveText('已拒絕')
  await api.emit({ ...base, kind: 'chat.answered', payload: { inbox_id: 'rm1', kind: 'approval', resolution: 'deny', text: '' } })

  // confirm 級的操作（例：刪除 Bot）：只能按；按了不帶 via
  await main.getByRole('button', { name: '靜音' }).click()
  api.inbox = [{ id: 'op1', kind: 'approval', group: 'decide', bot_id: mi.id, thread_id: th.id, ref: 'ap-del', title: 'Claude Code 想要：刪除 Bot', body: '要允許嗎？', payload: { op: 'xbot.bots.delete', params: { id: mi.id } }, status: 'open', read: false, created_at: Date.now() }]
  await api.emit({ kind: 'inbox.created', bot_id: mi.id, payload: { id: 'op1' } })
  await expect(ask).toContainText('刪除 Bot')
  await expect(page.getByTestId('call-tap-only')).toBeVisible()
  await ask.getByRole('button', { name: '允許這次' }).click()
  await expect.poll(() => api.calls.find((c) => c.op === 'approvals.decide')?.input).toEqual({ approval_id: 'ap-del', decision: 'allow' })
  await expect(page.getByTestId('call-decided')).toHaveText('已允許這次')
})

// ── 真的服務＋假的語音供應商 ──────────────────────────

test('真的服務：K02 試聽與語音輸入經 Model Proxy（假的 OpenAI 相容供應商，金鑰不到瀏覽器）', async ({ page, request }, info) => {
  test.skip(info.project.name !== 'desktop', '同一個服務只跑一次')
  const seen: Array<{ path: string; auth: string; body: string }> = []
  const upstream = http.createServer((req, res) => {
    const chunks: Buffer[] = []
    req.on('data', (c: Buffer) => chunks.push(c))
    req.on('end', () => {
      const body = Buffer.concat(chunks)
      seen.push({ path: req.url ?? '', auth: req.headers.authorization ?? '', body: body.toString('latin1') })
      if (req.url?.endsWith('/audio/speech')) {
        res.writeHead(200, { 'Content-Type': 'audio/wav' })
        res.end(replyWav)
      } else if (req.url?.endsWith('/audio/transcriptions')) {
        res.writeHead(200, { 'Content-Type': 'application/json' })
        res.end(JSON.stringify({ text: '明天早上提醒我開會', usage: { type: 'duration', seconds: 1.5 } }))
      } else {
        res.writeHead(404)
        res.end()
      }
    })
  })
  await new Promise<void>((r) => upstream.listen(0, '127.0.0.1', r))
  const port = (upstream.address() as { port: number }).port
  const key = 'sk-e2e-voice-not-real-1234567890'
  try {
    const post = async (op: string, data: unknown) => {
      const res = await request.post(`/api/v1/${op}`, { data })
      expect(res.ok(), `${op}: ${await res.text()}`).toBe(true)
      return (await res.json()).output
    }
    await post('models.set_provider', { id: 'e2e-voice', base_url: `http://127.0.0.1:${port}/v1`, api_key: key })
    await post('voice.settings_set', { tts: { provider: 'e2e-voice', model: 'tts-1' }, stt: { provider: 'e2e-voice', model: 'whisper-1' } })
    const bot = await post('bots.create', { name: `說話喵${Date.now() % 1000}`, color: 'purple', shape: 'star' })

    // K02：試聽「安」→ 服務經 Model Proxy 合成（換上真金鑰）→ 瀏覽器從 /api/v1/voice.audio 拿聲音
    const audio = page.waitForResponse((r) => r.url().includes('/api/v1/voice.audio?id='))
    await page.goto(`/bots/${bot.id}/voice`)
    await page.getByRole('button', { name: '試聽 安' }).click()
    const got = await audio
    expect([200, 206]).toContain(got.status())
    expect(got.headers()['content-type']).toBe('audio/wav')
    const speech = seen.find((x) => x.path === '/v1/audio/speech')!
    expect(speech.auth).toBe('Bearer ' + key)
    expect(JSON.parse(speech.body)).toMatchObject({ model: 'tts-1', voice: 'onyx', response_format: 'mp3' })
    const html = await page.content()
    expect(html).not.toContain(key)

    // 語音輸入：假麥克風錄一段 → 服務經 Model Proxy 辨識 → 文字進輸入框
    await page.goto(`/bots/${bot.id}`)
    const main = page.getByRole('main', { name: `和${bot.name}的對話` })
    await main.getByRole('button', { name: '語音輸入' }).click()
    await page.waitForTimeout(1500)
    await main.getByTestId('dictation').getByRole('button', { name: '說完了' }).click()
    await expect(main.getByRole('textbox')).toHaveValue('明天早上提醒我開會')
    const tr = seen.find((x) => x.path === '/v1/audio/transcriptions')!
    expect(tr.auth).toBe('Bearer ' + key)
    expect(tr.body).toContain('whisper-1')
    expect(tr.body).toContain('filename="speech.webm"')
  } finally {
    await request.post('/api/v1/voice.settings_set', { data: { tts: { provider: '', model: '' }, stt: { provider: '', model: '' } } })
    await new Promise<void>((r) => upstream.close(() => r()))
  }
})
