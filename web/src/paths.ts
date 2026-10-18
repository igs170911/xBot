// 各頁的網址樣式。放在這裡（不放在頁面檔）是讓 App 判斷路由時不用先載入那一頁的程式（v1.0：頁面拆成按需載入）。

/** 定時任務與行事曆：/calendar、/calendar/routines、/calendar/routines/<id>。 */
export const ROUTINES_PATH = /^\/calendar(?:\/routines(?:\/([^/]+))?)?\/?$/
/** 一個 Bot 的檔案。 */
export const BOT_FILES_PATH = /^\/bots\/([^/]+)\/files\/?$/
/** 連接應用程式：/apps、/apps/mcp、/apps/logins。 */
export const APPS_PATH = /^\/apps(?:\/(mcp|logins))?\/?$/
/** 一個 Bot 的聲音設定。 */
export const VOICE_PATH = /^\/bots\/([^/]+)\/voice\/?$/
