import { useEffect, useState } from 'react'
import { ChevronRight, CircleCheck, KeyRound, SquareTerminal } from 'lucide-react'
import { ApiError, botsCreate, enginesLinkCredentials, enginesStatus, modelsGet, type EnginesStatusOutput, type ModelsGetOutput } from '../api/generated'
import { upsertBot } from '../data/bots'
import { navigate } from '../router'
import { Avatar, Mascot } from '../ui/Avatar'
import { Button } from '../ui/Button'
import { Icon } from '../ui/Icon'
import { cx } from '../ui/cx'

// L08 手機的第一次設定：步驟 1 接上 AI（這台電腦已登入的訂閱 CLI、已設好的模型），一鍵「用這個」；
// 步驟 2 認識小秘（名字、用的 AI、權限都預設好），按「開始」建立後到待我處理。

type Choice = { id: string; label: string; sub: string; engine: 'hermes' | 'claude-code' | 'codex' }

function choicesOf(eng: EnginesStatusOutput | null, models: ModelsGetOutput | null): Choice[] {
  const out: Choice[] = []
  for (const h of eng?.host ?? []) {
    if (h.logged_in && (h.engine === 'claude-code' || h.engine === 'codex')) out.push({ id: h.engine, label: h.label, sub: `已登入 · ${h.plan}`, engine: h.engine })
  }
  if (models?.ready || models?.default) out.push({ id: 'hermes', label: '已設定的模型', sub: models.default?.model ? `Hermes · ${models.default.model}` : 'Hermes', engine: 'hermes' })
  return out
}

export function WelcomePage() {
  const [eng, setEng] = useState<EnginesStatusOutput | null>(null)
  const [models, setModels] = useState<ModelsGetOutput | null>(null)
  const [loaded, setLoaded] = useState(false)
  const [pick, setPick] = useState<Choice | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  useEffect(() => {
    Promise.all([enginesStatus({}).catch(() => null), modelsGet({}).catch(() => null)]).then(([e, m]) => {
      setEng(e)
      setModels(m)
      setLoaded(true)
    })
  }, [])
  const choices = choicesOf(eng, models)

  const start = async () => {
    if (!pick) return
    setBusy(true)
    setError(null)
    try {
      const bot = await botsCreate({ name: '小秘', title: '幕僚長', description: '每天整理重點，把工作分給其他 Bot', engine: pick.engine, shape: 'cursor', color: 'blue' })
      // 你按「用這個」就是同意這個 Bot 用你的訂閱（EN-2b）
      if (pick.engine !== 'hermes') await enginesLinkCredentials({ bot_id: bot.id, engine: pick.engine })
      upsertBot(bot)
      navigate('/', { replace: true })
    } catch (e) {
      setError(e instanceof ApiError || e instanceof Error ? e.message : String(e))
      setBusy(false)
    }
  }

  const step = pick ? 2 : 1
  return (
    <main className="flex min-h-dvh flex-1 flex-col gap-4 bg-app px-4 pb-[max(16px,env(safe-area-inset-bottom))] pt-[max(20px,calc(env(safe-area-inset-top)+12px))]" aria-label="第一次設定">
      <div className="flex items-center gap-3">
        <Mascot size={26} />
        <span className="flex gap-1.5" aria-hidden="true">
          <span className={cx('h-1 w-6 rounded-full', 'bg-ink')} />
          <span className={cx('h-1 w-6 rounded-full', step === 2 ? 'bg-ink' : 'bg-line')} />
        </span>
        <span className="text-sub text-ink-2">{step}/2</span>
      </div>
      {step === 1 ? (
        <>
          <div className="flex flex-col gap-1">
            <h1 className="m-0 text-page">先接上 AI</h1>
            <p className="m-0 text-ui text-ink-2">這台電腦上已經登入的 AI，選一個就能開始。</p>
          </div>
          {loaded && choices.length > 0 && (
            <p className="m-0 flex items-center gap-2 text-sub text-ink-2">
              <Icon icon={CircleCheck} size={16} className="text-success" />
              找到 {choices.length} 個可以用的
            </p>
          )}
          {choices.length > 0 && (
            <div className="flex flex-col overflow-hidden rounded-card bg-card">
              {choices.map((c, i) => (
                <div key={c.id} className="flex items-center gap-3 border-line px-3.5 py-3 not-first:border-t">
                  <span className="flex size-10 items-center justify-center rounded-field bg-control">
                    <Icon icon={SquareTerminal} size={20} />
                  </span>
                  <span className="flex min-w-0 flex-1 flex-col">
                    <span className="text-ui">{c.label}</span>
                    <span className="flex items-center gap-1.5 text-meta text-ink-2">
                      <span className="size-1.5 rounded-full bg-success" />
                      {c.sub}
                    </span>
                  </span>
                  <Button variant={i === 0 ? 'primary' : 'secondary'} className="min-h-11 rounded-full" onClick={() => setPick(c)}>
                    用這個
                  </Button>
                </div>
              ))}
            </div>
          )}
          {loaded && choices.length === 0 && <p className="m-0 rounded-card bg-card px-4 py-3 text-sub text-ink-2">這台電腦上還沒找到登入的 AI。貼上一把 API 金鑰就能開始。</p>}
          <button type="button" onClick={() => navigate('/settings/models')} className="flex min-h-12 items-center gap-3 rounded-card border border-line px-4 text-left text-ui text-ink">
            <Icon icon={KeyRound} size={18} />
            <span className="flex-1">沒有要用的？貼上 API 金鑰</span>
            <Icon icon={ChevronRight} size={18} className="text-ink-3" />
          </button>
          <span className="flex-1" />
          <span className="text-sub text-ink-3">下一步</span>
          <div className="flex items-center gap-3 rounded-card border border-dashed border-line px-4 py-3">
            <Avatar shape="cursor" color="blue" state="idle" dot="none" size={32} ring="#070707" />
            <span className="flex flex-1 flex-col">
              <span className="text-ui">2/2 認識小秘</span>
              <span className="text-meta text-ink-2">名字、顏色、權限都幫你設好了</span>
            </span>
          </div>
        </>
      ) : (
        <>
          <div className="flex flex-col items-center gap-2 pt-4 text-center">
            <Avatar shape="cursor" color="blue" state="happy" dot="none" size={88} ring="#070707" />
            <h1 className="m-0 text-page">嗨，我是小秘</h1>
            <p className="m-0 text-ui text-ink-2">你的幕僚長。交代我一件事就能開始，其他 Bot 我來分工。</p>
          </div>
          <div className="flex flex-col overflow-hidden rounded-card bg-card">
            {[
              ['名字', '小秘'],
              ['用的 AI', pick?.label ?? ''],
              ['權限', '先問我'],
              ['電腦', '第一次交辦時在背景準備'],
            ].map(([k, v]) => (
              <div key={k} className="flex items-center gap-2 border-line px-4 py-3 not-first:border-t">
                <span className="text-sub text-ink-2">{k}</span>
                <span className="flex-1 text-right text-ui">{v}</span>
              </div>
            ))}
          </div>
          {error && (
            <p role="alert" className="m-0 text-sub text-danger">
              {error}
            </p>
          )}
          <span className="flex-1" />
          <Button variant="primary" className="min-h-12 w-full" loading={busy} onClick={() => void start()}>
            開始
          </Button>
          <button type="button" onClick={() => setPick(null)} className="min-h-11 text-ui text-ink-2">
            換一個 AI
          </button>
        </>
      )}
    </main>
  )
}
