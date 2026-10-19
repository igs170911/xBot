import { Check, ChevronDown, MessageSquare, MessagesSquare, Plus, Trash2 } from 'lucide-react'
import type { ChatListThreadsOutput } from '../api/generated'
import { Icon } from '../ui/Icon'
import { cx } from '../ui/cx'
import { relTime } from './format'
import { Popover } from './Popover'

export type Thread = ChatListThreadsOutput['threads'][number]

// 對話串選單（CH-2，design/C03）：列出和這個 Bot 的對話串（目前的打勾），最下面開新的、刪掉目前這條。
export function ThreadMenu({
  botName,
  threads,
  threadId,
  onPick,
  onNew,
  onDelete,
  look = 'default',
}: {
  botName: string
  threads: Thread[] | null
  threadId?: string
  onPick: (id: string) => void
  onNew: () => void
  /** 刪除目前的對話串（有 threadId 時才出現）。 */
  onDelete?: () => void
  /** phone：手機頁首右上的「對話串」膠囊（L03）。 */
  look?: 'default' | 'phone'
}) {
  const current = threads?.find((t) => t.id === threadId)
  const title = threadId ? current?.title || '對話' : '新對話串'
  return (
    <Popover
      align={look === 'phone' ? 'end' : 'start'}
      trigger={(p) =>
        look === 'phone' ? (
          <button
            {...p}
            type="button"
            aria-label={`對話串：${title}`}
            className="flex h-11 flex-none items-center gap-1.5 rounded-full border border-divider bg-panel px-3.5 text-ui text-ink active:bg-control aria-expanded:bg-control"
          >
            <Icon icon={MessagesSquare} size={20} />
            對話串
          </button>
        ) : (
        <button
          {...p}
          type="button"
          aria-label={`對話串：${title}`}
          className="relative flex h-8 min-w-0 max-w-[240px] items-center gap-1.5 rounded-full border border-line bg-transparent pl-3 pr-2 text-sub text-ink touch-target hover:bg-control aria-expanded:bg-control"
        >
          <Icon icon={MessageSquare} size={16} className="text-ink-2" />
          <span className="truncate">{title}</span>
          {!!threads?.length && <span className="text-[12px] text-ink-3">{threads.length}</span>}
          <Icon icon={ChevronDown} size={16} className="text-ink-2" />
        </button>
        )
      }
    >
      {(close) => (
        <div role="menu" aria-label={`${botName}的對話串`} className="flex w-[248px] flex-col rounded-menu bg-card p-1.5 shadow-pop max-sm:w-[min(300px,calc(100vw-24px))]">
          <span className="px-2.5 pb-1 pt-1.5 text-meta text-ink-3" aria-hidden="true">
            {botName}的對話串
          </span>
          <div className="flex max-h-[min(360px,50dvh)] flex-col gap-0.5 overflow-y-auto">
            {(threads ?? []).map((t) => {
              const on = t.id === threadId
              return (
                <button
                  key={t.id}
                  type="button"
                  role="menuitemradio"
                  aria-checked={on}
                  tabIndex={-1}
                  onClick={() => {
                    close()
                    if (!on) onPick(t.id)
                  }}
                  className={cx('flex min-h-[46px] w-full items-center gap-2.5 rounded-seg px-2.5 py-1 text-left outline-none hover:bg-hover focus-visible:bg-hover', on ? 'bg-hover' : 'bg-transparent')}
                >
                  <Icon icon={MessageSquare} size={18} className="text-ink-2" />
                  <span className="flex min-w-0 flex-1 flex-col">
                    <span className="truncate text-ui text-ink">{t.title || '新的對話'}</span>
                    <span className="truncate text-[11.5px] text-ink-3">
                      {relTime(t.last_message_at || t.created_at)}
                      {t.unread > 0 && ` · ${t.unread} 則未讀`}
                    </span>
                  </span>
                  {on && <Icon icon={Check} size={18} color="var(--color-accent)" />}
                </button>
              )
            })}
          </div>
          {!!threads?.length && <div role="separator" className="mx-2 my-[5px] h-px bg-line" />}
          <button
            type="button"
            role="menuitem"
            tabIndex={-1}
            onClick={() => {
              close()
              onNew()
            }}
            className="flex h-[34px] w-full items-center gap-2.5 rounded-seg bg-transparent px-2.5 text-left text-ui text-ink outline-none hover:bg-hover focus-visible:bg-hover max-sm:h-11"
          >
            <Icon icon={Plus} size={18} />
            新對話串
          </button>
          {threadId && onDelete && (
            <button
              type="button"
              role="menuitem"
              tabIndex={-1}
              onClick={() => {
                close()
                onDelete()
              }}
              className="flex h-[34px] w-full items-center gap-2.5 rounded-seg bg-transparent px-2.5 text-left text-ui text-danger outline-none hover:bg-hover focus-visible:bg-hover max-sm:h-11"
            >
              <Icon icon={Trash2} size={18} />
              刪除這個對話串
            </button>
          )}
        </div>
      )}
    </Popover>
  )
}
