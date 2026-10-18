// 建置大小報告（v1.0 效能，結果記在 docs/perf.md）：node scripts/bundle-report.mjs [dist]
// 列出首頁一打開就要下載的 JS／CSS（index.html 的入口與 modulepreload），以及每個按需載入的 chunk（原始與 gzip 大小）。
import { readFileSync, readdirSync } from 'node:fs'
import { join } from 'node:path'
import { gzipSync } from 'node:zlib'

const dist = process.argv[2] ?? 'dist'
const html = readFileSync(join(dist, 'index.html'), 'utf8')
const initial = new Set([...html.matchAll(/(?:src|href)="\/(assets\/[^"]+\.(?:js|css))"/g)].map((m) => m[1]))

const size = (f) => {
  const b = readFileSync(join(dist, f))
  return { raw: b.length, gz: gzipSync(b, { level: 9 }).length }
}
const kb = (n) => (n / 1024).toFixed(1).padStart(7) + ' KiB'

let total = { raw: 0, gz: 0 }
console.log('首頁一打開就下載：')
for (const f of [...initial].sort()) {
  const s = size(f)
  total.raw += s.raw
  total.gz += s.gz
  console.log(`  ${kb(s.raw)}  gzip ${kb(s.gz)}  ${f}`)
}
console.log(`  合計 JS＋CSS ${kb(total.raw)}  gzip ${kb(total.gz)}`)
const js = [...initial].filter((f) => f.endsWith('.js')).map(size)
console.log(`  其中 JS ${kb(js.reduce((a, s) => a + s.raw, 0))}  gzip ${kb(js.reduce((a, s) => a + s.gz, 0))}`)

console.log('\n按需載入（最大的 25 個）：')
const lazy = readdirSync(join(dist, 'assets'))
  .filter((f) => f.endsWith('.js') && !initial.has('assets/' + f))
  .map((f) => ({ f, ...size('assets/' + f) }))
  .sort((a, b) => b.raw - a.raw)
for (const c of lazy.slice(0, 25)) console.log(`  ${kb(c.raw)}  gzip ${kb(c.gz)}  ${c.f}`)
console.log(`  共 ${lazy.length} 個，合計 ${kb(lazy.reduce((a, c) => a + c.raw, 0))}`)
