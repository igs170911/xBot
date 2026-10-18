import { createStore } from '../../data/store'

// 匯入 Bot 對話框（G14）開著沒有。獨立成一個小檔：開對話框的按鈕（首頁、新增 Bot）不用先載入整個分享／匯入的程式。
export const importBotDialog = createStore(false)
export const openImportBot = () => importBotDialog.set(true)
