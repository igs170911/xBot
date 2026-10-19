import { Hand, Monitor } from 'lucide-react'
import { useScreen } from '../computer/screenData'
import { useTakeover } from '../computer/hooks'
import { navigate } from '../router'
import { Button } from '../ui/Button'
import { Icon } from '../ui/Icon'

// 接手中（你在操作 Bot 的電腦）：Bot 暫停，新訊息只會排隊，要交還才會開始（2026-10-19 實際遇到：
// 傳了訊息等了 6 分鐘沒反應，因為電腦還在接手中）。輸入框上方明白寫出來，一鍵交還。
export function TakeoverBar({ botId, name, queued }: { botId: string; name: string; queued: boolean }) {
  const { screen, setScreen } = useScreen(botId)
  const { back, busy } = useTakeover(botId, name, setScreen)
  const t = screen?.takeover
  if (!t) return null
  const mine = t.by === 'owner'
  return (
    <div role="status" data-testid="takeover-bar" className="flex flex-wrap items-center gap-x-3 gap-y-2 rounded-menu bg-warning-soft py-2 pl-3.5 pr-2 text-ui-sm">
      <Icon icon={Hand} size={16} className="text-warning-ink" />
      <span className="min-w-0 flex-1 text-warning-ink">
        {mine ? `你正在操作${name}的電腦，${name}暫停中` : `${t.by_name} 正在操作${name}的電腦`}
        {queued ? '；你的訊息會在交還後開始' : '；交還後才會處理新訊息'}
      </span>
      <span className="flex gap-1.5">
        <Button size="sm" variant="ghost" icon={Monitor} onClick={() => navigate(`/bots/${botId}/computer`)}>
          看電腦
        </Button>
        {mine && (
          <Button size="sm" variant="primary" loading={busy === 'back'} onClick={() => void back('')}>
            交還電腦
          </Button>
        )}
      </span>
    </div>
  )
}
