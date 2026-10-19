import { useEffect, useState, type FormEvent } from 'react'
import { ChevronDown, Info, Plus, Sparkles, X } from 'lucide-react'
import { teamsCreate, teamsSuggest, teamsTemplates, type TeamsTemplatesOutput } from '../api/generated'
import { memberProblem, overLimit, peopleOf, type Person, type Template } from './model'
import { loadBots } from '../data/bots'
import { useStore } from '../data/store'
import { loadTeams, newTeamDialog } from '../data/teams'
import { navigate } from '../router'
import { Avatar } from '../ui/Avatar'
import { SHAPE_IDS, COLOR_IDS } from '../ui/avatar-data'
import { Button, IconButton } from '../ui/Button'
import { Switch } from '../ui/Controls'
import { Dialog } from '../ui/Dialog'
import { TextInput } from '../ui/Field'
import { Icon } from '../ui/Icon'
import { cx } from '../ui/cx'

// 建立團隊（design/F02、F03）：一句話或點範本 → 看成員預覽 → 建立 N 個 Bot。
// 名稱、成員、群組收在進階設定，全部有預設值；之後在組織架構與 Bot 設定裡改。

function Face({ i, size = 26 }: { i: number; size?: number }) {
  return <Avatar shape={SHAPE_IDS[i % SHAPE_IDS.length]} color={COLOR_IDS[i % COLOR_IDS.length]} dot="none" size={size} blink={false} ring="#1A1A1A" />
}

