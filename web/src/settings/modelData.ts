import { useCallback, useEffect, useState } from 'react'
import { modelsGet, modelsPresets, type ModelsGetOutput, type ModelsPresetsOutput } from '../api/generated'

export type Provider = ModelsGetOutput['providers'][number]
export type Preset = ModelsPresetsOutput['presets'][number]
export type ModelRef = { provider: string; model: string }

/** 讀模型設定與內建範本；reload 重讀。error 是讀不到的原因。 */
export function useModelData() {
  const [models, setModels] = useState<ModelsGetOutput | null>(null)
  const [presets, setPresets] = useState<Preset[]>([])
  const [error, setError] = useState<string | null>(null)
  const reload = useCallback(async () => {
    try {
      const m = await modelsGet({})
      setModels(m)
      setError(null)
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    }
  }, [])
  useEffect(() => {
    modelsGet({})
      .then(setModels)
      .catch((e: unknown) => setError(e instanceof Error ? e.message : String(e)))
    modelsPresets({})
      .then((p) => setPresets(p.presets))
      .catch(() => setPresets([]))
  }, [])
  return { models, presets, error, reload }
}

export function providerName(p: Pick<Provider, 'id' | 'label'>, presets: Preset[] = []): string {
  return p.label || presets.find((x) => x.id === p.id)?.label || p.id
}

/** 「供應商/模型」：模型名稱可能也有斜線，只切第一個。 */
export function splitRef(v: string): ModelRef {
  const i = v.indexOf('/')
  return i < 0 ? { provider: '', model: v } : { provider: v.slice(0, i), model: v.slice(i + 1) }
}

export function joinRef(r?: ModelRef | null): string {
  return r ? `${r.provider}/${r.model}` : ''
}

// 貼上金鑰時先從開頭猜是哪一家（J02「自動認出供應商」）。猜不到就請使用者選。
const KEY_PREFIX: Array<[RegExp, string]> = [
  [/^sk-or-/, 'openrouter'],
  [/^sk-ant-/, 'anthropic'],
  [/^xai-/, 'xai'],
  [/^AIza/, 'gemini'],
  [/^nvapi-/, 'nvidia'],
  [/^hf_/, 'huggingface'],
  [/^sk-(proj|svcacct|admin)-/, 'openai-api'],
]

export function guessPreset(key: string, presets: Preset[]): Preset | undefined {
  const k = key.trim()
  if (!k) return undefined
  const hit = KEY_PREFIX.find(([re]) => re.test(k))
  return hit ? presets.find((p) => p.id === hit[1]) : undefined
}

/** 把金鑰遮起來顯示：前 6 碼＋…＋後 4 碼。 */
export function maskKey(key: string): string {
  const k = key.trim()
  if (k.length <= 12) return '•'.repeat(k.length)
  return k.slice(0, 6) + '•'.repeat(12) + k.slice(-4)
}

export const PROBE_STATUS: Record<string, string> = {
  ok: '能用',
  protocol: '呼叫方式不合',
  unavailable: '這個模型不能用',
  auth: '金鑰不對或沒有權限',
  busy: '供應商忙碌或限流',
  free_tier: '免費額度不支援',
  unknown: '不明的錯誤',
}

export const TRANSPORT_LABEL: Record<string, string> = {
  chat_completions: 'Chat Completions',
  codex_responses: 'Responses',
  anthropic_messages: 'Anthropic Messages',
}
