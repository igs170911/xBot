// 手機清單用的時間格式（L01、L14）。

/** 列上的時間：今天「下午 3:02」、這週「週一」、更早「昨天」或「10/12」。 */
export function rowTime(ms: number, now = Date.now()): string {
  if (!ms) return ''
  const d = new Date(ms)
  const t = new Date(now)
  const day0 = (x: Date) => new Date(x.getFullYear(), x.getMonth(), x.getDate()).getTime()
  const days = Math.round((day0(t) - day0(d)) / 86_400_000)
  if (days <= 0) {
    const h = d.getHours()
    return `${h < 12 ? '上午' : '下午'} ${h % 12 === 0 ? 12 : h % 12}:${String(d.getMinutes()).padStart(2, '0')}`
  }
  if (days === 1) return '昨天'
  if (days < 7) return '週' + '日一二三四五六'[d.getDay()]
  return `${d.getMonth() + 1}/${d.getDate()}`
}
