import { useEffect, useState, type FormEvent } from 'react'
import { ApiError, modelsGet, modelsSetProvider, type ModelsGetOutput } from '../api/generated'
import { Button } from '../ui/Button'
import { Dialog } from '../ui/Dialog'
import { TextInput } from '../ui/Field'

// 最簡單的模型設定：一個 OpenAI 相容的供應商（網址＋金鑰）＋預設模型。
// 金鑰只送到服務端的 Model Proxy，不會再讀回來（MCP-11）。完整的模型頁是 J02。

type Props = {
  /** 存好之後（例：關掉對話框、重讀模型狀態）。 */
  onSaved?: (out: ModelsGetOutput) => void
  onCancel?: () => void
}

function fieldError(err: unknown, field: string): string | undefined {
  return err instanceof ApiError ? err.fields?.find((f) => f.field === field)?.message : undefined
}

export function ModelSettingsForm({ onSaved, onCancel }: Props) {
  const [current, setCurrent] = useState<ModelsGetOutput | null>(null)
  const [id, setId] = useState('openai')
  const [baseUrl, setBaseUrl] = useState('https://api.openai.com/v1')
  const [key, setKey] = useState('')
  const [model, setModel] = useState('')
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<unknown>(null)

  useEffect(() => {
    modelsGet({})
      .then((m) => {
        setCurrent(m)
        const p = m.providers.find((x) => x.id === m.default?.provider) ?? m.providers[0]
        if (p) {
          setId(p.id)
          setBaseUrl(p.base_url)
        }
        if (m.default?.model) setModel(m.default.model)
      })
      .catch(() => setCurrent(null))
  }, [])

  const existing = current?.providers.find((p) => p.id === id.trim())
  const submit = async (e: FormEvent) => {
    e.preventDefault()
    setBusy(true)
    setErr(null)
    try {
      await modelsSetProvider({ id: id.trim(), base_url: baseUrl.trim(), api_key: key || undefined, default_model: model.trim() || undefined })
      setKey('')
      const m = await modelsGet({})
      setCurrent(m)
      onSaved?.(m)
    } catch (e2) {
      setErr(e2)
    } finally {
      setBusy(false)
    }
  }

  const general = err && !(err instanceof ApiError && err.fields?.length) ? (err instanceof Error ? err.message : String(err)) : null

  return (
    <form onSubmit={submit} className="flex flex-col gap-3" aria-label="模型設定">
      <TextInput label="供應商代號" value={id} onChange={(e) => setId(e.target.value)} hint="小寫英文、數字、-，例：openai" error={fieldError(err, 'id')} required />
      <TextInput label="服務網址（OpenAI 相容）" value={baseUrl} onChange={(e) => setBaseUrl(e.target.value)} error={fieldError(err, 'base_url')} required inputMode="url" />
      <TextInput
        label="金鑰"
        type="password"
        autoComplete="off"
        value={key}
        onChange={(e) => setKey(e.target.value)}
        placeholder={existing?.api_key_set ? '已設定；留空保留原本的金鑰' : 'sk-…'}
        hint="只存在服務端，不會進 Bot 的電腦，也不會再顯示。"
        error={fieldError(err, 'api_key')}
      />
      <TextInput label="預設模型" value={model} onChange={(e) => setModel(e.target.value)} placeholder="例：gpt-4.1-mini" error={fieldError(err, 'default_model')} required />
      {general && (
        <p role="alert" className="m-0 text-meta text-danger">
          {general}
        </p>
      )}
      <div className="mt-1 flex flex-wrap justify-end gap-2">
        {onCancel && (
          <Button variant="secondary" onClick={onCancel} disabled={busy}>
            取消
          </Button>
        )}
        <Button variant="primary" type="submit" loading={busy}>
          儲存
        </Button>
      </div>
    </form>
  )
}

export function ModelSettingsDialog({ open, onClose, onSaved }: { open: boolean; onClose: () => void; onSaved?: (out: ModelsGetOutput) => void }) {
  return (
    <Dialog open={open} onClose={onClose} title="設定模型" description="Bot 用這個模型思考和回話。之後可以在設定裡改。">
      {open && (
        <ModelSettingsForm
          onCancel={onClose}
          onSaved={(m) => {
            onSaved?.(m)
            onClose()
          }}
        />
      )}
    </Dialog>
  )
}