function NewTeamForm({ onDone }: { onDone: () => void }) {
  const [data, setData] = useState<TeamsTemplatesOutput | null>(null)
  const [loadErr, setLoadErr] = useState<string | null>(null)
  const [key, setKey] = useState('marketing')
  const [people, setPeople] = useState<Person[]>([])
  const [name, setName] = useState('')
  const [sentence, setSentence] = useState('')
  const [hint, setHint] = useState<string | null>(null)
  const [chat, setChat] = useState(true)
  const [more, setMore] = useState(false)
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string | null>(null)

  useEffect(() => {
    teamsTemplates({})
      .then((d) => {
        setData(d)
        const t = d.templates.find((x) => x.key === 'marketing') ?? d.templates[0]
        if (t) {
          setKey(t.key)
          setPeople(peopleOf(t))
        }
      })
      .catch((e: unknown) => setLoadErr(e instanceof Error ? e.message : String(e)))
  }, [])

  const tpl = data?.templates.find((t) => t.key === key)
  const pick = (t: Template) => {
    setKey(t.key)
    setPeople(peopleOf(t))
    setName('')
    setErr(null)
  }
  const suggest = async () => {
    if (!sentence.trim()) return
    try {
      const out = await teamsSuggest({ sentence: sentence.trim() })
      const t = data?.templates.find((x) => x.key === out.template.key) ?? out.template
      pick(t)
      setHint(out.matched ? `建議用「${t.name}」` : `沒有對到範本，先用「${t.name}」；可以自己換`)
    } catch (e) {
      setHint(e instanceof Error ? e.message : String(e))
    }
  }

  const teamMax = data?.team_max ?? 10
  const max = data?.bots_max ?? 50
  const total = data?.bots_total ?? 0
  const over = overLimit(total, people.length, max)
  const problem = memberProblem(people, teamMax)
  const cta = `建立 ${people.length} 個 Bot`

  const submit = async (e: FormEvent) => {
    e.preventDefault()
    if (!tpl || over || problem) return
    setBusy(true)
    setErr(null)
    try {
      const [lead, ...members] = people
      const out = await teamsCreate({
        template: tpl.key as 'engineering' | 'company' | 'marketing',
        name: name.trim() || undefined,
        lead: { name: lead.name.trim(), title: lead.title, persona: lead.persona },
        members: members.map((m) => ({ name: m.name.trim(), title: m.title, persona: m.persona })),
        group_chat: chat,
      })
      await Promise.all([loadTeams(), loadBots()])
      onDone()
      navigate(`/teams#${out.team.id}`)
    } catch (e2) {
      setErr(e2 instanceof Error ? e2.message : String(e2))
      setBusy(false)
    }
  }

  if (loadErr) return <p role="alert" className="m-0 text-sub text-danger">讀不到範本：{loadErr}</p>
  if (!data) return <p className="m-0 text-sub text-ink-2">讀取範本…</p>

  return (
    <form onSubmit={submit} aria-label="建立團隊" className="flex flex-col gap-3.5">
      <div className="flex gap-2">
        <TextInput
          label="一句話說你要什麼團隊"
          hideLabel
          placeholder="一句話說你要什麼團隊，例：幫我經營 IG 和臉書粉專"
          value={sentence}
          onChange={(e) => setSentence(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter') {
              e.preventDefault()
              void suggest()
            }
          }}
          wrapperClassName="flex-1"
          maxLength={500}
        />
        <Button variant="secondary" icon={Sparkles} onClick={() => void suggest()} disabled={!sentence.trim()}>
          幫我挑
        </Button>
      </div>
      {hint && (
        <p className="m-0 text-meta text-ink-2" data-testid="team-hint">
          {hint}
        </p>
      )}

      <div role="radiogroup" aria-label="團隊範本" className="grid grid-cols-3 gap-2.5 max-sm:grid-cols-1">
        {data.templates.map((t) => {
          const on = t.key === key
          const n = t.members.length + 1
          return (
            <button
              key={t.key}
              type="button"
              role="radio"
              aria-checked={on}
              onClick={() => pick(t)}
              className={cx('flex flex-col gap-1.5 rounded-group border px-3.5 py-3 text-left', on ? 'border-ink/70 bg-control' : 'border-line bg-transparent hover:bg-control/50')}
            >
              <span className="flex items-baseline gap-1.5">
                <span className="text-body text-ink">{t.name}</span>
                <span className="text-meta text-ink-3">{n} 個 Bot</span>
              </span>
              <span className="text-meta text-ink-2">{t.about}</span>
              <span className="flex -space-x-1.5 overflow-hidden pt-1" aria-hidden="true">
                {Array.from({ length: Math.min(n, 8) }, (_, i) => (
                  <Face key={i} i={i} size={22} />
                ))}
              </span>
            </button>
          )
        })}
      </div>

      <section aria-label="成員預覽" className="flex flex-col gap-2 rounded-group bg-control/50 px-3.5 py-3">
        <div className="flex flex-wrap items-baseline justify-between gap-2">
          <span className="text-ui">{name.trim() || tpl?.name}：主管＋{people.length - 1} 位成員</span>
          <span className="text-meta text-ink-3">每個 Bot 都有自己的電腦，第一次收到工作才開機</span>
        </div>
        <ul className="m-0 grid list-none grid-cols-2 gap-x-3 gap-y-1.5 p-0 max-sm:grid-cols-1" data-testid="team-preview">
          {people.map((p, i) => (
            <li key={i} className="flex min-w-0 items-center gap-2">
              <Face i={i} />
              <span className="flex min-w-0 flex-col leading-[1.35]">
                <span className="truncate text-ui-sm">
                  {p.name || '（沒有名字）'}
                  {i === 0 && <span className="pl-1.5 text-meta text-accent-ink">主管</span>}
                </span>
                <span className="truncate text-meta text-ink-3">{i === 0 ? '向你報告' : `${p.title} · 向 ${people[0]?.name || '主管'} 報告`}</span>
              </span>
            </li>
          ))}
        </ul>
      </section>

      <p className="m-0 flex items-center gap-2 text-meta text-ink-2">
        <Icon icon={Info} size={15} />
        名字、成員之後都能改（組織架構、Bot 設定）。
      </p>

      <button type="button" aria-expanded={more} onClick={() => setMore((m) => !m)} className="flex items-center gap-1.5 self-start bg-transparent p-0 text-ui-sm text-ink hover:text-ink">
        <Icon icon={ChevronDown} size={16} className={cx('transition-transform', !more && '-rotate-90')} />
        進階設定
        <span className="text-meta text-ink-3">團隊名稱、成員、團隊群組</span>
      </button>
      {more && (
        <div className="flex flex-col gap-3.5 border-t border-line pt-3">
          <TextInput label="團隊名稱" hint="空白就用範本名稱；同名已存在會自動加「（2）」" value={name} onChange={(e) => setName(e.target.value)} maxLength={40} placeholder={tpl?.name} />
          <fieldset className="m-0 flex flex-col gap-1.5 border-0 p-0">
            <legend className="mb-1 p-0 text-meta text-ink-3">成員（第一位是主管）· 之後在組織架構改</legend>
            {people.map((p, i) => (
              <div key={i} className="flex items-center gap-2">
                <Face i={i} />
                <TextInput
                  label={i === 0 ? '主管名字' : `成員 ${i} 名字`}
                  hideLabel
                  value={p.name}
                  maxLength={40}
                  onChange={(e) => setPeople((ps) => ps.map((x, j) => (j === i ? { ...x, name: e.target.value } : x)))}
                  wrapperClassName="flex-1"
                />
                <span className="w-24 truncate text-meta text-ink-3 max-sm:hidden">{p.title}</span>
                {i > 0 ? (
                  <IconButton icon={X} label={`移除 ${p.name}`} onClick={() => setPeople((ps) => ps.filter((_, j) => j !== i))} />
                ) : (
                  <span className="size-9 flex-none" />
                )}
              </div>
            ))}
            <Button
              variant="outline"
              icon={Plus}
              disabled={people.length >= teamMax}
              onClick={() => setPeople((ps) => [...ps, { name: `成員 ${ps.length}`, title: '成員', persona: '' }])}
            >
              {people.length >= teamMax ? `已滿 ${teamMax} 位` : `加一位成員（還能加 ${teamMax - people.length} 位）`}
            </Button>
          </fieldset>
          <Switch checked={chat} onChange={setChat} label="同時開一個團隊群組">
            同時開一個團隊群組（主管負責回應）
          </Switch>
        </div>
      )}

      {(problem || err) && (
        <p role="alert" className="m-0 text-meta text-danger">
          {err ?? problem}
        </p>
      )}
      {over && (
        <p role="alert" className="m-0 text-meta text-danger">
          建立後會超過 {max} 個 Bot；可以先封存不用的 Bot。
        </p>
      )}
      <div className="flex flex-wrap items-center justify-end gap-2 pt-1">
        <span className="mr-auto text-meta text-ink-3" data-testid="team-total">
          建立後共 {total + people.length} 個 Bot（上限 {max}）
        </span>
        <Button variant="secondary" onClick={onDone} disabled={busy}>
          取消
        </Button>
        <Button variant="primary" type="submit" loading={busy} disabled={over || !!problem}>
          {cta}
        </Button>
      </div>
    </form>
  )
}

export function NewTeamDialog() {
  const open = useStore(newTeamDialog)
  const close = () => newTeamDialog.set(false)
  return (
    <Dialog
      open={open}
      onClose={close}
      title={
        <span className="flex flex-wrap items-center gap-3">
          建立團隊
        </span>
      }
      description="挑一個範本，看一下成員，按建立就好。成員都向主管報告，主管向你報告。"
      width={760}
    >
      {open && <NewTeamForm onDone={close} />}
    </Dialog>
  )
}
