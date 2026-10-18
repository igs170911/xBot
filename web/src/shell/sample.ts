// 示範資料：待我處理頁下半的示範卡片還在用（側欄已改接 bots.list）。角色照 N02「示範角色」：每個 Bot 一組形狀＋顏色，全站一致。
import type { AvatarColor, AvatarDot, AvatarShape, AvatarState } from '../ui/avatar-data'

export type SampleBot = {
  id: string
  name: string
  role: string
  shape: AvatarShape
  color: AvatarColor
  state: AvatarState
  dot: AvatarDot
  sub: string
  /** 名字下方的短狀態灰字 */
  status: string
  time?: string
  chief?: boolean
  unread?: boolean
}

export const BOTS: Record<string, SampleBot> = {
  xiaomi: { id: 'xiaomi', status: '工作中', name: '小秘', role: '幕僚長', shape: 'cursor', color: 'blue', state: 'working', dot: 'none', sub: '工作中…彙整三家競品定價', chief: true },
  azhe: { id: 'azhe', status: '工作中', name: '阿哲', role: '選品研究員', shape: 'blob', color: 'orange', state: 'searching', dot: 'none', sub: '工作中…在蝦皮比價第 3 家' },
  xiaobian: { id: 'xiaobian', status: '等你回覆', name: '小編', role: '社群小編', shape: 'circle', color: 'pink', state: 'notifying', dot: 'waiting', sub: '等你回覆：需要你輸入驗證碼' },
  akai: { id: 'akai', status: '待命', name: '阿凱', role: '業務開發', shape: 'squircle', color: 'green', state: 'idle', dot: 'none', sub: '3 封開發信草稿已完成', time: '10:42', unread: true },
  zhangfang: { id: 'zhangfang', status: '等你允許', name: '帳房', role: '費用管理', shape: 'hexagon', color: 'yellow', state: 'notifying', dot: 'waiting', sub: '等你允許：付款 NT$1,280' },
  shujumiao: { id: 'shujumiao', status: '排隊中', name: '數據喵', role: '數據分析師', shape: 'diamond', color: 'purple', state: 'idle', dot: 'queued', sub: '排隊中：週報資料處理' },
  xinchai: { id: 'xinchai', status: '休息', name: '信差', role: '收件匣管家', shape: 'star', color: 'cyan', state: 'sleeping', dot: 'none', sub: '整理了 24 封信', time: '昨天' },
  zhuachong: { id: 'zhuachong', status: '待命', name: '抓蟲', role: 'Bug 重現', shape: 'shield', color: 'red', state: 'idle', dot: 'none', sub: '待命' },
}
