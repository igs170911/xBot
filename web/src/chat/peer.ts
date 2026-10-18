import { registerCard } from './cards'
import { PeerCard } from './PeerCard'

/** 登記 kind=peer 的畫法（和隊友往來的那一行，C13；App 載入時呼叫一次）。 */
export function registerPeerCard() {
  return registerCard('peer', PeerCard)
}
