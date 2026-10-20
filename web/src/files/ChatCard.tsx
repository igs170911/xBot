import { ChevronRight, Download, ShieldCheck } from 'lucide-react'
import { filesDownloadUrl } from '../api/generated'
import type { CardProps } from '../chat/cards'
import { Markdown } from '../chat/markdown/Markdown'
import { Avatar } from '../ui/Avatar'
import { Icon } from '../ui/Icon'
import { filesHref, kindFromName } from './data'
import { fileClick } from '../chat/markdown/fileLink'
import { FileIcon } from './FilePreview'
import { look } from '../data/bots'

// 對話裡 kind=card 的卡片（Bot 用 xbot.cards.post 發的，C11）。檔案卡（E07 左側）：每個檔案一列，
// 點了到檔案頁預覽，右邊可以直接下載（C33）。

export function ChatCardView({ msg, bot }: CardProps) {
  const c = msg.card
  const botId = msg.sender.startsWith('bot:') ? msg.sender.slice(4) : (bot?.id ?? '')
  if (!c) return <div className="max-w-[min(100%,560px)] whitespace-pre-wrap rounded-bubble bg-bubble-bot px-4 py-3 text-body">{msg.content}</div>
  return (
    <section aria-label={c.title} className="flex max-w-[min(100%,560px)] gap-2.5 self-start" data-testid="chat-card">
      {bot && (
        <span className="pt-1">
          <Avatar {...look(bot)} dot="none" size={26} ring="#070707" />
        </span>
      )}
      <div className="flex min-w-0 flex-1 flex-col gap-2.5 rounded-[22px] bg-bubble-bot px-4 py-3.5">
        <div className="flex items-start gap-2">
          {c.type === 'approval' && (
            <span className="pt-0.5 text-warning-ink">
              <Icon icon={ShieldCheck} size={18} />
            </span>
          )}
          <span className="text-body">{c.title}</span>
        </div>
        {c.body && <Markdown text={c.body} className="text-ui" />}
        {c.files && c.files.length > 0 && (
          <ul className="m-0 flex list-none flex-col gap-1.5 p-0">
            {c.files.map((f) => {
              const name = f.name || f.path.slice(f.path.lastIndexOf('/') + 1)
              const to = filesHref(botId, f.path)
              return (
                <li key={f.path} className="flex items-center gap-1 rounded-[14px] bg-control/70 pr-1">
                  <a href={to} onClick={fileClick(to)} className="flex min-w-0 flex-1 items-center gap-3 rounded-[14px] py-2 pl-2.5 text-ink hover:bg-hover/50" aria-label={`預覽 ${name}`}>
                    <FileIcon kind={kindFromName(name)} size={34} />
                    <span className="flex min-w-0 flex-1 flex-col">
                      <span className="truncate text-ui">{name}</span>
                      <span className="text-meta text-ink-3">點一下預覽</span>
                    </span>
                    <span className="text-ink-3">
                      <Icon icon={ChevronRight} size={16} />
                    </span>
                  </a>
                  <a
                    href={filesDownloadUrl(botId, f.path)}
                    download={name}
                    aria-label={`下載 ${name}`}
                    title="下載"
                    className="flex size-9 flex-none items-center justify-center rounded-[10px] text-ink-2 hover:bg-hover hover:text-ink"
                  >
                    <Icon icon={Download} size={18} />
                  </a>
                </li>
              )
            })}
          </ul>
        )}
      </div>
    </section>
  )
}
