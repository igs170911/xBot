import { registerCard } from '../chat/cards'
import { ChatCardView } from './ChatCard'

/** 登記 kind=card 的畫法（Bot 用 xbot.cards.post 發的卡片；App 載入時呼叫一次）。 */
export function registerChatCards() {
  return registerCard('card', ChatCardView)
}
