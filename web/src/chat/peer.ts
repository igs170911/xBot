import { registerCard } from './cards'
import { ActivityLine } from './Message'
import { PeerCard } from './PeerCard'

/** 登記 kind=peer 的畫法（和隊友往來的那一行，C13；App 載入時呼叫一次）。 */
export function registerPeerCard() {
  return registerCard('peer', PeerCard)
}

/** 登記 kind=activity 的畫法（系統記的一行，例：自動繼續，X04；App 載入時呼叫一次）。 */
export function registerActivityCard() {
  return registerCard('activity', ActivityLine)
}
