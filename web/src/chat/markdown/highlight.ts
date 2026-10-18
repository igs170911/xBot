// 延遲載入高亮器：第一次遇到程式碼區塊才下載語法檔（約 40 KB gzip），平常的對話不用付這個成本。

export type HNode = { type: 'text'; value: string } | { type: 'element'; tagName: string; properties?: { className?: string[] | string }; children: HNode[] } | { type: string; children?: HNode[]; value?: string }

export type HRoot = { type: 'root'; children: HNode[] }

type Highlighter = {
  registered: (lang: string) => boolean
  highlight: (lang: string, code: string) => HRoot
}

let loading: Promise<Highlighter> | null = null

export function loadHighlighter(): Promise<Highlighter> {
  loading ??= import('./languages').then(({ lowlight }) => ({
    registered: (lang: string) => lowlight.registered(lang),
    highlight: (lang: string, code: string) => lowlight.highlight(lang, code) as unknown as HRoot,
  }))
  return loading
}

/** 常見的別名對到已註冊的語言。 */
export function normalizeLang(lang: string): string {
  const l = lang.trim().toLowerCase().split(/[\s{]/)[0]
  const alias: Record<string, string> = { sh: 'bash', zsh: 'bash', console: 'shell', py: 'python', js: 'javascript', jsx: 'javascript', mjs: 'javascript', ts: 'typescript', tsx: 'typescript', yml: 'yaml', html: 'xml', svg: 'xml', golang: 'go', rs: 'rust', 'c++': 'cpp', md: 'markdown', toml: 'ini', docker: 'dockerfile', patch: 'diff' }
  return alias[l] ?? l
}
