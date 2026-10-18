import { useCallback, useEffect, useRef, useState } from 'react'
import { ApiError, voiceBotGet, voiceThreadSet } from '../api/generated'
import type { ChatItem, ChatMsg } from '../chat/assemble'
import { useStore } from '../data/store'
import { useToast } from '../ui/toast-context'
import { playerStore, speak, stopPlayback } from './voice'

// 對話裡的語音（E32，VC-1）：唸出來、自動朗讀。

const errText = (e: unknown) => (e instanceof ApiError || e instanceof Error ? e.message : String(e))

/** 念一則 Bot 的回覆（「唸出來」按鈕、語音訊息的播放鈕）：經 XBot 的語音引擎，沒有時瀏覽器內建的聲音。 */
export function useReadAloud(botId: string | undefined, msg: ChatMsg): { state: 'idle' | 'loading' | 'playing'; toggle: () => void } {
  const player = useStore(playerStore)
  const toast = useToast()
  const key = 'msg:' + msg.id
  const state = player.key === key ? player.phase : 'idle'
  const toggle = () => {
    if (state !== 'idle') return stopPlayback()
    if (!botId) return
    speak(key, { bot_id: botId, message_id: msg.id }).catch((e: unknown) => toast.show({ kind: 'error', text: errText(e) }))
  }
  return { state, toggle }
}

/** 這條對話串要不要自動朗讀（Bot 設定＋對話串自己的開關），可以切換。 */
export function useAutoRead(botId: string, threadId: string | undefined) {
  const [now, setNow] = useState(false)
  const [own, setOwn] = useState<boolean | undefined>(undefined)
  const toast = useToast()
  useEffect(() => {
    let live = true
    voiceBotGet({ bot_id: botId, thread_id: threadId })
      .then((v) => {
        if (!live) return
        setNow(v.auto_read_now)
        setOwn(v.thread_auto_read)
      })
      .catch(() => {})
    return () => {
      live = false
    }
  }, [botId, threadId])
  const toggle = useCallback(() => {
    if (!threadId) return
    const next = !now
    setNow(next)
    voiceThreadSet({ thread_id: threadId, auto_read: next ? 'on' : 'off' })
      .then((v) => {
        setNow(v.auto_read_now)
        setOwn(v.thread_auto_read)
      })
      .catch((e: unknown) => {
        setNow(!next)
        toast.show({ kind: 'error', text: errText(e) })
      })
  }, [threadId, now, toast])
  return { autoRead: now, threadOwn: own, toggle }
}

/**
 * 自動朗讀（VC-1）：開著時，看著它寫完的 Bot 回覆（不是翻到的舊訊息）念出來，並標成語音訊息。
 * 一則只念一次；換對話串就重來。
 */
export function useAutoReadReplies(botId: string, threadId: string | undefined, items: ChatItem[], on: boolean) {
  const done = useRef(new Set<string>())
  const toast = useToast()
  useEffect(() => {
    done.current = new Set()
  }, [threadId])
  useEffect(() => {
    if (!on || !threadId) return
    for (const it of items) {
      if (it.type !== 'message') continue
      const m = it.msg
      if (m.sender === 'owner' || m.kind !== 'text' || !m.live || m.streaming || m.status !== 'complete' || !m.content.trim() || m.voice || m.silent) continue
      if (done.current.has(m.id)) continue
      done.current.add(m.id)
      speak('msg:' + m.id, { bot_id: botId, message_id: m.id, voice_note: true }).catch((e: unknown) => toast.show({ kind: 'error', text: '自動朗讀失敗：' + errText(e) }))
    }
  }, [items, on, botId, threadId, toast])
}
