import { useEffect, useState } from 'react'
import { Users } from 'lucide-react'
import { auditList, type AuditListOutput, type BotsGetOutput } from '../../api/generated'
import { botsStore } from '../../data/bots'
import { useStore } from '../../data/store'
import { linkClick } from '../../router'
import { Icon } from '../../ui/Icon'
import { StatusChip } from '../../ui/Pill'
import { errText } from '../format'
import { MODE_LABEL } from '../botModes'
import { Block, Group, Row } from '../parts'
import { BotVoiceBlock } from '../../voice/BotVoiceBlock'

// Bot 設定 · 進階 › 聲音（G10 的聲音區塊，E32）與 歷史（G12：這個 Bot 的設定改過什麼、誰改的，從稽核紀錄來，BT-9）。

type Bot = BotsGetOutput
type Entry = AuditListOutput['entries'][number]

const FIELD: Record<string, string> = {
  name: '名字',
  title: '職稱',
  description: '它做什麼',
  soul: '人設',
  color: '頭像顏色',
  shape: '頭像形狀',
  eyes: '頭像眼睛',
  image: '頭像圖片',
  engine: '引擎',
  model: '模型',
  fallback: '備援模型',
  effort: '思考強度',
  approval_mode: '權限',
  memory_enabled: '記憶開關',
  host_id: '放在哪台主機',
  can_manage: '團隊管理權限',
}

const OPS: Record<string, string> = {
  'xbot.bots.create': '建立了這個 Bot',
  'xbot.bots.import': '從 .xbot 檔匯入',
  'xbot.bots.archive': '封存或取消封存',
  'xbot.bots.export': '做成分享檔（.xbot）',
  'xbot.bots.update_my_profile': '自己改了職稱或頭像',
  'xbot.skills.create': '新增技能',
  'xbot.skills.update': '修改技能',
  'xbot.skills.enable': '打開技能',
  'xbot.skills.disable': '停用技能',
  'xbot.skills.delete': '刪除技能',
  'xbot.skills.install_to_bot': '從技能庫裝上技能',
  'xbot.skills.import': '匯入技能',
  'xbot.memory.write': '修改記憶',
  'xbot.memory.add': '記下一件事',
  'xbot.memory.restore': '復原記憶',
  'xbot.engines.link_credentials': '同意使用你的訂閱',
  'xbot.engines.unlink_credentials': '收回訂閱的使用權',
  'xbot.apps.grant_to_bot': '給它用一個應用程式',
  'xbot.apps.revoke_from_bot': '不再給它用一個應用程式',
  'xbot.usage.set_caps': '改花費上限',
  'xbot.computers.rebuild': '重建電腦',
  'xbot.computers.update': '更新電腦',
  'xbot.computers.set_network': '改電腦能連的網路',
}

/** 一筆稽核紀錄的白話說明；不是設定類的操作回 null（不列）。 */
export function describe(e: Entry): string | null {
  if (e.op === 'xbot.bots.update') {
    const p = (e.params ?? {}) as Record<string, unknown>
    const keys = Object.keys(p).filter((k) => k !== 'id' && FIELD[k])
    if (!keys.length) return null
    if (keys.length === 1 && keys[0] === 'approval_mode') return `權限改成「${MODE_LABEL[String(p.approval_mode)] ?? String(p.approval_mode)}」`
    return '改了' + keys.map((k) => FIELD[k]).join('、')
  }
  return OPS[e.op] ?? null
}

function when(ms: number): string {
  const d = new Date(ms)
  return d.toLocaleString('zh-TW', { month: 'numeric', day: 'numeric', hour: '2-digit', minute: '2-digit', hour12: false })
}

export function HistoryTab({ bot }: { bot: Bot }) {
  const bots = useStore(botsStore).bots
  const [entries, setEntries] = useState<Entry[] | null>(null)
  const [err, setErr] = useState<string | null>(null)
  useEffect(() => {
    auditList({ target: bot.id, limit: 200 })
      .then((o) => setEntries(o.entries))
      .catch((e) => setErr(errText(e)))
  }, [bot.id])

  const who = (actor: string) => {
    if (actor === 'owner' || actor === '') return '你'
    if (actor.startsWith('agent:')) return '外部代理（MCP）'
    if (actor.startsWith('bot:')) return bots?.find((b) => 'bot:' + b.id === actor)?.name ?? 'Bot'
    return actor
  }
  const rows = (entries ?? []).map((e) => ({ e, text: describe(e) })).filter((r) => r.text)

  return (
    <>
      <Block title="設定的變更" sub={`${bot.name}的名字、人設、權限、技能、記憶、訂閱等改過什麼、誰改的（新到舊）。`}>
        {err && <p className="m-0 rounded-group bg-card px-4 py-3 text-ui-sm text-danger">讀不到變更紀錄：{err}</p>}
        {entries && rows.length === 0 && <p className="m-0 rounded-group bg-card px-4 py-3 text-ui-sm text-ink-2">還沒有改過設定。</p>}
        {rows.length > 0 && (
          <Group label="設定的變更">
            {rows.slice(0, 50).map(({ e, text }) => (
              <div key={e.id} className="flex min-h-12 items-center gap-3 border-line px-4 py-2.5 not-first:border-t">
                <span className="w-[92px] flex-none text-[13px] text-ink-2 tabular-nums">{when(e.created_at)}</span>
                <span className="min-w-0 flex-1 text-ui">{text}</span>
                <span className="text-[13px] text-ink-2">{who(e.actor)}</span>
                {e.status === 'pending_approval' && (
                  <StatusChip size="sm" tone="warning">
                    等你核准
                  </StatusChip>
                )}
                {e.status === 'error' && (
                  <StatusChip size="sm" tone="danger">
                    沒有成功
                  </StatusChip>
                )}
              </div>
            ))}
          </Group>
        )}
        <p className="m-0 text-meta text-ink-3">
          記憶的每一次變更可以在「
          <a href={`/settings/bots/${bot.id}/abilities/memory`} onClick={linkClick(`/settings/bots/${bot.id}/abilities/memory`)}>
            記憶
          </a>
          」單項復原；花費明細在「
          <a href="/settings/usage" onClick={linkClick('/settings/usage')}>
            用量與花費
          </a>
          」。
        </p>
      </Block>
    </>
  )
}

export function VoiceTab({ bot }: { bot: Bot }) {
  return (
    <>
      <Block title="聲音" sub="語音通話和朗讀時用的聲音；通話時隨時可以打斷它。">
        <BotVoiceBlock botId={bot.id} botName={bot.name} />
      </Block>
      <Block title="誰看得到" sub={`哪些人可以找${bot.name}說話。`}>
        <Group>
          <Row title={<span className="flex items-center gap-2"><Icon icon={Users} size={16} />只有我</span>} sub="多人共用與組織成員在之後的版本開放。" />
        </Group>
      </Block>
    </>
  )
}
