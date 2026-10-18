import type { ComponentType } from 'react'
import type { Bot } from '../data/bots'
import type { Activity, ChatMsg } from './assemble'

// 對話裡的卡片登錄表（CH-7）：訊息的 kind 不是 text 時，用這裡登記的元件畫。
// 例：核准、提問、密鑰請求（C08～C10）、排程結果（C11）各自 registerCard('approval', ApprovalCard)。
// 沒有登記的 kind 退回一般的文字泡泡，不會整則消失。

export type CardProps = {
  msg: ChatMsg
  bot?: Bot
  threadId: string
  /** 這則訊息所屬工作的進度（有的話）。 */
  activity?: Activity
}

const registry = new Map<string, ComponentType<CardProps>>()

export function registerCard(kind: string, component: ComponentType<CardProps>): () => void {
  registry.set(kind, component)
  return () => {
    if (registry.get(kind) === component) registry.delete(kind)
  }
}

export function cardFor(kind: string): ComponentType<CardProps> | undefined {
  return kind === 'text' ? undefined : registry.get(kind)
}
