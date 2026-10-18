// @ 提到選單（L20）的觸發判斷。

/** 輸入框最後是不是正在打「@…」：回傳 @ 後面的字，不是就回 null。 */
export function mentionQuery(text: string): string | null {
  const m = text.match(/(^|\s)@([^\s@]*)$/)
  return m ? m[2] : null
}
