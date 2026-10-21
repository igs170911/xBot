import { useState } from 'react'
import { Trash2 } from 'lucide-react'
import { teamsUpdate } from '../api/generated'
import { botsStore, loadBots, look } from '../data/bots'
import { useStore } from '../data/store'
import { loadTeams, teamsStore, type Team } from '../data/teams'
import { Avatar } from '../ui/Avatar'
import { Button } from '../ui/Button'
import { Checkbox } from '../ui/Controls'
import { Dialog } from '../ui/Dialog'
import { TextInput } from '../ui/Field'
import { Select } from '../ui/Menu'
import { useToast } from '../ui/toast-context'

// 編輯團隊（xbot.teams.update）：名稱、成員、主管；最下面刪除團隊（要確認，在 DeleteDialog）。
// 勾進來的 Bot 如果在別的團隊，會從那個團隊移過來（旁邊寫出它現在在哪）。

/** 一個團隊最多幾位（含主管；limits.TeamMax）。 */
const MAX = 10

export function EditTeamDialog({ team, onClose, onDelete }: { team: Team; onClose: () => void; onDelete: () => void }) {
  const toast = useToast()
  // 大總管不加入團隊（後端也擋）；舊資料裡已經在這個團隊的照樣列出，才能把它移出
  const bots = (useStore(botsStore).bots ?? []).filter((b) => !b.archived && (!b.primary || b.team_id === team.id))
  const teams = useStore(teamsStore).teams ?? []
  const before = bots.filter((b) => b.team_id === team.id).map((b) => b.id)
  const [name, setName] = useState(team.name)
  const [members, setMembers] = useState<string[]>(before)
  const [chief, setChief] = useState(team.chief_id)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const toggle = (id: string) => setMembers((cur) => (cur.includes(id) ? cur.filter((x) => x !== id) : [...cur, id]))
  const chiefOK = chief && members.includes(chief) ? chief : ''
  const valid = name.trim() !== '' && members.length <= MAX

  const save = async () => {
    setBusy(true)
    setError(null)
    const add = members.filter((id) => !before.includes(id))
    const remove = before.filter((id) => !members.includes(id))
    try {
      // 從別的團隊搬過來的：先從原本的團隊移出（那邊的主管、報告線由後端調整）
      for (const id of add) {
        const from = bots.find((b) => b.id === id)?.team_id
        if (from) await teamsUpdate({ id: from, remove_ids: [id] })
      }
      await teamsUpdate({ id: team.id, name: name.trim(), chief_id: chiefOK !== team.chief_id ? chiefOK : undefined, add_ids: add.length ? add : undefined, remove_ids: remove.length ? remove : undefined })
      onClose()
      toast.show({ kind: 'done', text: `團隊「${name.trim()}」已更新` })
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
      void Promise.all([loadTeams(), loadBots()])
    }
  }

  return (
    <Dialog
      open
      onClose={onClose}
      busy={busy}
      width={560}
      title="編輯團隊"
      actions={
        <>
          <Button variant="danger" icon={Trash2} onClick={onDelete} disabled={busy} className="mr-auto">
            刪除團隊
          </Button>
          <Button onClick={onClose} disabled={busy}>
            取消
          </Button>
          <Button variant="primary" loading={busy} disabled={!valid} onClick={() => void save()}>
            儲存
          </Button>
        </>
      }
    >
      <div className="flex flex-col gap-4">
        <TextInput label="名稱" value={name} onChange={(e) => setName(e.target.value)} />
        <div className="flex flex-col gap-1.5">
          <span className="text-meta text-ink-3">
            成員（{members.length} 位，最多 {MAX} 位）
          </span>
          <div className="flex max-h-[260px] flex-col overflow-y-auto rounded-group bg-inset" role="group" aria-label="成員">
            {bots.map((b) => {
              const elsewhere = b.team_id && b.team_id !== team.id ? teams.find((t) => t.id === b.team_id)?.name : ''
              return (
                <Checkbox key={b.id} checked={members.includes(b.id)} onChange={() => toggle(b.id)} className="min-h-11 border-line px-3 not-first:border-t">
                  <Avatar {...look(b)} dot="none" size={24} blink={false} />
                  <span className="min-w-0 flex-1 truncate">{b.name}</span>
                  {elsewhere ? <span className="truncate text-meta text-ink-3">{members.includes(b.id) ? `從「${elsewhere}」移過來` : `在「${elsewhere}」`}</span> : b.title && <span className="truncate text-meta text-ink-3">{b.title}</span>}
                </Checkbox>
              )
            })}
          </div>
          {members.length > MAX && <span className="text-meta text-danger">一個團隊最多 {MAX} 位</span>}
        </div>
        <Select
          label="主管（成員向它報告）"
          value={chiefOK}
          options={[{ value: '', label: '沒有主管（成員直接向你報告）' }, ...members.map((id) => ({ value: id, label: bots.find((b) => b.id === id)?.name ?? id }))]}
          onChange={setChief}
        />
        {error && (
          <p role="alert" className="m-0 text-meta text-danger">
            {error}
          </p>
        )}
      </div>
    </Dialog>
  )
}
