// 群組的固定數字與回應規則（D01、D03；後端 internal/limits RoomMembersMin／Max、RoomRounds）。

export const ROOM_MIN = 2
export const ROOM_MAX = 10
/** 每次你發言後 Bot 之間最多接幾輪的預設（limits.DefaultTunables.RoomRounds）；畫面會再讀「限制」的設定值。 */
export const ROOM_ROUNDS = 10

export type Rule = 'auto' | 'lead' | 'mentioned' | 'everyone'

export const RULES: { value: Rule; label: (lead: string) => string; note: string }[] = [
  { value: 'auto', label: () => '自動挑一位', note: '系統挑最適合的一位回答；還沒接上快速判斷時交給主責' },
  { value: 'lead', label: (lead) => `主責 Bot：${lead}`, note: '沒被 @ 時一律由主責回答' },
  { value: 'mentioned', label: () => '只回應被點名的', note: '沒有 @ 就沒有人回' },
  { value: 'everyone', label: () => '所有成員都回應', note: '每則訊息叫醒全部成員，各自判斷要不要回，比較花錢' },
]

/** 名稱隨勾選即時更新（超過 3 位顯示「… 等 N 位」）。 */
export function autoName(names: string[]): string {
  if (names.length <= 3) return names.join('、')
  return `${names.slice(0, 3).join('、')} 等 ${names.length} 位`
}

