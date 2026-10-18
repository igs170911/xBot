// 把條件 class 串起來（不另外裝 clsx）。
export function cx(...parts: Array<string | false | null | undefined>): string {
  return parts.filter(Boolean).join(' ')
}
