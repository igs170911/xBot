import { useCallback, useEffect, useState } from 'react'
import { ChevronRight, FolderOpen, Monitor } from 'lucide-react'
import { appsGrantToBot, appsList, appsRevokeFromBot, botsUpdate, hostsList, type BotsGetOutput, type HostsListOutput } from '../../api/generated'
import { AppIcon } from '../../apps/shared'
import { statusOf, toneText, type Conn } from '../../apps/appsData'
import { linkClick } from '../../router'
import { Icon } from '../../ui/Icon'
import { Switch } from '../../ui/Controls'
import { Select } from '../../ui/Menu'
import { cx } from '../../ui/cx'
import { useToast } from '../../ui/toast-context'
import { errText, isPending } from '../format'
import { Block, Group, Row } from '../parts'

// Bot 設定 · 能力 › 應用程式與電腦（G07 畫板；任務卡的 G08「能用哪些應用程式、主機、檔案」）：
// 哪些已連接的應用程式給它用（連接一次，所有 Bot 都能用；這裡決定這個 Bot 能不能用），它的電腦放在哪台主機、檔案在哪。

type Bot = BotsGetOutput
type Host = HostsListOutput['hosts'][number]

const LINK_BTN = 'inline-flex h-9 items-center gap-2 whitespace-nowrap rounded-full bg-control px-4 text-ui text-ink hover:bg-hover hover:text-ink max-sm:h-11'

export function AccessTab({ bot, onSaved }: { bot: Bot; onSaved: (b: Bot) => void }) {
  const toast = useToast()
  const [conns, setConns] = useState<Conn[] | null>(null)
  const [hosts, setHosts] = useState<Host[]>([])
  const [err, setErr] = useState<string | null>(null)
  const [busy, setBusy] = useState<string | null>(null)

  const load = useCallback(() => {
    appsList({})
      .then((o) => {
        setConns(o.connections)
        setErr(null)
      })
      .catch((e) => setErr(errText(e)))
  }, [])
  useEffect(() => {
    load()
    hostsList({})
      .then((o) => setHosts(o.hosts))
      .catch(() => setHosts([]))
  }, [load])

  const toggle = async (c: Conn, on: boolean) => {
    setBusy(c.id)
    try {
      if (on) await appsGrantToBot({ id: c.id, bot_id: bot.id })
      else await appsRevokeFromBot({ id: c.id, bot_id: bot.id })
      toast.show({ kind: 'done', text: on ? `${bot.name}可以用 ${c.title}` : `${bot.name}不再用 ${c.title}` })
      load()
    } catch (e) {
      toast.show({ kind: isPending(e) ? 'info' : 'error', text: errText(e) })
    } finally {
      setBusy(null)
    }
  }

  const moveHost = async (id: string) => {
    try {
      const b = await botsUpdate({ id: bot.id, host_id: id })
      onSaved(b)
      toast.show({ kind: 'done', text: id ? `${bot.name}的電腦放在 ${hosts.find((h) => h.id === id)?.name ?? id}` : `${bot.name}的電腦自動擺放` })
    } catch (e) {
      toast.show({ kind: 'error', text: errText(e) })
    }
  }

  return (
    <div className="grid grid-cols-2 items-start gap-5 max-lg:grid-cols-1">
      <Block
        title="應用程式"
        sub={`連接一次，所有 Bot 都能用；這裡決定${bot.name}能不能用。`}
        aside={
          <a href="/apps" onClick={linkClick('/apps')} className="flex items-center gap-0.5 text-ui-sm">
            連接更多
            <ChevronRight size={14} />
          </a>
        }
      >
        {err && <p className="m-0 rounded-group bg-card px-4 py-3 text-ui-sm text-danger">讀不到應用程式：{err}</p>}
        {conns && conns.length === 0 && <p className="m-0 rounded-group bg-card px-4 py-3 text-ui-sm text-ink-2">還沒有連接任何應用程式。到「連接應用程式」點一個登入就好。</p>}
        {conns && conns.length > 0 && (
          <Group label={`${bot.name}能用的應用程式`}>
            {conns.map((c) => {
              const on = c.enabled && (c.all_bots || c.bot_ids.includes(bot.id))
              const st = statusOf(c.status)
              return (
                <div key={c.id} className="flex min-h-14 items-center gap-3 border-line px-4 py-2.5 not-first:border-t">
                  <AppIcon name={c.icon} size={32} />
                  <span className="min-w-0 flex-1">
                    <span className="block truncate text-ui">{c.title}</span>
                    <span className={cx('block truncate text-[13px]', c.status === 'ok' ? 'text-ink-2' : toneText[st.tone])}>
                      {on ? (c.all_bots ? '所有 Bot 都能用' : `給${bot.name}用`) : `尚未給${bot.name}`} · {st.text}
                    </span>
                  </span>
                  <Switch checked={on} disabled={!c.enabled || busy === c.id} label={`讓${bot.name}用 ${c.title}`} onChange={(v) => void toggle(c, v)} />
                </div>
              )
            })}
          </Group>
        )}
        <p className="m-0 text-meta text-ink-3">會寄出或刪除的動作，照「權限與花費」的設定先問你。</p>
      </Block>

      <Block title="電腦" sub={`${bot.name}有一台自己的電腦，第一次收到工作才開機，閒置 30 分鐘休眠；登入與檔案都會保留。`}>
        <Group>
          <Row title="在哪裡跑" sub="已經有電腦的 Bot 換主機要搬家（之後的版本）" wrap>
            <Select
              label="主機"
              hideLabel
              value={bot.host_id || ''}
              options={[{ value: '', label: '自動擺放' }, ...hosts.map((h) => ({ value: h.id, label: `${h.name}${h.kind === 'local' ? '（這台）' : ''}${h.status === 'online' ? '' : ' · 離線'}` }))]}
              onChange={(v) => void moveHost(v)}
            />
          </Row>
          <Row title="畫面" sub="看它正在做什麼，需要時接手">
            <a href={`/bots/${bot.id}/computer`} onClick={linkClick(`/bots/${bot.id}/computer`)} className={LINK_BTN}>
              <Icon icon={Monitor} size={16} />
              打開電腦
            </a>
          </Row>
          <Row title="檔案" sub="它電腦裡 /workspace 的檔案">
            <a href={`/bots/${bot.id}/files`} onClick={linkClick(`/bots/${bot.id}/files`)} className={LINK_BTN}>
              <Icon icon={FolderOpen} size={16} />
              打開檔案
            </a>
          </Row>
          <Row title="電腦資源" sub="2 CPU · 3 GB 記憶體 · 閒置 30 分鐘休眠 · 只能上網（連不到這台主機與區網）" />
        </Group>
      </Block>
    </div>
  )
}
