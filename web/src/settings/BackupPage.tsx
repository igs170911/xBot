import { useCallback, useEffect, useState } from 'react'
import { Archive, Check, Copy, DatabaseBackup, Info } from 'lucide-react'
import { backupCreate, backupList, systemPing, type BackupListOutput, type SystemPingOutput } from '../api/generated'
import { useCopy } from '../chat/useCopy'
import { sizeLabel } from '../files/data'
import { linkClick } from '../router'
import { Button } from '../ui/Button'
import { Icon } from '../ui/Icon'
import { useToast } from '../ui/toast-context'
import { errText } from './format'
import { Block, Group, Note, Row, SectionHeader } from './parts'

// 設定 › 進階 › 備份與更新（J07）：目前版本、最近的備份、「立即備份」（xbot.backup.create）。
// 還原一定要先停服務、換掉整個資料目錄，所以留在指令列（make restore），這裡只寫做法；更新同理（make update）。
// 和畫板不同：沒有每天自動備份、密碼加密匯出、網頁上的「重新啟動並更新」（v1.0 沒有這些功能）。

type Backup = BackupListOutput['backups'][number]

export function when(ms: number): string {
  const d = new Date(ms)
  return d.toLocaleString('zh-TW', { year: 'numeric', month: 'numeric', day: 'numeric', hour: '2-digit', minute: '2-digit', hour12: false })
}

function Cmd({ text }: { text: string }) {
  const [copied, copy] = useCopy()
  return (
    <span className="flex min-w-0 items-center gap-2 rounded-field bg-inset py-1 pl-3 pr-1 font-mono text-[13px]">
      <code className="min-w-0 flex-1 truncate" title={text}>
        {text}
      </code>
      <button type="button" aria-label={`複製：${text}`} onClick={() => void copy(text)} className="flex size-7 flex-none items-center justify-center rounded-field text-ink-2 hover:bg-hover hover:text-ink">
        <Icon icon={copied ? Check : Copy} size={14} />
      </button>
    </span>
  )
}

export function BackupPage() {
  const toast = useToast()
  const [list, setList] = useState<BackupListOutput | null>(null)
  const [err, setErr] = useState<string | null>(null)
  const [ping, setPing] = useState<SystemPingOutput | null>(null)
  const [busy, setBusy] = useState(false)
  const [copied, copy] = useCopy()

  const load = useCallback(() => {
    backupList({})
      .then((o) => {
        setList(o)
        setErr(null)
      })
      .catch((e: unknown) => setErr(errText(e)))
  }, [])
  useEffect(() => {
    load()
    systemPing({})
      .then(setPing)
      .catch(() => setPing(null))
  }, [load])

  const backupNow = async () => {
    setBusy(true)
    try {
      const out = await backupCreate({})
      toast.show({ kind: 'done', text: `已備份 ${out.files} 個檔案（${sizeLabel(out.file.size)}）` })
      load()
    } catch (e) {
      toast.show({ kind: 'error', text: errText(e) })
    } finally {
      setBusy(false)
    }
  }

  const latest: Backup | undefined = list?.backups[0]
  const version = ping?.build ?? '…'
  const restoreFile = latest?.path ?? '<備份檔>'

  return (
    <>
      <SectionHeader
        title="備份與更新"
        sub={
          <>
            資料都在這台主機的 <code className="font-mono text-[13px]">{list?.data_dir ?? '…'}</code>；備份和更新都不會動到你的 Bot 設定。
          </>
        }
      />

      <Block title="備份">
        {err && <p className="m-0 rounded-group bg-card px-4 py-3 text-ui-sm text-danger">讀不到備份清單：{err}</p>}
        <Group label="最近的備份">
          <Row
            title="立即備份"
            sub="所有 Bot、對話、技能、記憶、定時任務、連接的應用程式設定；資料庫用一致的快照，Bot 正在工作也能備份。"
            wrap
          >
            <Button variant="primary" size="sm" icon={DatabaseBackup} loading={busy} onClick={() => void backupNow()}>
              立即備份
            </Button>
          </Row>
          {list && list.backups.length === 0 && <Row title="還沒有備份" sub={`備份會放在 ${list.dir}`} />}
          {list?.backups.map((b) => (
            <div key={b.path} className="flex min-h-12 items-center gap-3 border-t border-line px-4 py-2.5" data-testid="backup-row">
              <Icon icon={Archive} size={16} className="flex-none text-ink-2" />
              <span className="min-w-0 flex-1">
                <span className="block truncate text-ui">{when(b.created_at)}</span>
                <span className="block truncate font-mono text-[12px] text-ink-3" title={b.path}>
                  {b.name}
                </span>
              </span>
              <span className="flex-none text-[13px] text-ink-2 tabular-nums">{sizeLabel(b.size)}</span>
            </div>
          ))}
        </Group>
        <Note tone="warning" icon={<Icon icon={Info} size={18} />}>
          備份不包含：Bot 電腦裡的檔案（家目錄、/workspace，存在 Docker volume）與網站登入。要一起備份請看管理手冊（docs/ADMIN.md）的「備份」。
        </Note>
      </Block>

      <Block title="從備份還原" sub="還原會取代這台主機上目前所有的資料；原本的資料會改名保留，不會刪掉。">
        <Group>
          <Row title="在這台主機的終端機執行" sub="還原時服務要先停下來（指令會自動停、還原、再啟動），所以不能在網頁上按。" wrap>
            <span className="flex min-w-0 max-w-[420px] flex-1 flex-col gap-1.5 max-sm:max-w-none">
              <Cmd text={`make restore FILE=${restoreFile}`} />
            </span>
          </Row>
        </Group>
      </Block>

      <Block title="更新">
        <Group>
          <Row title={`XBot ${version}`} sub="在原始碼資料夾執行：先拉新版、建置、自動備份一份，再換新版重啟；新版起不來會自動換回上一版。" wrap>
            <span className="flex min-w-0 max-w-[420px] flex-1 flex-col gap-1.5 max-sm:max-w-none">
              <Cmd text="make update" />
            </span>
          </Row>
          <Row
            title="更新 Bot 的電腦"
            sub={
              <>
                make update 不會重建 Bot 的電腦。電腦映像換了（make box）以後，到「
                <a href="/settings/computers" onClick={linkClick('/settings/computers')}>
                  電腦與裝置
                </a>
                」按「更新電腦」。
              </>
            }
          />
        </Group>
      </Block>

      <Block title="關於">
        <Group>
          <Row title="XBot" sub={`版本 ${version} · 操作 ${ping?.api ?? '…'}`}>
            <Button size="sm" icon={copied ? Check : Copy} onClick={() => void copy(`XBot ${version}（${ping?.api ?? ''}）${navigator.userAgent ? ' · ' + navigator.userAgent : ''}`)}>
              複製版本資訊
            </Button>
          </Row>
        </Group>
      </Block>
    </>
  )
}
