import { useCallback, useEffect, useState } from 'react'
import { ApiError, filesRead, type FilesReadOutput } from '../api/generated'
import { errText } from '../settings/format'
import { isPage, PAGE_MAX_BYTES } from './page'

type State = { path: string; sheet: string; data?: FilesReadOutput; error?: { text: string; missing: boolean } }

/** 讀一個檔案的預覽（xbot.files.read）。 */
export function usePreview(botId: string, path: string | null): [State | null, (sheet: string) => void, () => void] {
  const [state, setState] = useState<State | null>(null)
  // 選的工作表只屬於這個檔案：換檔案就回到第一個。
  const [picked, setPicked] = useState<{ path: string | null; sheet: string }>({ path, sheet: '' })
  const sheet = picked.path === path ? picked.sheet : ''
  const setSheet = useCallback((s: string) => setPicked({ path, sheet: s }), [path])
  const [n, setN] = useState(0)
  useEffect(() => {
    if (!path) return
    let live = true
    // 網頁要整份才畫得出來：讀到上限（一般文字預設只讀前面 256 KB）
    filesRead({ bot_id: botId, path, ...(sheet ? { sheet } : {}), ...(isPage(path) ? { max_bytes: PAGE_MAX_BYTES } : {}) })
      .then((data) => live && setState({ path, sheet, data }))
      .catch((e) => live && setState({ path, sheet, error: { text: errText(e), missing: e instanceof ApiError && e.code === 'not_found' } }))
    return () => {
      live = false
    }
  }, [botId, path, sheet, n])
  const current = state && state.path === path && state.sheet === sheet ? state : null
  return [current, setSheet, () => setN((x) => x + 1)]
}
