import { useCallback, useEffect } from 'react'
import { teachingGet, teachingShot, type TeachingGetOutput } from '../api/generated'
import { onEvent, onReconnect } from '../data/events'
import { createStore, useStore } from '../data/store'

/** 一次示範教學（B32，xbot.teaching.*）。 */
export type Lesson = NonNullable<TeachingGetOutput['lesson']>
export type Step = Lesson['steps'][number]
export type Draft = NonNullable<Lesson['draft']>

/** 每個 Bot 進行中的教學：undefined＝還沒讀、null＝沒有。teaching.updated 事件帶著最新的樣子。 */
export const lessonStore = createStore<Record<string, Lesson | null | undefined>>({})

export function setLesson(botId: string, lesson: Lesson | null) {
  const open = lesson && (lesson.state === 'recording' || lesson.state === 'paused' || lesson.state === 'stopped') ? lesson : null
  lessonStore.set((m) => ({ ...m, [botId]: open }))
}

/** 錄製中或暫停（電腦正在被你示範）。 */
export function isRecording(l: Lesson | null | undefined): boolean {
  return !!l && (l.state === 'recording' || l.state === 'paused')
}

/** 錄完，草稿等你確認。 */
export function isDraft(l: Lesson | null | undefined): boolean {
  return !!l && l.state === 'stopped' && !!l.draft
}

/** 一個 Bot 進行中的示範教學；事件來就更新，斷線重連後重讀。 */
export function useLesson(botId: string) {
  const all = useStore(lessonStore)
  const reload = useCallback(
    () =>
      teachingGet({ bot_id: botId })
        .then((o) => setLesson(botId, (o.lesson as Lesson | null) ?? null))
        .catch(() => {}),
    [botId],
  )
  useEffect(() => {
    void reload()
    const offs = [
      onEvent((e) => {
        if (e.kind !== 'teaching.updated' || e.bot_id !== botId) return
        const p = e.payload as { lesson?: Lesson | null } | undefined
        setLesson(botId, p?.lesson ?? null)
      }),
      onReconnect(() => void reload()),
    ]
    return () => offs.forEach((f) => f())
  }, [botId, reload])
  return { lesson: all[botId], reload }
}

/** 開始示範的對話框（E05）開在哪個 Bot 上；null＝關著。 */
export const teachStartStore = createStore<{ botId: string; threadId?: string } | null>(null)

// 截圖快取：同一張只讀一次（錄製檔存檔或捨棄後就刪掉了，讀不到時是 null）。
const shots = new Map<string, Promise<string | null>>()

export function loadShot(botId: string, lessonId: string, name: string): Promise<string | null> {
  const key = `${lessonId}/${name}`
  let p = shots.get(key)
  if (!p) {
    p = teachingShot({ bot_id: botId, lesson_id: lessonId, name })
      .then((o) => `data:${o.media_type || 'image/jpeg'};base64,${o.image}`)
      .catch(() => {
        shots.delete(key)
        return null
      })
    shots.set(key, p)
  }
  return p
}

/** 步驟來源的類型標籤（E04）。 */
export const SOURCE_LABEL: Record<string, string> = { browser: '瀏覽器', desktop: '桌面', user: '說明' }
